//go:build integration

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
package models

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"sync"
	"testing"

	"github.com/opendx-os/opendx-os/capabilities/internal/testutil"
)

func registryFixture(t *testing.T) (*sql.DB, *Registry, Catalog) {
	t.Helper()
	db := modelDB(t, true)
	applyRegistry(t, db)
	var name string
	if err := db.QueryRow("SELECT current_database()").Scan(&name); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	r := &Registry{DB: testutil.As(t, u.String(), "dxos_ai_model_catalog")}
	c := Catalog{SchemaVersion: 1, Providers: []ProviderConfig{{ID: "test", Adapter: "bounded-http-v1", Endpoint: "https://example.invalid"}}, Models: []CandidateDeclaration{{Adapter: "bounded-http-v1", Profile: Profile{ID: "candidate", Revision: 1, Purpose: "GENERATE", Execution: "HOSTED", ProviderID: "test", ModelID: "model", PricingRevision: "p1", Limits: Limits{MaxInputTokens: 100, MaxOutputTokens: 100, MaxBytes: 1024, TimeoutMS: 1000}}}}}
	return db, r, c
}
func registryState(t *testing.T, db *sql.DB) string {
	t.Helper()
	var s string
	if err := db.QueryRow(`SELECT jsonb_build_object('slots',(SELECT jsonb_agg(to_jsonb(s) ORDER BY capability) FROM cap_ai.global_model_slots s),'events',(SELECT count(*) FROM cap_ai.model_slot_events))::text`).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}
func TestRegistryReconcileIdempotencyAndNoPromotion(t *testing.T) {
	db, r, c := registryFixture(t)
	before := registryState(t, db)
	ctx := context.Background()
	got, err := r.Reconcile(ctx, c)
	if err != nil || got != (ReconcileReport{Created: 1}) {
		t.Fatal(got, err)
	}
	got, err = r.Reconcile(ctx, c)
	if err != nil || got != (ReconcileReport{Unchanged: 1}) {
		t.Fatal(got, err)
	}
	var n int
	if err = db.QueryRow("SELECT count(*) FROM cap_ai.model_profiles WHERE NOT enabled").Scan(&n); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	c.Models[0].Profile.ModelID = "changed"
	if _, err = r.Reconcile(ctx, c); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	fresh := c.Models[0]
	fresh.Profile.ID = "a-rollback"
	c.Models = append([]CandidateDeclaration{fresh}, c.Models...)
	// Reconcile sorts by ID: the fresh item must insert before the conflict.
	if c.Models[0].Profile.ID >= c.Models[1].Profile.ID {
		t.Fatal("rollback fixture must insert before conflicting identity")
	}
	got, err = r.Reconcile(ctx, c)
	if !errors.Is(err, ErrConflict) || got != (ReconcileReport{}) {
		t.Fatal(got, err)
	}
	for _, query := range []string{
		"SELECT count(*) FROM cap_ai.model_profiles WHERE id='a-rollback'",
		"SELECT count(*) FROM cap_ai.model_candidates WHERE profile_id='a-rollback'",
	} {
		if err = db.QueryRow(query).Scan(&n); err != nil || n != 0 {
			t.Fatal("partial batch survived rollback", n, err)
		}
	}
	if err = db.QueryRow("SELECT count(*) FROM cap_ai.model_profiles").Scan(&n); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if registryState(t, db) != before {
		t.Fatal("import changed slots or events")
	}
	if _, err = (&Registry{DB: db}).Reconcile(ctx, c); !errors.Is(err, ErrPolicy) {
		t.Fatal("privileged session accepted", err)
	}
	c.Models = c.Models[:1]
	c.Models[0].Profile.Enabled = true
	if _, err = r.Reconcile(ctx, c); !errors.Is(err, ErrPolicy) {
		t.Fatal("constructed enabled catalog accepted", err)
	}
}
func TestRegistryRestartCannotRebindActiveSlot(t *testing.T) {
	db, r, c := registryFixture(t)
	ctx := context.Background()
	if _, err := r.Reconcile(ctx, c); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE cap_ai.model_profiles SET enabled=true WHERE id='candidate'; UPDATE cap_ai.global_model_slots SET profile_id='candidate',profile_revision=1,slot_revision=1 WHERE capability='LLM'`); err != nil {
		t.Fatal(err)
	}
	before := registryState(t, db)
	c.Models[0].Profile.Revision = 2
	var wg sync.WaitGroup
	reports := make(chan ReconcileReport, 2)
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); got, err := r.Reconcile(ctx, c); reports <- got; errs <- err }()
	}
	wg.Wait()
	close(reports)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	total := ReconcileReport{}
	for report := range reports {
		total.Created += report.Created
		total.Unchanged += report.Unchanged
	}
	if total != (ReconcileReport{Created: 1, Unchanged: 1}) {
		t.Fatal(total)
	}
	c.Models[0].Profile.Revision = 1
	if got, err := r.Reconcile(ctx, c); err != nil || got != (ReconcileReport{Unchanged: 1}) {
		t.Fatal(got, err)
	}
	if registryState(t, db) != before {
		t.Fatal("restart rebound slot")
	}
	var n int
	if err := db.QueryRow("SELECT count(*) FROM cap_ai.model_candidates").Scan(&n); err != nil || n != 2 {
		t.Fatal(n, err)
	}
}

func TestRegistrySanitizedDeclarationAndValidation(t *testing.T) {
	db, r, c := registryFixture(t)
	ctx := context.Background()
	c.Providers[0].CredentialRef = "MODEL_KEY"
	c.Models[0].Profile.CredentialRef = "MODEL_KEY"
	c.Models[0].Profile.Purpose = "EMBED"
	c.Models[0].Profile.Dimension = 3
	if got, err := r.Reconcile(ctx, c); err != nil || got != (ReconcileReport{Created: 1}) {
		t.Fatal(got, err)
	}
	var digest, ref string
	var dimension int
	if err := db.QueryRow(`SELECT c.declaration_digest,p.credential_ref,p.dimension FROM cap_ai.model_candidates c JOIN cap_ai.model_profiles p ON(p.id=c.profile_id AND p.revision=c.revision)`).Scan(&digest, &ref, &dimension); err != nil || len(digest) != 64 || ref != "MODEL_KEY" || dimension != 3 {
		t.Fatal(digest, ref, dimension, err)
	}
	c.Providers[0].Endpoint = "https://changed.invalid"
	if _, err := r.Reconcile(ctx, c); !errors.Is(err, ErrConflict) {
		t.Fatal("endpoint edit reused immutable revision", err)
	}
	c.Models[0].Profile.Revision = 2
	if _, err := r.Reconcile(ctx, c); err != nil {
		t.Fatal(err)
	}
	c.Models[0].Adapter = "arbitrary"
	if _, err := r.Reconcile(ctx, c); !errors.Is(err, ErrPolicy) {
		t.Fatal("constructed unsupported adapter accepted", err)
	}
	c.Models[0].Adapter = "bounded-http-v1"
	c.Models[0].Profile.Dimension = 0
	if _, err := r.Reconcile(ctx, c); !errors.Is(err, ErrPolicy) {
		t.Fatal("constructed invalid dimension accepted", err)
	}
	c.Models = []CandidateDeclaration{}
	if got, err := r.Reconcile(ctx, c); err != nil || got != (ReconcileReport{}) {
		t.Fatal(got, err)
	}
}
