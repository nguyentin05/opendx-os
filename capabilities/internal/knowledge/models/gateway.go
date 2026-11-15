// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
package models

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
	"math"
	"time"
)

type Provider interface {
	Invoke(context.Context, Profile, Payload) (Result, error)
}
type Gateway struct {
	Policy    *Policy
	Budget    *Budget
	Providers map[string]Provider
}

func (g *Gateway) Call(ctx context.Context, actor *auth.Identity, op Operation, payload Payload) (Result, error) {
	approval, err := g.Policy.Check(ctx, actor, op)
	if err != nil {
		return Result{}, err
	}
	profile := approval.Profile
	provider := g.Providers[profile.ProviderID]
	if provider == nil || g.Budget == nil {
		return Result{}, ErrUnavailable
	}
	raw, err := json.Marshal(payload)
	// A byte is a conservative input-token upper bound, including fixed protocol
	// allowance. Adapter enforces these same limits at the model boundary.
	if err != nil || len(raw) > profile.Limits.MaxBytes || len(raw)+512 > profile.Limits.MaxInputTokens || len(payload.Evidence) > 8 || len(payload.Texts) > 1 {
		return Result{}, ErrPolicy
	}
	if profile.Limits.InputUnitPrice < 0 || profile.Limits.OutputUnitPrice < 0 || profile.Limits.MaxInputTokens > 1000000 || profile.Limits.MaxOutputTokens > 1000000 {
		return Result{}, ErrPolicy
	}
	in, out := int64(profile.Limits.MaxInputTokens), int64(profile.Limits.MaxOutputTokens)
	if profile.Limits.InputUnitPrice > math.MaxInt64/in || profile.Limits.OutputUnitPrice > math.MaxInt64/out {
		return Result{}, ErrPolicy
	}
	estimate := in * profile.Limits.InputUnitPrice
	outputCost := out * profile.Limits.OutputUnitPrice
	if estimate > math.MaxInt64-outputCost {
		return Result{}, ErrPolicy
	}
	estimate += outputCost
	if estimate < 1 {
		estimate = 1
	}
	hash := sha256.Sum256(append([]byte(approval.Fingerprint), raw...))
	r, err := g.Budget.Reserve(ctx, ReservationRequest{OperationKey: op.Key, ActorID: actor.UserID, AppID: op.AppID, ProfileID: profile.ID, ProfileRevision: profile.Revision, Estimate: estimate, PricingRevision: profile.PricingRevision, Fingerprint: hex.EncodeToString(hash[:])})
	if err != nil {
		return Result{}, err
	}
	if r.State != "RESERVED" {
		return Result{}, ErrUncertain
	}
	// Recheck after the reservation wait. Never reuse the first authority snapshot.
	fresh, err := g.Policy.Check(ctx, actor, op)
	if err != nil || fresh.Fingerprint != approval.Fingerprint {
		cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer stop()
		_ = g.Budget.finish(cleanup, r.ID, "CANCELLED", "")
		return Result{}, ErrPolicy
	}
	_, err = g.Budget.begin(ctx, r.ID)
	if err != nil {
		return Result{}, err
	}
	fresh, err = g.Policy.Check(ctx, actor, op)
	if err != nil || fresh.Fingerprint != approval.Fingerprint {
		cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer stop()
		_ = g.Budget.finish(cleanup, r.ID, "CANCELLED_UNSENT", "")
		return Result{}, ErrPolicy
	}
	// Persisted intent precedes the possible network side effect. Crash/timeout
	// leaves conservative funds; there is no automatic retry or expiration release.
	timeout := time.Duration(profile.Limits.TimeoutMS) * time.Millisecond
	if timeout <= 0 || timeout > 120*time.Second {
		timeout = 30 * time.Second
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	result, err := provider.Invoke(callCtx, profile, payload)
	finishCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer stop()
	if err != nil || callCtx.Err() != nil {
		_ = g.Budget.finish(finishCtx, r.ID, "UNCERTAIN", result.ProviderRequestID)
		return Result{}, ErrUncertain
	}
	u := result.Usage
	if u.InputTokens < 0 || u.OutputTokens < 0 || u.InputTokens > in || u.OutputTokens > out || len(result.Text) > profile.Limits.MaxBytes || len(result.Vector) > 16000 || profile.Purpose == "EMBED" && len(result.Vector) != profile.Dimension {
		_ = g.Budget.finish(finishCtx, r.ID, "UNCERTAIN", result.ProviderRequestID)
		return Result{}, ErrUncertain
	}
	for _, v := range result.Vector {
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			_ = g.Budget.finish(finishCtx, r.ID, "UNCERTAIN", result.ProviderRequestID)
			return Result{}, ErrUncertain
		}
	}
	u.Units = u.InputTokens*profile.Limits.InputUnitPrice + u.OutputTokens*profile.Limits.OutputUnitPrice
	u.PricingRevision = profile.PricingRevision
	if err = g.Budget.finish(finishCtx, r.ID, "SUCCEEDED", result.ProviderRequestID); err != nil {
		return Result{}, ErrUncertain
	}
	if err = g.Budget.Settle(finishCtx, r.ID, "provider-complete", u); err != nil {
		return Result{}, ErrUncertain
	}
	result.Usage = u
	result.ProviderRequestID = ""
	return result, nil
}
