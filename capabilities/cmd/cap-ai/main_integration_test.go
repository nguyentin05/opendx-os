//go:build integration

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/opendx-os/opendx-os/capabilities/internal/agent"
	"github.com/opendx-os/opendx-os/capabilities/internal/knowledge"
	"github.com/opendx-os/opendx-os/capabilities/internal/testutil"
)

func TestChatHTTPRuntimeWithReadyBillingAppAndNoModels(t *testing.T) {
	db := testutil.Database(t)
	for _, path := range []string{
		"init/01_core_schema.sql",
		"init/02_capability_registry.sql",
		"init/03_app_registry.sql",
		"init/05_operator_deployments.sql",
		"init/06_runtime_roles.sql",
		"init/07_app_definitions.sql",
		"capabilities/cap_ai.sql",
		"migrations/cap_ai/0001_metadata_jobs.sql",
		"migrations/cap_ai/0006_agent_conversations.sql",
		"migrations/cap_ai/0007_agent_scopes.sql",
	} {
		migration, err := os.ReadFile("../../../platform/postgres/" + path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(t.Context(), string(migration)); err != nil {
			t.Fatalf("apply %s: %v", path, err)
		}
	}
	_, err := db.ExecContext(t.Context(), `INSERT INTO core.apps(app_id,display_name,version,status,spec) VALUES
		('ready_app','Ready app','1','Ready','{}'),('pending_app','Pending app','1','Pending','{}'),
		('removed_app','Removed app','1','Removed','{}')`)
	if err != nil {
		t.Fatal(err)
	}
	var name string
	if err := db.QueryRowContext(t.Context(), "SELECT current_database()").Scan(&name); err != nil {
		t.Fatal(err)
	}
	dsn, err := url.Parse(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	dsn.Path = "/" + name
	api := testutil.As(t, dsn.String(), "dxos_ai_api")
	if err := validateChatBillingApp(t.Context(), api, "ready_app"); err != nil {
		t.Fatal(err)
	}
	for _, appID := range []string{"", "pending_app", "removed_app", "missing_app"} {
		if err := validateChatBillingApp(t.Context(), api, appID); err == nil {
			t.Fatalf("accepted unavailable billing app %q", appID)
		}
	}

	if _, err := db.ExecContext(t.Context(), `INSERT INTO core.users(id,email,display_name)
		VALUES('22222222-2222-4222-8222-222222222222','chat@example.test','Chat user')`); err != nil {
		t.Fatal(err)
	}
	sourceStore := knowledge.NewStore(api)
	chat := newRuntimeChatHandler(agent.ChatAuthenticator{Verifier: verifierStub{}, DB: api}, sourceStore,
		nil, nil, "ready_app", false, false)
	h := newHandlerWithKnowledge(testAuth(), sourceStore, true, chat)
	const path = "/api/cap/ai/conversations"
	r := httptest.NewRequest("POST", path, strings.NewReader(`{"title":"Private chat"}`))
	r.Header.Set("Authorization", "Bearer test-token")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var conversation agent.Conversation
	if err := json.Unmarshal(w.Body.Bytes(), &conversation); err != nil || w.Code != http.StatusCreated || conversation.ID == "" {
		t.Fatalf("creation without selected role: status %d, response %s, error %v", w.Code, w.Body.String(), err)
	}
	r = httptest.NewRequest("POST", path+"/"+conversation.ID+"/messages", strings.NewReader(`{"query":"question"}`))
	r.Header.Set("Authorization", "Bearer test-token")
	r.Header.Set("Idempotency-Key", "turn")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var result agent.TurnResult
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || w.Code != http.StatusOK ||
		result.Status != "NO_EVIDENCE" || result.SearchStatus != "LEXICAL_ONLY" {
		t.Fatalf("lexical-only runtime: status %d, response %s, error %v", w.Code, w.Body.String(), err)
	}
}
