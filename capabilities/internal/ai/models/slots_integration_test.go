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
	"testing"

	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
	"github.com/opendx-os/opendx-os/capabilities/internal/testutil"
)

func TestRegistrySlotUniqueness(t *testing.T) {
	db := modelDB(t, true)
	applyRegistry(t, db)
	for _, q := range []string{`INSERT INTO cap_ai.global_model_slots(capability) VALUES('LLM')`, `INSERT INTO cap_ai.global_model_slots(capability) VALUES('OTHER')`, `UPDATE cap_ai.global_model_slots SET slot_revision=-1 WHERE capability='LLM'`, `UPDATE cap_ai.global_model_slots SET profile_id='missing' WHERE capability='LLM'`} {
		if _, e := db.Exec(q); e == nil {
			t.Fatal(q)
		}
	}
}

const registryCandidate = `{"candidates":[{"adapter_key":"hosted-test","declaration_digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","profile":{"id":"registry-test","revision":1,"purpose":"GENERATE","execution":"HOSTED","provider_id":"test","model_id":"model-test","credential_ref":"TEST_KEY","limits":{"max_input_tokens":100,"max_output_tokens":100,"max_bytes":1024,"timeout_ms":1000}}}]}`

func resolverFixture(t *testing.T) (*sql.DB, *Registry) {
	t.Helper()
	db, importer, catalog := registryFixture(t)
	catalog.Models[0].Profile.Revision = 2
	if _, err := importer.Reconcile(context.Background(), catalog); err != nil {
		t.Fatal(err)
	}
	catalog.Models[0].Profile.Revision = 3
	if _, err := importer.Reconcile(context.Background(), catalog); err != nil {
		t.Fatal(err)
	}
	// Only the slot-bound revision may be live: at most one profile per purpose.
	if _, err := db.Exec(`UPDATE cap_ai.model_profiles SET enabled=(revision=2); UPDATE cap_ai.global_model_slots SET profile_id='candidate',profile_revision=2,slot_revision=1 WHERE capability='LLM'`); err != nil {
		t.Fatal(err)
	}
	var name string
	if err := db.QueryRow(`SELECT current_database()`).Scan(&name); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	return db, &Registry{DB: testutil.As(t, u.String(), "dxos_ai_gateway")}
}

func TestRegistryResolvePinnedRevision(t *testing.T) {
	db, r := resolverFixture(t)
	ctx := context.Background()
	first, err := r.Resolve(ctx, CapabilityLLM)
	if err != nil || first.Profile.ID != "candidate" || first.Profile.Revision != 2 || first.SlotRevision != 1 {
		t.Fatal(first, err)
	}
	for _, app := range []string{"app-a", "app-b"} {
		got, err := r.Resolve(context.WithValue(ctx, resolverAppContextKey{}, app), CapabilityLLM)
		if err != nil || got != first {
			t.Fatal(app, got, err)
		}
	}
	if _, err := db.Exec(`UPDATE cap_ai.model_profiles SET enabled=(revision=3); UPDATE cap_ai.global_model_slots SET profile_revision=3,slot_revision=2 WHERE capability='LLM'`); err != nil {
		t.Fatal(err)
	}
	fresh, err := r.Resolve(ctx, CapabilityLLM)
	if err != nil || fresh.Profile.Revision != 3 || fresh.SlotRevision != 2 || first.Profile.Revision != 2 {
		t.Fatal(first, fresh, err)
	}
}

type resolverAppContextKey struct{}

func TestRegistryResolveFailClosed(t *testing.T) {
	db, r := resolverFixture(t)
	ctx := context.Background()
	for _, c := range []Capability{"unknown", CapabilityEmbedding, CapabilityVLM, CapabilityReranking} {
		got, err := r.Resolve(ctx, c)
		if !errors.Is(err, ErrUnavailable) || got != (ActiveBinding{}) {
			t.Fatal(c, got, err)
		}
	}
	binding, err := r.Resolve(ctx, CapabilityLLM)
	if err != nil {
		t.Fatal(err)
	}
	// Revocation remains a per-call decision, even after resolution and approval.
	authority := &gatewayAuthority{profile: binding.Profile}
	g := Gateway{Policy: &Policy{Enabled: true, Authority: authority}}
	actor := &auth.Identity{UserID: "actor"}
	op := Operation{Key: "resolved-call", AppID: "app-a", ScopeKind: "APP", Purpose: "GENERATE", ProfileID: binding.Profile.ID, ProfileRevision: binding.Profile.Revision}
	if _, err := g.Policy.Check(ctx, actor, op); err != nil {
		t.Fatal(err)
	}
	authority.denied = true
	_, err = g.Call(ctx, actor, op, Payload{})
	if !errors.Is(err, ErrPolicy) {
		t.Fatal("resolved binding bypassed revoked policy", err)
	}
	for _, query := range []string{
		`UPDATE cap_ai.model_profiles SET enabled=false WHERE revision=2`,
		`SET session_replication_role=replica; UPDATE cap_ai.model_profiles SET enabled=true,purpose='EMBED',dimension=3 WHERE revision=2; SET session_replication_role=origin`,
	} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
		got, err := r.Resolve(ctx, CapabilityLLM)
		if !errors.Is(err, ErrUnavailable) || got != (ActiveBinding{}) {
			t.Fatal(got, err)
		}
	}
	// Privileged corruption fixtures exercise missing references despite production FKs.
	if _, err := db.Exec(`SET session_replication_role=replica; DELETE FROM cap_ai.model_profiles WHERE revision=2; SET session_replication_role=origin`); err != nil {
		t.Fatal(err)
	}
	if got, err := r.Resolve(ctx, CapabilityLLM); !errors.Is(err, ErrUnavailable) || got != (ActiveBinding{}) {
		t.Fatal(got, err)
	}
	if got, err := (&Registry{DB: db}).Resolve(ctx, CapabilityLLM); !errors.Is(err, ErrPolicy) || got != (ActiveBinding{}) {
		t.Fatal(got, err)
	}
	if err := r.DB.Close(); err != nil {
		t.Fatal(err)
	}
	if got, err := r.Resolve(ctx, CapabilityLLM); !errors.Is(err, ErrUnavailable) || got != (ActiveBinding{}) {
		t.Fatal(got, err)
	}
}

func TestRegistryRuntimeCatalogWithoutCandidates(t *testing.T) {
	db, registry := resolverFixture(t)
	var database string
	if err := db.QueryRow(`SELECT current_database()`).Scan(&database); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + database
	u.User = url.User("dxos_ai_gateway")
	file := t.TempDir() + "/catalog.json"
	if err = os.WriteFile(file, []byte(`{"schema_version":2,"providers":[{"id":"test","adapter":"bounded-http-v1","endpoint":"https://example.invalid"}],"models":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"RAG_MODELS_ENABLED": "true", "RAG_MODELS_QUALIFIED": "true", "RAG_SOURCE_QUALIFIED": "true", "RAG_METADATA_QUALIFIED": "true", "RAG_PARSER_QUALIFIED": "true", "RAG_GLOBAL_MODEL_REGISTRY_ENABLED": "true", "AI_GATEWAY_DATABASE_URL": u.String(), "RAG_MODEL_DEPLOYMENT_FILE": file}
	gateway, embed, generate, resolver, closeDB, err := RuntimeFromEnvironment(context.Background(), func(k string) string { return env[k] })
	defer closeDB()
	if err != nil || gateway == nil || resolver == nil || embed.ID != "" || generate.ID != "" {
		t.Fatal("runtime", err)
	}
	want, err := registry.Resolve(context.Background(), CapabilityLLM)
	if err != nil {
		t.Fatal(err)
	}
	got, err := resolver.Resolve(context.Background(), CapabilityLLM)
	if err != nil || got != want {
		t.Fatal(got, err)
	}
	for _, drift := range []ProviderConfig{
		{ID: "test", Adapter: "bounded-http-v1", Endpoint: "https://changed.invalid"},
		{ID: "test", Adapter: "bounded-http-v1", Endpoint: "https://example.invalid", LoopbackTest: true},
	} {
		resolver.providers["test"] = drift
		if _, err = resolver.Resolve(context.Background(), CapabilityLLM); err != ErrUnavailable {
			t.Fatal("provider drift accepted", drift, err)
		}
	}
	resolver.providers["test"] = ProviderConfig{ID: "test", Adapter: "bounded-http-v1", Endpoint: "https://example.invalid"}
	resolver.credentialRefs["test"] = "DIFFERENT"
	if _, err = resolver.Resolve(context.Background(), CapabilityLLM); err != ErrUnavailable {
		t.Fatal("credential mismatch accepted", err)
	}
	var count int
	if err = db.QueryRow(`SELECT count(*) FROM cap_ai.model_profiles`).Scan(&count); err != nil || count != 2 {
		t.Fatal("startup imported candidates", count, err)
	}
}

// Promotion and runtime selection use different logins and independent providers.
func TestRegistryIndependentHostedSlots(t *testing.T) {
	db, dsn := adminFixture(t)
	catalogLogin := testutil.As(t, dsn, "dxos_ai_model_catalog")
	admin := testutil.As(t, dsn, "dxos_ai_model_admin")
	registry := &Registry{DB: testutil.As(t, dsn, "dxos_ai_gateway")}
	catalog := Catalog{SchemaVersion: 2, Providers: []ProviderConfig{
		{ID: "embed-only", Adapter: "bounded-http-v1", Endpoint: "https://embed.invalid/v1/routes/old/call", CredentialRef: "EMBED_TOKEN"},
		{ID: "generate-only", Adapter: "bounded-http-v1", Endpoint: "https://generate.invalid/v1/routes/current/call", CredentialRef: "GENERATE_TOKEN"},
	}, Models: []CandidateDeclaration{}}
	for i, purpose := range []string{"EMBED", "GENERATE"} {
		p := Profile{ID: "independent-" + purpose, Revision: 1, Purpose: purpose, Execution: "HOSTED", ProviderID: catalog.Providers[i].ID, ModelID: "exact-model", CredentialRef: catalog.Providers[i].CredentialRef, Limits: Limits{MaxInputTokens: 1000, MaxOutputTokens: 100, MaxBytes: 1024, TimeoutMS: 1000}}
		if purpose == "EMBED" {
			p.Dimension = 3
		}
		catalog.Models = append(catalog.Models, CandidateDeclaration{Adapter: "bounded-http-v1", Profile: p})
	}
	if _, err := (&Registry{DB: catalogLogin}).Reconcile(context.Background(), catalog); err != nil {
		t.Fatal(err)
	}
	var enabled int
	if err := db.QueryRow(`SELECT count(*) FROM cap_ai.model_profiles WHERE id LIKE 'independent-%' AND enabled`).Scan(&enabled); err != nil || enabled != 0 {
		t.Fatal("import enabled profiles", enabled, err)
	}
	for i, slot := range []Capability{CapabilityEmbedding, CapabilityLLM} {
		if _, err := registry.Resolve(context.Background(), slot); !errors.Is(err, ErrUnavailable) {
			t.Fatal("unreviewed slot available", err)
		}
		p := catalog.Models[i].Profile
		digest := DeclarationDigest(p, "bounded-http-v1", catalog.Providers[i])
		if _, err := adminPromote(admin, string(slot), 0, p.ID, p.Revision, digest, adminReview(p.ID, p.Revision, digest, p.Purpose, p.Dimension)); err != nil {
			t.Fatal(err)
		}
	}
	embed, err := registry.Resolve(context.Background(), CapabilityEmbedding)
	if err != nil {
		t.Fatal(err)
	}
	generate, err := registry.Resolve(context.Background(), CapabilityLLM)
	if err != nil {
		t.Fatal(err)
	}
	if embed.Profile.ProviderID == generate.Profile.ProviderID || embed.Profile.CredentialRef == generate.Profile.CredentialRef {
		t.Fatal("slots lost independent provider credentials")
	}
	if embed.Profile.Purpose != "EMBED" || generate.Profile.Purpose != "GENERATE" {
		t.Fatal("slot purpose mismatch")
	}
	if _, err := adminChange(admin, adminChangeArgs{Actor: adminOperatorID, Slot: "LLM", Expected: 1, Action: "DISABLE", Review: `{"reason":"synthetic qualification"}`}); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Resolve(context.Background(), CapabilityLLM); !errors.Is(err, ErrUnavailable) {
		t.Fatal("disabled LLM available", err)
	}
	if got, err := registry.Resolve(context.Background(), CapabilityEmbedding); err != nil || got != embed {
		t.Fatal("LLM change affected EMBEDDING", got, err)
	}
}
