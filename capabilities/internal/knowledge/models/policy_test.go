// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package models

import (
	"context"
	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
	"github.com/opendx-os/opendx-os/capabilities/internal/knowledge/contracts"
	"testing"
)

type policyFixture struct {
	personal       bool
	approved       bool
	classification string
	reads          int
}

func (f *policyFixture) Current(ctx context.Context, a *auth.Identity, r Operation) (Decision, error) {
	f.reads++
	return Decision{Profile: Profile{ID: r.ProfileID, Revision: 1, Purpose: r.Purpose, Execution: "HOSTED", Enabled: true, Limits: Limits{MaxInputTokens: 4096, MaxOutputTokens: 256, MaxBytes: 16384}, PricingRevision: "price-1"}, PolicyRevision: 1, Allowed: true, Personal: f.personal, PersonalApproved: f.approved, QueryClassification: f.classification}, nil
}
func TestPersonalHostedDeniedWithoutApproval(t *testing.T) {
	for _, purpose := range []string{"EMBED", "RERANK", "GENERATE", "OCR"} {
		for _, scope := range []string{"PERSONAL", "APP"} {
			f := &policyFixture{personal: scope == "APP", classification: "APPROVED"}
			p := Policy{Authority: f, Enabled: true}
			_, err := p.Check(context.Background(), &auth.Identity{UserID: "actor"}, Operation{Key: "key", AppID: "app", ScopeKind: scope, Purpose: purpose, ProfileID: "approved", ProfileRevision: 1, QueryClassification: "APPROVED", Sources: []contracts.ReadScope{{DocumentID: "doc"}}})
			if err == nil {
				t.Fatal("personal hosted egress allowed", purpose, scope)
			}
		}
	}
}
func TestUnknownQueryClassificationDenied(t *testing.T) {
	f := &policyFixture{classification: "UNKNOWN"}
	p := Policy{Authority: f, Enabled: true}
	op := Operation{Key: "key", AppID: "app", ScopeKind: "APP", Purpose: "GENERATE", ProfileID: "approved", ProfileRevision: 1, QueryClassification: "APPROVED", Sources: []contracts.ReadScope{{DocumentID: "doc"}}}
	if _, err := p.Check(context.Background(), &auth.Identity{UserID: "actor"}, op); err == nil {
		t.Fatal("client classification overrode authority")
	}
	f.classification = "APPROVED"
	if _, err := p.Check(context.Background(), &auth.Identity{UserID: "actor"}, op); err != nil {
		t.Fatal(err)
	}
	f.personal = true
	if _, err := p.Check(context.Background(), &auth.Identity{UserID: "actor"}, op); err == nil || f.reads != 3 {
		t.Fatal("stale approval reused")
	}
}

func TestPersonalApprovalRequiresAuthoritativeClassificationAndPurpose(t *testing.T) {
	for _, purpose := range []string{"EMBED", "GENERATE", "RERANK", "OCR"} {
		for _, classification := range []string{"PERSONAL", "UNKNOWN", "APPROVED", "NON_PERSONAL"} {
			f := &policyFixture{personal: true, approved: true, classification: classification}
			p := Policy{Authority: f, Enabled: true}
			op := Operation{Key: "key", AppID: "app", ScopeKind: "PERSONAL", Purpose: purpose, ProfileID: "approved", ProfileRevision: 1, QueryClassification: "PERSONAL"}
			_, err := p.Check(context.Background(), &auth.Identity{UserID: "actor"}, op)
			allowed := (purpose == "EMBED" || purpose == "GENERATE") && classification == "PERSONAL"
			if (err == nil) != allowed {
				t.Fatalf("purpose=%s classification=%s: %v", purpose, classification, err)
			}
		}
	}
}
