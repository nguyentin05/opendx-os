//go:build integration

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package knowledge

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/opendx-os/opendx-os/capabilities/internal/ai/models"
)

// moveEmbeddingProfile records a reviewed revision and, when approved, the
// processing policy that lets it index this non-personal source. It then makes it
// the live EMBED selection through the real reconciliation path.
func moveEmbeddingProfile(t *testing.T, db *sql.DB, profile models.Profile, approved bool) models.Profile {
	t.Helper()
	limits, err := json.Marshal(profile.Limits)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO cap_ai.model_profiles(id,revision,purpose,execution,provider_id,model_id,dimension,limits,enabled)
		VALUES($1,$2,'EMBED','HOSTED','fake','fake',$3,$4,false) ON CONFLICT DO NOTHING`,
		profile.ID, profile.Revision, profile.Dimension, string(limits)); err != nil {
		t.Fatal(err)
	}
	if approved {
		if _, err = db.Exec(`INSERT INTO cap_ai.processing_policy_profiles(policy_id,policy_version,model_profile_id,profile_revision)
			VALUES('reviewed',1,$1,$2) ON CONFLICT DO NOTHING`, profile.ID, profile.Revision); err != nil {
			t.Fatal(err)
		}
	}
	selectDeployment(t, db, profile)
	return profile
}

// embeddingFacts is the indexing state a re-embed must not disturb: the parsed
// run, its chunks and the parse work already done.
type embeddingFacts struct {
	activeRun   string
	runs        int
	chunks      int
	parseJobs   int
	embedJobs   int
	generations int
	vectors     int
}

func readEmbeddingFacts(t *testing.T, db *sql.DB) embeddingFacts {
	t.Helper()
	var facts embeddingFacts
	for _, item := range []struct {
		query string
		into  *int
	}{
		{`SELECT count(*) FROM cap_ai.processing_runs`, &facts.runs},
		{`SELECT count(*) FROM cap_ai.knowledge_chunks`, &facts.chunks},
		{`SELECT count(*) FROM cap_ai.ingestion_jobs WHERE kind='PARSE'`, &facts.parseJobs},
		{`SELECT count(*) FROM cap_ai.ingestion_jobs WHERE kind='EMBED'`, &facts.embedJobs},
		{`SELECT count(*) FROM cap_ai.embedding_generations`, &facts.generations},
		{`SELECT count(*) FROM cap_ai.chunk_embeddings`, &facts.vectors},
	} {
		if err := db.QueryRow(item.query).Scan(item.into); err != nil {
			t.Fatal(item.query, err)
		}
	}
	if err := db.QueryRow(`SELECT v.active_run_id FROM cap_ai.knowledge_documents d JOIN cap_ai.document_versions v ON v.id=d.active_version_id JOIN cap_ai.knowledge_collections c ON c.id=d.collection_id WHERE c.app_id='library' LIMIT 1`).Scan(&facts.activeRun); err != nil {
		t.Fatal(err)
	}
	return facts
}

// TestWorkerDeploymentReembed pins the embedding-only change: the fixed current
// profile is the only one queued or claimed, the existing parsed run is reused
// (no reparse, re-OCR or lexical reset), old generations stay isolated, and only a
// complete, approved generation becomes usable.
func TestWorkerDeploymentReembed(t *testing.T) {
	db, api, actor, first := semanticFixture(t)
	ctx := context.Background()
	provider := &dimensionProvider{}
	gateway := &models.Gateway{
		Policy:     &models.Policy{Enabled: true, Authority: models.DatabaseAuthority{DB: db}},
		Operations: &models.OperationStore{DB: gatewayLogin(t, db)},
		Providers:  map[string]models.Provider{first.ProviderID: provider},
	}
	worker := workerStore(t, db)
	api.ConfigureModels(gateway, first, models.Profile{})
	worker.ConfigureModels(gateway, first, models.Profile{})

	// The first generation is complete and ready on the existing run.
	if err := api.EnqueueEmbeddings(ctx, actor); err != nil {
		t.Fatal(err)
	}
	lease, err := worker.ClaimEmbedding(ctx, ownerID, first)
	if err != nil || lease == nil {
		t.Fatal("first claim", lease, err)
	}
	if err = worker.BuildEmbeddings(ctx, *lease, first); err != nil {
		t.Fatal("first build", err)
	}
	before := readEmbeddingFacts(t, db)
	var ready int
	if err = db.QueryRow(`SELECT count(*) FROM cap_ai.embedding_generations WHERE state='READY' AND profile_revision=1 AND dimension=3`).Scan(&ready); err != nil || ready != 1 {
		t.Fatal("first generation not ready", ready, err)
	}
	if provider.calls != before.chunks {
		t.Fatalf("first generation calls %d, chunks %d", provider.calls, before.chunks)
	}

	// A new reviewed revision, not yet approved for this source: nothing is queued
	// or sent, even though the selection moved to it.
	newer := first
	newer.Revision = 2
	newer.Dimension = 5
	moveEmbeddingProfile(t, db, newer, false)
	api.ConfigureModels(gateway, newer, models.Profile{})
	worker.ConfigureModels(gateway, newer, models.Profile{})
	if err = api.EnqueueEmbeddings(ctx, actor); err != nil {
		t.Fatal(err)
	}
	if lease, err = worker.ClaimEmbedding(ctx, ownerID, newer); err != nil || lease != nil {
		t.Fatal("unapproved revision claimed", lease, err)
	}
	if provider.calls != before.chunks {
		t.Fatalf("unapproved revision sent %d calls", provider.calls-before.chunks)
	}
	var unapprovedJobs int
	if err = db.QueryRow(`SELECT count(*) FROM cap_ai.ingestion_jobs WHERE kind='EMBED' AND profile_key LIKE 'embed@2%'`).Scan(&unapprovedJobs); err != nil {
		t.Fatal(err)
	}
	if unapprovedJobs != 0 {
		t.Fatalf("an unapproved source was enqueued: %d jobs", unapprovedJobs)
	}

	// Approve the new revision; the same parsed run is re-embedded for it.
	if _, err = db.Exec(`INSERT INTO cap_ai.processing_policy_profiles(policy_id,policy_version,model_profile_id,profile_revision) VALUES('reviewed',1,'embed',2) ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	// Before its generation completes, lexical retrieval is still available and the
	// old vectors are not usable for the new identity.
	search, err := api.Search(ctx, actor, SearchRequest{Scope: Scope{Kind: "APP", AppID: "library"}, Query: "Director", Retrieval: "HYBRID"})
	if err != nil || len(search.Results) == 0 {
		t.Fatal("lexical retrieval lost during re-embed", search, err)
	}
	var newerVectors string
	if err = db.QueryRow(`SELECT cap_ai.search_vectors($1,'role_library_read','library','embed',2,'[1,0,0,0,0]'::vector,'{}'::uuid[])`, ownerID).Scan(&newerVectors); err != nil {
		t.Fatal(err)
	}
	if newerVectors != "[]" {
		t.Fatalf("old generation served the new identity: %s", newerVectors)
	}

	// Once queues the exact current profile (the old job is already succeeded and
	// cannot be duplicated), claims it and builds one complete generation. The
	// hybrid search above embedded its own query, so only the delta matters.
	callsBeforeReembed := provider.calls
	if err = (&Worker{Store: worker, WorkerID: ownerID, Parser: nil}).Once(ctx); err != nil {
		// Once tries parse work first; the fixture has no parseable source left, so it
		// falls through to the embedding job. A parser is not needed for EMBED.
		t.Fatal("re-embed", err)
	}
	var newReady, oldReady, oldVectors, newVectors int
	for _, item := range []struct {
		query string
		into  *int
	}{
		{`SELECT count(*) FROM cap_ai.embedding_generations WHERE state='READY' AND profile_revision=2 AND dimension=5 AND completed_count=expected_count`, &newReady},
		{`SELECT count(*) FROM cap_ai.embedding_generations WHERE state='READY' AND profile_revision=1 AND dimension=3 AND completed_count=expected_count`, &oldReady},
		{`SELECT count(*) FROM cap_ai.chunk_embeddings e JOIN cap_ai.embedding_generations g ON g.id=e.generation_id WHERE g.profile_revision=1 AND public.vector_dims(e.embedding)=3`, &oldVectors},
		{`SELECT count(*) FROM cap_ai.chunk_embeddings e JOIN cap_ai.embedding_generations g ON g.id=e.generation_id WHERE g.profile_revision=2 AND public.vector_dims(e.embedding)=5`, &newVectors},
	} {
		if err = db.QueryRow(item.query).Scan(item.into); err != nil {
			t.Fatal(item.query, err)
		}
	}
	if newReady != 1 || newVectors != before.chunks {
		t.Fatalf("new generation incomplete: ready=%d vectors=%d chunks=%d", newReady, newVectors, before.chunks)
	}
	if oldReady != 1 || oldVectors != before.chunks {
		t.Fatalf("old generation disturbed: ready=%d vectors=%d", oldReady, oldVectors)
	}

	// No reparse, re-OCR or lexical reset: the same run and chunks, and no new parse
	// job or processing run.
	after := readEmbeddingFacts(t, db)
	if after.activeRun != before.activeRun || after.runs != before.runs ||
		after.chunks != before.chunks || after.parseJobs != before.parseJobs {
		t.Fatalf("embedding-only change reparsed the source: before=%+v after=%+v", before, after)
	}
	if provider.calls-callsBeforeReembed != before.chunks {
		t.Fatalf("one send per chunk expected, got %d for %d chunks", provider.calls-callsBeforeReembed, before.chunks)
	}

	// The superseded revision keeps its own job and vectors but is not claimable.
	var oldState string
	if err = db.QueryRow(`SELECT state FROM cap_ai.ingestion_jobs WHERE kind='EMBED' AND profile_key LIKE 'embed@1%' ORDER BY updated_at LIMIT 1`).Scan(&oldState); err != nil {
		t.Fatal(err)
	}
	if oldState != "SUCCEEDED" {
		t.Fatalf("old job state %q", oldState)
	}
	api.ConfigureModels(gateway, first, models.Profile{})
	worker.ConfigureModels(gateway, first, models.Profile{})
	if lease, err = worker.ClaimEmbedding(ctx, ownerID, first); err != nil || lease != nil {
		t.Fatal("superseded generation claimable", lease, err)
	}
}

// TestWorkerRejectsObsoleteLease pins the lease boundaries of a profile change:
// obsolete pending or expired jobs never block parse work or the current-profile
// job, a stale fence cannot write, an eligible job resumes after a restart, and an
// ambiguous call is never resent.
func TestWorkerRejectsObsoleteLease(t *testing.T) {
	db, api, actor, first := semanticFixture(t)
	ctx := context.Background()
	provider := &dimensionProvider{}
	gateway := &models.Gateway{
		Policy:     &models.Policy{Enabled: true, Authority: models.DatabaseAuthority{DB: db}},
		Operations: &models.OperationStore{DB: gatewayLogin(t, db)},
		Providers:  map[string]models.Provider{first.ProviderID: provider},
	}
	worker := workerStore(t, db)
	api.ConfigureModels(gateway, first, models.Profile{})
	worker.ConfigureModels(gateway, first, models.Profile{})

	// An obsolete job is queued for the first profile and left pending.
	if err := api.EnqueueEmbeddings(ctx, actor); err != nil {
		t.Fatal(err)
	}
	lease, err := worker.ClaimEmbedding(ctx, ownerID, first)
	if err != nil || lease == nil {
		t.Fatal("obsolete claim", lease, err)
	}
	// A stale fence is one whose generation was superseded: drop the lease without
	// completing it, then move the deployment to the next revision.
	if _, err = db.Exec(`UPDATE cap_ai.ingestion_jobs SET lease_until=now()-interval '1 second' WHERE id=$1`, lease.JobID); err != nil {
		t.Fatal(err)
	}
	newer := first
	newer.Revision = 2
	newer.Dimension = 5
	moveEmbeddingProfile(t, db, newer, true)
	api.ConfigureModels(gateway, newer, models.Profile{})
	worker.ConfigureModels(gateway, newer, models.Profile{})

	// The stale fence cannot write: the old profile is no longer the live selection
	// and the generation is not the one the new deployment selected.
	if _, err = db.Exec(`SELECT cap_ai.embedding_work($1,$2,$3,$4,$5,$6)`, lease.JobID, ownerID, lease.Fence, lease.RunID, first.ID, first.Revision); err == nil {
		t.Fatal("a stale fence read embedding work after the cutover")
	}
	if _, err = db.Exec(`SELECT cap_ai.finish_embedding($1,$2,$3,$4,$5,true)`, lease.JobID, ownerID, lease.Fence, lease.RunID, lease.RunID); err == nil {
		t.Fatal("a stale fence finalized a generation")
	}
	// A pending parse job is still claimable: parse work is independent of model
	// health and of every obsolete embedding job.
	if _, err = api.Register(ctx, actor, "obsolete-lease", RegisterRequest{FileID: fileID, ParserProfile: "standard-text-v1"}); err != nil {
		t.Fatal(err)
	}
	parseLease, err := worker.Claim(ctx, ownerID, time.Now())
	if err != nil || parseLease == nil {
		t.Fatal("obsolete embedding jobs blocked parse work", parseLease, err)
	}

	// The current-profile job is claimable even though an older PENDING job for the
	// superseded profile sorts first.
	if err = api.EnqueueEmbeddings(ctx, actor); err != nil {
		t.Fatal(err)
	}
	if lease, err = worker.ClaimEmbedding(ctx, ownerID, newer); err != nil || lease == nil {
		t.Fatal("obsolete pending job blocked the current profile", lease, err)
	}
	if lease.WorkerID != ownerID {
		t.Fatal("foreign worker")
	}
	// Do not build yet: expire this lease to model a worker crash. The job becomes
	// eligible again and the restarted worker claims it with a newer fence.
	if _, err = db.Exec(`UPDATE cap_ai.ingestion_jobs SET lease_until=now()-interval '1 second' WHERE id=$1`, lease.JobID); err != nil {
		t.Fatal(err)
	}
	resumed, err := worker.ClaimEmbedding(ctx, ownerID, newer)
	if err != nil || resumed == nil || resumed.JobID != lease.JobID || resumed.Fence <= lease.Fence {
		t.Fatal("eligible job did not resume after restart", resumed, lease, err)
	}
	if err = worker.BuildEmbeddings(ctx, *resumed, newer); err != nil {
		t.Fatal("resumed build", err)
	}
	var resumedReady int
	if err = db.QueryRow(`SELECT count(*) FROM cap_ai.embedding_generations WHERE state='READY' AND profile_revision=2 AND completed_count=expected_count`).Scan(&resumedReady); err != nil || resumedReady != 1 {
		t.Fatal("resumed generation incomplete", resumedReady, err)
	}

	// Resuming advanced the fence on the same job instead of minting a second job
	// or a second generation for the current profile.
	var generations int
	if err = db.QueryRow(`SELECT count(*) FROM cap_ai.embedding_generations WHERE model_profile_id=$1 AND profile_revision=$2`,
		newer.ID, newer.Revision).Scan(&generations); err != nil {
		t.Fatal(err)
	}
	if generations != 1 {
		t.Fatalf("a resumed job left %d generations for the current profile", generations)
	}

	// An ambiguous external call is terminal: the same operation key is never sent
	// again, so a bounded retry cannot mint a second dispatch.
	ambiguous := &ambiguousProvider{}
	newerStill := newer
	newerStill.Revision = 3
	newerStill.Dimension = 5
	moveEmbeddingProfile(t, db, newerStill, true)
	ambiguousGateway := &models.Gateway{
		Policy:     &models.Policy{Enabled: true, Authority: models.DatabaseAuthority{DB: db}},
		Operations: &models.OperationStore{DB: gatewayLogin(t, db)},
		Providers:  map[string]models.Provider{first.ProviderID: ambiguous},
	}
	api.ConfigureModels(ambiguousGateway, newerStill, models.Profile{})
	worker.ConfigureModels(ambiguousGateway, newerStill, models.Profile{})
	if err = api.EnqueueEmbeddings(ctx, actor); err != nil {
		t.Fatal(err)
	}
	if lease, err = worker.ClaimEmbedding(ctx, ownerID, newerStill); err != nil || lease == nil {
		t.Fatal("ambiguous claim", lease, err)
	}
	if err = worker.BuildEmbeddings(ctx, *lease, newerStill); err == nil {
		t.Fatal("an ambiguous call was reported as success")
	}
	if ambiguous.calls != 1 {
		t.Fatalf("ambiguous call dispatched %d times", ambiguous.calls)
	}
	var ambiguousState, ambiguousCode string
	if err = db.QueryRow(`SELECT state, coalesce(error_code,'') FROM cap_ai.ingestion_jobs WHERE id=$1`, lease.JobID).Scan(&ambiguousState, &ambiguousCode); err != nil {
		t.Fatal(err)
	}
	if ambiguousState != "FAILED" || ambiguousCode != "MODEL_CALL_UNCERTAIN" {
		t.Fatalf("ambiguous job %s/%s", ambiguousState, ambiguousCode)
	}
	// A second worker pass does not requeue or resend the uncertain operation.
	if err = (&Worker{Store: worker, WorkerID: ownerID}).Once(ctx); err != nil {
		t.Fatal("second pass", err)
	}
	if ambiguous.calls != 1 {
		t.Fatalf("ambiguous operation was resent: %d calls", ambiguous.calls)
	}
}

// ambiguousProvider reports an uncertain external outcome once and refuses a
// second dispatch by counting every call it receives.
type ambiguousProvider struct{ calls int }

func (p *ambiguousProvider) Invoke(context.Context, models.Profile, models.Payload) (models.Result, error) {
	p.calls++
	return models.Result{}, models.ErrUncertain
}
