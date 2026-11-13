//go:build integration

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package reconcile

import (
	"context"
	"database/sql"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/opendx-os/opendx-os/operator/internal/apply"
	"github.com/opendx-os/opendx-os/operator/internal/testutil"
)

// copyPackage copies apps/<name> (manifest and workflows) into dir.
func copyPackage(t *testing.T, dir, name string) {
	t.Helper()
	src := filepath.Join("..", "..", "..", "apps", name)
	err := filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dir, name, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func appStatus(t *testing.T, db *sql.DB, app string) string {
	t.Helper()
	var status string
	if err := db.QueryRow(`SELECT status FROM core.apps WHERE app_id = $1`, app).Scan(&status); err != nil {
		t.Fatalf("status of %s: %v", app, err)
	}
	return status
}

func deployments(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM core.app_deployments`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestReconcileDirectoryIsTheInstalledSetIntegration(t *testing.T) {
	db := testutil.NewDatabase(t)
	hasura := testutil.NewFakeHasura()
	srv := httptest.NewServer(hasura)
	defer srv.Close()
	rec := NewRunner(db, apply.NewRunner(db, apply.Config{HasuraURL: srv.URL}, srv.Client()))
	ctx := context.Background()

	dir := t.TempDir()
	copyPackage(t, dir, "procurement")
	copyPackage(t, dir, "process-fixture")

	if err := rec.Run(ctx, dir); err != nil {
		t.Fatalf("first reconcile: %v", err)
	}
	if appStatus(t, db, "procurement") != "Ready" || appStatus(t, db, "process_fixture") != "Ready" {
		t.Fatal("both applications must be Ready")
	}
	if n := deployments(t, db); n != 2 {
		t.Fatalf("deployment attempts = %d, want 2", n)
	}

	// Reconciling an unchanged directory converges again without new attempts.
	if err := rec.Run(ctx, dir); err != nil {
		t.Fatalf("second reconcile: %v", err)
	}
	if n := deployments(t, db); n != 2 {
		t.Fatalf("deployment attempts after a no-op reconcile = %d, want 2", n)
	}

	reviewSelect := testutil.PermKey("select", "domain", "review_requests", "role_review")
	procurementSelect := testutil.PermKey("select", "domain", "procurement_requests", "role_procurement_manager")
	if !hasura.Has(reviewSelect) || !hasura.Has(procurementSelect) {
		t.Fatal("both applications must have their Hasura permissions")
	}

	// Removing a package uninstalls only that application.
	if err := os.RemoveAll(filepath.Join(dir, "process-fixture")); err != nil {
		t.Fatal(err)
	}
	if err := rec.Run(ctx, dir); err != nil {
		t.Fatalf("reconcile after removing a package: %v", err)
	}
	if appStatus(t, db, "process_fixture") != "Removed" || appStatus(t, db, "procurement") != "Ready" {
		t.Fatal("only process_fixture must be removed")
	}
	if hasura.Has(reviewSelect) {
		t.Fatal("the removed application's permissions must be dropped")
	}
	if !hasura.Has(procurementSelect) {
		t.Fatal("the remaining application's permissions must be kept")
	}
}
