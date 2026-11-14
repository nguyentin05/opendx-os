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
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// editManifest rewrites dir/<pkg>/manifest.yaml with old replaced by new.
func editManifest(t *testing.T, dir, pkg, old, new string) {
	t.Helper()
	path := filepath.Join(dir, pkg, "manifest.yaml")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), old) {
		t.Fatalf("%s does not contain %q", path, old)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(string(b), old, new, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReconcileRefusedPackageDoesNotBlockOthersIntegration(t *testing.T) {
	db := testutil.NewDatabase(t)
	srv := httptest.NewServer(testutil.NewFakeHasura())
	defer srv.Close()
	rec := NewRunner(db, apply.NewRunner(db, apply.Config{HasuraURL: srv.URL}, srv.Client()))
	ctx := context.Background()

	dir := t.TempDir()
	copyPackage(t, dir, "procurement")
	copyPackage(t, dir, "process-fixture")
	// cap-ai is not enabled in the test database, so the registry refuses it.
	editManifest(t, dir, "process-fixture", "  - cap-work\n", "  - cap-work\n  - cap-ai\n")

	res, err := rec.Pass(ctx, dir)
	if err == nil || !strings.Contains(err.Error(), "install process_fixture") {
		t.Fatalf("expected process_fixture to be refused, got %v", err)
	}
	if len(res.Installed) != 1 || res.Installed[0] != "procurement" {
		t.Fatalf("installed = %v, want [procurement]", res.Installed)
	}
	if appStatus(t, db, "procurement") != "Ready" || appStatus(t, db, "process_fixture") != "Failed" {
		t.Fatal("procurement must be Ready and process_fixture Failed")
	}
	attempts := deployments(t, db)

	// An unchanged refused package is held back instead of retried every pass.
	if _, err := rec.Pass(ctx, dir); err == nil || !strings.Contains(err.Error(), "package unchanged") {
		t.Fatalf("expected the refused package to be held back, got %v", err)
	}
	if n := deployments(t, db); n != attempts {
		t.Fatalf("deployment attempts = %d, want %d (no retry)", n, attempts)
	}

	// Fixing the package installs it on the next pass.
	editManifest(t, dir, "process-fixture", "  - cap-ai\n", "")
	if _, err := rec.Pass(ctx, dir); err != nil {
		t.Fatalf("pass after fixing the package: %v", err)
	}
	if appStatus(t, db, "process_fixture") != "Ready" {
		t.Fatal("process_fixture must be Ready once fixed")
	}
}

func TestReconcileKeepsInstalledAppsWhileAPackageIsInvalidIntegration(t *testing.T) {
	db := testutil.NewDatabase(t)
	srv := httptest.NewServer(testutil.NewFakeHasura())
	defer srv.Close()
	rec := NewRunner(db, apply.NewRunner(db, apply.Config{HasuraURL: srv.URL}, srv.Client()))
	ctx := context.Background()

	dir := t.TempDir()
	copyPackage(t, dir, "procurement")
	copyPackage(t, dir, "process-fixture")
	if err := rec.Run(ctx, dir); err != nil {
		t.Fatal(err)
	}

	// A package being edited may not parse; nothing is uninstalled meanwhile,
	// even an application whose package has gone.
	editManifest(t, dir, "procurement", "kind: DXApplication", "kind: [")
	if err := os.RemoveAll(filepath.Join(dir, "process-fixture")); err != nil {
		t.Fatal(err)
	}
	if err := rec.Run(ctx, dir); err == nil || !strings.Contains(err.Error(), "not uninstalling") {
		t.Fatalf("expected removals to wait for a valid directory, got %v", err)
	}
	if appStatus(t, db, "procurement") != "Ready" || appStatus(t, db, "process_fixture") != "Ready" {
		t.Fatal("both applications must stay Ready")
	}

	editManifest(t, dir, "procurement", "kind: [", "kind: DXApplication")
	if err := rec.Run(ctx, dir); err != nil {
		t.Fatal(err)
	}
	if appStatus(t, db, "process_fixture") != "Removed" {
		t.Fatal("process_fixture must be removed once the directory is valid")
	}
}

func TestReconcileRepairsHasuraDriftIntegration(t *testing.T) {
	db := testutil.NewDatabase(t)
	hasura := testutil.NewFakeHasura()
	srv := httptest.NewServer(hasura)
	defer srv.Close()
	rec := NewRunner(db, apply.NewRunner(db, apply.Config{HasuraURL: srv.URL}, srv.Client()))
	ctx := context.Background()

	dir := t.TempDir()
	copyPackage(t, dir, "procurement")
	if err := rec.Run(ctx, dir); err != nil {
		t.Fatal(err)
	}
	key := testutil.PermKey("select", "domain", "procurement_requests", "role_procurement_manager")
	hasura.Drop(key)
	attempts := deployments(t, db)

	res, err := rec.Pass(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Repaired) == 0 || !hasura.Has(key) {
		t.Fatalf("drift must be repaired, repaired=%v", res.Repaired)
	}
	if len(res.Installed) != 0 || deployments(t, db) != attempts {
		t.Fatal("repairing drift must not record a new deployment")
	}
}

func TestWatchInstallsAndRemovesCopiedPackagesIntegration(t *testing.T) {
	db := testutil.NewDatabase(t)
	srv := httptest.NewServer(testutil.NewFakeHasura())
	defer srv.Close()
	rec := NewRunner(db, apply.NewRunner(db, apply.Config{HasuraURL: srv.URL}, srv.Client()))

	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- rec.Watch(ctx, dir, 50*time.Millisecond, slog.New(slog.NewTextHandler(io.Discard, nil)))
	}()

	waitStatus := func(app, want string) {
		t.Helper()
		deadline := time.Now().Add(20 * time.Second)
		for {
			var got string
			_ = db.QueryRow(`SELECT status FROM core.apps WHERE app_id = $1`, app).Scan(&got)
			if got == want {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s status = %q, want %q", app, got, want)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}

	copyPackage(t, dir, "process-fixture")
	waitStatus("process_fixture", "Ready")
	if err := os.RemoveAll(filepath.Join(dir, "process-fixture")); err != nil {
		t.Fatal(err)
	}
	waitStatus("process_fixture", "Removed")

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Watch returned %v", err)
	}
}

// All sample applications together: three are department-scoped and share
// the cap_hr.org_units.staff relationship that their permission filters
// traverse, so installing one must not disturb the others.
func TestReconcileInstallsEverySampleAppIntegration(t *testing.T) {
	db := testutil.NewDatabase(t)
	hasura := testutil.NewFakeHasura()
	srv := httptest.NewServer(hasura)
	defer srv.Close()
	rec := NewRunner(db, apply.NewRunner(db, apply.Config{HasuraURL: srv.URL}, srv.Client()))
	ctx := context.Background()

	dir := t.TempDir()
	for _, pkg := range []string{"maintenance", "procurement", "process-fixture", "timesheet"} {
		copyPackage(t, dir, pkg)
	}
	res, err := rec.Pass(ctx, dir)
	if err != nil {
		t.Fatalf("first pass: %v", err)
	}
	if len(res.Installed) != 4 {
		t.Fatalf("installed = %v, want all four", res.Installed)
	}
	for _, app := range []string{"maintenance", "procurement", "process_fixture", "timesheet"} {
		if appStatus(t, db, app) != "Ready" {
			t.Fatalf("%s must be Ready", app)
		}
	}
	for _, key := range []string{
		testutil.PermKey("select", "domain", "procurement_requests", "role_procurement_manager"),
		testutil.PermKey("select", "domain", "maintenance_records", "role_maintenance_manager"),
	} {
		if !hasura.Has(key) {
			t.Fatalf("department-scoped permission %s is missing", key)
		}
	}

	attempts := deployments(t, db)
	res, err = rec.Pass(ctx, dir)
	if err != nil || res.Changed() {
		t.Fatalf("second pass must find nothing to do: %+v, %v", res, err)
	}
	if deployments(t, db) != attempts {
		t.Fatal("second pass recorded deployments")
	}
}
