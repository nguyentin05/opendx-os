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
	"strconv"

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
	var token sql.NullString
	if err := s.DB.QueryRowContext(ctx, `SELECT cap_ai.begin_model_operation($1,$2)`, key, fingerprint).Scan(&token); err != nil {
		return "", operationError(err)
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

// operationFingerprint binds the approved authority snapshot and the canonical
// payload with unambiguous, length-prefixed framing. For a leased EMBED only the
// lease fence is normalized away: a fresh fence for the same claim is the same
// stored operation, while every in-attempt comparison still uses the real fence.
func operationFingerprint(actor *auth.Identity, approval Approval, op Operation, payload Payload) (string, error) {
	binding := op
	if binding.Lease != nil && binding.Purpose == "EMBED" {
		lease := *binding.Lease
		lease.Fence = 0
		binding.Lease = &lease
	}
	sources, err := json.Marshal(binding.Sources)
	if err != nil {
		return "", ErrPolicy
	}
	agentSources, err := json.Marshal(binding.AgentSources)
	if err != nil {
		return "", ErrPolicy
	}
	canonical, err := json.Marshal(payload)
	if err != nil {
		return "", ErrPolicy
	}
	digest := sha256.New()
	writeField(digest, "dxos-model-operation-v1")
	writeField(digest, actor.UserID)
	writeField(digest, approval.Profile.ID)
	writeField(digest, strconv.FormatInt(approval.Profile.Revision, 10))
	writeField(digest, strconv.FormatInt(approval.PolicyRevision, 10))
	writeField(digest, binding.AppID)
	writeField(digest, binding.ScopeKind)
	writeField(digest, binding.Purpose)
	writeField(digest, strconv.FormatInt(binding.LLMSlotRevision, 10))
	writeField(digest, strconv.FormatInt(binding.EmbeddingSlotRevision, 10))
	writeField(digest, strconv.FormatBool(binding.RequirePersonalQueryApproval))
	if binding.Lease != nil {
		writeField(digest, "lease")
		writeField(digest, binding.Lease.JobID)
		writeField(digest, binding.Lease.WorkerID)
		writeField(digest, binding.Lease.RunID)
		writeField(digest, strconv.FormatInt(binding.Lease.Fence, 10))
	} else {
		writeField(digest, "no-lease")
	}
	writeField(digest, string(sources))
	writeField(digest, string(agentSources))
	writeField(digest, string(canonical))
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func writeField(digest hash.Hash, value string) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	_, _ = digest.Write(length[:])
	_, _ = digest.Write([]byte(value))
}
