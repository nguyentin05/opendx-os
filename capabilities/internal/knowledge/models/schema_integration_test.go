//go:build integration

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
package models

import (
	"database/sql"
	"github.com/opendx-os/opendx-os/capabilities/internal/testutil"
	"os"
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
