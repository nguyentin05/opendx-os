//go:build integration

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package agent

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
	"github.com/opendx-os/opendx-os/capabilities/internal/knowledge"
	"github.com/opendx-os/opendx-os/capabilities/internal/knowledge/models"
)

type endToEndChat struct {
	agentTurnFixture
	handler     http.Handler
	path, token string
	calls       chan models.Payload
}

func newEndToEndChat(t *testing.T) endToEndChat {
	t.Helper()
	calls := make(chan models.Payload, 10)
	spy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Purpose string
			Input   models.Payload
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(&req); err != nil {
			t.Errorf("adapter request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		select {
		case calls <- req.Input:
		default:
			t.Error("unexpected extra calls")
		}
		if req.Purpose == "EMBED" {
			_ = json.NewEncoder(w).Encode(models.Result{Vector: []float32{1, 0, 0}, Usage: &models.Usage{InputTokens: 3}})
			return
		}
		if req.Purpose != "GENERATE" {
			t.Errorf("unexpected egress: %s", req.Purpose)
		}
		var ids []string
		for _, evidence := range req.Input.Evidence {
			ids = append(ids, evidence.ID)
		}
		_ = json.NewEncoder(w).Encode(models.Result{Text: "Response about " + req.Input.Query, CitationIDs: ids, Usage: &models.Usage{InputTokens: 3, OutputTokens: 2}})
	}))
	t.Cleanup(spy.Close)
	provider, err := models.NewHTTPProvider(spy.URL, "", true)
	if err != nil {
		t.Fatal(err)
	}
	f := newAgentTurnFixture(t, provider)
	f.addPackage(t, "procurement", "role_procurement", "Laptop procurement policy for onboarding.")
	f.addPackage(t, "facilities", "role_facilities", "Locker policy for onboarding.")
	f.addPersonal(t, "Journal checklist for onboarding.")
	sources := f.retrieval.Search.(*knowledge.Store)
	sources.ConfigureModels(f.gateway, models.Profile{}, models.Profile{})
	sources.ConfigureModelResolver(f.resolver)
	verifier, token := signedChatIdentity(t, f.actor.UserID)
	handler := NewHandler(ChatAuthenticator{Verifier: verifier, DB: f.store.DB}, f.store, f.orchestrator(f.retrieval), f.retrieval.Scopes, sources)
	w := chatRequest(handler, "POST", ConversationsPath, token, "", `{"title":"Onboarding"}`)
	requireChatStatus(t, w, http.StatusCreated)
	conv := chatJSON[Conversation](t, w.Body.String())
	return endToEndChat{
		agentTurnFixture: f, handler: handler, path: ConversationsPath + "/" + conv.ID,
		token: token, calls: calls,
	}
}

func (c endToEndChat) turn(t *testing.T, key, query string) TurnResult {
	t.Helper()
	body, err := json.Marshal(map[string]string{"query": query})
	if err != nil {
		t.Fatal(err)
	}
	w := chatRequest(c.handler, "POST", c.path+"/messages", c.token, key, string(body))
	requireChatStatus(t, w, http.StatusOK)
	return chatJSON[TurnResult](t, w.Body.String())
}

func (c endToEndChat) call(t *testing.T) models.Payload {
	t.Helper()
	select {
	case input := <-c.calls:
		return input
	default:
		t.Fatal("missing adapter call")
		return models.Payload{}
	}
}

func TestAgentEndToEndCrossAppPersonalFollowup(t *testing.T) {
	c := newEndToEndChat(t)
	c.approveGeneration(t, "agent_billing", "reviewed billing question")
	c.approveGeneration(t, "library", "reviewed personal journal")
	first := c.turn(t, "first", "onboarding")
	if first.Status != "ANSWERED" || first.Answer == nil || first.MessageSeq != 2 || len(first.Citations) != 3 {
		t.Fatalf("mixed turn: %+v", first)
	}
	input := c.call(t)
	seen := map[string]bool{}
	for _, proof := range first.Citations {
		w := chatRequest(c.handler, "GET", proof.URL, c.token, "", "")
		requireChatStatus(t, w, http.StatusOK)
		current := chatJSON[knowledge.Citation](t, w.Body.String())
		seen[current.Snippet] = true
	}
	if len(input.Evidence) != 3 || !seen["Laptop procurement policy for onboarding."] || !seen["Locker policy for onboarding."] || !seen["Journal checklist for onboarding."] {
		t.Fatalf("mixed evidence: %+v %+v", input, seen)
	}
	second := c.turn(t, "second", "laptop")
	if second.Status != "ANSWERED" || second.MessageSeq != 4 || len(second.Citations) != 1 || !strings.Contains(strings.Join(c.call(t).Texts, " "), *first.Answer) {
		t.Fatalf("follow-up omitted authorized memory: %+v", second)
	}
}

func TestAgentEndToEndRevocation(t *testing.T) {
	c := newEndToEndChat(t)
	c.approveGeneration(t, "agent_billing", "reviewed billing question")
	personal := c.approveGeneration(t, "library", "reviewed personal journal")
	first := c.turn(t, "first", "journal")
	if first.Answer == nil || len(first.Citations) != 1 {
		t.Fatalf("first answer: %+v", first)
	}
	c.call(t)
	var disabled bool
	if err := c.admin.QueryRow("SELECT cap_ai.disable_agent_processing($1,$2,'review withdrawn')", c.actor.UserID, personal).Scan(&disabled); err != nil || !disabled {
		t.Fatal("disable personal review", err)
	}
	denied := c.turn(t, "revoked", "journal")
	if denied.Answer != nil || denied.Status != "SEARCH_ONLY" || denied.Reason != models.ErrPolicy.Error() || len(c.calls) != 0 {
		t.Fatalf("revoked policy reached generation: %+v", denied)
	}
	turnSQL(t, c.db, "DELETE FROM core.user_roles WHERE user_id=$1 AND role_name='role_library'", c.actor.UserID)
	requireChatStatus(t, chatRequest(c.handler, "GET", first.Citations[0].URL, c.token, "", ""), http.StatusNotFound)
	w := chatRequest(c.handler, "GET", c.path, c.token, "", "")
	requireChatStatus(t, w, http.StatusOK)
	if !strings.Contains(w.Body.String(), *first.Answer) {
		t.Fatal("missing owned history")
	}
	next := c.turn(t, "accessible", "laptop")
	input := c.call(t)
	if next.Status != "ANSWERED" || len(input.Evidence) != 1 ||
		!strings.Contains(input.Evidence[0].Text, "Laptop procurement") ||
		strings.Contains(strings.Join(input.Texts, " "), *first.Answer) {
		t.Fatalf("revoked context in new turn: %+v %+v", next, input)
	}
}

func TestAgentEndToEndMissingApprovalDoesNotCallProvider(t *testing.T) {
	for _, missing := range []string{"personal source", "billing query"} {
		t.Run(missing, func(t *testing.T) {
			c := newEndToEndChat(t)
			// Make ordinary selected-app query embedding available. The same
			// question in global chat needs its own PERSONAL query review.
			turnSQL(t, c.db, `INSERT INTO cap_ai.model_profiles(id,revision,purpose,execution,provider_id,model_id,dimension,limits,enabled)
                SELECT 'fixture-embed',1,'EMBED',execution,provider_id,'fixture-embed',3,limits,true
                FROM cap_ai.model_profiles WHERE id='fixture-llm';
                INSERT INTO cap_ai.model_candidates(profile_id,revision,adapter_key,declaration_digest,declaration)
                VALUES('fixture-embed',1,'bounded-http-v1',repeat('b',64),'{}');
                UPDATE cap_ai.global_model_slots SET profile_id='fixture-embed',profile_revision=1,slot_revision=1 WHERE capability='EMBEDDING';
                INSERT INTO cap_ai.processing_policy_profiles(policy_id,policy_version,model_profile_id,profile_revision)
                VALUES('procurement',1,'fixture-embed',1);
`)
			ordinary, err := c.retrieval.Search.Search(t.Context(), &auth.Identity{UserID: c.actor.UserID, Role: "role_procurement"},
				knowledge.SearchRequest{Scope: knowledge.Scope{Kind: "APP", AppID: "procurement"}, Query: "onboarding", Retrieval: "HYBRID"})
			if err != nil || ordinary.Status != "HYBRID" || c.call(t).Query != "onboarding" {
				t.Fatalf("ordinary APP query control: %+v, %v", ordinary, err)
			}
			if missing == "personal source" {
				c.approveGeneration(t, "agent_billing", "reviewed billing question")
			} else {
				c.approveGeneration(t, "library", "reviewed personal journal")
			}
			got := c.turn(t, "no-review", "onboarding")
			if got.Status != "SEARCH_ONLY" || got.Answer != nil || got.Reason != models.ErrPolicy.Error() || len(got.Citations) != 3 || len(c.calls) != 0 {
				t.Fatalf("missing %s: %+v, sends=%d", missing, got, len(c.calls))
			}
		})
	}
}

func signedChatIdentity(t *testing.T, subject string) (auth.SubjectVerifier, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	encode := base64.RawURLEncoding.EncodeToString
	jwk := map[string]string{"kid": "fixture", "alg": "RS256", "kty": "RSA", "use": "sig", "n": encode(key.N.Bytes()), "e": encode(big.NewInt(int64(key.E)).Bytes())}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{jwk}})
	}))
	t.Cleanup(server.Close)
	verifier, err := auth.NewSubjectVerifier(t.Context(), auth.VerifierConfig{IssuerURL: server.URL, JWKSURL: server.URL, Audience: "runtime"})
	if err != nil {
		t.Fatal(err)
	}
	claims, err := json.Marshal(map[string]any{"iss": server.URL, "aud": "runtime", "exp": time.Now().Add(time.Minute * 10).Unix(), "sub": subject})
	if err != nil {
		t.Fatal(err)
	}
	signed := encode([]byte(`{"kid":"fixture","alg":"RS256"}`)) + "." + encode(claims)
	digest := sha256.Sum256([]byte(signed))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return verifier, signed + "." + encode(sig)
}
