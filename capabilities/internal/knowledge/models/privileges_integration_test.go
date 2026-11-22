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
	_, dsn := modelOperationFixture(t)
	gateway := testutil.As(t, dsn, "dxos_ai_gateway")
	// The gateway advances an operation only through the narrow SQL boundary.
	fingerprint := strings.Repeat("a", 64)
	var state string
	if e := gateway.QueryRow(`SELECT cap_ai.claim_model_operation('privilege-op',$1,'app','gen',1,'GENERATE',$2)`, actorID, fingerprint).Scan(&state); e != nil || state != "CLAIMED" {
		t.Fatal("gateway claim", state, e)
	}
	for _, q := range []string{`SELECT * FROM cap_ai.model_operations`, `SELECT dispatch_token FROM cap_ai.model_operations`, `UPDATE cap_ai.model_operations SET state='SUCCEEDED'`, `DELETE FROM cap_ai.model_operations`, `SELECT * FROM cap_ai.knowledge_chunks`, `SELECT * FROM cap_ai.knowledge_documents`, `UPDATE cap_ai.model_profiles SET enabled=false`, `UPDATE cap_ai.document_grants SET revoked_at=now()`} {
		if _, e := gateway.Exec(q); e == nil {
			t.Fatal("Gateway privilege widened", q)
		}
	}
	api := testutil.As(t, dsn, "dxos_ai_api")
	if _, e := api.Exec(`SELECT cap_ai.claim_model_operation('api-op',$1,'app','gen',1,'GENERATE',$2)`, actorID, fingerprint); e == nil {
		t.Fatal("API can claim a model operation directly")
	}
	if _, e := api.Exec(`SELECT * FROM cap_ai.model_operations`); e == nil {
		t.Fatal("API can read model operations")
	}
	worker := testutil.As(t, dsn, "dxos_ai_worker")
	if _, e := worker.Exec(`SELECT cap_ai.begin_model_operation('privilege-op',$1)`, fingerprint); e == nil {
		t.Fatal("worker can begin a model operation directly")
	}
}

func TestModelOperationPrivileges(t *testing.T) {
	db, dsn := modelOperationFixture(t)
	gateway := testutil.As(t, dsn, "dxos_ai_gateway")
	claim := func(key, fingerprint, purpose string) (string, error) {
		var state string
		e := gateway.QueryRow(`SELECT cap_ai.claim_model_operation($1,$2,'app','gen',1,$3,$4)`, key, actorID, purpose, fingerprint).Scan(&state)
		return state, e
	}
	begin := func(key, fingerprint string) (sql.NullString, error) {
		var token sql.NullString
		e := gateway.QueryRow(`SELECT cap_ai.begin_model_operation($1,$2)`, key, fingerprint).Scan(&token)
		return token, e
	}
	finish := func(key, fingerprint string, token, state, provider, input, output, code, latency any) (bool, error) {
		var applied bool
		e := gateway.QueryRow(`SELECT cap_ai.finish_model_operation($1,$2,$3,$4,$5,$6,$7,$8,$9)`, key, fingerprint, token, state, provider, input, output, code, latency).Scan(&applied)
		return applied, e
	}
	snapshot := func(key string) string {
		var value string
		if e := db.QueryRow(`SELECT to_jsonb(m)::text FROM cap_ai.model_operations m WHERE operation_key=$1`, key).Scan(&value); e != nil {
			t.Fatal(e)
		}
		return value
	}
	// Claim locks the full binding and returns the existing state for an exact repeat.
	first := strings.Repeat("1", 64)
	if state, e := claim("matrix-success", first, "GENERATE"); e != nil || state != "CLAIMED" {
		t.Fatal("claim", state, e)
	}
	if state, e := claim("matrix-success", first, "GENERATE"); e != nil || state != "CLAIMED" {
		t.Fatal("repeat claim", state, e)
	}
	for _, mismatch := range []struct{ key, fingerprint, purpose string }{{"matrix-success", strings.Repeat("2", 64), "GENERATE"}, {"matrix-success", first, "EMBED"}} {
		if _, e := claim(mismatch.key, mismatch.fingerprint, mismatch.purpose); e == nil || !strings.Contains(e.Error(), "operation conflict") {
			t.Fatal("binding mismatch accepted", mismatch, e)
		}
	}
	// Only CLAIMED -> MAY_HAVE_SENT issues a token; the second begin observes null.
	token, e := begin("matrix-success", first)
	if e != nil || !token.Valid {
		t.Fatal("begin", e)
	}
	if again, e := begin("matrix-success", first); e != nil || again.Valid {
		t.Fatal("second begin disclosed a dispatch token", e)
	}
	if _, e := begin("matrix-success", strings.Repeat("2", 64)); e == nil || !strings.Contains(e.Error(), "operation conflict") {
		t.Fatal("begin accepted a mismatched binding", e)
	}
	// A wrong token is rejected and changes nothing.
	before := snapshot("matrix-success")
	if applied, e := finish("matrix-success", first, "00000000-0000-4000-8000-000000000000", "SUCCEEDED", nil, int64(10), int64(5), nil, int64(100)); e != nil || applied {
		t.Fatal("wrong token applied", applied, e)
	}
	if after := snapshot("matrix-success"); after != before {
		t.Fatal("rejected finish mutated the row", after)
	}
	if applied, e := finish("matrix-success", first, token.String, "SUCCEEDED", nil, int64(10), int64(5), nil, int64(100)); e != nil || !applied {
		t.Fatal("successful finish", applied, e)
	}
	// Terminal rows are immutable; only an exact replay succeeds.
	before = snapshot("matrix-success")
	if applied, e := finish("matrix-success", first, token.String, "SUCCEEDED", nil, int64(11), int64(5), nil, int64(100)); e != nil || applied {
		t.Fatal("changed finalization metadata applied", applied, e)
	}
	if applied, e := finish("matrix-success", first, token.String, "UNCERTAIN", nil, nil, nil, "PROVIDER_TIMEOUT", nil); e != nil || applied {
		t.Fatal("terminal transition applied", applied, e)
	}
	if after := snapshot("matrix-success"); after != before {
		t.Fatal("terminal row mutated", after)
	}
	if applied, e := finish("matrix-success", first, token.String, "SUCCEEDED", nil, int64(10), int64(5), nil, int64(100)); e != nil || !applied {
		t.Fatal("exact replay rejected", applied, e)
	}
	if again, e := begin("matrix-success", first); e != nil || again.Valid {
		t.Fatal("terminal begin disclosed a token", e)
	}
	// A definite pre-intent rejection needs a null token and no usage facts.
	pre := strings.Repeat("3", 64)
	if state, e := claim("matrix-pre-intent", pre, "GENERATE"); e != nil || state != "CLAIMED" {
		t.Fatal("claim pre-intent", state, e)
	}
	if applied, e := finish("matrix-pre-intent", pre, nil, "SUCCEEDED", nil, nil, nil, nil, nil); e != nil || applied {
		t.Fatal("CLAIMED accepted a post-intent state", applied, e)
	}
	if applied, e := finish("matrix-pre-intent", pre, "00000000-0000-4000-8000-000000000000", "FAILED_UNSENT", nil, nil, nil, "PROVIDER_REJECTED", nil); e != nil || applied {
		t.Fatal("CLAIMED accepted a pre-intent failure with a token", applied, e)
	}
	if applied, e := finish("matrix-pre-intent", pre, nil, "FAILED_UNSENT", nil, nil, nil, "PROVIDER_REJECTED", int64(3)); e != nil || !applied {
		t.Fatal("pre-intent failure rejected", applied, e)
	}
	if applied, e := finish("matrix-pre-intent", pre, nil, "FAILED_UNSENT", nil, nil, nil, "PROVIDER_REJECTED", int64(3)); e != nil || !applied {
		t.Fatal("pre-intent replay rejected", applied, e)
	}
	if again, e := begin("matrix-pre-intent", pre); e != nil || again.Valid {
		t.Fatal("failed pre-intent begin disclosed a token", e)
	}
	// MAY_HAVE_SENT reaches UNCERTAIN, which retains no usage counters.
	uncertain := strings.Repeat("4", 64)
	if state, e := claim("matrix-uncertain", uncertain, "GENERATE"); e != nil || state != "CLAIMED" {
		t.Fatal("claim uncertain", state, e)
	}
	uncertainToken, e := begin("matrix-uncertain", uncertain)
	if e != nil || !uncertainToken.Valid {
		t.Fatal("begin uncertain", e)
	}
	if applied, e := finish("matrix-uncertain", uncertain, uncertainToken.String, "UNCERTAIN", "req-7", int64(1), int64(1), "PROVIDER_TIMEOUT", nil); e != nil || applied {
		t.Fatal("UNCERTAIN accepted usage counters", applied, e)
	}
	if applied, e := finish("matrix-uncertain", uncertain, uncertainToken.String, "UNCERTAIN", "req-7", nil, nil, "PROVIDER_TIMEOUT", nil); e != nil || !applied {
		t.Fatal("UNCERTAIN rejected", applied, e)
	}
	if applied, e := finish("matrix-uncertain", uncertain, uncertainToken.String, "SUCCEEDED", nil, int64(1), int64(1), nil, nil); e != nil || applied {
		t.Fatal("terminal UNCERTAIN transitioned", applied, e)
	}
	// Half-populated and negative counters are rejected without a constraint leak.
	invalid := strings.Repeat("5", 64)
	if state, e := claim("matrix-invalid", invalid, "GENERATE"); e != nil || state != "CLAIMED" {
		t.Fatal("claim invalid", state, e)
	}
	invalidToken, e := begin("matrix-invalid", invalid)
	if e != nil || !invalidToken.Valid {
		t.Fatal("begin invalid", e)
	}
	for _, bad := range []struct{ input, output any }{{int64(3), nil}, {int64(-1), int64(-1)}} {
		if applied, e := finish("matrix-invalid", invalid, invalidToken.String, "SUCCEEDED", nil, bad.input, bad.output, nil, nil); e != nil || applied {
			t.Fatal("invalid token pair applied", bad, applied, e)
		}
	}
	if applied, e := finish("matrix-invalid", invalid, invalidToken.String, "SUCCEEDED", nil, int64(3), int64(2), "PROVIDER_ERROR", nil); e != nil || applied {
		t.Fatal("SUCCEEDED accepted an error code", applied, e)
	}
	if applied, e := finish("matrix-invalid", invalid, invalidToken.String, "FAILED_UNSENT", "req-1", nil, nil, "PROVIDER_ERROR", nil); e != nil || applied {
		t.Fatal("FAILED_UNSENT accepted a provider id", applied, e)
	}
	// SQL constraints enforce the same state-specific data outside the functions.
	for _, bad := range []string{
		`INSERT INTO cap_ai.model_operations(operation_key,actor_id,app_id,profile_id,profile_revision,purpose,fingerprint,state,dispatch_token) VALUES('constraint-token',$1,'app','gen',1,'GENERATE',repeat('6',64),'CLAIMED',gen_random_uuid())`,
		`INSERT INTO cap_ai.model_operations(operation_key,actor_id,app_id,profile_id,profile_revision,purpose,fingerprint,state,input_tokens,output_tokens) VALUES('constraint-usage',$1,'app','gen',1,'GENERATE',repeat('6',64),'MAY_HAVE_SENT',1,1)`,
		`INSERT INTO cap_ai.model_operations(operation_key,actor_id,app_id,profile_id,profile_revision,purpose,fingerprint,state,dispatch_token,provider_request_id,error_code,finished_at) VALUES('constraint-provider',$1,'app','gen',1,'GENERATE',repeat('6',64),'FAILED_UNSENT',gen_random_uuid(),'req', 'X',now())`,
		`INSERT INTO cap_ai.model_operations(operation_key,actor_id,app_id,profile_id,profile_revision,purpose,fingerprint,state,dispatch_token,input_tokens,output_tokens,finished_at) VALUES('constraint-negative',$1,'app','gen',1,'GENERATE',repeat('6',64),'SUCCEEDED',gen_random_uuid(),-1,-1,now())`,
	} {
		if _, e := db.Exec(bad, actorID); e == nil {
			t.Fatal("constraint accepted invalid state data", bad)
		}
	}
	// The dispatch token never leaves the owner boundary.
	if _, e := gateway.Exec(`SELECT dispatch_token FROM cap_ai.model_operations`); e == nil {
		t.Fatal("gateway can read dispatch tokens")
	}
	api := testutil.As(t, dsn, "dxos_ai_api")
	worker := testutil.As(t, dsn, "dxos_ai_worker")
	for _, other := range []*sql.DB{api, worker} {
		if _, e := other.Exec(`SELECT * FROM cap_ai.model_operations`); e == nil {
			t.Fatal("unrelated session can read model operations")
		}
		if _, e := other.Exec(`UPDATE cap_ai.model_operations SET state='CLAIMED'`); e == nil {
			t.Fatal("unrelated session can write model operations")
		}
		if _, e := other.Exec(`SELECT cap_ai.finish_model_operation('matrix-invalid',$1,NULL,'FAILED_UNSENT',NULL,NULL,NULL,'X',NULL)`, invalid); e == nil {
			t.Fatal("unrelated session can finish operations")
		}
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
