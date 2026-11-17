//go:build integration

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package knowledge

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/opendx-os/opendx-os/capabilities/internal/knowledge/models"
	"github.com/opendx-os/opendx-os/capabilities/internal/testutil"
)

func TestGlobalAppSearchRequiresExactPersonalQueryReview(t *testing.T) {
	db, api, actor, profile := semanticFixture(t)
	applyHybridFixtureMigrations(t, db, "0005_model_registry.sql", "0007_agent_scopes.sql",
		"0008_personal_model_processing.sql", "0009_personal_hybrid.sql", "0010_agent_model_authority.sql")
	var name string
	if err := db.QueryRow(`SELECT current_database()`).Scan(&name); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	api.db = testutil.As(t, u.String(), "dxos_ai_api")
	gatewayDB := testutil.As(t, u.String(), "dxos_ai_gateway")
	adminDB := testutil.As(t, u.String(), "dxos_authz")
	if _, err := db.Exec(`
INSERT INTO core.user_roles(user_id, role_name) VALUES($1, 'role_operator');`, actor.UserID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
INSERT INTO core.apps(app_id,display_name,version,status,spec)
VALUES ('elsewhere','Other app','1','Ready','{}');`); err != nil {
		t.Fatal(err)
	}

	spy := &personalEmbeddingVectorProvider{documentVector: []float32{1, 0, 0}, queryVector: []float32{1, 0, 0}}
	gateway := &models.Gateway{
		Policy: &models.Policy{Enabled: true, Authority: models.DatabaseAuthority{DB: gatewayDB}},
		Budget: &models.Budget{DB: gatewayDB}, Providers: map[string]models.Provider{"fake": spy},
	}
	api.ConfigureModels(gateway, profile, models.Profile{})
	worker := workerStore(t, db)
	worker.ConfigureModels(gateway, profile, models.Profile{})
	ctx := context.Background()
	if err := api.EnqueueEmbeddings(ctx, actor, profile); err != nil {
		t.Fatal(err)
	}
	lease, err := worker.ClaimEmbedding(ctx, actor.UserID)
	if err != nil || lease == nil {
		t.Fatal("claiming APP embedding", lease, err)
	}
	if err := worker.BuildEmbeddings(ctx, *lease, profile); err != nil || spy.documentCalls != 1 || spy.queryCalls != 0 {
		t.Fatal("APP worker source approval", err, spy.documentCalls, spy.queryCalls)
	}

	req := SearchRequest{Scope: Scope{Kind: "APP", AppID: "library"}, Query: "unmatched", Retrieval: "HYBRID"}
	ordinary, err := api.Search(ctx, actor, req)
	if err != nil || ordinary.Status != "HYBRID" || len(ordinary.Results) != 1 || spy.queryCalls != 1 {
		t.Fatalf("ordinary selected-scope APP search should still embed: %+v err=%v calls=%d", ordinary, err, spy.queryCalls)
	}

	req.RequirePersonalQueryApproval = true
	req.Query = "Director"
	verifyLexicalOnly := func(expectedCalls int) {
		t.Helper()
		got, err := api.Search(ctx, actor, req)
		if err != nil || got.Status != "LEXICAL_ONLY" || len(got.Results) != 1 ||
			got.Results[0].DocumentID != ordinary.Results[0].DocumentID || !slices.Equal(got.Warnings, []string{"SEMANTIC_UNAVAILABLE"}) ||
			spy.queryCalls != expectedCalls {
			t.Fatalf("global query without exact PERSONAL approval: %+v err=%v calls=%d", got, err, spy.queryCalls)
		}
	}
	verifyLexicalOnly(1)

	approve := func(app string) string {
		t.Helper()
		raw := `{"profile_id":"embed","profile_revision":1,"provider_id":"fake","purpose":"EMBED",
		"source_policy_id":"local-only","source_policy_version":1,"source_classification":"PERSONAL",
		"query_app_id":"APP","query_classification":"PERSONAL","retention_evidence":"fixture review",
		"training_use_evidence":"fixture review","region_evidence":"fixture review"}`
		var id string
		if err := adminDB.QueryRow(`SELECT cap_ai.approve_agent_processing($1,$2::jsonb)`,
			actor.UserID, strings.Replace(raw, `"APP"`, `"`+app+`"`, 1)).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	approve("elsewhere")
	verifyLexicalOnly(1)

	review := approve("library")
	req.Query = "unmatched"
	hybrid, err := api.Search(ctx, actor, req)
	if err != nil || hybrid.Status != "HYBRID" || len(hybrid.Results) != 1 || spy.queryCalls != 2 {
		t.Fatalf("exact PERSONAL approval should allow authorized vector search: %+v err=%v calls=%d", hybrid, err, spy.queryCalls)
	}
	hit := hybrid.Results[0]
	read, err := NewPolicy(api.db).CanRead(ctx, actor, hit.DocumentID)
	if err != nil || hit.ReadScope != read || read.RunID == "" || hit.RunID != "" {
		t.Fatalf("native search did not retain its internal authority snapshot: hit=%+v read=%+v err=%v", hit, read, err)
	}
	encoded, err := json.Marshal(hit)
	if err != nil || strings.Contains(string(encoded), read.RunID) || strings.Contains(string(encoded), "policy_revision") {
		t.Fatalf("authority snapshot exposed in search JSON: %s, %v", encoded, err)
	}

	var disabled bool
	err = adminDB.QueryRow(`SELECT cap_ai.disable_agent_processing($1,$2,'fixture review withdrawn')`, actor.UserID, review).Scan(&disabled)
	if err != nil || !disabled {
		t.Fatal("disable exact query review", disabled, err)
	}
	req.Query = "Director"
	verifyLexicalOnly(2)
}
