//go:build integration

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
package knowledge

import (
	"context"
	"database/sql"
	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
	"github.com/opendx-os/opendx-os/capabilities/internal/knowledge/docir"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func derivedFixture(t *testing.T) (*sql.DB, *sql.DB, *auth.Identity) {
	db, api, a := metadataFixture(t)
	raw, e := os.ReadFile("../../../platform/postgres/migrations/cap_ai/0002_docir_fts.sql")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(string(raw)); e != nil {
		t.Fatal(e)
	}
	return db, api, a
}
func resultFixture(text string) docir.Result {
	return docir.Result{IRVersion: 1, SourceSHA256: strings.Repeat("a", 64), Parser: docir.ParserInfo{Name: "goldmark", Version: "1.7.8", Recipe: "standard-text-v1"}, Elements: []docir.Element{{LocalID: "e1", Kind: "paragraph", Ordinal: 0, SectionPath: []string{"Policy"}, Text: text, Confidence: 1, Provenance: "EXTRACTED"}}, Assets: []docir.Asset{}, Warnings: []string{}}
}
func TestActivateCompleteCurrentRun(t *testing.T) {
	db, api, a := derivedFixture(t)
	ctx := context.Background()
	reg, e := NewStore(api).Register(ctx, a, "activate", RegisterRequest{FileID: fileID, ParserProfile: "standard-text-v1"})
	if e != nil {
		t.Fatal(e)
	}
	worker := workerStore(t, db)
	lease, e := worker.Claim(ctx, "worker", time.Now())
	if e != nil || lease == nil {
		t.Fatal(e)
	}
	if e = worker.Activate(ctx, *lease, lease.RunID); e == nil {
		t.Fatal("incomplete run activated")
	}
	r := resultFixture("Policy amount Director")
	chunks, e := docir.Chunk(r, docir.DefaultChunkRecipe())
	if e != nil {
		t.Fatal(e)
	}
	if e = worker.SaveRun(ctx, *lease, r, chunks); e != nil {
		t.Fatal(e)
	}
	if e = worker.Activate(ctx, *lease, lease.RunID); e != nil {
		t.Fatal(e)
	}
	var active string
	if e = db.QueryRow(`SELECT active_version_id::text FROM cap_ai.knowledge_documents WHERE id=$1`, reg.DocumentID).Scan(&active); e != nil || active != reg.VersionID {
		t.Fatal("activation missing", active, e)
	}
}
func TestWorkerRestart(t *testing.T) {
	if os.Getenv("RAG_CRASH_CHILD") == "1" {
		db, e := sql.Open("postgres", os.Getenv("RAG_CRASH_DB"))
		if e != nil {
			os.Exit(2)
		}
		db.SetMaxOpenConns(1)
		if _, e = db.Exec(`SET SESSION AUTHORIZATION dxos_ai_worker`); e != nil {
			os.Exit(3)
		}
		lease, e := NewStore(db).Claim(context.Background(), "worker", time.Now())
		if e != nil || lease == nil {
			os.Exit(4)
		}
		os.Exit(87)
	}
	db, api, a := derivedFixture(t)
	ctx := context.Background()
	if _, e := NewStore(api).Register(ctx, a, "restart", RegisterRequest{FileID: fileID, ParserProfile: "standard-text-v1"}); e != nil {
		t.Fatal(e)
	}
	var name string
	if e := db.QueryRow(`SELECT current_database()`).Scan(&name); e != nil {
		t.Fatal(e)
	}
	u, _ := url.Parse(os.Getenv("TEST_DATABASE_URL"))
	u.Path = "/" + name
	child := exec.Command(os.Args[0], "-test.run=^TestWorkerRestart$")
	child.Env = append(os.Environ(), "RAG_CRASH_CHILD=1", "RAG_CRASH_DB="+u.String())
	e := child.Run()
	exit, ok := e.(*exec.ExitError)
	if !ok || exit.ExitCode() != 87 {
		t.Fatal("worker did not crash after durable claim", e)
	}
	if _, e = db.Exec(`UPDATE cap_ai.ingestion_jobs SET lease_until=now()-interval '1 second' WHERE state='LEASED'`); e != nil {
		t.Fatal(e)
	}
	worker := workerStore(t, db)
	two, e := worker.Claim(ctx, "worker", time.Now())
	if e != nil || two == nil || two.Fence != 2 {
		t.Fatal("restart did not fence old process", two, e)
	}
	r := resultFixture("Restart durable complete run")
	chunks, _ := docir.Chunk(r, docir.DefaultChunkRecipe())
	if e = worker.SaveRun(ctx, *two, r, chunks); e != nil {
		t.Fatal(e)
	}
	if e = worker.Activate(ctx, *two, two.RunID); e != nil {
		t.Fatal(e)
	}
	var active int
	if e = db.QueryRow(`SELECT count(*) FROM cap_ai.processing_runs WHERE state='COMPLETE'`).Scan(&active); e != nil || active != 1 {
		t.Fatal("restart produced multiple complete runs", active, e)
	}
	t.Log("real child exited 87 after claim; restarted claimant fenced attempt1 and activated exactly one complete run")
}

func TestCompleteRunRetryPreservesEvidence(t *testing.T) {
	db, api, a := derivedFixture(t)
	ctx := context.Background()
	_, err := NewStore(api).Register(ctx, a, "retry-complete", RegisterRequest{FileID: fileID, ParserProfile: "standard-text-v1"})
	if err != nil {
		t.Fatal(err)
	}
	worker := workerStore(t, db)
	lease, err := worker.Claim(ctx, "worker", time.Now())
	if err != nil || lease == nil {
		t.Fatal(err)
	}
	r := resultFixture("Director retry complete evidence")
	chunks, _ := docir.Chunk(r, docir.DefaultChunkRecipe())
	if err = worker.SaveRun(ctx, *lease, r, chunks); err != nil {
		t.Fatal(err)
	}
	if err = worker.Retry(ctx, *lease, time.Now(), 1, "TEMPORARY_UNAVAILABLE", false); err != nil {
		t.Fatal("complete-but-unactivated run cannot retry", err)
	}
	var state string
	if err = db.QueryRow(`SELECT state FROM cap_ai.processing_runs WHERE id=$1`, lease.RunID).Scan(&state); err != nil || state != "COMPLETE" {
		t.Fatal("complete evidence changed", state, err)
	}
}

func TestActivateRejectsLateCandidate(t *testing.T) {
	for _, scenario := range []string{"source-revoked", "delete", "new-desired", "stale-fence", "failed-candidate"} {
		t.Run(scenario, func(t *testing.T) {
			db, api, a := derivedFixture(t)
			ctx := context.Background()
			s := NewStore(api)
			worker := workerStore(t, db)
			old := activateText(t, s, worker, a, fileID, "old", "Director old eligible policy")
			var revision int64
			if err := db.QueryRow(`SELECT revision FROM cap_ai.knowledge_documents WHERE id=$1`, old.DocumentID).Scan(&revision); err != nil {
				t.Fatal(err)
			}
			newFile := "66666666-6666-4666-8666-666666666666"
			if _, err := db.Exec(`INSERT INTO core.files(id,app_id,bucket,object_key,file_name,scan_status,sha256_hash,uploaded_by,mime_type,size_bytes,read_policy) SELECT $1,app_id,bucket,'replacement',file_name,scan_status,sha256_hash,uploaded_by,mime_type,size_bytes,read_policy FROM core.files WHERE id=$2`, newFile, fileID); err != nil {
				t.Fatal(err)
			}
			candidate, err := s.ReplaceVersion(ctx, a, "new", old.DocumentID, revision, RegisterRequest{FileID: newFile, ParserProfile: "standard-text-v1"})
			if err != nil {
				t.Fatal(err)
			}
			lease, err := worker.Claim(ctx, "worker", time.Now())
			if err != nil || lease == nil {
				t.Fatal(err)
			}
			r := resultFixture("Director candidate policy")
			chunks, _ := docir.Chunk(r, docir.DefaultChunkRecipe())
			if scenario != "failed-candidate" {
				if err = worker.SaveRun(ctx, *lease, r, chunks); err != nil {
					t.Fatal(err)
				}
			}
			switch scenario {
			case "source-revoked":
				_, err = db.Exec(`UPDATE core.files SET scan_status='INFECTED' WHERE id=$1`, newFile)
			case "delete":
				err = s.Delete(ctx, a, "delete", old.DocumentID, candidate.Revision)
			case "new-desired":
				third := "77777777-7777-4777-8777-777777777777"
				_, err = db.Exec(`INSERT INTO core.files(id,app_id,bucket,object_key,file_name,scan_status,sha256_hash,uploaded_by,mime_type,size_bytes,read_policy) SELECT $1,app_id,bucket,'third',file_name,scan_status,sha256_hash,uploaded_by,mime_type,size_bytes,read_policy FROM core.files WHERE id=$2`, third, fileID)
				if err == nil {
					_, err = s.ReplaceVersion(ctx, a, "third", old.DocumentID, candidate.Revision, RegisterRequest{FileID: third, ParserProfile: "standard-text-v1"})
				}
			case "stale-fence":
				lease.Fence++
			case "failed-candidate":
				err = worker.Retry(ctx, *lease, time.Now(), 1, "PARSER_UNAVAILABLE", true)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = worker.Activate(ctx, *lease, lease.RunID); err == nil {
				t.Fatal("late candidate activated")
			}
			var active string
			if err = db.QueryRow(`SELECT active_version_id::text FROM cap_ai.knowledge_documents WHERE id=$1`, old.DocumentID).Scan(&active); err != nil || active != old.VersionID {
				t.Fatal("candidate erased old active", active, err)
			}
			if scenario == "failed-candidate" {
				result, err := s.Search(ctx, a, SearchRequest{Scope: Scope{Kind: "PERSONAL"}, Query: "eligible"})
				if err != nil || len(result.Results) != 1 {
					t.Fatal("failed candidate hid eligible old version", result, err)
				}
			}
		})
	}
}
