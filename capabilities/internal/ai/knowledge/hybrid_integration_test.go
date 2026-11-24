//go:build integration

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
package knowledge

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/lib/pq"
	"github.com/opendx-os/opendx-os/capabilities/internal/ai/knowledge/docir"
	"github.com/opendx-os/opendx-os/capabilities/internal/ai/models"
	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
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
	for _, migration := range []string{"0004_models_vectors.sql", "0005_model_registry.sql"} {
		raw, e := os.ReadFile(capAIMigrations + "/" + migration)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = db.Exec(string(raw)); e != nil {
			t.Fatalf("%s: %v", migration, e)
		}
	}
	profile := models.Profile{ID: "embed", Revision: 1, Purpose: "EMBED", Execution: "HOSTED", ProviderID: "fake", ModelID: "fake", Dimension: 3, Limits: models.Limits{MaxInputTokens: 8192, MaxOutputTokens: 512, MaxBytes: 16384, TimeoutMS: 500}}
	limits, _ := json.Marshal(profile.Limits)
	_, e = db.Exec(strings.NewReplacer("$1", pq.QuoteLiteral(string(limits)), "$2", pq.QuoteLiteral(ownerID)).Replace(`INSERT INTO cap_ai.model_profiles(id,revision,purpose,execution,provider_id,model_id,dimension,limits,enabled) VALUES('embed',1,'EMBED','HOSTED','fake','fake',3,$1,false);
 INSERT INTO cap_ai.processing_policies(id,version,execution,classification,approved_by,approved_at,enabled,query_app_id,provider_id,retention_approved) VALUES('reviewed',1,'APPROVED_PROVIDER','NON_PERSONAL',$2,now(),true,'library','fake',true);
 INSERT INTO cap_ai.processing_policy_profiles(policy_id,policy_version,model_profile_id,profile_revision) VALUES('reviewed',1,'embed',1);
 UPDATE cap_ai.knowledge_documents SET processing_policy_id='reviewed',processing_policy_version=1,policy_revision=policy_revision+1;`))
	if e != nil {
		t.Fatal(e)
	}
	// The deployment-authority chain and the exact live selection replace the
	// legacy slot. Nothing else in the fixture selects the model.
	applyDeploymentAuthority(t, db)
	residentProfile(t, db, profile)
	return db, s, a, profile
}

// dimensionProvider answers with a vector of the requested profile's dimension, so
// a fixture can move between revisions with different widths.
type dimensionProvider struct{ calls int }

func (p *dimensionProvider) Invoke(_ context.Context, profile models.Profile, _ models.Payload) (models.Result, error) {
	p.calls++
	vector := make([]float32, profile.Dimension)
	if len(vector) > 0 {
		vector[0] = 1
	}
	return models.Result{Vector: vector, Usage: &models.Usage{InputTokens: 10}}, nil
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
	if e := s.EnqueueEmbeddings(context.Background(), a); e != nil {
		t.Fatal(e)
	}
	lease, e := s.ClaimEmbedding(context.Background(), ownerID, models.Profile{ID: "embed", Revision: 1})
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
	if e := s.EnqueueEmbeddings(context.Background(), a); e != nil {
		t.Fatal(e)
	}
	lease, e := s.ClaimEmbedding(context.Background(), ownerID, models.Profile{ID: "embed", Revision: 1})
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
	if e := s.EnqueueEmbeddings(context.Background(), a); e != nil {
		t.Fatal(e)
	}
	lease, e := s.ClaimEmbedding(context.Background(), ownerID, models.Profile{ID: "embed", Revision: 1})
	if e != nil || lease == nil {
		t.Fatal(e)
	}
	if e = s.BuildEmbeddings(context.Background(), *lease, profile); e != models.ErrUncertain {
		t.Fatal(e)
	}
	if _, e = db.Exec(`UPDATE cap_ai.ingestion_jobs SET next_attempt_at=now()-interval '1 second' WHERE kind='EMBED'`); e != nil {
		t.Fatal(e)
	}
	next, e := s.ClaimEmbedding(context.Background(), ownerID, models.Profile{ID: "embed", Revision: 1})
	if e != nil || next != nil {
		t.Fatal("possibly billed embedding automatically retried", e, next)
	}
}
func TestEmbeddingCrashAfterSendDoesNotResend(t *testing.T) {
	db, s, a, profile := semanticFixture(t)
	provider := &embeddingProvider{before: func() { panic("simulated worker loss after provider accepted") }}
	s.ConfigureModels(&models.Gateway{Policy: &models.Policy{Enabled: true, Authority: models.DatabaseAuthority{DB: db}}, Operations: &models.OperationStore{DB: gatewayLogin(t, db)}, Providers: map[string]models.Provider{"fake": provider}}, profile, models.Profile{})
	if e := s.EnqueueEmbeddings(context.Background(), a); e != nil {
		t.Fatal(e)
	}
	lease, e := s.ClaimEmbedding(context.Background(), ownerID, models.Profile{ID: "embed", Revision: 1})
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
	next, e := s.ClaimEmbedding(context.Background(), ownerID, models.Profile{ID: "embed", Revision: 1})
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
	s.ConfigureModels(&models.Gateway{Policy: &models.Policy{Enabled: true, Authority: models.DatabaseAuthority{DB: db}}, Operations: &models.OperationStore{DB: gatewayLogin(t, db)}, Providers: map[string]models.Provider{"fake": provider}}, profile, models.Profile{})
	if err := s.EnqueueEmbeddings(context.Background(), actor); err != nil {
		t.Fatal(err)
	}
	result, err := s.Search(context.Background(), actor, SearchRequest{Scope: Scope{Kind: "APP", AppID: "library"}, Query: "Director", Retrieval: "HYBRID"})
	if err != nil || len(result.Results) != 1 || result.Status != "LEXICAL_ONLY" || provider.calls != 1 {
		t.Fatal(result, err, provider.calls)
	}
	// An installation with no configured embedding purpose makes no model call and
	// keeps the same lexical answer.
	s.ConfigureModels(&models.Gateway{Policy: &models.Policy{Enabled: true, Authority: models.DatabaseAuthority{DB: db}}, Operations: &models.OperationStore{DB: gatewayLogin(t, db)}, Providers: map[string]models.Provider{"fake": provider}}, models.Profile{}, models.Profile{})
	result, err = s.Search(context.Background(), actor, SearchRequest{Scope: Scope{Kind: "APP", AppID: "library"}, Query: "Director", Retrieval: "HYBRID"})
	if err != nil || len(result.Results) != 1 || provider.calls != 1 {
		t.Fatal("unbound fallback", result, err, provider.calls)
	}
}

// TestEmbeddingLeasePinsDeploymentRevision replaces the retired slot-lease
// resolution cases. A job queued for a superseded revision is never claimed by any
// worker — not by the superseded profile and not by the current one — so a lease
// can no longer recover a route the deployment stopped selecting. The remaining
// security cases (the unreachable resolver interface and the revoked processing
// review) are asserted against the deployment authority.
func TestEmbeddingLeasePinsDeploymentRevision(t *testing.T) {
	db, s, actor, profile := semanticFixture(t)
	limits, _ := json.Marshal(profile.Limits)
	newer := profile
	newer.Revision = 2
	newer.Dimension = 5
	if _, err := db.Exec(`INSERT INTO cap_ai.model_profiles(id,revision,purpose,execution,provider_id,model_id,dimension,limits,enabled)
		VALUES('embed',2,'EMBED','HOSTED','fake','fake',5,$1,false)`, string(limits)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO cap_ai.processing_policy_profiles(policy_id,policy_version,model_profile_id,profile_revision)
		VALUES('reviewed',1,'embed',2)`); err != nil {
		t.Fatal(err)
	}
	provider := &dimensionProvider{}
	gateway := &models.Gateway{Policy: &models.Policy{Enabled: true, Authority: models.DatabaseAuthority{DB: db}}, Operations: &models.OperationStore{DB: gatewayLogin(t, db)}, Providers: map[string]models.Provider{"fake": provider}}
	// Revision 1 is still the live selection: its job is queued.
	s.ConfigureModels(gateway, profile, models.Profile{})
	if err := s.EnqueueEmbeddings(context.Background(), actor); err != nil {
		t.Fatal(err)
	}
	// The deployment moves to revision 2.
	selectDeployment(t, db, newer)
	worker := workerStore(t, db)
	if stale, err := worker.ClaimEmbedding(context.Background(), ownerID, profile); err != nil || stale != nil {
		t.Fatal("the superseded revision still claims its job", stale, err)
	}
	if foreign, err := worker.ClaimEmbedding(context.Background(), ownerID, newer); err != nil || foreign != nil {
		t.Fatal("the current revision claimed a superseded job", foreign, err)
	}
	// The current revision builds its own job and pins its own identity.
	s.ConfigureModels(gateway, newer, models.Profile{})
	worker.ConfigureModels(gateway, newer, models.Profile{})
	if err := s.EnqueueEmbeddings(context.Background(), actor); err != nil {
		t.Fatal(err)
	}
	lease, err := worker.ClaimEmbedding(context.Background(), ownerID, newer)
	if err != nil || lease == nil {
		t.Fatal(lease, err)
	}
	if err = worker.BuildEmbeddings(context.Background(), *lease, newer); err != nil {
		t.Fatal(err)
	}
	var revision, dimension int
	var state string
	if err = db.QueryRow(`SELECT profile_revision,dimension,state FROM cap_ai.embedding_generations WHERE job_id=$1`, lease.JobID).Scan(&revision, &dimension, &state); err != nil || revision != 2 || dimension != 5 || state != "READY" {
		t.Fatal(revision, dimension, state, err)
	}
	// The retired lease resolver is unreachable for every runtime login.
	// No runtime Go path calls the retired lease resolver, and no non-gateway
	// runtime login can execute it. (The gateway keeps the 0005 grant until T9
	// removes the function; it has no caller.)
	for _, role := range []string{"dxos_ai_api", "dxos_ai_worker", "dxos_ai_model_catalog"} {
		var allowed bool
		if err = db.QueryRow(`SELECT has_function_privilege($1,'cap_ai.registry_embedding_lease_profile(uuid,text,bigint,uuid)','EXECUTE')`, role).Scan(&allowed); err != nil || allowed {
			t.Fatal("lease resolver privilege", role, allowed, err)
		}
	}
	// A revoked processing review denies the next build.
	if _, err = db.Exec(`UPDATE cap_ai.ingestion_jobs SET state='PENDING',next_attempt_at=now() WHERE id=$1`, lease.JobID); err != nil {
		t.Fatal(err)
	}
	again, err := worker.ClaimEmbedding(context.Background(), ownerID, newer)
	if err != nil || again == nil {
		t.Fatal(again, err)
	}
	if _, err = db.Exec(`UPDATE cap_ai.processing_policies SET enabled=false WHERE id='reviewed'`); err != nil {
		t.Fatal(err)
	}
	// The dispatch is refused before any egress once the review is withdrawn.
	calls := provider.calls
	if err = worker.BuildEmbeddings(context.Background(), *again, newer); err == nil || provider.calls != calls {
		t.Fatal("revoked review recovered", err, provider.calls)
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
		if e := s.EnqueueEmbeddings(context.Background(), actor); e != nil {
			t.Fatal(e)
		}
		first, e := s.ClaimEmbedding(context.Background(), ownerID, models.Profile{ID: "embed", Revision: 1})
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
		second, e := s.ClaimEmbedding(context.Background(), ownerID, models.Profile{ID: "embed", Revision: 1})
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
		if e := s.EnqueueEmbeddings(context.Background(), actor); e != nil {
			t.Fatal(e)
		}
		first, e := s.ClaimEmbedding(context.Background(), ownerID, models.Profile{ID: "embed", Revision: 1})
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
			hook.next, hook.nextErr = s.ClaimEmbedding(context.Background(), ownerID, models.Profile{ID: "embed", Revision: 1})
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
		if e := s.EnqueueEmbeddings(context.Background(), actor); e != nil {
			t.Fatal(e)
		}
		first, e := s.ClaimEmbedding(context.Background(), ownerID, models.Profile{ID: "embed", Revision: 1})
		if e != nil || first == nil {
			t.Fatal(first, e)
		}
		hook := &claimHookStore{OperationStore: store}
		hook.afterBegin = func() {
			if _, e := db.Exec(`UPDATE cap_ai.ingestion_jobs SET lease_until=now()-interval '1 second' WHERE id=$1`, first.JobID); e != nil {
				t.Error(e)
				return
			}
			hook.next, hook.nextErr = s.ClaimEmbedding(context.Background(), ownerID, models.Profile{ID: "embed", Revision: 1})
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
		if e := s.EnqueueEmbeddings(context.Background(), actor); e != nil {
			t.Fatal(e)
		}
		first, e := s.ClaimEmbedding(context.Background(), ownerID, models.Profile{ID: "embed", Revision: 1})
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
		second, e := s.ClaimEmbedding(context.Background(), ownerID, models.Profile{ID: "embed", Revision: 1})
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
