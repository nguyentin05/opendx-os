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

package apply

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/opendx-os/opendx-os/operator/internal/manifest"
	"github.com/opendx-os/opendx-os/operator/internal/planner"
	"github.com/opendx-os/opendx-os/operator/internal/testutil"
)

func TestApplyConvergesAdditiveSchemaAndRefusesBreakingChanges(t *testing.T) {
	db := testutil.NewDatabase(t)
	wsID := "11111111-1111-4111-8111-111111111111"
	if _, err := db.Exec(`INSERT INTO core.workspaces (id, code, name) VALUES ($1, 'ws-a', 'Workspace A')`, wsID); err != nil {
		t.Fatal(err)
	}
	v1, err := manifest.ParseFile(filepath.Join("..", "..", "..", "contracts", "fixtures", "procurement.yaml"))
	if err != nil {
		t.Fatal(err)
	}

	var metadataCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		metadataCalls.Add(1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"version":3,"sources":[]}`))
	}))
	defer srv.Close()
	runner := NewRunner(db, Config{HasuraURL: srv.URL}, srv.Client())
	ctx := context.Background()

	apply := func(app manifest.DXApplication, live planner.ManagedState) error {
		plan, err := planner.BuildPlan([]manifest.DXApplication{app}, live)
		if err != nil {
			t.Fatal(err)
		}
		return runner.Run(ctx, plan, PublishTo(wsID, plan))
	}
	registryVersion := func() string {
		var v string
		if err := db.QueryRow(`SELECT version FROM core.app_registry WHERE workspace_id = $1 AND app_id = 'procurement'`, wsID).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	columnExists := func(name string) bool {
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM information_schema.columns WHERE table_schema = 'domain' AND table_name = 'procurement_requests' AND column_name = $1`, name).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n == 1
	}

	if err := apply(*v1, planner.ManagedState{}); err != nil {
		t.Fatalf("v1: %v", err)
	}

	// v2 adds a nullable column: converged with ALTER TABLE and published.
	v2 := *v1
	v2.Metadata.Version = "0.2.0"
	v2.Spec.DomainTable.Columns = append(append([]manifest.ColumnDef(nil), v1.Spec.DomainTable.Columns...),
		manifest.ColumnDef{Name: "note", Type: "text", Nullable: true})
	if err := apply(v2, planner.ManagedState{ActiveApps: []manifest.DXApplication{*v1}}); err != nil {
		t.Fatalf("v2: %v", err)
	}
	if !columnExists("note") {
		t.Fatal("v2 published without adding the declared column")
	}
	if registryVersion() != "0.2.0" {
		t.Fatalf("registry version = %s, want 0.2.0", registryVersion())
	}

	// v3 makes a column optional: not convergeable, blocked before any side effect.
	v3 := v2
	v3.Metadata.Version = "0.3.0"
	v3.Spec.DomainTable.Columns = append([]manifest.ColumnDef(nil), v2.Spec.DomainTable.Columns...)
	v3.Spec.DomainTable.Columns[1].Nullable = true
	before := metadataCalls.Load()
	err = apply(v3, planner.ManagedState{ActiveApps: []manifest.DXApplication{v2}})
	if err == nil || !strings.Contains(err.Error(), "cannot be converged") {
		t.Fatalf("v3 must be refused with a schema error, got %v", err)
	}
	if metadataCalls.Load() != before {
		t.Fatal("a refused schema change must not call Hasura")
	}
	if registryVersion() != "0.2.0" {
		t.Fatalf("registry version after refusal = %s, want 0.2.0", registryVersion())
	}
	var status, lastErr string
	if err := db.QueryRow(`SELECT status, COALESCE(last_error, '') FROM core.app_deployments WHERE workspace_id = $1 AND app_id = 'procurement' ORDER BY started_at DESC LIMIT 1`, wsID).Scan(&status, &lastErr); err != nil {
		t.Fatal(err)
	}
	if status != "Failed" || !strings.Contains(lastErr, "vendor_name") {
		t.Fatalf("deployment attempt = %s %q, want Failed mentioning vendor_name", status, lastErr)
	}
}
