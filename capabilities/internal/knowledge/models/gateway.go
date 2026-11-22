// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package models

import (
	"context"
	"encoding/json"
	"math"
	"time"

	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
)

type Provider interface {
	Invoke(context.Context, Profile, Payload) (Result, error)
}

// Gateway owns the only dispatch transition: policy, claim, recheck, persisted
// intent, final authority check, one adapter invocation, validated finish.
type Gateway struct {
	Policy     *Policy
	Operations OperationLog
	Providers  map[string]Provider
}

func (g *Gateway) Call(ctx context.Context, actor *auth.Identity, op Operation, payload Payload) (Result, error) {
	if g == nil || g.Policy == nil || actor == nil {
		return Result{}, ErrUnavailable
	}
	if op.ScopeKind == "AGENT" && (payload.Query == "" || len(payload.Evidence) == 0) {
		return Result{}, ErrPolicy
	}
	if op.RequirePersonalQueryApproval && (payload.Query == "" || len(payload.Texts) != 0 || len(payload.Evidence) != 0) {
		return Result{}, ErrPolicy
	}
	approval, err := g.Policy.Check(ctx, actor, op)
	if err != nil {
		return Result{}, err
	}
	profile := approval.Profile
	provider := g.Providers[profile.ProviderID]
	if provider == nil || g.Operations == nil {
		return Result{}, ErrUnavailable
	}
	raw, err := json.Marshal(payload)
	// A byte is a conservative input-token upper bound, including fixed protocol
	// allowance. Adapter enforces these same limits at the model boundary.
	if err != nil || len(raw) > profile.Limits.MaxBytes || len(raw)+512 > profile.Limits.MaxInputTokens || len(payload.Evidence) > 8 || len(payload.Texts) > 1 {
		return Result{}, ErrPolicy
	}
	if profile.Limits.MaxInputTokens > 1000000 || profile.Limits.MaxOutputTokens > 1000000 {
		return Result{}, ErrPolicy
	}
	fingerprint, err := operationFingerprint(actor, approval, op, payload)
	if err != nil {
		return Result{}, err
	}
	state, err := g.Operations.Claim(ctx, OperationBinding{
		Key: op.Key, ActorID: actor.UserID, AppID: op.AppID, ProfileID: profile.ID,
		Revision: profile.Revision, Purpose: op.Purpose, Fingerprint: fingerprint,
	})
	if err != nil {
		return Result{}, err
	}
	switch state {
	case OperationClaimed:
		// A fresh or resumed claim is the only state that may start a dispatch.
	case OperationMayHaveSent, OperationSucceeded, OperationUncertain:
		return Result{}, ErrUncertain
	case OperationFailedUnsent:
		return Result{}, ErrPolicy
	default:
		return Result{}, ErrUncertain
	}
	// A leased worker EMBED claim is shared by whichever fence currently holds
	// the lease: a stale attempt leaves it for the next authorized one instead of
	// terminalizing it without a token.
	shared := op.Lease != nil && op.Purpose == "EMBED"
	// Recheck after the claim write. The first authority snapshot is never reused
	// once the database has serialized concurrent callers.
	fresh, err := g.Policy.Check(ctx, actor, op)
	if err != nil || fresh.Fingerprint != approval.Fingerprint {
		// The outcome is "definitely unsent" only once the store records it.
		// Without that write the row stays CLAIMED, so a later authorized attempt
		// can still begin: report retryable unavailability instead of a policy
		// block the database never stored. The shared leased-EMBED path records
		// nothing by design and keeps the claim for the next fence.
		if !shared && !g.record(ctx, op.Key, fingerprint, "", Finalization{State: OperationFailedUnsent, ErrorCode: codeAuthorityChanged}) {
			return Result{}, ErrUnavailable
		}
		return Result{}, ErrPolicy
	}
	token, err := g.Operations.Begin(ctx, op.Key, fingerprint)
	if err != nil {
		return Result{}, err
	}
	if token == "" {
		// A matching caller lost the begin race; its outcome is ambiguous.
		return Result{}, ErrUncertain
	}
	// The current authority is verified immediately before the possible network
	// side effect, so a revocation during the intent write still blocks egress.
	fresh, err = g.Policy.Check(ctx, actor, op)
	if err != nil || fresh.Fingerprint != approval.Fingerprint {
		// Nothing was sent, but persisted intent already committed: only this
		// caller's token can terminalize the row. If that write cannot be applied
		// the operation stays MAY_HAVE_SENT, which the claim path maps to
		// uncertainty, so the caller must not be told "definitely unsent".
		if !g.record(ctx, op.Key, fingerprint, token, Finalization{State: OperationFailedUnsent, ErrorCode: codeAuthorityChanged}) {
			return Result{}, ErrUncertain
		}
		return Result{}, ErrPolicy
	}
	// Persisted intent precedes the possible network side effect. A crash or
	// timeout leaves an ambiguous operation that is never redispatched.
	timeout := time.Duration(profile.Limits.TimeoutMS) * time.Millisecond
	if timeout <= 0 || timeout > 120*time.Second {
		timeout = 30 * time.Second
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	started := time.Now()
	result, callErr := provider.Invoke(callCtx, profile, payload)
	latency := time.Since(started).Milliseconds()
	if latency < 0 {
		latency = 0
	}
	if latency > 86400000 {
		latency = 86400000
	}
	if callErr != nil || callCtx.Err() != nil {
		g.record(ctx, op.Key, fingerprint, token, Finalization{State: OperationUncertain, ProviderRequestID: result.ProviderRequestID, ErrorCode: codeProviderUncertain, LatencyMS: latency})
		return Result{}, ErrUncertain
	}
	if !withinLimits(result.Usage, profile) {
		g.record(ctx, op.Key, fingerprint, token, Finalization{State: OperationUncertain, ProviderRequestID: result.ProviderRequestID, ErrorCode: codeInvalidUsage, LatencyMS: latency})
		return Result{}, ErrUncertain
	}
	if !validResult(profile, result) {
		g.record(ctx, op.Key, fingerprint, token, Finalization{State: OperationUncertain, ProviderRequestID: result.ProviderRequestID, ErrorCode: codeInvalidResult, LatencyMS: latency})
		return Result{}, ErrUncertain
	}
	if !g.record(ctx, op.Key, fingerprint, token, Finalization{State: OperationSucceeded, ProviderRequestID: result.ProviderRequestID, Usage: result.Usage, LatencyMS: latency}) {
		return Result{}, ErrUncertain
	}
	// A global query vector is usable only while its dispatch approval remains
	// current. The completed operation is still recorded if approval changed.
	if op.RequirePersonalQueryApproval {
		fresh, err := g.Policy.Check(ctx, actor, op)
		if err != nil || fresh.Fingerprint != approval.Fingerprint {
			return Result{}, ErrPolicy
		}
	}
	result.ProviderRequestID = ""
	return result, nil
}

// record finalizes under a bounded detached context so a canceled or
// disconnected caller can still record a known outcome.
func (g *Gateway) record(ctx context.Context, key, fingerprint, token string, f Finalization) bool {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	applied, err := g.Operations.Finish(cleanup, key, fingerprint, token, f)
	return err == nil && applied
}

func withinLimits(usage *Usage, profile Profile) bool {
	if usage == nil {
		return true
	}
	return usage.InputTokens >= 0 && usage.OutputTokens >= 0 &&
		usage.InputTokens <= int64(profile.Limits.MaxInputTokens) &&
		usage.OutputTokens <= int64(profile.Limits.MaxOutputTokens)
}

func validResult(profile Profile, result Result) bool {
	if len(result.Text) > profile.Limits.MaxBytes || len(result.Vector) > 16000 {
		return false
	}
	if profile.Purpose != "EMBED" {
		return true
	}
	if len(result.Vector) != profile.Dimension {
		return false
	}
	for _, value := range result.Vector {
		if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
			return false
		}
	}
	return true
}
