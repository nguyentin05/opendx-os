//go:build integration

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
	"github.com/opendx-os/opendx-os/capabilities/internal/knowledge"
)

type chatSubjectVerifier struct{}

func (chatSubjectVerifier) VerifySubject(_ context.Context, token string) (auth.Subject, error) {
	if token != alice && token != bob {
		return auth.Subject{}, auth.ErrUnauthenticated
	}
	return auth.Subject{UserID: token, ExpiresAt: time.Now().Add(time.Hour)}, nil
}

func chatJSON[T any](t *testing.T, payload string) T {
	t.Helper()
	var v T
	if err := json.Unmarshal([]byte(payload), &v); err != nil {
		t.Fatalf("invalid response %q: %v", payload, err)
	}
	return v
}

func createChatConversation(t *testing.T, h http.Handler, title string) Conversation {
	t.Helper()
	body, err := json.Marshal(map[string]string{"title": title})
	if err != nil {
		t.Fatal(err)
	}
	w := chatRequest(h, "POST", testChatPath, alice, "", string(body))
	requireChatStatus(t, w, http.StatusCreated)
	conv := chatJSON[Conversation](t, w.Body.String())
	if !validID(conv.ID) || conv.OwnerUserID != alice || conv.Title != title || conv.State != "ACTIVE" || conv.LastSeq != 0 {
		t.Fatalf("unexpected conversation: %+v", conv)
	}
	return conv
}

func TestChatHTTPCannotReadForeignConversation(t *testing.T) {
	adminDB, apiDB := scopeDB(t)
	store := &PostgresConversationStore{DB: apiDB}
	scopes := &PostgresScopeResolver{DB: apiDB}
	reader := knowledge.NewPolicy(apiDB)
	sourceStore := knowledge.NewStore(apiDB)
	reply := NewOrchestrator(store, NewWorkingMemory(nil, 0), &RetrievalCoordinator{Scopes: scopes, Search: sourceStore, Reader: reader}, reader, nil, nil, "")
	h := NewHandler(ChatAuthenticator{Verifier: chatSubjectVerifier{}, DB: apiDB}, store, reply, scopes, sourceStore)
	conv := createChatConversation(t, h, "Owner-only title")
	path := testChatPath + "/" + conv.ID
	for _, method := range []string{"GET", "DELETE", "POST"} {
		endpoint, key, body := path, "", ""
		if method == "POST" {
			endpoint, key, body = path+"/messages", "some-key", `{"query":"foreign message"}`
		}
		w := chatRequest(h, method, endpoint, bob, key, body)
		requireChatStatus(t, w, http.StatusNotFound)
		if strings.Contains(w.Body.String(), conv.Title) {
			t.Fatalf("foreign conversation title leaked: %s", w.Body.String())
		}
	}
	list := chatRequest(h, "GET", testChatPath, bob, "", "")
	requireChatStatus(t, list, http.StatusOK)
	if list.Body.String() != "{\"conversations\":[],\"next_cursor\":null}\n" {
		t.Fatalf("foreign list: %s", list.Body.String())
	}
	own := chatRequest(h, "GET", path, alice, "", "")
	requireChatStatus(t, own, http.StatusOK)
	page := chatJSON[struct {
		ConversationID string    `json:"conversation_id"`
		Messages       []Message `json:"messages"`
		NextAfterSeq   *int64    `json:"next_after_seq"`
	}](t, own.Body.String())
	if page.ConversationID != conv.ID || page.Messages == nil || len(page.Messages) != 0 || page.NextAfterSeq != nil {
		t.Fatalf("unexpected empty transcript: %s", own.Body.String())
	}
	second := createChatConversation(t, h, "Second")
	list = chatRequest(h, "GET", testChatPath+"?limit=1", alice, "", "")
	requireChatStatus(t, list, http.StatusOK)
	type conversationPage struct {
		Conversations []Conversation `json:"conversations"`
		NextCursor    *string        `json:"next_cursor"`
	}
	conversations := chatJSON[conversationPage](t, list.Body.String())
	if len(conversations.Conversations) != 1 || conversations.NextCursor == nil || *conversations.NextCursor != conversations.Conversations[0].ID {
		t.Fatalf("unexpected list pagination %s", list.Body.String())
	}
	list = chatRequest(h, "GET", testChatPath+"?limit=1&cursor="+*conversations.NextCursor, alice, "", "")
	requireChatStatus(t, list, http.StatusOK)
	last := chatJSON[conversationPage](t, list.Body.String())
	if len(last.Conversations) != 1 || last.NextCursor != nil ||
		!((last.Conversations[0].ID == conv.ID && *conversations.NextCursor == second.ID) ||
			(last.Conversations[0].ID == second.ID && *conversations.NextCursor == conv.ID)) {
		t.Fatalf("unexpected final list page %s", list.Body.String())
	}
	requireChatStatus(t, chatRequest(h, "DELETE", path, alice, "", ""), http.StatusNoContent)
	requireChatStatus(t, chatRequest(h, "GET", path, alice, "", ""), http.StatusNotFound)
	turnSQL(t, adminDB, "UPDATE core.users SET status='DISABLED' WHERE id=$1", alice)
	requireChatStatus(t, chatRequest(h, "GET", testChatPath, alice, "", ""), http.StatusUnauthorized)
}

func TestChatHTTPIdempotentPost(t *testing.T) {
	_, apiDB := scopeDB(t)
	store := &PostgresConversationStore{DB: apiDB}
	scopes := &PostgresScopeResolver{DB: apiDB}
	sourceStore := knowledge.NewStore(apiDB)
	var calls atomic.Int64
	started := make(chan struct{})
	resume := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-resume:
		default:
			close(resume)
		}
	})
	retrieve := turnRetrieverFunc(func(context.Context, Actor, string) (RetrievalResult, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		<-resume
		return RetrievalResult{SearchStatus: "LEXICAL_ONLY"}, nil
	})
	reply := NewOrchestrator(store, NewWorkingMemory(nil, 0), retrieve, knowledge.NewPolicy(apiDB), nil, nil, "")
	h := NewHandler(ChatAuthenticator{Verifier: chatSubjectVerifier{}, DB: apiDB}, store, reply, scopes, sourceStore)
	conv := createChatConversation(t, h, "Idempotent")
	path := testChatPath + "/" + conv.ID
	key, body := "same-key", `{"query":"What is the current policy?"}`
	finished := make(chan string, 1)
	go func() {
		w := chatRequest(h, "POST", path+"/messages", alice, key, body)
		if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("winning request: %d %s", w.Code, w.Body.String())
		}
		finished <- w.Body.String()
	}()
	select {
	case <-started:
	case result := <-finished:
		t.Fatalf("request finished before retrieval: %s", result)
	case <-time.After(3 * time.Second):
		t.Fatal("request did not reach retrieval")
	}
	pending := chatRequest(h, "POST", path+"/messages", alice, key, body)
	requireChatStatus(t, pending, http.StatusOK)
	result := chatJSON[TurnResult](t, pending.Body.String())
	if result.Status != "SEARCH_ONLY" || result.Reason != "TURN_PENDING" || result.Answer != nil || result.Citations == nil || len(result.Citations) != 0 {
		t.Fatalf("unexpected pending response: %s", pending.Body.String())
	}
	requireChatStatus(t, chatRequest(h, "POST", path+"/messages", alice, "other-key", body), http.StatusConflict)
	close(resume)
	var first string
	select {
	case first = <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("request did not complete")
	}
	replay := chatRequest(h, "POST", path+"/messages", alice, key, body)
	requireChatStatus(t, replay, http.StatusOK)
	if first != replay.Body.String() || calls.Load() != 1 || chatJSON[TurnResult](t, first).Status != "NO_EVIDENCE" {
		t.Fatalf("replay changed first result or reran work: %q -> %q (%d calls)", first, replay.Body.String(), calls.Load())
	}
	requireChatStatus(t, chatRequest(h, "POST", path+"/messages", alice, key, `{"query":"changed"}`), http.StatusConflict)
	page := chatRequest(h, "GET", path+"?limit=1", alice, "", "")
	requireChatStatus(t, page, http.StatusOK)
	if !strings.Contains(page.Body.String(), `"next_after_seq":1`) || !strings.Contains(page.Body.String(), `"role":"USER"`) || strings.Contains(page.Body.String(), `"role":"ASSISTANT"`) {
		t.Fatalf("unexpected first transcript page: %s", page.Body.String())
	}
	page = chatRequest(h, "GET", path+"?after_seq=1", alice, "", "")
	requireChatStatus(t, page, http.StatusOK)
	if !strings.Contains(page.Body.String(), `"next_after_seq":null`) || !strings.Contains(page.Body.String(), `"role":"ASSISTANT"`) || strings.Contains(page.Body.String(), `"role":"USER"`) {
		t.Fatalf("unexpected final transcript page: %s", page.Body.String())
	}
}

func TestChatHTTPProtectedCrossAppCitation(t *testing.T) {
	f := newAgentTurnFixture(t, nil)
	f.addPackage(t, "procurement", "role_procurement", "Laptop procedure requires purchase approval.")
	f.addPackage(t, "facilities", "role_facilities", "Laptop storage requires a locked cabinet.")
	scopes := f.retrieval.Scopes
	sourceStore := f.retrieval.Search.(*knowledge.Store)
	reply := NewOrchestrator(f.store, NewWorkingMemory(nil, 0), f.retrieval, f.reader, nil, nil, "agent_billing")
	h := NewHandler(ChatAuthenticator{Verifier: chatSubjectVerifier{}, DB: f.store.DB}, f.store, reply, scopes, sourceStore)
	conv := createChatConversation(t, h, "Cross-app")
	path := testChatPath + "/" + conv.ID
	w := chatRequest(h, "POST", path+"/messages", alice, "question", `{"query":"laptop"}`)
	requireChatStatus(t, w, http.StatusOK)
	result := chatJSON[TurnResult](t, w.Body.String())
	if result.Status != "SEARCH_ONLY" || result.SearchStatus != "LEXICAL_ONLY" || len(result.Citations) != 2 || result.Answer != nil {
		t.Fatalf("expected cross-app FTS fallback: %s", w.Body.String())
	}
	var revoked, remaining knowledge.Citation
	for _, citation := range result.Citations {
		if !strings.HasPrefix(citation.URL, path+"/citations/") || !strings.Contains(citation.URL, "message_seq=2") {
			t.Fatalf("citation escaped chat boundary: %+v", citation)
		}
		w = chatRequest(h, "GET", citation.URL, alice, "", "")
		requireChatStatus(t, w, http.StatusOK)
		current := chatJSON[knowledge.Citation](t, w.Body.String())
		if current.URL != citation.URL || current.ChunkID != citation.ChunkID || !strings.Contains(current.Snippet, "Laptop") {
			t.Fatalf("wrong canonical citation: %+v", current)
		}
		if strings.Contains(current.Snippet, "purchase approval") {
			revoked = citation
		} else {
			remaining = citation
		}
	}
	if revoked.URL == "" || remaining.URL == "" {
		t.Fatalf("missing one app citation: %+v", result.Citations)
	}
	for _, attempt := range []struct{ url, token string }{
		{revoked.URL, bob},
		{strings.Replace(revoked.URL, "message_seq=2", "message_seq=1", 1), alice},
	} {
		w = chatRequest(h, "GET", attempt.url, attempt.token, "", "")
		requireChatStatus(t, w, http.StatusNotFound)
		if strings.Contains(w.Body.String(), "purchase approval") || strings.Contains(w.Body.String(), revoked.Title) {
			t.Fatalf("unauthorized citation disclosure: %s", w.Body.String())
		}
	}
	requireChatStatus(t, chatRequest(h, "POST", path+"/messages", alice, "another", `{"query":"unmatched"}`), http.StatusOK)
	requireChatStatus(t, chatRequest(h, "GET", strings.Replace(revoked.URL, "message_seq=2", "message_seq=4", 1), alice, "", ""), http.StatusNotFound)
	requireChatStatus(t, chatRequest(h, "GET", revoked.URL+"&role=role_procurement", alice, "", ""), http.StatusBadRequest)
	turnSQL(t, f.db, "INSERT INTO core.roles(name,app_id) VALUES('role_procurement_alt','procurement')")
	turnSQL(t, f.db, "INSERT INTO core.user_roles(user_id,role_name) VALUES($1,'role_procurement_alt')", alice)
	turnSQL(t, f.db, "DELETE FROM core.user_roles WHERE user_id=$1 AND role_name='role_procurement'", alice)
	w = chatRequest(h, "GET", revoked.URL, alice, "", "")
	requireChatStatus(t, w, http.StatusNotFound)
	if strings.Contains(w.Body.String(), "purchase approval") || strings.Contains(w.Body.String(), revoked.Title) {
		t.Fatalf("revoked citation disclosure: %s", w.Body.String())
	}
	requireChatStatus(t, chatRequest(h, "GET", remaining.URL, alice, "", ""), http.StatusOK)
	w = chatRequest(h, "GET", path, alice, "", "")
	requireChatStatus(t, w, http.StatusOK)
	if !strings.Contains(w.Body.String(), "purchase approval") || strings.Contains(w.Body.String(), "scope_snapshot") || strings.Contains(w.Body.String(), "acting_role") {
		t.Fatalf("history lost its owned snapshot or leaked private provenance: %s", w.Body.String())
	}
	// Simulate publishing a new access ceiling for the same immutable source.
	turnSQL(t, f.db, `UPDATE core.apps SET publication_generation=2,
		spec=jsonb_set(jsonb_set(jsonb_set(spec, '{_publication,generation}', '2'),
			'{_publication,sources,0,authorization_generation}', '2'),
			'{knowledge,sources,0,access,allowedRoles}', '["role_procurement_alt"]')
		WHERE app_id='procurement'`)
	requireChatStatus(t, chatRequest(h, "GET", revoked.URL, alice, "", ""), http.StatusOK)
}
