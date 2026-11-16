//go:build integration

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package models

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/opendx-os/opendx-os/capabilities/internal/testutil"
	"net/url"
	"os"
	"strings"
	"testing"
)

func TestGatewayDatabasePrivileges(t *testing.T) {
	db, _, req := budgetFixture(t)
	var name string
	if e := db.QueryRow(`SELECT current_database()`).Scan(&name); e != nil {
		t.Fatal(e)
	}
	u, _ := url.Parse(os.Getenv("TEST_DATABASE_URL"))
	u.Path = "/" + name
	gateway := testutil.As(t, u.String(), "dxos_ai_gateway")
	b := Budget{DB: gateway}
	r, e := b.Reserve(context.Background(), req)
	if e != nil {
		t.Fatal(e)
	}
	if e = b.Settle(context.Background(), r.ID, "done", Usage{Units: 5, PricingRevision: "price-1"}); e != nil {
		t.Fatal(e)
	}
	for _, q := range []string{`SELECT * FROM cap_ai.knowledge_chunks`, `SELECT * FROM cap_ai.knowledge_documents`, `UPDATE cap_ai.model_profiles SET enabled=false`, `DELETE FROM cap_ai.usage_ledger`, `UPDATE cap_ai.document_grants SET revoked_at=now()`} {
		if _, e = gateway.Exec(q); e == nil {
			t.Fatal("Gateway privilege widened", q)
		}
	}
	api := testutil.As(t, u.String(), "dxos_ai_api")
	if _, e = api.Exec(`SELECT cap_ai.reserve_model('{}')`); e == nil {
		t.Fatal("API can reserve budget directly")
	}
}

func TestRegistryDatabasePrivileges(t *testing.T) {
	db := modelDB(t, true)
	applyRegistry(t, db)
	var name string
	db.QueryRow(`SELECT current_database()`).Scan(&name)
	u, _ := url.Parse(os.Getenv("TEST_DATABASE_URL"))
	u.Path = "/" + name
	catalog := testutil.As(t, u.String(), "dxos_ai_model_catalog")
	gateway := testutil.As(t, u.String(), "dxos_ai_gateway")
	api := testutil.As(t, u.String(), "dxos_ai_api")
	for _, c := range []*sql.DB{gateway, api} {
		if _, e := c.Exec(`SELECT cap_ai.registry_import_candidates($1::jsonb)`, registryCandidate); e == nil {
			t.Fatal("runtime imported")
		}
	}
	for _, c := range []*sql.DB{catalog, gateway} {
		for _, q := range []string{`SELECT * FROM cap_ai.knowledge_chunks`, `SELECT * FROM cap_ai.model_candidates`, `SELECT * FROM cap_ai.model_profiles`, `UPDATE cap_ai.global_model_slots SET slot_revision=1`, `UPDATE cap_ai.model_profiles SET enabled=true`, `UPDATE cap_ai.document_grants SET revoked_at=now()`, `UPDATE cap_ai.processing_policies SET enabled=true`, `DELETE FROM cap_ai.model_slot_events`} {
			if _, e := c.Exec(q); e == nil {
				t.Fatal("privilege widened", q)
			}
		}
	}
	// A mistaken future EXECUTE grant still cannot substitute for deployment identity.
	if _, e := db.Exec(`GRANT EXECUTE ON FUNCTION cap_ai.registry_import_candidates(jsonb) TO dxos_ai_gateway`); e != nil {
		t.Fatal(e)
	}
	if _, e := gateway.Exec(`SELECT cap_ai.registry_import_candidates($1::jsonb)`, registryCandidate); e == nil {
		t.Fatal("session identity check bypassed")
	}
	if _, e := db.Exec(`REVOKE EXECUTE ON FUNCTION cap_ai.registry_import_candidates(jsonb) FROM dxos_ai_gateway`); e != nil {
		t.Fatal(e)
	}
	// Membership plus SET ROLE is insufficient: session_user must be the deployment login.
	if _, e := db.Exec(`GRANT dxos_ai_model_catalog TO dxos_ai_api`); e != nil {
		t.Fatal(e)
	}
	conn, e := api.Conn(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = conn.ExecContext(context.Background(), `SET ROLE dxos_ai_model_catalog`); e != nil {
		t.Fatal(e)
	}
	if _, e = conn.ExecContext(context.Background(), `SELECT cap_ai.registry_import_candidates($1::jsonb)`, registryCandidate); e == nil {
		t.Fatal("SET ROLE bypassed session authority")
	}
	if _, e = conn.ExecContext(context.Background(), `RESET ROLE`); e != nil {
		t.Fatal(e)
	}
	conn.Close()
	if _, e = db.Exec(`REVOKE dxos_ai_model_catalog FROM dxos_ai_api`); e != nil {
		t.Fatal(e)
	}
	// Concurrent equal imports agree on a single immutable revision.
	errors := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			_, e := catalog.Exec(`SELECT cap_ai.registry_import_candidates($1::jsonb)`, registryCandidate)
			errors <- e
		}()
	}
	for i := 0; i < 2; i++ {
		if e := <-errors; e != nil {
			t.Fatal(e)
		}
	}
	for i := 0; i < 2; i++ {
		if _, e := catalog.Exec(`SELECT cap_ai.registry_import_candidates($1::jsonb)`, registryCandidate); e != nil {
			t.Fatal(e)
		}
	}
	for _, bad := range []string{strings.Replace(registryCandidate, "model-test", "different", 1), strings.Replace(registryCandidate, "hosted-test", "other", 1), `null`, `{}`, `{"candidates":null}`, `{"candidates":[null]}`, `{"candidates":[],"activate":true}`, strings.Replace(registryCandidate, `"purpose":"GENERATE"`, `"purpose":"GENERATE","enabled":true`, 1), strings.Replace(registryCandidate, `"credential_ref":"TEST_KEY"`, `"credential_ref":"raw-secret"`, 1)} {
		if _, e := catalog.Exec(`SELECT cap_ai.registry_import_candidates($1::jsonb)`, bad); e == nil {
			t.Fatal("accepted invalid/conflicting declaration", bad)
		}
	}
	// A valid first item followed by an invalid item must roll back the entire import.
	var batch map[string]any
	if e := json.Unmarshal([]byte(strings.Replace(registryCandidate, "registry-test", "rollback-test", 1)), &batch); e != nil {
		t.Fatal(e)
	}
	batch["candidates"] = append(batch["candidates"].([]any), nil)
	batchJSON, _ := json.Marshal(batch)
	if _, e := catalog.Exec(`SELECT cap_ai.registry_import_candidates($1::jsonb)`, string(batchJSON)); e == nil {
		t.Fatal("partial invalid batch accepted")
	}
	var partial int
	if e := db.QueryRow(`SELECT count(*) FROM cap_ai.model_profiles WHERE id='rollback-test'`).Scan(&partial); e != nil || partial != 0 {
		t.Fatal("partial import persisted", partial, e)
	}
	var n int
	if e := db.QueryRow(`SELECT count(*) FROM cap_ai.model_profiles WHERE enabled`).Scan(&n); e != nil || n != 0 {
		t.Fatal(n, e)
	}
	var result string
	for _, capability := range []string{"EMBEDDING", "LLM", "VLM", "RERANKING", "OTHER"} {
		if e := gateway.QueryRow(`SELECT cap_ai.registry_resolve_slot($1)`, capability).Scan(&result); e != nil {
			t.Fatal(e)
		}
		var out map[string]any
		json.Unmarshal([]byte(result), &out)
		if out["status"] != "MODEL_UNAVAILABLE" {
			t.Fatal(result)
		}
	}
	if _, e := db.Exec(`UPDATE cap_ai.model_profiles SET enabled=true;UPDATE cap_ai.global_model_slots SET profile_id='registry-test',profile_revision=1,slot_revision=1 WHERE capability='LLM'`); e != nil {
		t.Fatal(e)
	}
	if e := gateway.QueryRow(`SELECT cap_ai.registry_resolve_slot('LLM')`).Scan(&result); e != nil {
		t.Fatal(e)
	}
	var out map[string]any
	json.Unmarshal([]byte(result), &out)
	profile, ok := out["profile"].(map[string]any)
	if !ok || profile["id"] != "registry-test" || profile["credential_ref"] != "TEST_KEY" {
		t.Fatal(result)
	}
}
