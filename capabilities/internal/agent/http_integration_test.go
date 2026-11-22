//go:build integration

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
	"github.com/opendx-os/opendx-os/capabilities/internal/knowledge"
	"github.com/opendx-os/opendx-os/capabilities/internal/knowledge/models"
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
	if result.MessageSeq != 0 || result.Status != "SEARCH_ONLY" || result.Reason != "TURN_PENDING" || result.Answer != nil || result.Citations == nil || len(result.Citations) != 0 {
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
	if first != replay.Body.String() || calls.Load() != 1 || (chatJSON[TurnResult](t, first).Status != "NO_EVIDENCE" || chatJSON[TurnResult](t, first).MessageSeq != 2) {
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
	reply := NewOrchestrator(f.store, NewWorkingMemory(nil, 0), f.retrieval, f.reader, nil, nil, "agent_processing")
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

func (f agentTurnFixture) addPersonal(t *testing.T, text string) {
	t.Helper()
	const fileID = "33333333-3333-4333-8333-333333333333"
	turnSQL(t, f.db, `INSERT INTO core.apps(app_id,display_name,version,status,spec)
		VALUES('library','Library','1','Ready',
			'{"knowledge":{"version":1,"personalLibrary":{"enabled":true,"allowedRoles":["role_library"]}}}');
		INSERT INTO core.roles(name,app_id) VALUES('role_library','library');
		INSERT INTO core.app_capabilities(app_id,cap_id) VALUES('library','cap-ai');`)
	turnSQL(t, f.db, `INSERT INTO core.user_roles(user_id,role_name) VALUES($1,'role_library')`, f.actor.UserID)
	turnSQL(t, f.db, `INSERT INTO core.files
		(id,app_id,bucket,object_key,file_name,scan_status,sha256_hash,uploaded_by,mime_type,size_bytes,read_policy)
		VALUES($1,'library','test','notes','journal.pdf','CLEAN',repeat('a',64),$2,'application/pdf',4,'OWNER_ONLY')`, fileID, f.actor.UserID)
	identity := &auth.Identity{UserID: f.actor.UserID, Role: "role_library"}
	sourceStore := f.retrieval.Search.(*knowledge.Store)
	reg, err := sourceStore.Register(t.Context(), identity, "personal", knowledge.RegisterRequest{
		FileID: fileID, ParserProfile: "standard-text-v1",
	})
	if err != nil {
		t.Fatal(err)
	}
	var run string
	if err := f.db.QueryRow(`INSERT INTO cap_ai.processing_runs(version_id,ingestion_job_id,fencing_token,attempt_no,state)
		SELECT version_id,id,1,1,'COMPLETE' FROM cap_ai.ingestion_jobs WHERE version_id=$1 RETURNING id`, reg.VersionID).Scan(&run); err != nil {
		t.Fatal(err)
	}
	turnSQL(t, f.db, "UPDATE cap_ai.document_versions SET state='READY',active_run_id=$1 WHERE id=$2", run, reg.VersionID)
	turnSQL(t, f.db, "UPDATE cap_ai.knowledge_documents SET active_version_id=$1 WHERE id=$2", reg.VersionID, reg.DocumentID)
	f.addChunk(t, run, text)
}

// A potentially sent generation is persisted as SEARCH_ONLY /
// MODEL_CALL_UNCERTAIN. The normal turn handler returns HTTP 200, the same turn
// replays the persisted owner-only completion, and no second model call happens.
func TestChatHTTPUncertainTurnReturns200AndReplay(t *testing.T) {
	var sends atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sends.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer server.Close()
	provider, err := models.NewHTTPProvider(server.URL, "", true)
	if err != nil {
		t.Fatal(err)
	}
	f := newAgentTurnFixture(t, provider)
	f.addPackage(t, "procurement", "role_procurement", "New hires can request a laptop.")
	f.approveGeneration(t, "agent_processing", "reviewed processing fixture")
	checker := NewSourcePolicyChecker(f.reader, f.resolver, f.gateway.Policy, "agent_processing")
	reply := NewOrchestrator(f.store, NewWorkingMemory(checker, 0), f.retrieval, f.reader, f.resolver, f.gateway, "agent_processing")
	h := NewHandler(ChatAuthenticator{Verifier: chatSubjectVerifier{}, DB: f.store.DB}, f.store, reply, f.retrieval.Scopes, f.retrieval.Search.(*knowledge.Store))
	conv := createChatConversation(t, h, "Uncertain turn")
	path := testChatPath + "/" + conv.ID
	body := `{"query":"laptop"}`
	first := chatRequest(h, "POST", path+"/messages", alice, "uncertain-key", body)
	requireChatStatus(t, first, http.StatusOK)
	raw := first.Body.String()
	result := chatJSON[TurnResult](t, raw)
	if result.Status != "SEARCH_ONLY" || result.Reason != "MODEL_CALL_UNCERTAIN" || result.Answer != nil ||
		result.Usage != nil || len(result.Citations) != 0 || len(result.Sources) != 0 ||
		strings.Contains(raw, `"usage"`) || !strings.Contains(raw, `"citations":[]`) {
		t.Fatalf("uncertain completion escaped: %s", raw)
	}
	replay := chatRequest(h, "POST", path+"/messages", alice, "uncertain-key", body)
	requireChatStatus(t, replay, http.StatusOK)
	if replay.Body.String() != raw || sends.Load() != 1 {
		t.Fatalf("owner replay changed the persisted completion or sent again: %q -> %q (%d sends)", raw, replay.Body.String(), sends.Load())
	}
	// A client-supplied app or model profile is rejected, never accepted as an
	// override of the server-configured processing app: no operation row and no
	// changed attribution results from it.
	for _, override := range []string{
		`{"query":"laptop","app_id":"other"}`,
		`{"query":"laptop","profile":"other"}`,
		`{"query":"laptop","model_profile":"other"}`,
	} {
		w := chatRequest(h, "POST", path+"/messages", alice, "override-key", override)
		requireChatStatus(t, w, http.StatusBadRequest)
		if strings.Contains(w.Body.String(), "other") || strings.Contains(w.Body.String(), "laptop") {
			t.Fatalf("override request echoed its input: %s", w.Body.String())
		}
	}
	var state, app string
	var input, output sql.NullInt64
	var operations, foreign int
	if err := f.db.QueryRow(`SELECT count(*),count(*) FILTER (WHERE app_id<>'agent_processing')
		FROM cap_ai.model_operations`).Scan(&operations, &foreign); err != nil {
		t.Fatal(err)
	}
	if operations != 1 || foreign != 0 {
		t.Fatalf("client override changed operation attribution: %d operations, %d foreign app(s)", operations, foreign)
	}
	if err := f.db.QueryRow(`SELECT state,app_id,input_tokens,output_tokens FROM cap_ai.model_operations`).Scan(&state, &app, &input, &output); err != nil ||
		state != "UNCERTAIN" || app != "agent_processing" || input.Valid || output.Valid {
		t.Fatalf("uncertain operation: state=%s app=%s usage=%+v/%+v err=%v", state, app, input, output, err)
	}
}

// A raw uncertainty error reaching chatError is the public 503 code, never the
// generic authority body and never the existing MODEL_UNAVAILABLE.
func TestChatHTTPRawUncertaintyError(t *testing.T) {
	h := NewHandler(requestAuthFunc(httpActor), &PostgresConversationStore{}, replyFunc(func(context.Context, Actor, TurnRequest) (TurnResult, error) {
		return TurnResult{}, models.ErrUncertain
	}), &PostgresScopeResolver{}, knowledge.NewStore(nil))
	w := chatRequest(h, "POST", testChatPath+"/"+testConversationID+"/messages", "owner", "key", `{"query":"question"}`)
	requireChatStatus(t, w, http.StatusServiceUnavailable)
	if !strings.Contains(w.Body.String(), `"code":"MODEL_CALL_UNCERTAIN"`) || strings.Contains(w.Body.String(), "MODEL_UNAVAILABLE") {
		t.Fatalf("raw uncertainty mapping: %s", w.Body.String())
	}
}

func TestChatHTTPDisabledPersonalHistoryStaysOutOfGeneration(t *testing.T) {
	const privateAnswer = "Personal journal answer"
	var sends atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sends.Add(1)
		var req struct{ Input models.Payload }
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("invalid provider request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		answer := privateAnswer
		// The PERSONAL turn reports no usage, the APP turn reports a zero pair;
		// the public turn bodies must keep that distinction.
		var usage *models.Usage
		if req.Input.Query == "laptop" {
			if strings.Contains(strings.Join(req.Input.Texts, " "), privateAnswer) {
				t.Errorf("disabled PERSONAL assistant content reached generation: %v", req.Input.Texts)
			}
			answer = "Laptop purchase policy"
			usage = &models.Usage{}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(models.Result{Text: answer, CitationIDs: []string{"E1"}, Usage: usage})
	}))
	defer server.Close()
	provider, err := models.NewHTTPProvider(server.URL, "", true)
	if err != nil {
		t.Fatal(err)
	}
	f := newAgentTurnFixture(t, provider)
	f.addPackage(t, "procurement", "role_procurement", "Laptop purchase requires approval.")
	f.addPersonal(t, "Journal notes from my private collection.")
	f.approveGeneration(t, "library", "approved private source")
	f.approveGeneration(t, "agent_processing", "approved processing app query")
	build := func(personal bool) http.Handler {
		scopes := SourceKindGate{ScopeResolver: f.retrieval.Scopes, AppEnabled: true, PersonalEnabled: personal}
		retrieval := *f.retrieval
		retrieval.Scopes = scopes
		checker := NewSourcePolicyChecker(f.reader, f.resolver, f.gateway.Policy, "agent_processing")
		memory := NewWorkingMemory(scopes.FilterHistory(checker), 0)
		reply := NewOrchestrator(f.store, memory, &retrieval, f.reader, f.resolver, f.gateway, "agent_processing")
		return NewHandler(ChatAuthenticator{Verifier: chatSubjectVerifier{}, DB: f.store.DB},
			f.store, reply, scopes, f.retrieval.Search.(*knowledge.Store))
	}
	withPersonal := build(true)
	conv := createChatConversation(t, withPersonal, "Scoped model history")
	path := testChatPath + "/" + conv.ID
	first := chatRequest(withPersonal, "POST", path+"/messages", alice, "personal", `{"query":"journal"}`)
	requireChatStatus(t, first, http.StatusOK)
	prior := chatJSON[TurnResult](t, first.Body.String())
	if prior.Status != "ANSWERED" || prior.Answer == nil || *prior.Answer != privateAnswer || len(prior.Citations) != 1 {
		t.Fatalf("PERSONAL fixture is not generation-eligible: %s", first.Body.String())
	}
	if prior.Usage != nil || strings.Contains(first.Body.String(), `"usage"`) {
		t.Fatalf("absent usage serialized in the turn body: %s", first.Body.String())
	}

	appOnly := build(false)
	next := chatRequest(appOnly, "POST", path+"/messages", alice, "app", `{"query":"laptop"}`)
	requireChatStatus(t, next, http.StatusOK)
	if result := chatJSON[TurnResult](t, next.Body.String()); result.Status != "ANSWERED" {
		t.Fatalf("enabled APP did not generate: %s", next.Body.String())
	}
	if !strings.Contains(next.Body.String(), `"usage":{"input_tokens":0,"output_tokens":0}`) {
		t.Fatalf("reported zero usage did not survive the turn response: %s", next.Body.String())
	}
	rows, err := f.store.Messages(t.Context(), f.actor, conv.ID, 2, 2)
	if err != nil || len(rows) != 2 || len(rows[1].Sources) != 1 || rows[1].Sources[0].ScopeKind != "APP" {
		t.Errorf("new turn retained disabled source proofs: %+v, err=%v", rows, err)
	}
	requireChatStatus(t, chatRequest(appOnly, "GET", prior.Citations[0].URL, alice, "", ""), http.StatusNotFound)
	history := chatRequest(appOnly, "GET", path, alice, "", "")
	requireChatStatus(t, history, http.StatusOK)
	if !strings.Contains(history.Body.String(), privateAnswer) {
		t.Fatal("owned history lost its earlier PERSONAL answer")
	}
	replay := chatRequest(appOnly, "POST", path+"/messages", alice, "personal", `{"query":"journal"}`)
	requireChatStatus(t, replay, http.StatusOK)
	if replay.Body.String() != first.Body.String() || sends.Load() != 2 {
		t.Fatal("completed replay changed the stored answer or contacted the model")
	}
}
