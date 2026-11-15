//go:build integration

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
package knowledge

import (
	"context"
	"encoding/json"
	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
	"github.com/opendx-os/opendx-os/capabilities/internal/knowledge/models"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAnswerCurrentDatabaseRevocation(t *testing.T) {
	db, s, a, profile := semanticFixture(t)
	// The new revision is independently approved for GENERATE, with no vectors.
	if _, e := db.Exec(`INSERT INTO cap_ai.model_profiles(id,revision,purpose,execution,provider_id,model_id,limits,pricing_revision,enabled) SELECT 'gen',revision,'GENERATE',execution,provider_id,model_id,limits,pricing_revision,enabled FROM cap_ai.model_profiles WHERE id='embed';INSERT INTO cap_ai.processing_policy_profiles VALUES('reviewed',1,'gen',1,now());`); e != nil {
		t.Fatal(e)
	}
	profile.ID = "gen"
	profile.Purpose = "GENERATE"
	profile.Dimension = 0
	var called bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if _, e := db.Exec(`DELETE FROM core.user_roles WHERE user_id=$1`, a.UserID); e != nil {
			t.Error(e)
		}
		json.NewEncoder(w).Encode(models.Result{Text: "Director approval.", CitationIDs: []string{"E1"}, Usage: models.Usage{InputTokens: 10, OutputTokens: 5}})
	}))
	defer server.Close()
	provider, e := models.NewHTTPProvider(server.URL, "", true)
	if e != nil {
		t.Fatal(e)
	}
	g := &models.Gateway{Policy: &models.Policy{Enabled: true, Authority: models.DatabaseAuthority{DB: db}}, Budget: &models.Budget{DB: db}, Providers: map[string]models.Provider{"fake": provider}}
	service := AnswerService{Search: s, Reader: NewPolicy(db), Policy: g.Policy, Gateway: g, Profile: profile}
	response, e := service.Answer(context.Background(), a, AnswerRequest{Scope: Scope{Kind: "APP", AppID: "library"}, Query: "Director"})
	if e != nil || !called || response.Reason != "AUTHORIZATION_CHANGED" || response.Answer != nil || len(response.Results) != 0 || len(response.Citations) != 0 {
		t.Fatal(response, e, called)
	}
}
func TestEmbeddingLeaseLossPreservesLexical(t *testing.T) {
	db, s, a, profile := semanticFixture(t)
	provider := &embeddingProvider{before: func() {
		_, e := db.Exec(`UPDATE cap_ai.ingestion_jobs SET lease_until=now()-interval '1 second' WHERE kind='EMBED'`)
		if e != nil {
			t.Error(e)
		}
	}}
	s.ConfigureModels(&models.Gateway{Policy: &models.Policy{Enabled: true, Authority: models.DatabaseAuthority{DB: db}}, Budget: &models.Budget{DB: db}, Providers: map[string]models.Provider{"fake": provider}}, profile, models.Profile{})
	if e := s.EnqueueEmbeddings(context.Background(), a, profile); e != nil {
		t.Fatal(e)
	}
	lease, e := s.ClaimEmbedding(context.Background(), ownerID)
	if e != nil || lease == nil {
		t.Fatal(e)
	}
	if e = s.BuildEmbeddings(context.Background(), *lease, profile); e == nil {
		t.Fatal("stale vector published")
	}
	if _, e = db.Exec(`UPDATE cap_ai.ingestion_jobs SET attempts=max_attempts WHERE kind='EMBED'`); e != nil {
		t.Fatal(e)
	}
	if e = s.Recover(context.Background(), time.Now()); e != nil {
		t.Fatal(e)
	}
	result, e := s.Search(context.Background(), a, SearchRequest{Scope: Scope{Kind: "APP", AppID: "library"}, Query: "Director"})
	if e != nil || len(result.Results) != 1 {
		t.Fatal("embedding recovery destroyed lexical readiness", e, result)
	}
}
func TestAnswersHTTPContractAndPolicyErrors(t *testing.T) {
	_, s, a, _ := semanticFixture(t)
	s.personalEnabled = true
	for _, test := range []struct {
		body   string
		status int
	}{{`{"scope":{"kind":"APP","app_id":"library"},"query":"Director"}`, 200}, {`{"scope":{"kind":"APP","app_id":"library"},"query":"Director","require_generation":true}`, 403}, {`{"scope":{"kind":"APP","app_id":"library"},"query":"Director","query_classification":"NON_PERSONAL"}`, 400}} {
		r := httptest.NewRequest("POST", "/api/cap/ai/answers", strings.NewReader(test.body))
		r = r.WithContext(auth.WithIdentity(r.Context(), a))
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != test.status {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}

type localAnswerProvider struct{ calls int }

func (p *localAnswerProvider) Invoke(context.Context, models.Profile, models.Payload) (models.Result, error) {
	p.calls++
	return models.Result{Text: "Director approval", CitationIDs: []string{"E1"}, Usage: models.Usage{InputTokens: 10, OutputTokens: 5}}, nil
}
func TestQualifiedLocalPersonalAnswer(t *testing.T) {
	db, s, a, profile := semanticFixture(t)
	activateText(t, s, workerStore(t, db), a, fileID, "local-personal", "Director approval private evidence")
	if _, e := db.Exec(`INSERT INTO cap_ai.model_profiles(id,revision,purpose,execution,provider_id,model_id,limits,pricing_revision,enabled) SELECT 'local-gen',revision,'GENERATE','LOCAL',provider_id,model_id,limits,pricing_revision,enabled FROM cap_ai.model_profiles WHERE id='embed';INSERT INTO cap_ai.processing_policy_profiles VALUES('local-only',1,'local-gen',1,now());`); e != nil {
		t.Fatal(e)
	}
	profile.ID = "local-gen"
	profile.Purpose = "GENERATE"
	profile.Execution = "LOCAL"
	profile.Dimension = 0
	provider := &localAnswerProvider{}
	g := &models.Gateway{Policy: &models.Policy{Enabled: true, Authority: models.DatabaseAuthority{DB: db}}, Budget: &models.Budget{DB: db}, Providers: map[string]models.Provider{"fake": provider}}
	service := AnswerService{Search: s, Reader: NewPolicy(db), Policy: g.Policy, Gateway: g, Profile: profile}
	response, e := service.Answer(context.Background(), a, AnswerRequest{Scope: Scope{Kind: "PERSONAL"}, Query: "Director", RequireGeneration: true})
	if e != nil || response.Status != "ANSWERED" || provider.calls != 1 {
		t.Fatal("qualified local personal answer failed", response, e, provider.calls)
	}
}
