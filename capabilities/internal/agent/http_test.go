// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package agent

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
	"github.com/opendx-os/opendx-os/capabilities/internal/knowledge"
)

const testChatPath = "/api/cap/ai/conversations"
const testConversationID = "babababa-1111-2222-3333-aaaaaaaaaaaa"

type requestAuthFunc func(*http.Request) (Actor, error)

func (f requestAuthFunc) Authenticate(r *http.Request) (Actor, error) { return f(r) }

type replyFunc func(context.Context, Actor, TurnRequest) (TurnResult, error)

func (f replyFunc) Reply(ctx context.Context, actor Actor, req TurnRequest) (TurnResult, error) {
	return f(ctx, actor, req)
}

func httpActor(r *http.Request) (Actor, error) {
	if r.Header.Get("Authorization") != "Bearer owner" {
		return Actor{}, auth.ErrUnauthenticated
	}
	return Actor{UserID: "11111111-1111-1111-1111-111111111111"}, nil
}

func chatRequest(h http.Handler, method, path, token, key, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	if key != "" {
		r.Header.Set("Idempotency-Key", key)
	}
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func requireChatStatus(t *testing.T, w *httptest.ResponseRecorder, status int) {
	t.Helper()
	if w.Code != status || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("status or cache policy: got %d with %q and body %q, want %d and no-store",
			w.Code, w.Header().Get("Cache-Control"), w.Body.String(), status)
	}
}

func TestChatHTTPRejectsUnauthenticated(t *testing.T) {
	h := NewHandler(requestAuthFunc(httpActor), &PostgresConversationStore{}, replyFunc(nil), &PostgresScopeResolver{}, knowledge.NewStore(nil))
	for _, tc := range []struct{ method, path string }{
		{"GET", testChatPath},
		{"POST", testChatPath},
		{"GET", testChatPath + "/" + testConversationID},
		{"POST", testChatPath + "/" + testConversationID + "/messages"},
		{"DELETE", testChatPath + "/" + testConversationID},
		{"GET", testChatPath + "/" + testConversationID + "/citations/" + testConversationID},
	} {
		requireChatStatus(t, chatRequest(h, tc.method, tc.path, "", "", ""), http.StatusUnauthorized)
	}
}

func TestChatHTTPRejectsModelOverride(t *testing.T) {
	h := NewHandler(requestAuthFunc(httpActor), &PostgresConversationStore{}, replyFunc(func(context.Context, Actor, TurnRequest) (TurnResult, error) {
		t.Fatal("invalid input reached turn execution")
		return TurnResult{}, nil
	}), &PostgresScopeResolver{}, knowledge.NewStore(nil))
	path := testChatPath + "/" + testConversationID + "/messages"
	for _, tc := range []struct{ path, key, body string }{
		{path, "key", `{"query":"SECRET","provider":"override"}`},
		{path, "key", `{"query":"SECRET","model":"override"}`},
		{path, "key", `{"query":"SECRET","profile":"override"}`},
		{path, "key", `{"query":"SECRET","url":"override"}`},
		{path, "key", `{"query":"SECRET","role":"override"}`},
		{path, "key", `{"query":"SECRET","app_id":"override"}`},
		{path, "key", `{"query":"SECRET","query":"other"}`},
		{path, "key", `{"Query":"SECRET"}`},
		{path, "key", `{"query":"SECRET"} {}`},
		{path, "key", `{"query":null}`},
		{path, "key", `{"query":" "}`},
		{path, "key", `{"query":"SECRET"}` + strings.Repeat(" ", 64<<10)},
		{path + "?role=override", "key", `{"query":"SECRET"}`},
		{path, "", `{"query":"SECRET"}`},
	} {
		w := chatRequest(h, "POST", tc.path, "owner", tc.key, tc.body)
		requireChatStatus(t, w, http.StatusBadRequest)
		if strings.Contains(w.Body.String(), "SECRET") || strings.Contains(w.Body.String(), "override") {
			t.Fatalf("unsafe error %s", w.Body.String())
		}
	}
	r := httptest.NewRequest("POST", path, strings.NewReader(`{"query":"SECRET"}`))
	r.Header.Set("Authorization", "Bearer owner")
	r.Header.Add("Idempotency-Key", "first")
	r.Header.Add("Idempotency-Key", "second")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	requireChatStatus(t, w, http.StatusBadRequest)
}

func TestChatHTTPStrictPaginationAndBodies(t *testing.T) {
	h := NewHandler(requestAuthFunc(httpActor), &PostgresConversationStore{}, replyFunc(nil), &PostgresScopeResolver{}, knowledge.NewStore(nil))
	conversation := testChatPath + "/" + testConversationID
	for _, tc := range []struct{ method, path, body string }{
		{"GET", testChatPath + "?limit=51", ""},
		{"GET", testChatPath + "?limit=0", ""},
		{"GET", testChatPath + "?limit=1&limit=2", ""},
		{"GET", testChatPath + "?limit=%zz", ""},
		{"GET", testChatPath + "?cursor=", ""},
		{"GET", testChatPath + "?cursor=guessed", ""},
		{"GET", testChatPath, "{}"},
		{"GET", conversation + "?after_seq=-1", ""},
		{"GET", conversation + "?after_seq=%2B1", ""},
		{"GET", conversation + "?after_seq=9223372036854775808", ""},
		{"DELETE", conversation + "?app_id=injected", ""},
		{"DELETE", conversation, "{}"},
		{"POST", testChatPath, `{"title":"private","owner_user_id":"override"}`},
	} {
		requireChatStatus(t, chatRequest(h, tc.method, tc.path, "owner", "", tc.body), http.StatusBadRequest)
	}
}

func TestChatHTTPReturnsSafeUnavailable(t *testing.T) {
	h := NewHandler(requestAuthFunc(func(*http.Request) (Actor, error) {
		return Actor{}, errors.New("database detail: SECRET")
	}), &PostgresConversationStore{}, replyFunc(nil), &PostgresScopeResolver{}, knowledge.NewStore(nil))
	w := chatRequest(h, "GET", testChatPath, "owner", "", "")
	requireChatStatus(t, w, http.StatusServiceUnavailable)
	if strings.Contains(w.Body.String(), "SECRET") {
		t.Fatal(w.Body.String())
	}
}

func TestChatHTTPAcceptsBodyAtByteLimit(t *testing.T) {
	h := NewHandler(requestAuthFunc(httpActor), &PostgresConversationStore{}, replyFunc(func(_ context.Context, actor Actor, req TurnRequest) (TurnResult, error) {
		if actor.UserID == "" || req.Query != "question" || req.ConversationID != testConversationID || req.ClientTurnID != "key" {
			t.Fatalf("unexpected turn: %+v %+v", actor, req)
		}
		return TurnResult{Status: "NO_EVIDENCE"}, nil
	}), &PostgresScopeResolver{}, knowledge.NewStore(nil))
	path := testChatPath + "/" + testConversationID + "/messages"
	body := `{"query":"question"}`
	body += strings.Repeat(" ", (64<<10)-len(body))
	requireChatStatus(t, chatRequest(h, "POST", path, "owner", "key", body), http.StatusOK)
}
