//go:build integration

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
package knowledge

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/opendx-os/opendx-os/capabilities/internal/ai/knowledge/docir"
	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
	"github.com/opendx-os/opendx-os/capabilities/internal/testutil"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const packageIncarnation = "77777777-7777-4777-8777-777777777777"

func packageFixture(t *testing.T) (*sql.DB, *Store, *auth.Identity, string) {
	t.Helper()
	db, _, actor := derivedFixture(t)
	migration, e := os.ReadFile("../../../platform/postgres/migrations/cap_ai/0003_package_knowledge.sql")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(string(migration)); e != nil {
		t.Fatal(e)
	}
	content := []byte("# Package policy\n\nDirector approval.\n")
	hash := fmt.Sprintf("%x", sha256.Sum256(content))
	ref := "library/policy/" + hash
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "library/policy"), 0700)
	os.WriteFile(filepath.Join(root, ref), content, 0600)
	source := map[string]any{"source_id": "policy", "incarnation": packageIncarnation, "authorization_generation": 1, "declaration_hash": strings.Repeat("d", 64), "artifact_ref": ref, "sha256": hash, "source_path": "knowledge/policy.md", "media_type": "text/markdown", "size_bytes": len(content), "parser_profile": "standard-text-v1", "safety_state": "APPROVED"}
	publication := map[string]any{"generation": 1, "spec_digest": strings.Repeat("b", 64), "package_digest": strings.Repeat("c", 64), "sources": []any{source}}
	raw, _ := json.Marshal(publication)
	_, e = db.Exec(`UPDATE core.apps SET publication_generation=1,spec=jsonb_set(jsonb_set(spec,'{knowledge,sources}','[{"id":"policy","path":"knowledge/policy.md","mediaType":"text/markdown","indexingProfile":"standard-text-v1","access":{"mode":"ROLE","allowedRoles":["role_library_user"]}}]'),'{_publication}',$1::jsonb) WHERE app_id='library'`, string(raw))
	if e != nil {
		t.Fatal(e)
	}
	store := NewStore(db)
	if e = store.ConfigurePackages(true, root); e != nil {
		t.Fatal(e)
	}
	return db, store, actor, hash
}
func TestPackageReplay(t *testing.T) {
	db, store, _, _ := packageFixture(t)
	ctx := context.Background()
	for range 3 {
		if e := store.ReconcilePackage(ctx, "library", 1, strings.Repeat("b", 64)); e != nil {
			t.Fatal(e)
		}
	}
	for _, table := range []string{"package_sources", "package_source_revisions", "knowledge_documents", "document_versions", "ingestion_jobs"} {
		var n int
		db.QueryRow("SELECT count(*) FROM cap_ai." + table).Scan(&n)
		if n != 1 {
			t.Fatal("reconcile duplicated", table, n)
		}
	}
}
func TestDelayedDispatchRemoval(t *testing.T) {
	db, store, actor, _ := packageFixture(t)
	ctx := context.Background()
	if e := store.ReconcilePackage(ctx, "library", 1, strings.Repeat("b", 64)); e != nil {
		t.Fatal(e)
	}
	var doc string
	db.QueryRow(`SELECT id::text FROM cap_ai.knowledge_documents`).Scan(&doc)
	if _, e := NewPolicy(store.db).CanRead(ctx, actor, doc); e != nil {
		t.Fatal("declared ROLE source denied", e)
	}
	if _, e := db.Exec(`UPDATE core.apps SET publication_generation=2,spec=jsonb_set(jsonb_set(jsonb_set(spec,'{knowledge,sources}','[]'),'{_publication,sources}','[]'),'{_publication,generation}','2') WHERE app_id='library'`); e != nil {
		t.Fatal(e)
	}
	if _, e := NewPolicy(store.db).CanRead(ctx, actor, doc); e == nil {
		t.Fatal("removed source readable before dispatch")
	}
	if e := store.ReconcilePackage(ctx, "library", 1, strings.Repeat("b", 64)); e == nil {
		t.Fatal("stale event accepted")
	}
	var n int
	db.QueryRow(`SELECT count(*) FROM cap_ai.ingestion_jobs`).Scan(&n)
	if n != 1 {
		t.Fatal("stale event duplicated desired job")
	}
}
func TestAppReadyWithoutIndex(t *testing.T) {
	db, store, actor, hash := packageFixture(t)
	ctx := context.Background()
	if e := store.ReconcilePackage(ctx, "library", 1, strings.Repeat("b", 64)); e != nil {
		t.Fatal(e)
	}
	var status string
	db.QueryRow(`SELECT status FROM core.apps WHERE app_id='library'`).Scan(&status)
	if status != "Ready" {
		t.Fatal("indexing blocked app readiness")
	}
	worker := workerStore(t, db)
	lease, e := worker.Claim(ctx, ownerID, time.Now())
	if e != nil || lease == nil {
		t.Fatal("package not eligible for worker", e)
	}
	result := resultFixture("Director approval")
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
	hits, e := store.Search(ctx, actor, SearchRequest{Scope: Scope{Kind: "APP", AppID: "library"}, Query: "Director"})
	if e != nil || len(hits.Results) != 1 {
		t.Fatal("activated package not searchable", e, hits)
	}
}

func TestPackageOriginalAndDelegatedSource(t *testing.T) {
	db, store, actor, _ := packageFixture(t)
	ctx := context.Background()
	if e := store.ReconcilePackage(ctx, "library", 1, strings.Repeat("b", 64)); e != nil {
		t.Fatal(e)
	}
	var doc, revision string
	db.QueryRow(`SELECT d.id::text,v.package_revision_id::text FROM cap_ai.knowledge_documents d JOIN cap_ai.document_versions v ON v.id=d.desired_version_id`).Scan(&doc, &revision)
	original, e := store.Original(ctx, actor, doc, "")
	if e != nil || string(original) != "# Package policy\n\nDirector approval.\n" {
		t.Fatal("protected package original missing", e)
	}
	worker := workerStore(t, db)
	worker.ConfigurePackages(true, store.packageRoot)
	lease, e := worker.Claim(ctx, ownerID, time.Now())
	if e != nil || lease == nil {
		t.Fatal(e)
	}
	token, e := worker.IssueDelegation(ctx, *lease, time.Now())
	if e != nil {
		t.Fatal(e)
	}
	fetched, e := worker.PackageSource(ctx, token, ownerID, revision)
	if e != nil || string(fetched) != string(original) {
		t.Fatal("exact package delegation failed", e)
	}
	db.Exec(`UPDATE core.apps SET spec=jsonb_set(spec,'{knowledge,sources}','[]') WHERE app_id='library'`)
	if _, e = worker.PackageSource(ctx, token, ownerID, revision); e == nil {
		t.Fatal("removed delegated package remained readable")
	}
}

func TestPackageServiceDatabaseRoles(t *testing.T) {
	db, store, _, _ := packageFixture(t)
	var name string
	db.QueryRow(`SELECT current_database()`).Scan(&name)
	u, _ := url.Parse(os.Getenv("TEST_DATABASE_URL"))
	u.Path = "/" + name
	dispatcher := NewStore(testutil.As(t, u.String(), "dxos_ai_dispatcher"))
	dispatcher.ConfigurePackages(true, store.packageRoot)
	if e := dispatcher.ReconcilePackage(context.Background(), "library", 1, strings.Repeat("b", 64)); e != nil {
		t.Fatal("narrow dispatcher denied", e)
	}
	api := NewStore(testutil.As(t, u.String(), "dxos_ai_api"))
	api.ConfigurePackages(true, store.packageRoot)
	if e := api.ReconcilePackage(context.Background(), "library", 1, strings.Repeat("b", 64)); e == nil {
		t.Fatal("user API database can reconcile packages")
	}
	if _, e := dispatcher.db.Query(`SELECT * FROM cap_ai.knowledge_chunks`); e == nil {
		t.Fatal("dispatcher can read unscoped chunks")
	}
}

func TestAppStatusPendingAndManagerHasNoContent(t *testing.T) {
	_, store, _, manager, _ := restrictedFixture(t)
	rows, e := store.AppKnowledge(context.Background(), manager, "library")
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(rows)
	if len(rows.Sources) != 1 || rows.Sources[0].Readable || !rows.Sources[0].Manageable || rows.Sources[0].Status == "READY" || strings.Contains(string(raw), "title") || strings.Contains(string(raw), "Director") {
		t.Fatal("manager status widened READ", string(raw))
	}
}

func TestPackageDisableClosesPublicMetadata(t *testing.T) {
	db, store, actor, _ := packageFixture(t)
	if e := store.ReconcilePackage(context.Background(), "library", 1, strings.Repeat("b", 64)); e != nil {
		t.Fatal(e)
	}
	var doc, job string
	db.QueryRow(`SELECT d.id::text,j.id::text FROM cap_ai.knowledge_documents d JOIN cap_ai.document_versions v ON v.document_id=d.id JOIN cap_ai.ingestion_jobs j ON j.version_id=v.id`).Scan(&doc, &job)
	store.packageEnabled = false
	store.personalEnabled = true
	for _, path := range []string{"/api/cap/ai/documents/" + doc, "/api/cap/ai/documents/" + doc + "/original", "/api/cap/ai/ingestions/" + job} {
		req := httptest.NewRequest("GET", path, nil)
		req = req.WithContext(auth.WithIdentity(req.Context(), actor))
		response := httptest.NewRecorder()
		store.Handler().ServeHTTP(response, req)
		if response.Code == 200 {
			t.Fatal("disabled package route served metadata", path, response.Body.String())
		}
	}
}

func TestHistoricalPackageContentGetsFreshFencedJob(t *testing.T) {
	for _, completed := range []bool{true, false} {
		t.Run(fmt.Sprintf("initial_complete_%v", completed), func(t *testing.T) {
			db, store, _, hashA := packageFixture(t)
			ctx := context.Background()
			worker := workerStore(t, db)
			reconcile := func(generation int64) {
				t.Helper()
				if e := store.ReconcilePackage(ctx, "library", generation, strings.Repeat("b", 64)); e != nil {
					t.Fatal(e)
				}
			}
			activate := func(hash string) *Lease {
				t.Helper()
				lease, e := worker.Claim(ctx, ownerID, time.Now())
				if e != nil || lease == nil {
					t.Fatal("desired historical content has no runnable ingestion job", e)
				}
				result := resultFixture("Director approval")
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
				return lease
			}
			setContent := func(generation int64, body, pkg string) {
				t.Helper()
				hash := fmt.Sprintf("%x", sha256.Sum256([]byte(body)))
				ref := "library/policy/" + hash
				if e := os.WriteFile(filepath.Join(store.packageRoot, ref), []byte(body), 0600); e != nil {
					t.Fatal(e)
				}
				_, e := db.Exec(`UPDATE core.apps SET publication_generation=$1,spec=jsonb_set(jsonb_set(jsonb_set(jsonb_set(jsonb_set(jsonb_set(spec,'{_publication,generation}',to_jsonb($1::bigint)),'{_publication,package_digest}',to_jsonb($2::text)),'{_publication,sources,0,sha256}',to_jsonb($3::text)),'{_publication,sources,0,artifact_ref}',to_jsonb($4::text)),'{_publication,sources,0,size_bytes}',to_jsonb($5::bigint)),'{_publication,spec_digest}',to_jsonb($6::text)) WHERE app_id='library'`, generation, pkg, hash, ref, len(body), strings.Repeat("b", 64))
				if e != nil {
					t.Fatal(e)
				}
			}
			reconcile(1)
			var initialVersion, initialJob string
			if e := db.QueryRow(`SELECT version_id::text,id::text FROM cap_ai.ingestion_jobs`).Scan(&initialVersion, &initialJob); e != nil {
				t.Fatal(e)
			}
			var firstRun string
			if completed {
				firstRun = activate(hashA).RunID
			}
			bodyB := "# Changed policy\n\nManager approval.\n"
			hashB := fmt.Sprintf("%x", sha256.Sum256([]byte(bodyB)))
			setContent(2, bodyB, strings.Repeat("e", 64))
			reconcile(2)
			activate(hashB)
			setContent(3, "# Package policy\n\nDirector approval.\n", strings.Repeat("c", 64))
			reconcile(3)
			restored := activate(hashA)
			if restored.VersionID != initialVersion || restored.JobID == initialJob {
				t.Fatal("historical version/attempt identity was rewritten", restored)
			}
			var active string
			if e := db.QueryRow(`SELECT active_version_id::text FROM cap_ai.knowledge_documents WHERE source_kind='PACKAGE'`).Scan(&active); e != nil || active != initialVersion {
				t.Fatal("desired A did not become active", e)
			}
			reconcile(3)
			var jobs int
			db.QueryRow(`SELECT count(*) FROM cap_ai.ingestion_jobs WHERE version_id=$1`, initialVersion).Scan(&jobs)
			if jobs != 2 {
				t.Fatal("replay duplicated historical ingestion", jobs)
			}
			if completed {
				var state string
				db.QueryRow(`SELECT state FROM cap_ai.processing_runs WHERE id=$1`, firstRun).Scan(&state)
				if state != "COMPLETE" {
					t.Fatal("original COMPLETE run was mutated", state)
				}
			}
		})
	}
}
