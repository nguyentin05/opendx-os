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
	if p == nil || !p.Enabled || p.Authority == nil || actor == nil || actor.UserID == "" || req.Key == "" || len(req.Key) > 200 || req.ProfileID == "" || req.ProfileRevision < 1 || !req.validScope() {
		return Approval{}, ErrPolicy
	}
	d, err := p.Authority.Current(ctx, actor, req)
	if err != nil {
		return Approval{}, ErrUnavailable
	}
	profile := d.Profile
	if !d.Allowed || !profile.Enabled || profile.ID != req.ProfileID || profile.Revision != req.ProfileRevision || profile.Purpose != req.Purpose || profile.Limits.MaxInputTokens < 1 || profile.Limits.MaxOutputTokens < 1 || profile.Limits.MaxBytes < 1 {
		return Approval{}, ErrPolicy
	}
	if profile.Execution != "LOCAL" && profile.Execution != "HOSTED" {
		return Approval{}, ErrPolicy
	}
	if req.ScopeKind == "AGENT" && (!d.PersonalApproved || d.QueryClassification != "PERSONAL") {
		return Approval{}, ErrPolicy
	}
	if profile.Execution == "HOSTED" {
		personal := req.ScopeKind == "PERSONAL" || req.RequirePersonalQueryApproval || d.Personal || d.QueryClassification == "PERSONAL"
		if personal {
			if !d.PersonalApproved || (req.Purpose != "EMBED" && req.Purpose != "GENERATE") || d.QueryClassification != "PERSONAL" {
				return Approval{}, ErrPolicy
			}
		} else if d.QueryClassification != "NON_PERSONAL" && d.QueryClassification != "APPROVED" {
			return Approval{}, ErrPolicy
		}
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

func (op Operation) validScope() bool {
	switch op.ScopeKind {
	case "APP", "PERSONAL":
		if op.LLMSlotRevision != 0 || len(op.AgentSources) != 0 {
			return false
		}
		if op.RequirePersonalQueryApproval {
			return op.Purpose == "EMBED" && op.EmbeddingSlotRevision > 0 && len(op.Sources) == 0 && op.Lease == nil
		}
		return op.EmbeddingSlotRevision == 0 && len(op.Sources) <= 50
	case "AGENT":
		return op.Purpose == "GENERATE" && op.AppID != "" && op.LLMSlotRevision > 0 && op.Lease == nil && len(op.Sources) == 0 &&
			op.EmbeddingSlotRevision == 0 && !op.RequirePersonalQueryApproval && len(op.AgentSources) > 0 && len(op.AgentSources) <= 50
	default:
		return false
	}
}
