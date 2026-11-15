//go:build integration

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
package knowledge

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/lib/pq"
	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
	"github.com/opendx-os/opendx-os/capabilities/internal/knowledge/docir"
	"github.com/opendx-os/opendx-os/capabilities/internal/knowledge/models"
	"os"
	"strings"
	"testing"
	"time"
)

func semanticFixture(t *testing.T) (*sql.DB, *Store, *auth.Identity, models.Profile) {
	t.Helper()
	db, s, a, hash := packageFixture(t)
	ctx := context.Background()
	if err := s.ReconcilePackage(ctx, "library", 1, repeatHash("b")); err != nil {
		t.Fatal(err)
	}
	worker := workerStore(t, db)
	lease, e := worker.Claim(ctx, ownerID, time.Now())
	if e != nil || lease == nil {
		t.Fatal(e)
	}
	result := resultFixture("Director approval for amounts above 10000000 VND")
	result.SourceSHA256 = hash
	chunks, e := docir.Chunk(result, docir.DefaultChunkRecipe())
	if e != nil {
		t.Fatal(e)
	}
	if e = worker.SaveRun(ctx, *lease, result, chunks); e != nil {
		t.Fatal(e)
	}
	if e = worker.Activate(ctx, *lease, lease.RunID); e != nil {
		t.Fatal(e)
	}
	raw, e := os.ReadFile("../../../platform/postgres/migrations/cap_ai/0004_models_vectors.sql")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(string(raw)); e != nil {
		t.Fatal(e)
	}
	profile := models.Profile{ID: "embed", Revision: 1, Purpose: "EMBED", Execution: "HOSTED", ProviderID: "fake", ModelID: "fake", Dimension: 3, Enabled: true, PricingRevision: "price-1", Limits: models.Limits{MaxInputTokens: 8192, MaxOutputTokens: 512, MaxBytes: 16384, InputUnitPrice: 1, OutputUnitPrice: 1, TimeoutMS: 500}}
	limits, _ := json.Marshal(profile.Limits)
	_, e = db.Exec(strings.NewReplacer("$1", pq.QuoteLiteral(string(limits)), "$2", pq.QuoteLiteral(ownerID)).Replace(`INSERT INTO cap_ai.model_profiles(id,revision,purpose,execution,provider_id,model_id,dimension,limits,pricing_revision,enabled) VALUES('embed',1,'EMBED','HOSTED','fake','fake',3,$1,'price-1',true);
 INSERT INTO cap_ai.processing_policies(id,version,execution,classification,approved_by,approved_at,enabled,query_app_id,provider_id,retention_approved) VALUES('reviewed',1,'APPROVED_PROVIDER','NON_PERSONAL',$2,now(),true,'library','fake',true);
 INSERT INTO cap_ai.processing_policy_profiles(policy_id,policy_version,model_profile_id,profile_revision) VALUES('reviewed',1,'embed',1);
 UPDATE cap_ai.knowledge_documents SET processing_policy_id='reviewed',processing_policy_version=1,policy_revision=policy_revision+1;
 INSERT INTO cap_ai.budget_accounts(scope,scope_key,period_start,period_end,limit_units,pricing_revision) VALUES('INSTALLATION','installation',now()-interval '1 day',now()+interval '1 day',1000000,'price-1'),('APP','library',now()-interval '1 day',now()+interval '1 day',1000000,'price-1'),('USER',$2::text,now()-interval '1 day',now()+interval '1 day',1000000,'price-1');`))
	if e != nil {
		t.Fatal(e)
	}
	return db, s, a, profile
}
func repeatHash(char string) string {
	s := ""
	for i := 0; i < 64; i++ {
		s += char
	}
	return s
}

type embeddingProvider struct {
	fail   bool
	calls  int
	before func()
}

func (p *embeddingProvider) Invoke(ctx context.Context, profile models.Profile, payload models.Payload) (models.Result, error) {
	p.calls++
	if p.before != nil {
		p.before()
	}
	if p.fail {
		return models.Result{}, models.ErrUnavailable
	}
	return models.Result{Vector: []float32{1, 0, 0}, Usage: models.Usage{InputTokens: 10, OutputTokens: 0}}, nil
}
func TestEmbeddingReadinessIndependent(t *testing.T) {
	db, s, a, profile := semanticFixture(t)
	provider := &embeddingProvider{fail: true}
	s.ConfigureModels(&models.Gateway{Policy: &models.Policy{Enabled: true, Authority: models.DatabaseAuthority{DB: db}}, Budget: &models.Budget{DB: db}, Providers: map[string]models.Provider{"fake": provider}}, profile, models.Profile{})
	if e := s.EnqueueEmbeddings(context.Background(), a, profile); e != nil {
		t.Fatal(e)
	}
	lease, e := s.ClaimEmbedding(context.Background(), ownerID)
	if e != nil || lease == nil {
		t.Fatal(e)
	}
	if e = s.BuildEmbeddings(context.Background(), *lease, profile); e == nil {
		t.Fatal("provider failure ignored")
	}
	result, e := s.Search(context.Background(), a, SearchRequest{Scope: Scope{Kind: "APP", AppID: "library"}, Query: "Director", Retrieval: "HYBRID"})
	if e != nil || len(result.Results) != 1 {
		t.Fatal("FTS lost", result, e)
	}
	var ready int
	if e = db.QueryRow(`SELECT count(*) FROM cap_ai.embedding_generations WHERE state='READY'`).Scan(&ready); e != nil || ready != 0 {
		t.Fatal("partial ready", e)
	}
}
func TestScopedExactVectorRanking(t *testing.T) {
	db, s, a, profile := semanticFixture(t)
	provider := &embeddingProvider{}
	s.ConfigureModels(&models.Gateway{Policy: &models.Policy{Enabled: true, Authority: models.DatabaseAuthority{DB: db}}, Budget: &models.Budget{DB: db}, Providers: map[string]models.Provider{"fake": provider}}, profile, models.Profile{})
	if e := s.EnqueueEmbeddings(context.Background(), a, profile); e != nil {
		t.Fatal(e)
	}
	lease, e := s.ClaimEmbedding(context.Background(), ownerID)
	if e != nil || lease == nil {
		t.Fatal(e)
	}
	if e = s.BuildEmbeddings(context.Background(), *lease, profile); e != nil {
		t.Fatal(e)
	}
	result, e := s.Search(context.Background(), a, SearchRequest{Scope: Scope{Kind: "APP", AppID: "library"}, Query: "unmatched", Retrieval: "HYBRID"})
	if e != nil || len(result.Results) != 1 || result.Status != "HYBRID" {
		t.Fatal(result, e)
	}
	_, e = db.Exec(`DELETE FROM core.user_roles WHERE user_id=$1`, a.UserID)
	if e != nil {
		t.Fatal(e)
	}
	result, e = s.Search(context.Background(), a, SearchRequest{Scope: Scope{Kind: "APP", AppID: "library"}, Query: "unmatched", Retrieval: "HYBRID"})
	if e == nil && len(result.Results) != 0 {
		t.Fatal("hidden vector eligible", result)
	}
}
func TestPrivateQuestionNoHostedEmbedding(t *testing.T) {
	db, s, a, profile := semanticFixture(t)
	provider := &embeddingProvider{}
	s.ConfigureModels(&models.Gateway{Policy: &models.Policy{Enabled: true, Authority: models.DatabaseAuthority{DB: db}}, Budget: &models.Budget{DB: db}, Providers: map[string]models.Provider{"fake": provider}}, profile, models.Profile{})
	_, _ = s.Search(context.Background(), a, SearchRequest{Scope: Scope{Kind: "PERSONAL"}, Query: "Director private query", Retrieval: "HYBRID"})
	if provider.calls != 0 {
		t.Fatal("personal question sent to provider")
	}
}
func TestUncertainEmbeddingIsNotAutomaticallyRetried(t *testing.T) {
	db, s, a, profile := semanticFixture(t)
	provider := &embeddingProvider{fail: true}
	s.ConfigureModels(&models.Gateway{Policy: &models.Policy{Enabled: true, Authority: models.DatabaseAuthority{DB: db}}, Budget: &models.Budget{DB: db}, Providers: map[string]models.Provider{"fake": provider}}, profile, models.Profile{})
	if e := s.EnqueueEmbeddings(context.Background(), a, profile); e != nil {
		t.Fatal(e)
	}
	lease, e := s.ClaimEmbedding(context.Background(), ownerID)
	if e != nil || lease == nil {
		t.Fatal(e)
	}
	if e = s.BuildEmbeddings(context.Background(), *lease, profile); e != models.ErrUncertain {
		t.Fatal(e)
	}
	if _, e = db.Exec(`UPDATE cap_ai.ingestion_jobs SET next_attempt_at=now()-interval '1 second' WHERE kind='EMBED'`); e != nil {
		t.Fatal(e)
	}
	next, e := s.ClaimEmbedding(context.Background(), ownerID)
	if e != nil || next != nil {
		t.Fatal("possibly billed embedding automatically retried", e, next)
	}
}
func TestEmbeddingCrashAfterSendDoesNotResend(t *testing.T) {
	db, s, a, profile := semanticFixture(t)
	provider := &embeddingProvider{before: func() { panic("simulated worker loss after provider accepted") }}
	s.ConfigureModels(&models.Gateway{Policy: &models.Policy{Enabled: true, Authority: models.DatabaseAuthority{DB: db}}, Budget: &models.Budget{DB: db}, Providers: map[string]models.Provider{"fake": provider}}, profile, models.Profile{})
	if e := s.EnqueueEmbeddings(context.Background(), a, profile); e != nil {
		t.Fatal(e)
	}
	lease, e := s.ClaimEmbedding(context.Background(), ownerID)
	if e != nil || lease == nil {
		t.Fatal(e)
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Error("crash was not injected")
			}
		}()
		_ = s.BuildEmbeddings(context.Background(), *lease, profile)
	}()
	provider.before = nil
	if _, e = db.Exec(`UPDATE cap_ai.ingestion_jobs SET lease_until=now()-interval '1 second',next_attempt_at=now()-interval '1 second' WHERE kind='EMBED'`); e != nil {
		t.Fatal(e)
	}
	next, e := s.ClaimEmbedding(context.Background(), ownerID)
	if e != nil || next == nil {
		t.Fatal(e)
	}
	_ = s.BuildEmbeddings(context.Background(), *next, profile)
	if provider.calls != 1 {
		t.Fatal("crashed possibly billed embedding was automatically repeated", provider.calls)
	}
}
func TestSlowSemanticFailurePreservesLexical(t *testing.T) {
	db, s, a, profile := semanticFixture(t)
	provider := &embeddingProvider{before: func() { time.Sleep(3100 * time.Millisecond) }, fail: true}
	profile.Limits.TimeoutMS = 4000
	s.ConfigureModels(&models.Gateway{Policy: &models.Policy{Enabled: true, Authority: models.DatabaseAuthority{DB: db}}, Budget: &models.Budget{DB: db}, Providers: map[string]models.Provider{"fake": provider}}, profile, models.Profile{})
	result, e := s.Search(context.Background(), a, SearchRequest{Scope: Scope{Kind: "APP", AppID: "library"}, Query: "Director", Retrieval: "HYBRID"})
	if e != nil || len(result.Results) != 1 || result.Status != "LEXICAL_ONLY" {
		t.Fatal("slow semantic failure destroyed lexical fallback", e, result)
	}
}
