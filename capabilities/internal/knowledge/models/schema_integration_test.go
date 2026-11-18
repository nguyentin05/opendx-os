//go:build integration

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package models

import (
	"crypto/sha256"
	"database/sql"
	"fmt"
	"github.com/opendx-os/opendx-os/capabilities/internal/testutil"
	"net/url"
	"os"
	"os/exec"
	"testing"
)

func modelDB(t *testing.T, vector bool) *sql.DB {
	t.Helper()
	db := testutil.Database(t)
	for _, path := range []string{"init/01_core_schema.sql", "init/02_capability_registry.sql", "init/03_app_registry.sql", "init/05_operator_deployments.sql", "init/06_runtime_roles.sql", "init/07_app_definitions.sql", "capabilities/cap_ai.sql", "migrations/cap_ai/0001_metadata_jobs.sql", "migrations/cap_ai/0002_docir_fts.sql", "migrations/core/0003_publication_generation.sql", "migrations/cap_ai/0003_package_knowledge.sql"} {
		raw, e := os.ReadFile("../../../../platform/postgres/" + path)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = db.Exec(string(raw)); e != nil {
			t.Fatalf("%s: %v", path, e)
		}
	}
	if vector {
		applyModels(t, db)
	}
	return db
}
func applyModels(t *testing.T, db *sql.DB) {
	t.Helper()
	raw, e := os.ReadFile("../../../../platform/postgres/migrations/cap_ai/0004_models_vectors.sql")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(string(raw)); e != nil {
		t.Fatal(e)
	}
}
func TestFTSWithoutVectorExtension(t *testing.T) {
	db := modelDB(t, false)
	var works bool
	if e := db.QueryRow(`SELECT to_tsvector('simple','policy') @@ plainto_tsquery('simple','policy')`).Scan(&works); e != nil || !works {
		t.Fatal("FTS unavailable", e)
	}
	var present bool
	if e := db.QueryRow(`SELECT EXISTS(SELECT FROM pg_extension WHERE extname='vector')`).Scan(&present); e != nil || present {
		t.Fatal("FTS bootstrap requires vector", e)
	}
	var available bool
	if e := db.QueryRow(`SELECT EXISTS(SELECT FROM pg_available_extensions WHERE name='vector')`).Scan(&available); e != nil {
		t.Fatal(e)
	}
	if available {
		applyModels(t, db)
	} else {
		raw, e := os.ReadFile("../../../../platform/postgres/migrations/cap_ai/0004_models_vectors.sql")
		if e != nil {
			t.Fatal(e)
		}
		tx, e := db.Begin()
		if e != nil {
			t.Fatal(e)
		}
		defer tx.Rollback()
		if _, e = tx.Exec(string(raw)); e == nil {
			t.Fatal("model migration accepted missing extension")
		}
	}
}

func applyRegistry(t *testing.T, db *sql.DB) {
	t.Helper()
	raw, e := os.ReadFile("../../../../platform/postgres/migrations/cap_ai/0005_model_registry.sql")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(string(raw)); e != nil {
		t.Fatal(e)
	}
}
func TestRegistryOptionalSchemaAndFTS(t *testing.T) {
	db := modelDB(t, false)
	var n int
	db.QueryRow(`SELECT count(*) FROM pg_tables WHERE schemaname='cap_ai' AND tablename IN('model_profiles','model_candidates','global_model_slots')`).Scan(&n)
	if n != 0 {
		t.Fatal(n)
	}
	raw, e := os.ReadFile("../../../../platform/postgres/migrations/cap_ai/0005_model_registry.sql")
	if e != nil {
		t.Fatal(e)
	}
	tx, e := db.Begin()
	if e != nil {
		t.Fatal(e)
	}
	if _, e = tx.Exec(string(raw)); e == nil {
		t.Fatal("accepted missing 0004")
	}
	tx.Rollback()
	applyModels(t, db)
	applyRegistry(t, db)
	for _, q := range []string{`SELECT count(*) FROM cap_ai.model_profiles`, `SELECT count(*) FROM cap_ai.model_candidates`, `SELECT count(*) FROM cap_ai.model_slot_events`, `SELECT count(*) FROM cap_ai.global_model_slots WHERE profile_id IS NOT NULL`} {
		if e = db.QueryRow(q).Scan(&n); e != nil || n != 0 {
			t.Fatal(q, n, e)
		}
	}
	if e = db.QueryRow(`SELECT count(*) FROM cap_ai.global_model_slots WHERE capability IN('EMBEDDING','LLM','VLM','RERANKING') AND slot_revision=0 AND profile_id IS NULL AND profile_revision IS NULL`).Scan(&n); e != nil || n != 4 {
		t.Fatal(n, e)
	}
}

func TestRegistryExplicitMigrationRunner(t *testing.T) {
	db := modelDB(t, false)
	ledger, e := os.ReadFile("../../../../platform/postgres/migrations/ledger.sql")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(string(ledger)); e != nil {
		t.Fatal(e)
	}
	// modelDB owns the base migrations; record their actual checksums before the runner.
	for _, file := range []string{"0001_metadata_jobs.sql", "0002_docir_fts.sql", "0003_package_knowledge.sql"} {
		raw, e := os.ReadFile("../../../../platform/postgres/migrations/cap_ai/" + file)
		if e != nil {
			t.Fatal(e)
		}
		sum := sha256.Sum256(raw)
		if _, e = db.Exec(`INSERT INTO core.schema_migrations(scope,version,checksum) VALUES('cap-ai',$1,$2)`, file[:4], fmt.Sprintf("%x", sum)); e != nil {
			t.Fatal(e)
		}
	}
	var name string
	if e = db.QueryRow(`SELECT current_database()`).Scan(&name); e != nil {
		t.Fatal(e)
	}
	u, e := url.Parse(os.Getenv("TEST_DATABASE_URL"))
	if e != nil {
		t.Fatal(e)
	}
	u.Path = "/" + name
	run := func(version string, flags ...string) error {
		command := exec.Command("python3", "../../../../scripts/migrate_rag.py", "--database-url-env", "REGISTRY_MIGRATION_TEST_URL", "--scope", "cap-ai", "--through", version)
		command.Env = append(os.Environ(), "REGISTRY_MIGRATION_TEST_URL="+u.String())
		command.Env = append(command.Env, flags...)
		raw, e := command.CombinedOutput()
		if e != nil {
			return fmt.Errorf("runner: %w: %s", e, raw)
		}
		return nil
	}
	if e = run("0007"); e != nil {
		t.Fatal(e)
	}
	var present bool
	if e = db.QueryRow(`SELECT to_regclass('cap_ai.global_model_slots') IS NOT NULL`).Scan(&present); e != nil || present {
		t.Fatal(present, e)
	}
	if e = db.QueryRow(`SELECT EXISTS(SELECT FROM pg_extension WHERE extname='vector')`).Scan(&present); e != nil || present {
		t.Fatal("FTS upgrade installed vector", present, e)
	}
	if e = db.QueryRow(`SELECT to_regclass('cap_ai.agent_conversations') IS NOT NULL AND to_regprocedure('cap_ai.agent_authorized_scopes(uuid)') IS NOT NULL`).Scan(&present); e != nil || !present {
		t.Fatal("FTS agent upgrade missing", present, e)
	}
	if e = run("0010"); e == nil {
		t.Fatal("model-dependent upgrade accepted without gates")
	}
	for i := 0; i < 2; i++ {
		if e = run("0005"); e != nil {
			t.Fatal(e)
		}
	}
	var count int
	if e = db.QueryRow(`SELECT count(*) FROM core.schema_migrations WHERE scope='cap-ai' AND version IN('0004','0005')`).Scan(&count); e != nil || count != 2 {
		t.Fatal(count, e)
	}
	flags := []string{"RAG_MODELS_SCHEMA_ENABLED=true", "RAG_GLOBAL_MODEL_REGISTRY_SCHEMA_ENABLED=true", "RAG_AGENT_MODELS_SCHEMA_ENABLED=true"}
	for i := 0; i < 2; i++ {
		if e = run("0010", flags...); e != nil {
			t.Fatal(e)
		}
	}
	if e = db.QueryRow(`SELECT to_regprocedure('cap_ai.search_personal_vectors(uuid,text,text,bigint,public.vector,uuid[])') IS NOT NULL`).Scan(&present); e != nil || !present {
		t.Fatal("optional PERSONAL vector search missing", present, e)
	}
	if e = db.QueryRow(`SELECT to_regprocedure('cap_ai.agent_model_policy(uuid,text,text,bigint,bigint,jsonb)') IS NOT NULL
		AND to_regprocedure('cap_ai.agent_query_model_policy(uuid,text,text,text,text,bigint)') IS NOT NULL`).Scan(&present); e != nil || !present {
		t.Fatal("optional AGENT model authority missing", present, e)
	}
	if e = db.QueryRow(`SELECT count(*) FROM cap_ai.agent_processing_approvals`).Scan(&count); e != nil || count != 0 {
		t.Fatal("seeded approval", count, e)
	}
	if _, e = db.Exec(`UPDATE core.schema_migrations SET checksum=repeat('0',64) WHERE scope='cap-ai' AND version='0005'`); e != nil {
		t.Fatal(e)
	}
	if e = run("0005"); e == nil {
		t.Fatal("runner accepted registry checksum mismatch")
	}
}
