// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package models

import (
	"context"
	"errors"
	"github.com/opendx-os/opendx-os/capabilities/internal/ai/contracts"
	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
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
	return Decision{Profile: Profile{ID: r.ProfileID, Revision: 1, Purpose: r.Purpose, Execution: "HOSTED", Enabled: true, Limits: Limits{MaxInputTokens: 4096, MaxOutputTokens: 256, MaxBytes: 16384}}, PolicyRevision: 1, Allowed: true, Personal: f.personal, PersonalApproved: f.approved, QueryClassification: f.classification}, nil
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

// A leased worker attempt is the same durable operation for every fence, while
// the in-attempt approval fingerprint still changes with the real fence.
func TestGatewayFingerprintNormalizesOnlyLeaseFence(t *testing.T) {
	profile := Profile{ID: "embed", Revision: 1, Purpose: "EMBED", Execution: "HOSTED", ProviderID: "fake", Enabled: true,
		Dimension: 3, Limits: Limits{MaxInputTokens: 4096, MaxOutputTokens: 20, MaxBytes: 8192}}
	p := Policy{Authority: fingerprintAuthority{profile: profile}, Enabled: true}
	actor := &auth.Identity{UserID: "actor"}
	payload := Payload{Texts: []string{"chunk text"}}
	scope := contracts.ReadScope{DocumentID: "doc", VersionID: "version", RunID: "run"}
	base := Operation{Key: "embed:job:chunk", AppID: "app", ScopeKind: "APP", Purpose: "EMBED", ProfileID: "embed", ProfileRevision: 1,
		Sources: []contracts.ReadScope{scope}, Lease: &LeaseRef{JobID: "job", WorkerID: "worker", RunID: "run", Fence: 1}}
	recovered := base
	recovered.Lease = &LeaseRef{JobID: "job", WorkerID: "worker", RunID: "run", Fence: 2}

	approval := func(op Operation) Approval {
		t.Helper()
		got, err := p.Check(context.Background(), actor, op)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	stored := func(op Operation) string {
		t.Helper()
		fingerprint, err := operationFingerprint(actor, approval(op), op, payload)
		if err != nil {
			t.Fatal(err)
		}
		return fingerprint
	}

	first, second := approval(base), approval(recovered)
	if first.Fingerprint == second.Fingerprint {
		t.Fatal("fence change did not change the full approval fingerprint")
	}
	if stored(base) != stored(recovered) {
		t.Fatal("fence change altered the stored worker embedding fingerprint")
	}
	for _, change := range []struct {
		name string
		op   func(*Operation)
	}{
		{"worker identity", func(o *Operation) { o.Lease.WorkerID = "other" }},
		{"lease run", func(o *Operation) { o.Lease.RunID = "other" }},
		{"lease job", func(o *Operation) { o.Lease.JobID = "other" }},
		{"source snapshot", func(o *Operation) { o.Sources = []contracts.ReadScope{{DocumentID: "other"}} }},
		{"profile revision", func(o *Operation) { o.ProfileRevision = 2 }},
	} {
		changed := recovered
		lease := *recovered.Lease
		changed.Lease = &lease
		change.op(&changed)
		if stored(changed) == stored(base) {
			t.Fatalf("%s did not change the stored binding", change.name)
		}
	}
	if stored(recovered) == stored2(t, actor, p, recovered, Payload{Texts: []string{"other chunk"}}) {
		t.Fatal("payload change did not change the stored binding")
	}
	// A query embedding has no lease, so its full binding is always stored.
	query := base
	query.Key, query.Lease, query.Sources, query.RequirePersonalQueryApproval = "query", nil, nil, false
	if stored2(t, actor, p, query, payload) == stored2(t, actor, p, query, Payload{Texts: []string{"other"}}) {
		t.Fatal("query embedding payload change ignored")
	}
}

func stored2(t *testing.T, actor *auth.Identity, p Policy, op Operation, payload Payload) string {
	t.Helper()
	approval, err := p.Check(context.Background(), actor, op)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint, err := operationFingerprint(actor, approval, op, payload)
	if err != nil {
		t.Fatal(err)
	}
	return fingerprint
}

type fingerprintAuthority struct{ profile Profile }

func (a fingerprintAuthority) Current(_ context.Context, _ *auth.Identity, op Operation) (Decision, error) {
	profile := a.profile
	profile.ID, profile.Revision, profile.Purpose = op.ProfileID, op.ProfileRevision, op.Purpose
	return Decision{Profile: profile, PolicyRevision: 7, Allowed: true, QueryClassification: "NON_PERSONAL"}, nil
}

// The retired slot revisions are dead fields, not ignored hints: a caller that
// still sets one — including the personal query-approval path, which used to
// return before looking at them — is denied instead of silently accepted.
func TestRetiredSlotRevisionsAreDenied(t *testing.T) {
	f := &policyFixture{personal: true, approved: true, classification: "PERSONAL"}
	p := Policy{Authority: f, Enabled: true}
	actor := &auth.Identity{UserID: "actor"}
	query := Operation{Key: "query", AppID: "app", ScopeKind: "PERSONAL", Purpose: "EMBED", ProfileID: "approved",
		ProfileRevision: 1, QueryClassification: "PERSONAL", RequirePersonalQueryApproval: true}
	if _, err := p.Check(context.Background(), actor, query); err != nil {
		t.Fatalf("the personal query approval was denied without a retired field: %v", err)
	}
	// The retired slot fields no longer exist, so a stale caller cannot express one
	// at all. The remaining shape rules of a personal query approval still fail
	// closed: it may carry no source proof and no lease.
	for _, change := range []struct {
		name string
		op   func(*Operation)
	}{
		{"source proof", func(o *Operation) { o.Sources = []contracts.ReadScope{{DocumentID: "doc"}} }},
		{"lease", func(o *Operation) { o.Lease = &LeaseRef{JobID: "job"} }},
		{"generation purpose", func(o *Operation) { o.Purpose = "GENERATE" }},
	} {
		op := query
		change.op(&op)
		if _, err := p.Check(context.Background(), actor, op); !errors.Is(err, ErrPolicy) {
			t.Fatalf("%s was ignored on a personal query approval: %v", change.name, err)
		}
	}
}
