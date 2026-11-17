//go:build integration

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package agent

import (
	"database/sql"
	"encoding/json"
	"net/url"
	"os"
	"testing"

	"github.com/opendx-os/opendx-os/capabilities/internal/testutil"
)

const alice = "11111111-1111-1111-1111-111111111111"
const bob = "22222222-2222-2222-2222-222222222222"

func agentDB(t *testing.T) *sql.DB {
	t.Helper()
	db := testutil.Database(t)
	for _, p := range []string{"init/01_core_schema.sql", "init/02_capability_registry.sql", "init/03_app_registry.sql", "init/05_operator_deployments.sql", "init/06_runtime_roles.sql", "init/07_app_definitions.sql", "capabilities/cap_ai.sql", "migrations/cap_ai/0001_metadata_jobs.sql", "migrations/cap_ai/0002_docir_fts.sql", "migrations/core/0003_publication_generation.sql", "migrations/cap_ai/0003_package_knowledge.sql", "migrations/cap_ai/0006_agent_conversations.sql"} {
		b, e := os.ReadFile("../../../platform/postgres/" + p)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = db.Exec(string(b)); e != nil {
			t.Fatalf("%s: %v", p, e)
		}
	}
	if _, e := db.Exec(`INSERT INTO core.users(id,email,display_name) VALUES ($1,'a@test','Alice'),($2,'b@test','Bob')`, alice, bob); e != nil {
		t.Fatal(e)
	}
	return db
}
func call(t *testing.T, db *sql.DB, q string, args ...any) map[string]any {
	t.Helper()
	var raw []byte
	if e := db.QueryRow(q, args...).Scan(&raw); e != nil {
		t.Fatal(e)
	}
	var v map[string]any
	if e := json.Unmarshal(raw, &v); e != nil {
		t.Fatal(e)
	}
	return v
}
func create(t *testing.T, db *sql.DB) string {
	t.Helper()
	return call(t, db, `SELECT cap_ai.agent_create_conversation($1,'title')`, alice)["id"].(string)
}
func api(t *testing.T, db *sql.DB) *sql.Conn {
	t.Helper()
	var name string
	if e := db.QueryRow(`SELECT current_database()`).Scan(&name); e != nil {
		t.Fatal(e)
	}
	u, e := url.Parse(os.Getenv("TEST_DATABASE_URL"))
	if e != nil {
		t.Fatal(e)
	}
	u.Path = "/" + name
	u.User = url.User("dxos_ai_api")
	roleDB, e := sql.Open("postgres", u.String())
	if e != nil {
		t.Fatal(e)
	}
	c, e := roleDB.Conn(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { c.Close(); roleDB.Close() })
	return c
}
func TestAgentConversationSchemaRoles(t *testing.T) {
	db := agentDB(t)
	c := api(t, db)
	for _, table := range []string{"agent_conversations", "agent_messages", "agent_message_sources"} {
		if _, e := c.ExecContext(t.Context(), `SELECT * FROM cap_ai.`+table); e == nil {
			t.Fatal("raw table readable", table)
		}
	}
	var raw []byte
	if e := c.QueryRowContext(t.Context(), `SELECT cap_ai.agent_create_conversation($1,'hello')`, alice).Scan(&raw); e != nil {
		t.Fatal(e)
	}
	var conv map[string]any
	if e := json.Unmarshal(raw, &conv); e != nil {
		t.Fatal(e)
	}
	id := conv["id"].(string)
	for _, q := range []string{`SELECT cap_ai.agent_list_conversations($1,10,NULL)`, `SELECT cap_ai.agent_get_messages($1,$2,10,0)`} {
		args := []any{alice}
		if q == `SELECT cap_ai.agent_get_messages($1,$2,10,0)` {
			args = append(args, id)
		}
		if e := c.QueryRowContext(t.Context(), q, args...).Scan(&raw); e != nil {
			t.Fatal(e)
		}
	}
	if e := c.QueryRowContext(t.Context(), `SELECT cap_ai.agent_begin_turn($1,$2,'key','hash','query')`, alice, id).Scan(&raw); e != nil {
		t.Fatal(e)
	}
	var msg map[string]any
	if e := json.Unmarshal(raw, &msg); e != nil {
		t.Fatal(e)
	}
	if e := c.QueryRowContext(t.Context(), `SELECT cap_ai.agent_finish_turn($1,$2,'{"response":{"answer":"ok"},"sources":[]}'::jsonb)`, alice, msg["id"]).Scan(&raw); e != nil {
		t.Fatal(e)
	}
	if e := c.QueryRowContext(t.Context(), `SELECT cap_ai.agent_delete_conversation($1,$2)`, alice, id).Scan(&raw); e != nil {
		t.Fatal(e)
	}
	if _, e := db.Exec(`SET ROLE dxos_ai_worker`); e != nil {
		t.Fatal(e)
	}
	defer db.Exec(`RESET ROLE`)
	if _, e := db.Exec(`SELECT cap_ai.agent_create_conversation($1,'no')`, alice); e == nil {
		t.Fatal("worker can create")
	}
}
func TestAgentConversationOwnerIsolation(t *testing.T) {
	db := agentDB(t)
	id := create(t, db)
	var raw []byte
	if e := db.QueryRow(`SELECT cap_ai.agent_list_conversations($1,10,NULL)`, bob).Scan(&raw); e != nil || string(raw) != "[]" {
		t.Fatal(string(raw), e)
	}
	if e := db.QueryRow(`SELECT cap_ai.agent_get_messages($1,$2,10,0)`, bob, id).Scan(&raw); e == nil {
		t.Fatal("other owner read")
	}
	if _, e := db.Exec(`UPDATE core.users SET status='DISABLED' WHERE id=$1`, alice); e != nil {
		t.Fatal(e)
	}
	if _, e := db.Exec(`SELECT cap_ai.agent_list_conversations($1,10,NULL)`, alice); e == nil {
		t.Fatal("disabled actor")
	}
}
func TestAgentBeginTurnIdempotent(t *testing.T) {
	db := agentDB(t)
	id := create(t, db)
	q := `SELECT cap_ai.agent_begin_turn($1,$2,$3,$4,$5)`
	a := call(t, db, q, alice, id, "key", "hash", "question")
	b := call(t, db, q, alice, id, "key", "hash", "question")
	if a["id"] != b["id"] {
		t.Fatal(a, b)
	}
	if _, e := db.Exec(q, alice, id, "key", "changed", "question"); e == nil {
		t.Fatal("changed hash accepted")
	}
	if _, e := db.Exec(q, alice, id, "next", "hash2", "next"); e == nil {
		t.Fatal("unfinished turn accepted")
	}
}
func TestAgentFinishTurnImmutable(t *testing.T) {
	db := agentDB(t)
	id := create(t, db)
	m := call(t, db, `SELECT cap_ai.agent_begin_turn($1,$2,'key','hash','question')`, alice, id)
	q := `SELECT cap_ai.agent_finish_turn($1,$2,$3::jsonb)`
	a := call(t, db, q, alice, m["id"], `{"response":{"answer":"first"},"sources":[]}`)
	b := call(t, db, q, alice, m["id"], `{"response":{"answer":"second"},"sources":[]}`)
	if a["id"] != b["id"] || a["response"].(map[string]any)["answer"] != "first" || b["response"].(map[string]any)["answer"] != "first" {
		t.Fatal(a, b)
	}
	next := call(t, db, `SELECT cap_ai.agent_begin_turn($1,$2,'next','hash2','next')`, alice, id)
	if next["seq"].(float64) != 3 {
		t.Fatal(next)
	}
}
func TestAgentConversationDeleteOwnerOnly(t *testing.T) {
	db := agentDB(t)
	id := create(t, db)
	if _, e := db.Exec(`SELECT cap_ai.agent_delete_conversation($1,$2)`, bob, id); e == nil {
		t.Fatal("other owner deleted")
	}
	call(t, db, `SELECT cap_ai.agent_delete_conversation($1,$2)`, alice, id)
	if _, e := db.Exec(`SELECT cap_ai.agent_get_messages($1,$2,10,0)`, alice, id); e == nil {
		t.Fatal("deleted visible")
	}
}

func TestAgentBeginTurnConcurrent(t *testing.T) {
	db := agentDB(t)
	id := create(t, db)
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, key := range []string{"one", "two"} {
		go func(k string) {
			<-start
			_, e := db.Exec(`SELECT cap_ai.agent_begin_turn($1,$2,$3,$3,$3)`, alice, id, k)
			results <- e
		}(key)
	}
	close(start)
	a, b := <-results, <-results
	if (a == nil) == (b == nil) {
		t.Fatalf("expected one pending turn, got %v and %v", a, b)
	}
}
func TestAgentFinishTurnProvenanceAndReplay(t *testing.T) {
	db := agentDB(t)
	id := create(t, db)
	m := call(t, db, `SELECT cap_ai.agent_begin_turn($1,$2,'key','hash','question')`, alice, id)
	if _, e := db.Exec(`INSERT INTO core.roles(name) VALUES('role_test')`); e != nil {
		t.Fatal(e)
	}
	if _, e := db.Exec(`INSERT INTO core.user_roles(user_id,role_name) VALUES ($1,'role_test')`, alice); e != nil {
		t.Fatal(e)
	}
	body := `{"response":{"status":"answered","answer":"first","citations":[{"label":"[1]"}],"usage":{"tokens":7}},"sources":[{"app_id":"app","scope_kind":"APP","acting_role":"role_test","document_id":"33333333-3333-3333-3333-333333333333","version_id":"44444444-4444-4444-4444-444444444444","chunk_id":"55555555-5555-5555-5555-555555555555","citation_label":"[1]","scope_snapshot":{"scope":"organization"}}]}`
	answer := call(t, db, `SELECT cap_ai.agent_finish_turn($1,$2,$3::jsonb)`, alice, m["id"], body)
	call(t, db, `SELECT cap_ai.agent_finish_turn($1,$2,$3::jsonb)`, alice, m["id"], `{"response":{"answer":"changed"},"sources":[]}`)
	if _, e := db.Exec(`DELETE FROM core.user_roles WHERE user_id=$1 AND role_name='role_test'`, alice); e != nil {
		t.Fatal(e)
	}
	replay := call(t, db, `SELECT cap_ai.agent_begin_turn($1,$2,'key','hash','question')`, alice, id)
	completion := replay["completion"].(map[string]any)
	if completion["id"] != answer["id"] || completion["response"].(map[string]any)["status"] != "answered" {
		t.Fatal(replay)
	}
	var raw []byte
	if e := db.QueryRow(`SELECT cap_ai.agent_get_messages($1,$2,10,0)`, alice, id).Scan(&raw); e != nil {
		t.Fatal(e)
	}
	var messages []map[string]any
	if e := json.Unmarshal(raw, &messages); e != nil {
		t.Fatal(e)
	}
	sources := messages[1]["sources"].([]any)
	if len(sources) != 1 || sources[0].(map[string]any)["citation_label"] != "[1]" {
		t.Fatal(messages)
	}
	if _, e := db.Exec(`SELECT cap_ai.agent_finish_turn($1,$2,'{}'::jsonb)`, bob, m["id"]); e == nil {
		t.Fatal("other actor finished")
	}
}

func TestAgentFinishTurnNullAnswerReplay(t *testing.T) {
	db := agentDB(t)
	id := create(t, db)
	m := call(t, db, `SELECT cap_ai.agent_begin_turn($1,$2,'key','hash','question')`, alice, id)
	body := `{"response":{"status":"SEARCH_ONLY","answer":null,"reason":"unavailable","citations":[{"label":"[1]"}]},"sources":[]}`
	answer := call(t, db, `SELECT cap_ai.agent_finish_turn($1,$2,$3::jsonb)`, alice, m["id"], body)
	if answer["content"] != "" {
		t.Fatal(answer)
	}
	replay := call(t, db, `SELECT cap_ai.agent_begin_turn($1,$2,'key','hash','question')`, alice, id)
	completion := replay["completion"].(map[string]any)
	response := completion["response"].(map[string]any)
	if completion["id"] != answer["id"] || response["answer"] != nil || response["status"] != "SEARCH_ONLY" || response["reason"] != "unavailable" || len(response["citations"].([]any)) != 1 {
		t.Fatal(replay)
	}
	next := call(t, db, `SELECT cap_ai.agent_begin_turn($1,$2,'next','hash2','next')`, alice, id)
	if next["seq"] != float64(3) {
		t.Fatal(next)
	}
}
