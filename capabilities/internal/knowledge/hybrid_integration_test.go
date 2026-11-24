//go:build integration

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
package knowledge

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/lib/pq"
	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
	"github.com/opendx-os/opendx-os/capabilities/internal/knowledge/docir"
	"github.com/opendx-os/opendx-os/capabilities/internal/knowledge/models"
	"github.com/opendx-os/opendx-os/capabilities/internal/testutil"
	"net/url"
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
	profile := models.Profile{ID: "embed", Revision: 1, Purpose: "EMBED", Execution: "HOSTED", ProviderID: "fake", ModelID: "fake", Dimension: 3, Enabled: true, Limits: models.Limits{MaxInputTokens: 8192, MaxOutputTokens: 512, MaxBytes: 16384, TimeoutMS: 500}}
	limits, _ := json.Marshal(profile.Limits)
	_, e = db.Exec(strings.NewReplacer("$1", pq.QuoteLiteral(string(limits)), "$2", pq.QuoteLiteral(ownerID)).Replace(`INSERT INTO cap_ai.model_profiles(id,revision,purpose,execution,provider_id,model_id,dimension,limits,enabled) VALUES('embed',1,'EMBED','HOSTED','fake','fake',3,$1,true);
 INSERT INTO cap_ai.processing_policies(id,version,execution,classification,approved_by,approved_at,enabled,query_app_id,provider_id,retention_approved) VALUES('reviewed',1,'APPROVED_PROVIDER','NON_PERSONAL',$2,now(),true,'library','fake',true);
 INSERT INTO cap_ai.processing_policy_profiles(policy_id,policy_version,model_profile_id,profile_revision) VALUES('reviewed',1,'embed',1);
 UPDATE cap_ai.knowledge_documents SET processing_policy_id='reviewed',processing_policy_version=1,policy_revision=policy_revision+1;`))
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
	return models.Result{Vector: []float32{1, 0, 0}, Usage: &models.Usage{InputTokens: 10, OutputTokens: 0}}, nil
}
func TestEmbeddingReadinessIndependent(t *testing.T) {
	db, s, a, profile := semanticFixture(t)
	provider := &embeddingProvider{fail: true}
	s.ConfigureModels(&models.Gateway{Policy: &models.Policy{Enabled: true, Authority: models.DatabaseAuthority{DB: db}}, Operations: &models.OperationStore{DB: gatewayLogin(t, db)}, Providers: map[string]models.Provider{"fake": provider}}, profile, models.Profile{})
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
	s.ConfigureModels(&models.Gateway{Policy: &models.Policy{Enabled: true, Authority: models.DatabaseAuthority{DB: db}}, Operations: &models.OperationStore{DB: gatewayLogin(t, db)}, Providers: map[string]models.Provider{"fake": provider}}, profile, models.Profile{})
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
	s.ConfigureModels(&models.Gateway{Policy: &models.Policy{Enabled: true, Authority: models.DatabaseAuthority{DB: db}}, Operations: &models.OperationStore{DB: gatewayLogin(t, db)}, Providers: map[string]models.Provider{"fake": provider}}, profile, models.Profile{})
	_, _ = s.Search(context.Background(), a, SearchRequest{Scope: Scope{Kind: "PERSONAL"}, Query: "Director private query", Retrieval: "HYBRID"})
	if provider.calls != 0 {
		t.Fatal("personal question sent to provider")
	}
}
func TestUncertainEmbeddingIsNotAutomaticallyRetried(t *testing.T) {
	db, s, a, profile := semanticFixture(t)
	provider := &embeddingProvider{fail: true}
	s.ConfigureModels(&models.Gateway{Policy: &models.Policy{Enabled: true, Authority: models.DatabaseAuthority{DB: db}}, Operations: &models.OperationStore{DB: gatewayLogin(t, db)}, Providers: map[string]models.Provider{"fake": provider}}, profile, models.Profile{})
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
	s.ConfigureModels(&models.Gateway{Policy: &models.Policy{Enabled: true, Authority: models.DatabaseAuthority{DB: db}}, Operations: &models.OperationStore{DB: gatewayLogin(t, db)}, Providers: map[string]models.Provider{"fake": provider}}, profile, models.Profile{})
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
	s.ConfigureModels(&models.Gateway{Policy: &models.Policy{Enabled: true, Authority: models.DatabaseAuthority{DB: db}}, Operations: &models.OperationStore{DB: gatewayLogin(t, db)}, Providers: map[string]models.Provider{"fake": provider}}, profile, models.Profile{})
	result, e := s.Search(context.Background(), a, SearchRequest{Scope: Scope{Kind: "APP", AppID: "library"}, Query: "Director", Retrieval: "HYBRID"})
	if e != nil || len(result.Results) != 1 || result.Status != "LEXICAL_ONLY" {
		t.Fatal("slow semantic failure destroyed lexical fallback", e, result)
	}
}

func TestRegistrySemanticFailurePreservesFTS(t *testing.T) {
	db, s, actor, profile := semanticFixture(t)
	provider := &embeddingProvider{fail: true}
	s.ConfigureModels(&models.Gateway{Policy: &models.Policy{Enabled: true, Authority: models.DatabaseAuthority{DB: db}}, Operations: &models.OperationStore{DB: gatewayLogin(t, db)}, Providers: map[string]models.Provider{"fake": provider}}, models.Profile{}, models.Profile{})
	s.ConfigureModelResolver(&fixedResolver{profile: profile})
	if err := s.EnqueueEmbeddings(context.Background(), actor, models.Profile{ID: "request-cannot-select"}); err != nil {
		t.Fatal("enqueue did not resolve global binding", err)
	}
	result, err := s.Search(context.Background(), actor, SearchRequest{Scope: Scope{Kind: "APP", AppID: "library"}, Query: "Director", Retrieval: "HYBRID"})
	if err != nil || len(result.Results) != 1 || result.Status != "LEXICAL_ONLY" || provider.calls != 1 {
		t.Fatal(result, err, provider.calls)
	}
	s.ConfigureModelResolver(&fixedResolver{err: models.ErrUnavailable})
	result, err = s.Search(context.Background(), actor, SearchRequest{Scope: Scope{Kind: "APP", AppID: "library"}, Query: "Director", Retrieval: "HYBRID"})
	if err != nil || len(result.Results) != 1 || provider.calls != 1 {
		t.Fatal("unbound fallback", result, err, provider.calls)
	}
}

func TestRegistryEmbeddingLeaseRecoveryPinsOriginalRevision(t *testing.T) {
	for _, unset := range []bool{false, true} {
		t.Run(fmt.Sprint("unset=", unset), func(t *testing.T) {
			db, s, actor, profile := semanticFixture(t)
			raw, err := os.ReadFile("../../../platform/postgres/migrations/cap_ai/0005_model_registry.sql")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = db.Exec(string(raw)); err != nil {
				t.Fatal(err)
			}
			var fixtureDB string
			if err = db.QueryRow("SELECT current_database()").Scan(&fixtureDB); err != nil {
				t.Fatal(err)
			}
			fixtureURL, _ := url.Parse(os.Getenv("TEST_DATABASE_URL"))
			fixtureURL.Path = "/" + fixtureDB
			importer := &models.Registry{DB: testutil.As(t, fixtureURL.String(), "dxos_ai_model_catalog")}
			catalog := models.Catalog{SchemaVersion: 2, Providers: []models.ProviderConfig{{ID: "fake", Adapter: "bounded-http-v1", Endpoint: "https://example.invalid"}}, Models: []models.CandidateDeclaration{}}
			for _, revision := range []int64{2, 3} {
				candidate := profile
				candidate.Revision = revision
				candidate.Enabled = false
				if revision == 3 {
					candidate.Dimension = 5
				}
				catalog.Models = append(catalog.Models, models.CandidateDeclaration{Profile: candidate, Adapter: "bounded-http-v1"})
			}
			if _, err = importer.Reconcile(context.Background(), catalog); err != nil {
				t.Fatal(err)
			}
			if _, err = db.Exec(`UPDATE cap_ai.model_profiles SET enabled=true;
 INSERT INTO cap_ai.processing_policy_profiles(policy_id,policy_version,model_profile_id,profile_revision) VALUES('reviewed',1,'embed',2),('reviewed',1,'embed',3);
 UPDATE cap_ai.global_model_slots SET profile_id='embed',profile_revision=2,slot_revision=1 WHERE capability='EMBEDDING'`); err != nil {
				t.Fatal(err)
			}
			profile.Revision = 2
			provider := &embeddingProvider{}
			gateway := &models.Gateway{Policy: &models.Policy{Enabled: true, Authority: models.DatabaseAuthority{DB: db}}, Operations: &models.OperationStore{DB: gatewayLogin(t, db)}, Providers: map[string]models.Provider{"fake": provider}}
			s.ConfigureModels(gateway, models.Profile{}, models.Profile{})
			if err = s.EnqueueEmbeddings(context.Background(), actor, profile); err != nil {
				t.Fatal(err)
			}
			worker := workerStore(t, db)
			worker.ConfigureModels(gateway, models.Profile{}, models.Profile{})
			first, err := worker.ClaimEmbedding(context.Background(), ownerID)
			if err != nil || first == nil {
				t.Fatal(first, err)
			}
			var started embeddingWork
			if err = decodeRow(worker.db.QueryRow(`SELECT cap_ai.embedding_work($1,$2,$3,$4,'embed',2)`, first.JobID, first.WorkerID, first.Fence, first.RunID), &started); err != nil {
				t.Fatal(err)
			}
			if _, err = db.Exec(`UPDATE cap_ai.ingestion_jobs SET lease_until=now()-interval '1 second' WHERE id=$1;`, first.JobID); err != nil {
				t.Fatal(err)
			}
			if _, err = db.Exec(`UPDATE cap_ai.global_model_slots SET profile_revision=3,slot_revision=2 WHERE capability='EMBEDDING'`); err != nil {
				t.Fatal(err)
			}
			var database string
			if err = db.QueryRow(`SELECT current_database()`).Scan(&database); err != nil {
				t.Fatal(err)
			}
			u, err := url.Parse(os.Getenv("TEST_DATABASE_URL"))
			if err != nil {
				t.Fatal(err)
			}
			u.Path = "/" + database
			registry := &models.Registry{DB: testutil.As(t, u.String(), "dxos_ai_gateway")}
			worker.ConfigureModelResolver(registry)
			// An unset current slot must not prevent recovery of an existing pinned job.
			if unset {
				if _, err = db.Exec(`UPDATE cap_ai.global_model_slots SET profile_id=NULL,profile_revision=NULL,slot_revision=3 WHERE capability='EMBEDDING'`); err != nil {
					t.Fatal(err)
				}
			}
			if err = (&Worker{Store: worker, WorkerID: ownerID}).Once(context.Background()); err != nil {
				t.Fatal("resume", err)
			}
			var revision int
			var dimension int
			var state string
			if err = db.QueryRow(`SELECT profile_revision,dimension,state FROM cap_ai.embedding_generations WHERE id=$1`, started.GenerationID).Scan(&revision, &dimension, &state); err != nil || revision != 2 || dimension != 3 || state != "READY" || provider.calls != 1 {
				t.Fatal(revision, dimension, state, provider.calls, err)
			}
			// The lease resolver also rechecks current policy and profile availability.
			if _, err = db.Exec(`UPDATE cap_ai.ingestion_jobs SET state='PENDING',next_attempt_at=now() WHERE id=$1`, first.JobID); err != nil {
				t.Fatal(err)
			}
			lease, err := worker.ClaimEmbedding(context.Background(), ownerID)
			if err != nil || lease == nil {
				t.Fatal(lease, err)
			}
			ref := models.LeaseRef{JobID: lease.JobID, WorkerID: lease.WorkerID, RunID: lease.RunID, Fence: lease.Fence}
			// Restart with providers only: both slot and recovered lease must reject
			// changed connection metadata before dispatching to any provider.
			for _, drift := range []string{"unchanged", "endpoint", "loopback"} {
				config := catalog
				config.Models = []models.CandidateDeclaration{}
				if drift == "endpoint" {
					config.Providers = []models.ProviderConfig{{ID: "fake", Adapter: "bounded-http-v1", Endpoint: "https://changed.invalid"}}
				}
				if drift == "loopback" {
					config.Providers = []models.ProviderConfig{{ID: "fake", Adapter: "bounded-http-v1", Endpoint: "https://example.invalid", LoopbackTest: true}}
				}
				data, _ := json.Marshal(config)
				file := t.TempDir() + "/catalog.json"
				if err = os.WriteFile(file, data, 0600); err != nil {
					t.Fatal(err)
				}
				gatewayURL := *fixtureURL
				gatewayURL.User = url.User("dxos_ai_gateway")
				env := map[string]string{"RAG_MODELS_ENABLED": "true", "RAG_MODELS_QUALIFIED": "true", "RAG_SOURCE_QUALIFIED": "true", "RAG_METADATA_QUALIFIED": "true", "RAG_PARSER_QUALIFIED": "true", "RAG_GLOBAL_MODEL_REGISTRY_ENABLED": "true", "RAG_MODEL_TEST_MODE": "true", "AI_GATEWAY_DATABASE_URL": gatewayURL.String(), "RAG_MODEL_DEPLOYMENT_FILE": file}
				_, _, _, runtime, closeRuntime, runtimeErr := models.RuntimeFromEnvironment(context.Background(), func(k string) string { return env[k] })
				if runtimeErr != nil {
					t.Fatal(drift, runtimeErr)
				}
				_, runtimeErr = runtime.ResolveEmbeddingLease(context.Background(), ref)
				closeRuntime()
				if drift == "unchanged" && runtimeErr != nil || drift != "unchanged" && runtimeErr != models.ErrUnavailable {
					t.Fatal(drift, runtimeErr)
				}
				if provider.calls != 1 {
					t.Fatal("unexpected egress", provider.calls)
				}
			}

			current, resolveErr := registry.ResolveEmbeddingLease(context.Background(), ref)
			if resolveErr != nil {
				t.Fatal(resolveErr)
			}
			stale := ref
			stale.Fence--
			if _, err = registry.ResolveEmbeddingLease(context.Background(), stale); err == nil {
				t.Fatal("stale lease resolved")
			}
			if _, err = db.Exec(`UPDATE cap_ai.model_profiles SET enabled=false WHERE id=$1 AND revision=$2`, current.ID, current.Revision); err != nil {
				t.Fatal(err)
			}
			if _, err = registry.ResolveEmbeddingLease(context.Background(), ref); err == nil {
				t.Fatal("disabled profile resolved")
			}
			if _, err = db.Exec(`UPDATE cap_ai.model_profiles SET enabled=true WHERE id=$1 AND revision=$2`, current.ID, current.Revision); err != nil {
				t.Fatal(err)
			}
			for _, role := range []string{"dxos_ai_api", "dxos_ai_worker", "dxos_ai_model_catalog"} {
				var allowed bool
				if err = db.QueryRow(`SELECT has_function_privilege($1,'cap_ai.registry_embedding_lease_profile(uuid,text,bigint,uuid)','EXECUTE')`, role).Scan(&allowed); err != nil || allowed {
					t.Fatal("lease resolver privilege", role, allowed, err)
				}
			}
			if _, err = db.Exec(`UPDATE cap_ai.processing_policies SET enabled=false WHERE id='reviewed'`); err != nil {
				t.Fatal(err)
			}
			if _, err = registry.ResolveEmbeddingLease(context.Background(), ref); err == nil {
				t.Fatal("revoked policy recovered")
			}
		})
	}
}

// gatewayLogin opens the restricted deployment session the gateway uses for
// durable operation dispatch.
func gatewayLogin(t *testing.T, db *sql.DB) *sql.DB {
	t.Helper()
	var name string
	if err := db.QueryRow(`SELECT current_database()`).Scan(&name); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	return testutil.As(t, u.String(), "dxos_ai_gateway")
}

// operationRow reads durable dispatch facts through the privileged fixture.
func operationRow(t *testing.T, db *sql.DB, like string) (key, fingerprint, state string, inputs sql.NullInt64, outputs sql.NullInt64, intentSet bool) {
	t.Helper()
	err := db.QueryRow(`SELECT operation_key,fingerprint,state,input_tokens,output_tokens,intent_at IS NOT NULL
		FROM cap_ai.model_operations WHERE operation_key LIKE $1 ORDER BY claimed_at LIMIT 1`, like).
		Scan(&key, &fingerprint, &state, &inputs, &outputs, &intentSet)
	if err != nil {
		t.Fatal(err)
	}
	return
}

// TestEmbeddingClaimedOperationLeaseRecovery proves a claim recorded at fence 1
// is resumed by fence 2 as the same durable operation.
func TestEmbeddingClaimedOperationLeaseRecovery(t *testing.T) {
	t.Run("reclaim after claim", func(t *testing.T) {
		db, s, actor, profile := semanticFixture(t)
		provider := &embeddingProvider{}
		store := &models.OperationStore{DB: gatewayLogin(t, db)}
		s.ConfigureModels(&models.Gateway{Policy: &models.Policy{Enabled: true, Authority: models.DatabaseAuthority{DB: db}},
			Operations: store, Providers: map[string]models.Provider{"fake": provider}}, profile, models.Profile{})
		if e := s.EnqueueEmbeddings(context.Background(), actor, profile); e != nil {
			t.Fatal(e)
		}
		first, e := s.ClaimEmbedding(context.Background(), ownerID)
		if e != nil || first == nil {
			t.Fatal(first, e)
		}
		// Stop after the claim: the begin write fails, so the row stays CLAIMED.
		s.ConfigureModels(&models.Gateway{Policy: &models.Policy{Enabled: true, Authority: models.DatabaseAuthority{DB: db}},
			Operations: &failingBeginStore{OperationStore: store}, Providers: map[string]models.Provider{"fake": provider}}, profile, models.Profile{})
		if e = s.BuildEmbeddings(context.Background(), *first, profile); e == nil {
			t.Fatal("begin failure ignored")
		}
		claimedKey, claimedFingerprint, state, _, _, intent := operationRow(t, db, "embed:"+first.JobID+":%")
		if state != "CLAIMED" || intent || provider.calls != 0 {
			t.Fatal("claim not left for recovery", state, intent, provider.calls)
		}
		// Reclaim the same job under the same service identity at a fresh fence.
		if _, e = db.Exec(`UPDATE cap_ai.ingestion_jobs SET lease_until=now()-interval '1 second',next_attempt_at=now()-interval '1 second' WHERE id=$1`, first.JobID); e != nil {
			t.Fatal(e)
		}
		second, e := s.ClaimEmbedding(context.Background(), ownerID)
		if e != nil || second == nil || second.JobID != first.JobID || second.Fence <= first.Fence {
			t.Fatal("reclaim", second, e)
		}
		s.ConfigureModels(&models.Gateway{Policy: &models.Policy{Enabled: true, Authority: models.DatabaseAuthority{DB: db}},
			Operations: store, Providers: map[string]models.Provider{"fake": provider}}, profile, models.Profile{})
		if e = s.BuildEmbeddings(context.Background(), *second, profile); e != nil {
			t.Fatal("recovery", e)
		}
		if provider.calls != 1 {
			t.Fatal("recovery sent", provider.calls)
		}
		recoveredKey, recoveredFingerprint, state, inputs, _, _ := operationRow(t, db, "embed:"+first.JobID+":%")
		if recoveredKey != claimedKey || recoveredFingerprint != claimedFingerprint || state != "SUCCEEDED" || inputs.Int64 != 10 {
			t.Fatal("recovered binding changed", recoveredKey, recoveredFingerprint, state, inputs)
		}
		var revision int
		var readyFence int64
		var generationState string
		if e = db.QueryRow(`SELECT g.profile_revision,j.fencing_token,g.state FROM cap_ai.embedding_generations g
			JOIN cap_ai.ingestion_jobs j ON j.id=g.job_id WHERE j.id=$1`, first.JobID).Scan(&revision, &readyFence, &generationState); e != nil ||
			revision != 1 || readyFence != second.Fence || generationState != "READY" {
			t.Fatal("generation not published by the recovering fence", revision, readyFence, generationState, e)
		}
	})

	t.Run("stale caller after claim", func(t *testing.T) {
		db, s, actor, profile := semanticFixture(t)
		provider := &embeddingProvider{}
		store := &models.OperationStore{DB: gatewayLogin(t, db)}
		s.ConfigureModels(&models.Gateway{Policy: &models.Policy{Enabled: true, Authority: models.DatabaseAuthority{DB: db}},
			Operations: store, Providers: map[string]models.Provider{"fake": provider}}, profile, models.Profile{})
		if e := s.EnqueueEmbeddings(context.Background(), actor, profile); e != nil {
			t.Fatal(e)
		}
		first, e := s.ClaimEmbedding(context.Background(), ownerID)
		if e != nil || first == nil {
			t.Fatal(first, e)
		}
		// The lease rotates between the claim and the next policy check.
		hook := &claimHookStore{OperationStore: store}
		gateway := &models.Gateway{Policy: &models.Policy{Enabled: true, Authority: models.DatabaseAuthority{DB: db}},
			Operations: hook, Providers: map[string]models.Provider{"fake": provider}}
		hook.afterClaim = func() {
			if _, e := db.Exec(`UPDATE cap_ai.ingestion_jobs SET lease_until=now()-interval '1 second' WHERE id=$1`, first.JobID); e != nil {
				t.Error(e)
				return
			}
			hook.next, hook.nextErr = s.ClaimEmbedding(context.Background(), ownerID)
		}
		s.ConfigureModels(gateway, profile, models.Profile{})
		if e = s.BuildEmbeddings(context.Background(), *first, profile); !errors.Is(e, models.ErrPolicy) {
			t.Fatalf("stale fence survived the live policy recheck: %v", e)
		}
		if provider.calls != 0 {
			t.Fatal("stale caller invoked the provider", provider.calls)
		}
		_, _, state, _, _, _ := operationRow(t, db, "embed:"+first.JobID+":%")
		if state != "CLAIMED" {
			t.Fatal("stale caller terminalized the shared claim", state)
		}
		if hook.nextErr != nil || hook.next == nil {
			t.Fatal("reclaim", hook.next, hook.nextErr)
		}
		s.ConfigureModels(&models.Gateway{Policy: &models.Policy{Enabled: true, Authority: models.DatabaseAuthority{DB: db}},
			Operations: store, Providers: map[string]models.Provider{"fake": provider}}, profile, models.Profile{})
		if e = s.BuildEmbeddings(context.Background(), *hook.next, profile); e != nil {
			t.Fatal("fence 2 recovery", e)
		}
		if provider.calls != 1 {
			t.Fatal("fence 2 did not dispatch exactly once", provider.calls)
		}
		if _, _, state, _, _, _ = operationRow(t, db, "embed:"+first.JobID+":%"); state != "SUCCEEDED" {
			t.Fatal("state", state)
		}
	})
}

// TestEmbeddingIntentBlocksLeaseRecovery proves persisted intent blocks any
// later fence from redispatching the same operation.
func TestEmbeddingIntentBlocksLeaseRecovery(t *testing.T) {
	t.Run("rotated after intent", func(t *testing.T) {
		db, s, actor, profile := semanticFixture(t)
		provider := &embeddingProvider{}
		store := &models.OperationStore{DB: gatewayLogin(t, db)}
		s.ConfigureModels(&models.Gateway{Policy: &models.Policy{Enabled: true, Authority: models.DatabaseAuthority{DB: db}},
			Operations: store, Providers: map[string]models.Provider{"fake": provider}}, profile, models.Profile{})
		if e := s.EnqueueEmbeddings(context.Background(), actor, profile); e != nil {
			t.Fatal(e)
		}
		first, e := s.ClaimEmbedding(context.Background(), ownerID)
		if e != nil || first == nil {
			t.Fatal(first, e)
		}
		hook := &claimHookStore{OperationStore: store}
		hook.afterBegin = func() {
			if _, e := db.Exec(`UPDATE cap_ai.ingestion_jobs SET lease_until=now()-interval '1 second' WHERE id=$1`, first.JobID); e != nil {
				t.Error(e)
				return
			}
			hook.next, hook.nextErr = s.ClaimEmbedding(context.Background(), ownerID)
		}
		s.ConfigureModels(&models.Gateway{Policy: &models.Policy{Enabled: true, Authority: models.DatabaseAuthority{DB: db}},
			Operations: hook, Providers: map[string]models.Provider{"fake": provider}}, profile, models.Profile{})
		if e = s.BuildEmbeddings(context.Background(), *first, profile); e == nil {
			t.Fatal("rotated lease escaped the final check")
		}
		if provider.calls != 0 {
			t.Fatal("stale fence invoked the provider", provider.calls)
		}
		_, _, state, _, _, intent := operationRow(t, db, "embed:"+first.JobID+":%")
		if state != "FAILED_UNSENT" || !intent {
			t.Fatal("state", state, intent)
		}
		if hook.nextErr != nil || hook.next == nil {
			t.Fatal("reclaim", hook.next, hook.nextErr)
		}
		s.ConfigureModels(&models.Gateway{Policy: &models.Policy{Enabled: true, Authority: models.DatabaseAuthority{DB: db}},
			Operations: store, Providers: map[string]models.Provider{"fake": provider}}, profile, models.Profile{})
		if e = s.BuildEmbeddings(context.Background(), *hook.next, profile); !errors.Is(e, models.ErrPolicy) {
			t.Fatalf("terminal intent allowed redispatch: %v", e)
		}
		if provider.calls != 0 {
			t.Fatal("fence 2 redispatch", provider.calls)
		}
		// Stale save and publish attempts are refused by SQL.
		var generation string
		if e = db.QueryRow(`SELECT id FROM cap_ai.embedding_generations WHERE job_id=$1`, first.JobID).Scan(&generation); e != nil {
			t.Fatal(e)
		}
		if _, e = db.Exec(`SELECT cap_ai.save_embedding($1,$2,$3,$4,$5,gen_random_uuid(),repeat('a',64),'[1,0,0]'::public.vector)`,
			first.JobID, first.WorkerID, first.Fence, first.RunID, generation); e == nil {
			t.Fatal("stale save accepted")
		}
		if _, e = db.Exec(`SELECT cap_ai.finish_embedding($1,$2,$3,$4,$5,true)`, first.JobID, first.WorkerID, first.Fence, first.RunID, generation); e == nil {
			t.Fatal("stale publish accepted")
		}
	})

	t.Run("lost begin acknowledgment", func(t *testing.T) {
		db, s, actor, profile := semanticFixture(t)
		provider := &embeddingProvider{}
		store := &models.OperationStore{DB: gatewayLogin(t, db)}
		s.ConfigureModels(&models.Gateway{Policy: &models.Policy{Enabled: true, Authority: models.DatabaseAuthority{DB: db}},
			Operations: store, Providers: map[string]models.Provider{"fake": provider}}, profile, models.Profile{})
		if e := s.EnqueueEmbeddings(context.Background(), actor, profile); e != nil {
			t.Fatal(e)
		}
		first, e := s.ClaimEmbedding(context.Background(), ownerID)
		if e != nil || first == nil {
			t.Fatal(first, e)
		}
		s.ConfigureModels(&models.Gateway{Policy: &models.Policy{Enabled: true, Authority: models.DatabaseAuthority{DB: db}},
			Operations: &lostAckStore{OperationStore: store}, Providers: map[string]models.Provider{"fake": provider}}, profile, models.Profile{})
		func() {
			defer func() {
				if recover() == nil {
					t.Fatal("lost acknowledgment was not injected")
				}
			}()
			_ = s.BuildEmbeddings(context.Background(), *first, profile)
		}()
		key, fingerprint, state, _, _, intent := operationRow(t, db, "embed:"+first.JobID+":%")
		if state != "MAY_HAVE_SENT" || !intent || provider.calls != 0 {
			t.Fatal("state", state, intent, provider.calls)
		}
		if _, e = db.Exec(`UPDATE cap_ai.ingestion_jobs SET lease_until=now()-interval '1 second',next_attempt_at=now()-interval '1 second' WHERE id=$1`, first.JobID); e != nil {
			t.Fatal(e)
		}
		second, e := s.ClaimEmbedding(context.Background(), ownerID)
		if e != nil || second == nil {
			t.Fatal(second, e)
		}
		s.ConfigureModels(&models.Gateway{Policy: &models.Policy{Enabled: true, Authority: models.DatabaseAuthority{DB: db}},
			Operations: store, Providers: map[string]models.Provider{"fake": provider}}, profile, models.Profile{})
		if e = s.BuildEmbeddings(context.Background(), *second, profile); !errors.Is(e, models.ErrUncertain) {
			t.Fatalf("fence 2 dispatched ambiguous intent: %v", e)
		}
		if provider.calls != 0 {
			t.Fatal("ambiguous intent redispatched", provider.calls)
		}
		recoveredKey, recoveredFingerprint, state, _, _, _ := operationRow(t, db, "embed:"+first.JobID+":%")
		if recoveredKey != key || recoveredFingerprint != fingerprint || state != "MAY_HAVE_SENT" {
			t.Fatal("fence 2 reissued or changed the operation", recoveredKey, recoveredFingerprint, state)
		}
		if token, e := store.Begin(context.Background(), key, fingerprint); e != nil || token != "" {
			t.Fatal("a second dispatch token was issued", token, e)
		}
	})
}

// failingBeginStore refuses the intent write so the claim is left CLAIMED.
type failingBeginStore struct{ *models.OperationStore }

func (s *failingBeginStore) Begin(context.Context, string, string) (string, error) {
	return "", models.ErrUnavailable
}

// claimHookStore runs a hook after the claim or after the intent write.
type claimHookStore struct {
	*models.OperationStore
	afterClaim, afterBegin func()
	next                   *Lease
	nextErr                error
}

func (s *claimHookStore) Claim(ctx context.Context, binding models.OperationBinding) (string, error) {
	state, err := s.OperationStore.Claim(ctx, binding)
	if s.afterClaim != nil {
		s.afterClaim()
	}
	return state, err
}

func (s *claimHookStore) Begin(ctx context.Context, key, fingerprint string) (string, error) {
	token, err := s.OperationStore.Begin(ctx, key, fingerprint)
	if s.afterBegin != nil {
		s.afterBegin()
	}
	return token, err
}

// lostAckStore simulates a worker that dies after its begin committed: the
// dispatch intent is durable but the caller never observes the token.
type lostAckStore struct{ *models.OperationStore }

func (s *lostAckStore) Begin(ctx context.Context, key, fingerprint string) (string, error) {
	if _, err := s.OperationStore.Begin(ctx, key, fingerprint); err != nil {
		return "", err
	}
	panic("lost begin acknowledgment")
}
