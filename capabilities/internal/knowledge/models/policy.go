// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
package models

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
)

type Policy struct {
	Authority CurrentAuthority
	Enabled   bool
}

func (p *Policy) Check(ctx context.Context, actor *auth.Identity, req Operation) (Approval, error) {
	if p == nil || !p.Enabled || p.Authority == nil || actor == nil || actor.UserID == "" || req.Key == "" || len(req.Key) > 200 || req.ProfileID == "" || req.ProfileRevision < 1 || (req.ScopeKind != "PERSONAL" && req.ScopeKind != "APP") || len(req.Sources) > 50 {
		return Approval{}, ErrPolicy
	}
	d, err := p.Authority.Current(ctx, actor, req)
	if err != nil {
		return Approval{}, ErrUnavailable
	}
	profile := d.Profile
	if !d.Allowed || !profile.Enabled || profile.ID != req.ProfileID || profile.Revision != req.ProfileRevision || profile.Purpose != req.Purpose || profile.Limits.MaxInputTokens < 1 || profile.Limits.MaxOutputTokens < 1 || profile.Limits.MaxBytes < 1 || profile.PricingRevision == "" {
		return Approval{}, ErrPolicy
	}
	if profile.Execution != "LOCAL" && profile.Execution != "HOSTED" {
		return Approval{}, ErrPolicy
	}
	if profile.Execution == "HOSTED" && (req.ScopeKind == "PERSONAL" || d.Personal || d.QueryClassification != "NON_PERSONAL" && d.QueryClassification != "APPROVED") {
		return Approval{}, ErrPolicy
	}
	// The authority, never the request's QueryClassification, approves query egress.
	raw, _ := json.Marshal(struct {
		Actor     *auth.Identity
		Operation Operation
		Decision  Decision
	}{actor, req, d})
	hash := sha256.Sum256(raw)
	return Approval{Profile: profile, PolicyRevision: d.PolicyRevision, Fingerprint: hex.EncodeToString(hash[:])}, nil
}
