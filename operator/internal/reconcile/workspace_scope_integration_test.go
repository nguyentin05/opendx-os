//go:build integration

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package reconcile

import (
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/opendx-os/opendx-os/operator/internal/apply"
	"github.com/opendx-os/opendx-os/operator/internal/testutil"
)

func copyAppPackage(t *testing.T, name, dst string) {
	t.Helper()
	src := filepath.Join("..", "..", "..", "apps", name)
	err := filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, name, rel)
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

func TestReconcileOneWorkspaceKeepsOtherWorkspaceGrantsIntegration(t *testing.T) {
	db := testutil.NewDatabase(t)
	const wsA = "11111111-1111-4111-8111-111111111111"
	const wsB = "22222222-2222-4222-8222-222222222222"
	if _, err := db.Exec(`INSERT INTO core.workspaces (id, code, name) VALUES ($1, 'ws-a', 'A'), ($2, 'ws-b', 'B')`, wsA, wsB); err != nil {
		t.Fatal(err)
	}

	hasura := testutil.NewFakeHasura()
	srv := httptest.NewServer(hasura)
	defer srv.Close()
	applier := apply.NewRunner(db, apply.Config{HasuraURL: srv.URL}, srv.Client())
	rec := NewRunner(db, applier)
	ctx := context.Background()

	both := t.TempDir()
	copyAppPackage(t, "procurement", both)
	if err := rec.Run(ctx, wsA, both); err != nil {
		t.Fatalf("publish procurement to A: %v", err)
	}
	if err := rec.Run(ctx, wsB, both); err != nil {
		t.Fatalf("publish procurement to B: %v", err)
	}

	// Workspace A switches to another application; B still runs procurement.
	other := t.TempDir()
	copyAppPackage(t, "process-fixture", other)
	if err := rec.Run(ctx, wsA, other); err != nil {
		t.Fatalf("reconcile A without procurement: %v", err)
	}

	for _, key := range []string{
		"select:domain.procurement_requests:role_procurement",
		"insert:domain.procurement_requests:role_procurement",
		"update:domain.procurement_requests:role_procurement",
		"select:core.employees:role_procurement",
	} {
		if !hasura.Has(key) {
			t.Errorf("permission %s used by workspace B was revoked", key)
		}
	}

	var apps []string
	rows, err := db.Query(`SELECT workspace_id::text || ':' || app_id FROM core.app_registry ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		apps = append(apps, s)
	}
	_ = rows.Close()
	want := []string{wsA + ":process_fixture", wsB + ":procurement"}
	if fmt.Sprint(apps) != fmt.Sprint(want) {
		t.Fatalf("registry = %v, want %v", apps, want)
	}

	var defs int
	if err := db.QueryRow(`SELECT count(*) FROM core.app_definitions WHERE app_id IN ('procurement', 'process_fixture')`).Scan(&defs); err != nil || defs != 2 {
		t.Fatalf("expected both global definitions kept, got %d (err=%v)", defs, err)
	}

	// When the last workspace leaves, the definition and its exclusive grants go away.
	if err := rec.Run(ctx, wsB, other); err != nil {
		t.Fatalf("reconcile B without procurement: %v", err)
	}
	if hasura.Has("insert:domain.procurement_requests:role_procurement") {
		t.Error("grant of an application no workspace publishes must be revoked")
	}
	if err := db.QueryRow(`SELECT count(*) FROM core.app_definitions WHERE app_id = 'procurement'`).Scan(&defs); err != nil || defs != 0 {
		t.Fatalf("expected procurement definition removed, got %d (err=%v)", defs, err)
	}
	var rowsKept int
	if err := db.QueryRow(`SELECT count(*) FROM information_schema.tables WHERE table_schema = 'domain' AND table_name = 'procurement_requests'`).Scan(&rowsKept); err != nil || rowsKept != 1 {
		t.Fatalf("domain table must be retained after unpublishing, got %d (err=%v)", rowsKept, err)
	}
}
