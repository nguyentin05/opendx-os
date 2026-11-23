// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package models

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"hash"
	"net"

	"github.com/lib/pq"
	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
)

// Durable operation states. Only CLAIMED may start a dispatch; every other
// non-terminal-in-progress state is either finished or unsafe to repeat.
const (
	OperationClaimed      = "CLAIMED"
	OperationMayHaveSent  = "MAY_HAVE_SENT"
	OperationSucceeded    = "SUCCEEDED"
	OperationFailedUnsent = "FAILED_UNSENT"
	OperationUncertain    = "UNCERTAIN"
)

// Sanitized error codes persisted with a terminal operation. They describe the
// gateway's own decision, never provider diagnostics.
const (
	codeAuthorityChanged     = "AUTHORITY_CHANGED"
	codeAuthorityUnavailable = "AUTHORITY_UNAVAILABLE"
	codeProviderUncertain    = "PROVIDER_UNCERTAIN"
	codeInvalidUsage         = "INVALID_USAGE"
	codeInvalidResult        = "INVALID_RESULT"
)

// OperationBinding is the complete identity of one durable operation.
type OperationBinding struct {
	Key, ActorID, AppID, ProfileID string
	Revision                       int64
	Purpose, Fingerprint           string
}

// Finalization describes a terminal transition. Usage is nil for failures.
type Finalization struct {
	State             string
	ProviderRequestID string
	Usage             *Usage
	ErrorCode         string
	LatencyMS         int64
}

// OperationLog is the gateway's only durable dispatch boundary.
type OperationLog interface {
	Claim(context.Context, OperationBinding) (string, error)
	Begin(context.Context, string, string) (string, error)
	Finish(context.Context, string, string, string, Finalization) (bool, error)
}

// OperationStore forwards to the narrow SQL functions owned by cap_ai. It never
// reads the operation table directly and never returns a dispatch token onward.
type OperationStore struct{ DB *sql.DB }

func (s *OperationStore) Claim(ctx context.Context, b OperationBinding) (string, error) {
	if s == nil || s.DB == nil {
		return "", ErrUnavailable
	}
	var state string
	err := s.DB.QueryRowContext(ctx, `SELECT cap_ai.claim_model_operation($1,$2,$3,$4,$5,$6,$7)`,
		b.Key, b.ActorID, b.AppID, b.ProfileID, b.Revision, b.Purpose, b.Fingerprint).Scan(&state)
	if err != nil {
		return "", operationError(err)
	}
	return state, nil
}

func (s *OperationStore) Begin(ctx context.Context, key, fingerprint string) (string, error) {
	if s == nil || s.DB == nil {
		return "", ErrUnavailable
	}
	// A context that is already done proves the write never happened: the pool
	// refuses to lease a connection, so no intent can exist. Only a caller that
	// stops waiting after the statement is under way leaves the outcome open.
	if err := ctx.Err(); err != nil {
		return "", ErrUnavailable
	}
	var token sql.NullString
	if err := s.DB.QueryRowContext(ctx, `SELECT cap_ai.begin_model_operation($1,$2)`, key, fingerprint).Scan(&token); err != nil {
		return "", beginError(err)
	}
	return token.String, nil
}

func (s *OperationStore) Finish(ctx context.Context, key, fingerprint, token string, f Finalization) (bool, error) {
	if s == nil || s.DB == nil {
		return false, ErrUnavailable
	}
	var applied bool
	err := s.DB.QueryRowContext(ctx, `SELECT cap_ai.finish_model_operation($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		key, fingerprint, nullableText(token), f.State, nullableText(f.ProviderRequestID),
		nullableCounter(f.Usage, true), nullableCounter(f.Usage, false), nullableText(f.ErrorCode), nullableLatency(f.LatencyMS)).Scan(&applied)
	if err != nil {
		return false, operationError(err)
	}
	return applied, nil
}

func nullableText(value string) any {
	if value == "" {
		return nil
	}
	return value
}

// nullableCounter keeps the missing/reported-zero distinction: no usage is two
// SQL NULLs, a reported zero is two SQL zeros.
func nullableCounter(usage *Usage, input bool) any {
	if usage == nil {
		return nil
	}
	if input {
		return usage.InputTokens
	}
	return usage.OutputTokens
}

func nullableLatency(ms int64) any {
	if ms < 0 {
		return nil
	}
	return ms
}

// operationError keeps the SQL boundary sanitized: callers only ever see the
// package errors, never a driver message or SQL state.
func operationError(err error) error {
	if err == nil {
		return nil
	}
	var pg *pq.Error
	if errors.As(err, &pg) {
		switch pg.Code {
		case "P0409":
			return ErrConflict
		case "42501":
			// A missing privilege on a cap_ai interface is a deployment or
			// configuration failure of the gateway role, never the caller's
			// policy decision.
			return ErrUnavailable
		}
	}
	return ErrUnavailable
}

// SQLSTATEs by which PostgreSQL itself reports that it cannot say what became of
// the statement. Only the codes that describe a lost or unresolved execution
// belong here: codes that report a connection which was never established
// (08001, 08004) prove nothing was written and keep their ErrUnavailable.
const (
	pqConnectionFailure            = "08006"
	pqTransactionResolutionUnknown = "08007"
	pqStatementCompletionUnknown   = "40003"
)

// beginError classifies a failed dispatch-intent write. Begin is the last step
// before a possible network side effect, so an error may only be reported as
// retryable unavailability when "definitely unsent" is proven:
//
//   - A PostgreSQL error means the server answered. The statement was refused
//     rather than lost, so the existing mapping stands (ErrConflict for a
//     binding conflict, ErrUnavailable otherwise) — except the SQLSTATEs by
//     which the server itself reports that it cannot say what happened.
//   - A dial failure proves the connection never opened, so a merely
//     unreachable database stays ErrUnavailable.
//   - Nothing else proves the statement was rejected. A result read that fails
//     after the request was sent, a connection the driver abandoned, and a
//     caller that stops waiting mid-flight all leave the intent possibly
//     committed, so they are ErrUncertain and never carry a token.
//
// The decision uses the error alone and never reads the operation back: a
// read-back would be a second ambiguous observation, and the durable
// MAY_HAVE_SENT intent already protects every later attempt. A context that is
// already done is not classified here; Begin rejects it before writing anything.
func beginError(err error) error {
	if err == nil {
		return nil
	}
	var pg *pq.Error
	if errors.As(err, &pg) {
		switch string(pg.Code) {
		case pqConnectionFailure, pqTransactionResolutionUnknown, pqStatementCompletionUnknown:
			return ErrUncertain
		}
		return operationError(err)
	}
	var op *net.OpError
	if errors.As(err, &op) && op.Op == "dial" {
		// The connection was never established, so no intent was written.
		return ErrUnavailable
	}
	return ErrUncertain
}

// operationFingerprint binds the entire approved context — the full actor
// identity, the complete operation, the verified decision and the canonical
// payload — with unambiguous, length-prefixed framing. For a leased EMBED only
// the lease fence is normalized away: a fresh fence for the same claim is the
// same stored operation, while every in-attempt comparison still uses the real
// fence. The caller's Approval.Fingerprint is never hashed here, because it
// carries the unnormalized fence; only the digest is ever persisted.
func operationFingerprint(actor *auth.Identity, approval Approval, op Operation, payload Payload) (string, error) {
	binding := op
	if binding.Lease != nil && binding.Purpose == "EMBED" {
		lease := *binding.Lease
		lease.Fence = 0
		binding.Lease = &lease
	}
	identity, err := json.Marshal(actor)
	if err != nil {
		return "", ErrPolicy
	}
	operation, err := json.Marshal(binding)
	if err != nil {
		return "", ErrPolicy
	}
	decision, err := json.Marshal(approval.Decision)
	if err != nil {
		return "", ErrPolicy
	}
	canonical, err := json.Marshal(payload)
	if err != nil {
		return "", ErrPolicy
	}
	digest := sha256.New()
	writeField(digest, "dxos-model-operation-v2")
	writeField(digest, string(identity))
	writeField(digest, string(operation))
	writeField(digest, string(decision))
	writeField(digest, string(canonical))
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func writeField(digest hash.Hash, value string) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	_, _ = digest.Write(length[:])
	_, _ = digest.Write([]byte(value))
}
