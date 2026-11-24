//go:build integration

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package knowledge

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"slices"
	"testing"

	"github.com/opendx-os/opendx-os/capabilities/internal/ai/models"
	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
	"github.com/opendx-os/opendx-os/capabilities/internal/testutil"
)

type personalHybridFixture struct {
	db, admin   *sql.DB
	api, worker *Store
	owner       *auth.Identity
	spy         *embeddingProvider
	registry    *models.Registry
}

func applyHybridFixtureMigrations(t *testing.T, db *sql.DB, migrations ...string) {
	t.Helper()
	for _, migration := range migrations {
		raw, err := os.ReadFile("../../../platform/postgres/migrations/cap_ai/" + migration)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(string(raw)); err != nil {
			t.Fatalf("%s: %v", migration, err)
		}
	}
}

func newPersonalHybridFixture(t *testing.T) personalHybridFixture {
	t.Helper()
	db, api, owner := derivedFixture(t)
	applyHybridFixtureMigrations(t, db, "0003_package_knowledge.sql", "0004_models_vectors.sql",
		"0005_model_registry.sql", "0008_personal_model_processing.sql", "0009_personal_hybrid.sql")
	_, err := db.Exec(`
INSERT INTO core.user_roles(user_id, role_name)
VALUES ('11111111-1111-4111-8111-111111111111', 'role_operator');
INSERT INTO cap_ai.model_profiles(id, revision, purpose, execution, provider_id, model_id, dimension, limits, enabled)
VALUES ('embed', 1, 'EMBED', 'HOSTED', 'fake', 'fake', 3,
 '{"max_input_tokens":8192,"max_output_tokens":512,"max_bytes":16384,"timeout_ms":500}', true);
INSERT INTO cap_ai.model_candidates(profile_id, revision, adapter_key, declaration_digest, declaration)
VALUES ('embed', 1, 'bounded-http-v1', repeat('a',64), '{}');
UPDATE cap_ai.global_model_slots SET profile_id='embed', profile_revision=1, slot_revision=1 WHERE capability='EMBEDDING';
`)
	if err != nil {
		t.Fatal(err)
	}
	var name string
	if err := db.QueryRow("SELECT current_database()").Scan(&name); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	gatewayDB := testutil.As(t, u.String(), "dxos_ai_gateway")
	registry := &models.Registry{DB: gatewayDB}
	spy := &embeddingProvider{}
	gateway := &models.Gateway{
		Policy:     &models.Policy{Enabled: true, Authority: models.DatabaseAuthority{DB: gatewayDB}},
		Operations: &models.OperationStore{DB: gatewayDB}, Providers: map[string]models.Provider{"fake": spy},
	}
	s := NewStore(api)
	worker := workerStore(t, db)
	for _, store := range []*Store{s, worker} {
		store.ConfigureModels(gateway, models.Profile{}, models.Profile{})
		store.ConfigureModelResolver(registry)
	}
	return personalHybridFixture{db: db, admin: testutil.As(t, u.String(), "dxos_authz"), api: s, worker: worker, owner: owner, spy: spy, registry: registry}
}

func (f personalHybridFixture) approve(t *testing.T) string {
	t.Helper()
	var id string
	err := f.admin.QueryRow(`SELECT cap_ai.approve_agent_processing($1,
 '{"profile_id":"embed","profile_revision":1,"provider_id":"fake","purpose":"EMBED","source_policy_id":"local-only",
 "source_policy_version":1,"source_classification":"PERSONAL","query_app_id":"library","query_classification":"PERSONAL",
 "retention_evidence":"fixture review","training_use_evidence":"fixture review","region_evidence":"fixture review"}')`, f.owner.UserID).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func (f personalHybridFixture) disable(t *testing.T, approval string) {
	t.Helper()
	var disabled bool
	err := f.admin.QueryRow(`SELECT cap_ai.disable_agent_processing($1,$2,'fixture review withdrawn')`, f.owner.UserID, approval).Scan(&disabled)
	if err != nil || !disabled {
		t.Fatal("disable PERSONAL approval", disabled, err)
	}
}

func (f personalHybridFixture) activate(t *testing.T, actor *auth.Identity, key, text string) Registration {
	t.Helper()
	var file string
	err := f.db.QueryRow(`INSERT INTO core.files(app_id,bucket,object_key,file_name,scan_status,sha256_hash,uploaded_by,mime_type,size_bytes,read_policy)
 SELECT app_id,bucket,$1,file_name,scan_status,sha256_hash,$2,mime_type,size_bytes,read_policy
 FROM core.files WHERE id=$3 RETURNING id`, key, actor.UserID, fileID).Scan(&file)
	if err != nil {
		t.Fatal(err)
	}
	return activateText(t, f.api, f.worker, actor, file, key, text)
}

func (f personalHybridFixture) enqueue(t *testing.T, actor *auth.Identity) {
	t.Helper()
	if err := f.api.EnqueueEmbeddings(context.Background(), actor, models.Profile{}); err != nil {
		t.Fatal(err)
	}
}

func (f personalHybridFixture) claim(t *testing.T) (*Lease, models.Profile) {
	t.Helper()
	ctx := context.Background()
	lease, err := f.worker.ClaimEmbedding(ctx, ownerID)
	if err != nil || lease == nil {
		t.Fatalf("expected a PERSONAL embedding lease, got %v, %v", lease, err)
	}
	profile, err := f.registry.ResolveEmbeddingLease(ctx, models.LeaseRef{
		JobID: lease.JobID, WorkerID: lease.WorkerID, RunID: lease.RunID, Fence: lease.Fence,
	})
	if err != nil {
		t.Fatal("resolve PERSONAL embedding lease", err)
	}
	return lease, profile
}

func (f personalHybridFixture) build(t *testing.T) {
	t.Helper()
	lease, profile := f.claim(t)
	if err := f.worker.BuildEmbeddings(context.Background(), *lease, profile); err != nil {
		t.Fatal("build PERSONAL embeddings", err)
	}
}

func (f personalHybridFixture) search(t *testing.T, actor *auth.Identity, query string, collections ...string) SearchResult {
	t.Helper()
	result, err := f.api.Search(context.Background(), actor, SearchRequest{
		Scope: Scope{Kind: "PERSONAL", CollectionIDs: collections}, Query: query, Retrieval: "HYBRID",
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestPersonalVectorOwnerIsolation(t *testing.T) {
	f := newPersonalHybridFixture(t)
	other := &auth.Identity{UserID: otherID, Role: f.owner.Role}
	first := f.activate(t, f.owner, "owner-a", "identical private text")
	second := f.activate(t, other, "owner-b", "identical private text")
	f.approve(t)

	if _, err := f.api.db.Exec(`SELECT cap_ai.queue_embeddings(NULL,NULL,'embed',1)`); err == nil {
		t.Fatal("API opened the worker's unscoped queue")
	}
	f.enqueue(t, f.owner)
	f.enqueue(t, f.owner)
	var jobs int
	err := f.db.QueryRow(`SELECT count(*) FROM cap_ai.ingestion_jobs WHERE kind='EMBED' AND version_id=$1`, first.VersionID).Scan(&jobs)
	if err != nil || jobs != 1 {
		t.Fatal("owner enqueue duplicated or lost the job", jobs, err)
	}
	firstLease, err := f.worker.ClaimEmbedding(context.Background(), ownerID)
	if err != nil || firstLease == nil || firstLease.VersionID != first.VersionID {
		t.Fatal("owner enqueue delegated the wrong version", firstLease, err)
	}
	if _, err := f.db.Exec(`UPDATE cap_ai.ingestion_jobs SET lease_until=now()-interval '1 second' WHERE id=$1`, firstLease.JobID); err != nil {
		t.Fatal(err)
	}
	resumed, profile := f.claim(t)
	if resumed.JobID != firstLease.JobID || resumed.Fence <= firstLease.Fence {
		t.Fatal("retry changed the job identity or reused the fence", firstLease, resumed)
	}
	if err := f.worker.BuildEmbeddings(context.Background(), *resumed, profile); err != nil {
		t.Fatal(err)
	}
	if lease, err := f.worker.ClaimEmbedding(context.Background(), ownerID); err != nil || lease != nil {
		t.Fatal("owner enqueue also scheduled a foreign document", lease, err)
	}
	f.enqueue(t, other)
	f.build(t)
	for _, owner := range []struct {
		actor *auth.Identity
		want  Registration
	}{{f.owner, first}, {other, second}} {
		result := f.search(t, owner.actor, "unmatched")
		if result.Status != "HYBRID" || len(result.Results) != 1 || result.Results[0].DocumentID != owner.want.DocumentID {
			t.Fatalf("equal vectors must rank only the caller's own document: %+v", result)
		}
	}
	var foreignCollection string
	if err := f.db.QueryRow(`SELECT collection_id FROM cap_ai.knowledge_documents WHERE id=$1`, second.DocumentID).Scan(&foreignCollection); err != nil {
		t.Fatal(err)
	}
	calls := f.spy.calls
	foreign := f.search(t, f.owner, "identical", foreignCollection)
	if len(foreign.Results) != 0 || foreign.Status != "LEXICAL_ONLY" || f.spy.calls != calls {
		t.Fatal("foreign collection qualified for hosted search", foreign, f.spy.calls)
	}
}

func TestPersonalHybridFusesFTSAndVector(t *testing.T) {
	f := newPersonalHybridFixture(t)
	common := f.activate(t, f.owner, "common", "director approves purchase")
	semantic := f.activate(t, f.owner, "semantic", "head of department authorizes requisition")
	f.approve(t)
	worker := Worker{Store: f.worker, WorkerID: ownerID}
	for range 2 {
		if err := worker.Once(context.Background()); err != nil {
			t.Fatal("background PERSONAL embedding", err)
		}
	}
	lexical := f.activate(t, f.owner, "lexical", "director attends meeting")

	result := f.search(t, f.owner, "director")
	if result.Status != "HYBRID" || len(result.Results) != 3 || result.Results[0].DocumentID != common.DocumentID {
		t.Fatalf("shared result must outrank lexical-only and vector-only hits: %+v", result)
	}
	ids := []string{result.Results[1].DocumentID, result.Results[2].DocumentID}
	if !slices.Contains(ids, lexical.DocumentID) || !slices.Contains(ids, semantic.DocumentID) || f.spy.calls != 3 {
		t.Fatal("fusion dropped a retrieval path", ids, f.spy.calls)
	}
}

func TestPersonalHybridNoApprovedEmbeddingFallback(t *testing.T) {
	f := newPersonalHybridFixture(t)
	doc := f.activate(t, f.owner, "fallback", "director private")
	assertFallback := func() {
		t.Helper()
		calls := f.spy.calls
		result := f.search(t, f.owner, "director")
		if result.Status != "LEXICAL_ONLY" || !slices.Equal(result.Warnings, []string{"SEMANTIC_UNAVAILABLE"}) ||
			len(result.Results) != 1 || result.Results[0].DocumentID != doc.DocumentID || f.spy.calls != calls {
			t.Fatal("expected authorized FTS without provider egress", result, f.spy.calls)
		}
	}
	f.enqueue(t, f.owner)
	if lease, err := f.worker.ClaimEmbedding(context.Background(), ownerID); err != nil || lease != nil {
		t.Fatal("unapproved document received a lease", lease, err)
	}
	assertFallback()

	approval := f.approve(t)
	assertFallback()
	f.enqueue(t, f.owner)
	f.disable(t, approval)
	if lease, err := f.worker.ClaimEmbedding(context.Background(), ownerID); err != nil || lease != nil {
		t.Fatal("pending job claimed after approval revocation", lease, err)
	}
	assertFallback()
	approval = f.approve(t)
	f.enqueue(t, f.owner)
	f.build(t)
	f.disable(t, approval)
	assertFallback()
}

func TestPersonalHybridRechecksApprovalAtSaveAndActivation(t *testing.T) {
	t.Run("save", func(t *testing.T) {
		f := newPersonalHybridFixture(t)
		f.activate(t, f.owner, "save", "director private")
		approval := f.approve(t)
		f.enqueue(t, f.owner)
		lease, profile := f.claim(t)
		f.spy.before = func() { f.disable(t, approval) }
		if err := f.worker.BuildEmbeddings(context.Background(), *lease, profile); err != ErrStaleLease {
			t.Fatal("saved vector after processing approval was withdrawn", err)
		}
		var vectors int
		if err := f.db.QueryRow(`SELECT count(*) FROM cap_ai.chunk_embeddings`).Scan(&vectors); err != nil || vectors != 0 || f.spy.calls != 1 {
			t.Fatal("revoked embedding persisted or provider was not reached", vectors, f.spy.calls, err)
		}
	})
	t.Run("activate", func(t *testing.T) {
		f := newPersonalHybridFixture(t)
		f.activate(t, f.owner, "activation", "director private")
		approval := f.approve(t)
		f.enqueue(t, f.owner)
		lease, profile := f.claim(t)
		var work embeddingWork
		err := decodeRow(f.worker.db.QueryRow(`SELECT cap_ai.embedding_work($1,$2,$3,$4,$5,$6)`,
			lease.JobID, lease.WorkerID, lease.Fence, lease.RunID, profile.ID, profile.Revision), &work)
		if err != nil || work.Chunk == nil {
			t.Fatal("prepare chunk", work, err)
		}
		_, err = f.worker.db.Exec(`SELECT cap_ai.save_embedding($1,$2,$3,$4,$5,$6,$7,'[1,0,0]'::public.vector)`,
			lease.JobID, lease.WorkerID, lease.Fence, lease.RunID, work.GenerationID, work.Chunk.ID, work.Chunk.Hash)
		if err != nil {
			t.Fatal("save under active policy", err)
		}
		f.disable(t, approval)
		_, err = f.worker.db.Exec(`SELECT cap_ai.finish_embedding($1,$2,$3,$4,$5,true)`,
			lease.JobID, lease.WorkerID, lease.Fence, lease.RunID, work.GenerationID)
		if dbError(err) != ErrStaleLease {
			t.Fatal("published generation after processing approval was withdrawn", err)
		}
	})
}

func TestPersonalVectorCurrentGenerationFences(t *testing.T) {
	f := newPersonalHybridFixture(t)
	doc := f.activate(t, f.owner, "fenced", "director private")
	f.approve(t)
	f.enqueue(t, f.owner)
	f.build(t)
	for _, query := range []struct {
		revision int
		vector   string
		want     bool
	}{{1, "[1,0,0]", true}, {2, "[1,0,0]", false}, {1, "[1,0]", false}} {
		var rows []candidate
		err := decodeRow(f.api.db.QueryRow(`SELECT cap_ai.search_personal_vectors($1,$2,'embed',$3,$4::public.vector,NULL)`,
			f.owner.UserID, f.owner.Role, query.revision, query.vector), &rows)
		if query.want && (err != nil || len(rows) != 1 || rows[0].Hit.DocumentID != doc.DocumentID) {
			t.Fatal("compatible generation unavailable", rows, err)
		}
		if !query.want && err != ErrNotFound {
			t.Fatal("incompatible profile or dimension qualified", query, rows, err)
		}
	}

	if _, err := f.db.Exec(`UPDATE cap_ai.knowledge_documents SET policy_revision=policy_revision+1 WHERE id=$1`, doc.DocumentID); err != nil {
		t.Fatal(err)
	}
	calls := f.spy.calls
	result := f.search(t, f.owner, "director")
	if result.Status != "LEXICAL_ONLY" || len(result.Results) != 1 || f.spy.calls != calls {
		t.Fatal("stale policy generation remained searchable", result, f.spy.calls)
	}

	if _, err := f.db.Exec(`UPDATE core.files SET scan_status='INFECTED'
	 WHERE id=(SELECT source_file_id FROM cap_ai.document_versions WHERE id=$1)`, doc.VersionID); err != nil {
		t.Fatal(err)
	}
	result = f.search(t, f.owner, "director")
	if result.Status != "LEXICAL_ONLY" || len(result.Results) != 0 || f.spy.calls != calls {
		t.Fatal("unclean source remained searchable", result, f.spy.calls)
	}
}

func TestPersonalHybridDiscardsVectorAfterQueryApprovalRevocation(t *testing.T) {
	f := newPersonalHybridFixture(t)
	f.activate(t, f.owner, "query-revoked", "director private")
	approval := f.approve(t)
	f.enqueue(t, f.owner)
	f.build(t)
	f.spy.before = func() { f.disable(t, approval) }

	result := f.search(t, f.owner, "director")
	if result.Status != "LEXICAL_ONLY" || !slices.Equal(result.Warnings, []string{"SEMANTIC_UNAVAILABLE"}) ||
		len(result.Results) != 1 || f.spy.calls != 2 {
		t.Fatal("query response used vector after approval withdrawal", result, f.spy.calls)
	}
}

func TestPersonalHybridMigrationPreservesAppEmbedding(t *testing.T) {
	db, api, actor, _ := semanticFixture(t)
	applyHybridFixtureMigrations(t, db, "0005_model_registry.sql", "0008_personal_model_processing.sql", "0009_personal_hybrid.sql")
	if _, err := db.Exec(`INSERT INTO cap_ai.model_candidates(profile_id, revision, adapter_key, declaration_digest, declaration)
	 VALUES ('embed',1,'bounded-http-v1',repeat('a',64),'{}');
	 UPDATE cap_ai.global_model_slots SET profile_id='embed',profile_revision=1,slot_revision=1 WHERE capability='EMBEDDING'`); err != nil {
		t.Fatal(err)
	}
	var name string
	if err := db.QueryRow("SELECT current_database()").Scan(&name); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	api.db = testutil.As(t, u.String(), "dxos_ai_api")
	gatewayDB := testutil.As(t, u.String(), "dxos_ai_gateway")
	spy := &embeddingProvider{}
	gateway := &models.Gateway{
		Policy:     &models.Policy{Enabled: true, Authority: models.DatabaseAuthority{DB: gatewayDB}},
		Operations: &models.OperationStore{DB: gatewayDB}, Providers: map[string]models.Provider{"fake": spy},
	}
	worker := workerStore(t, db)
	for _, store := range []*Store{api, worker} {
		store.ConfigureModels(gateway, models.Profile{}, models.Profile{})
		store.ConfigureModelResolver(&models.Registry{DB: gatewayDB})
	}
	if err := (&Worker{Store: worker, WorkerID: ownerID}).Once(context.Background()); err != nil {
		t.Fatal("APP embedding after migration", err)
	}
	result, err := api.Search(context.Background(), actor, SearchRequest{
		Scope: Scope{Kind: "APP", AppID: "library"}, Query: "unmatched", Retrieval: "HYBRID",
	})
	if err != nil || result.Status != "HYBRID" || len(result.Results) != 1 || spy.calls != 2 {
		t.Fatal("PERSONAL migration changed APP search", result, spy.calls, err)
	}
}

func TestPersonalHybridRebuildsAfterApprovalRenewal(t *testing.T) {
	f := newPersonalHybridFixture(t)
	doc := f.activate(t, f.owner, "renewal", "director private")
	ctx := context.Background()
	worker := Worker{Store: f.worker, WorkerID: ownerID}
	approval := f.approve(t)
	if err := worker.Once(ctx); err != nil {
		t.Fatal("first build", err)
	}
	var originalJob, originalRun, originalOperation string
	err := f.db.QueryRow(`SELECT j.id, g.run_id, mo.operation_key
	 FROM cap_ai.ingestion_jobs j JOIN cap_ai.embedding_generations g ON g.job_id=j.id
	 JOIN cap_ai.model_operations mo ON mo.operation_key LIKE 'embed:' || j.id::text || ':%'
	 WHERE j.version_id=$1 AND g.state='READY'`, doc.VersionID).Scan(&originalJob, &originalRun, &originalOperation)
	if err != nil {
		t.Fatal(err)
	}

	f.disable(t, approval)
	f.approve(t)
	f.enqueue(t, f.owner)
	unready := f.search(t, f.owner, "director")
	if unready.Status != "LEXICAL_ONLY" || len(unready.Results) != 1 || f.spy.calls != 1 {
		t.Fatal("withdrawn generation qualified after replacement approval", unready, f.spy.calls)
	}

	lease, err := f.worker.ClaimEmbedding(ctx, ownerID)
	if err != nil || lease == nil || lease.JobID == originalJob || lease.RunID != originalRun {
		t.Fatal("replacement approval needs its own job on the same parse run", lease, err)
	}
	if _, err = f.db.Exec(`UPDATE cap_ai.ingestion_jobs SET lease_until=now()-interval '1 second' WHERE id=$1`, lease.JobID); err != nil {
		t.Fatal(err)
	}
	if err := worker.Once(ctx); err != nil {
		t.Fatal("recover replacement approval's embedding lease", err)
	}
	var buildCount, originalCount int
	err = f.db.QueryRow(`SELECT count(*), count(*) FILTER (WHERE operation_key=$1)
	 FROM cap_ai.model_operations WHERE operation_key LIKE 'embed:%'`, originalOperation).Scan(&buildCount, &originalCount)
	if err != nil || buildCount != 2 || originalCount != 1 || f.spy.calls != 2 {
		t.Fatal("expected exactly one billable operation per authority snapshot", buildCount, originalCount, f.spy.calls, err)
	}
	var newRuns int
	err = f.db.QueryRow(`SELECT count(*) FROM cap_ai.embedding_generations
	 WHERE job_id=$1 AND run_id=$2 AND model_profile_id='embed' AND profile_revision=1 AND state='READY'`,
		lease.JobID, originalRun).Scan(&newRuns)
	if err != nil || newRuns != 1 {
		t.Fatal("retry changed job, profile, or parse run", newRuns, err)
	}
	result := f.search(t, f.owner, "unmatched")
	if result.Status != "HYBRID" || len(result.Results) != 1 || result.Results[0].DocumentID != doc.DocumentID || f.spy.calls != 3 {
		t.Fatal("renewed approval did not restore owner-only semantic retrieval", result, f.spy.calls)
	}
	var rows []candidate
	err = decodeRow(f.api.db.QueryRow(`SELECT cap_ai.search_personal_vectors($1,$2,'embed',1,'[1,0,0]'::public.vector,NULL)`,
		f.owner.UserID, f.owner.Role), &rows)
	if err != nil || len(rows) != 1 {
		t.Fatal("replacement search ranked an old generation", rows, err)
	}
}

type personalEmbeddingVectorProvider struct {
	documentVector, queryVector []float32
	documentCalls, queryCalls   int
}

func (p *personalEmbeddingVectorProvider) Invoke(_ context.Context, _ models.Profile, payload models.Payload) (models.Result, error) {
	vector := p.queryVector
	if len(payload.Texts) > 0 {
		p.documentCalls++
		vector = p.documentVector
	} else {
		p.queryCalls++
	}
	return models.Result{Vector: vector, Usage: &models.Usage{InputTokens: 10}}, nil
}

func TestPersonalHybridZeroDocumentVectorFallback(t *testing.T) {
	t.Run("provider", func(t *testing.T) {
		f := newPersonalHybridFixture(t)
		f.activate(t, f.owner, "zero", "director private")
		f.approve(t)
		spy := &personalEmbeddingVectorProvider{documentVector: []float32{0, 0, 0}, queryVector: []float32{1, 0, 0}}
		f.worker.gateway.Providers["fake"] = spy
		err := (&Worker{Store: f.worker, WorkerID: ownerID}).Once(context.Background())
		if err != nil && err != ErrStaleLease {
			t.Fatal("zero vector build returned unexpected error", err)
		}

		result := f.search(t, f.owner, "director")
		if result.Status != "LEXICAL_ONLY" || !slices.Equal(result.Warnings, []string{"SEMANTIC_UNAVAILABLE"}) ||
			len(result.Results) != 1 || spy.documentCalls != 1 || spy.queryCalls != 0 {
			t.Fatal("a zero document vector qualified for query egress", result, spy.documentCalls, spy.queryCalls)
		}
		var vectors int
		if err := f.db.QueryRow(`SELECT count(*) FROM cap_ai.chunk_embeddings`).Scan(&vectors); err != nil || vectors != 0 {
			t.Fatal("zero vector persisted", vectors, err)
		}
	})
	t.Run("stored-vector", func(t *testing.T) {
		f := newPersonalHybridFixture(t)
		f.activate(t, f.owner, "stored-zero", "director private")
		f.approve(t)
		f.enqueue(t, f.owner)
		lease, profile := f.claim(t)
		var work embeddingWork
		err := decodeRow(f.worker.db.QueryRow(`SELECT cap_ai.embedding_work($1,$2,$3,$4,$5,$6)`,
			lease.JobID, lease.WorkerID, lease.Fence, lease.RunID, profile.ID, profile.Revision), &work)
		if err != nil || work.Chunk == nil {
			t.Fatal("prepare chunk", work, err)
		}
		_, err = f.worker.db.Exec(`SELECT cap_ai.save_embedding($1,$2,$3,$4,$5,$6,$7,'[1,0,0]'::public.vector)`,
			lease.JobID, lease.WorkerID, lease.Fence, lease.RunID, work.GenerationID, work.Chunk.ID, work.Chunk.Hash)
		if err != nil {
			t.Fatal("save nonzero vector", err)
		}
		if _, err := f.db.Exec(`UPDATE cap_ai.chunk_embeddings SET embedding='[0,0,0]'::public.vector`); err != nil {
			t.Fatal(err)
		}
		_, err = f.worker.db.Exec(`SELECT cap_ai.finish_embedding($1,$2,$3,$4,$5,true)`,
			lease.JobID, lease.WorkerID, lease.Fence, lease.RunID, work.GenerationID)
		if dbError(err) != ErrStaleLease {
			t.Fatal("zero vector activated", err)
		}
		// Simulate an unusable generation that was already stored as READY.
		if _, err := f.db.Exec(`UPDATE cap_ai.embedding_generations SET state='READY' WHERE id=$1`, work.GenerationID); err != nil {
			t.Fatal(err)
		}
		spy := &personalEmbeddingVectorProvider{queryVector: []float32{1, 0, 0}}
		f.api.gateway.Providers["fake"] = spy
		result := f.search(t, f.owner, "director")
		if result.Status != "LEXICAL_ONLY" || !slices.Equal(result.Warnings, []string{"SEMANTIC_UNAVAILABLE"}) ||
			len(result.Results) != 1 || spy.queryCalls != 0 {
			t.Fatal("stored zero vector qualified for query egress", result, spy.queryCalls)
		}
	})
}

func TestPersonalHybridZeroQueryVectorFallback(t *testing.T) {
	f := newPersonalHybridFixture(t)
	f.activate(t, f.owner, "zero-query", "director private")
	f.approve(t)
	f.enqueue(t, f.owner)
	f.build(t)
	spy := &personalEmbeddingVectorProvider{queryVector: []float32{0, 0, 0}}
	f.api.gateway.Providers["fake"] = spy

	result := f.search(t, f.owner, "director")
	if result.Status != "LEXICAL_ONLY" || !slices.Equal(result.Warnings, []string{"SEMANTIC_UNAVAILABLE"}) ||
		len(result.Results) != 1 || spy.queryCalls != 1 {
		t.Fatal("zero query vector qualified for hybrid search", result, spy.queryCalls)
	}
}
