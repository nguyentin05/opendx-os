//go:build integration

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
package files

import (
	"bytes"
	"context"
	"database/sql"
	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
	"github.com/opendx-os/opendx-os/capabilities/internal/knowledge"
	"github.com/opendx-os/opendx-os/capabilities/internal/knowledge/docir"
	"github.com/opendx-os/opendx-os/capabilities/internal/testutil"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
	"time"
)

func aiConnection(t *testing.T, db *sql.DB, role string) *sql.DB {
	t.Helper()
	var name string
	if e := db.QueryRow(`SELECT current_database()`).Scan(&name); e != nil {
		t.Fatal(e)
	}
	u, _ := url.Parse(os.Getenv("TEST_DATABASE_URL"))
	u.Path = "/" + name
	return testutil.As(t, u.String(), role)
}
func TestPersonalMarkdownOwnerFlow(t *testing.T) {
	s, db, a, _ := libraryFixture(t)
	ctx := context.Background()
	for _, file := range []string{"0001_metadata_jobs.sql", "0002_docir_fts.sql"} {
		raw, e := os.ReadFile("../../../platform/postgres/migrations/cap_ai/" + file)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = db.Exec(string(raw)); e != nil {
			t.Fatal(e)
		}
	}
	data, e := os.ReadFile("../knowledge/docir/testdata/policy.md")
	if e != nil {
		t.Fatal(e)
	}
	f, e := s.UploadPrivate(ctx, a, "procurement", "policy.md", bytes.NewReader(data))
	if e != nil {
		t.Fatal(e)
	}
	api := knowledge.NewStore(aiConnection(t, db, "dxos_ai_api"))
	req := knowledge.RegisterRequest{FileID: f.ID, ParserProfile: "standard-text-v1"}
	if _, e = api.Register(ctx, a, "dirty", req); e != knowledge.ErrUnclean {
		t.Fatal("dirty source registered", e)
	}
	if e = s.ScanPending(ctx, 10); e != nil {
		t.Fatal(e)
	}
	reg, e := api.Register(ctx, a, "clean", req)
	if e != nil {
		t.Fatal(e)
	}
	workerStore := knowledge.NewStore(aiConnection(t, db, "dxos_ai_worker"))
	workerID := "77777777-7777-4777-8777-777777777777"
	verifier := serviceVerifier(func(context.Context, string) (auth.Subject, error) {
		return auth.Subject{UserID: workerID, ExpiresAt: time.Now().Add(time.Hour)}, nil
	})
	source := httptest.NewServer(s.DelegatedHandler(verifier, workerID, workerStore))
	defer source.Close()
	worker := knowledge.Worker{Store: workerStore, Parser: docir.Parser{}, WorkerID: workerID, ServiceToken: auth.StaticToken("service-fixture"), FileEndpoint: source.URL}
	if e = worker.Once(ctx); e != nil {
		t.Fatal(e)
	}
	hits, e := api.Search(ctx, a, knowledge.SearchRequest{Scope: knowledge.Scope{Kind: "PERSONAL"}, Query: "Director"})
	if e != nil || len(hits.Results) == 0 {
		t.Fatal("ingested table not searchable", hits, e)
	}
	if _, e = api.Citation(ctx, a, reg.DocumentID, reg.VersionID, hits.Results[0].ChunkID); e != nil {
		t.Fatal(e)
	}
	t.Log("private upload -> dirty rejection -> CLEAN scan -> durable register -> delegated fetch -> Markdown AST -> fenced activation -> FTS -> protected citation")
}
