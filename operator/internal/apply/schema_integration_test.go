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
	"errors"
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
	v1, err := manifest.ParseFile(filepath.Join("..", "..", "..", "apps", "procurement", "manifest.yaml"))
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
		return runner.Run(ctx, plan, []string{app.Metadata.Name})
	}
	registryVersion := func() string {
		var v string
		if err := db.QueryRow(`SELECT version FROM core.apps WHERE app_id = 'procurement' AND status = 'Ready'`).Scan(&v); err != nil {
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
	v2.Metadata.Version = "0.3.0"
	v2.Spec.DomainTable.Columns = append(append([]manifest.ColumnDef(nil), v1.Spec.DomainTable.Columns...),
		manifest.ColumnDef{Name: "note", Type: "text", Nullable: true})
	if err := apply(v2, planner.ManagedState{ActiveApps: []manifest.DXApplication{*v1}}); err != nil {
		t.Fatalf("v2: %v", err)
	}
	if !columnExists("note") {
		t.Fatal("v2 published without adding the declared column")
	}
	if registryVersion() != "0.3.0" {
		t.Fatalf("registry version = %s, want 0.3.0", registryVersion())
	}

	// v3 makes a column optional: not convergeable, blocked before any side effect.
	v3 := v2
	v3.Metadata.Version = "0.4.0"
	v3.Spec.DomainTable.Columns = append([]manifest.ColumnDef(nil), v2.Spec.DomainTable.Columns...)
	v3.Spec.DomainTable.Columns[0].Nullable = true
	before := metadataCalls.Load()
	err = apply(v3, planner.ManagedState{ActiveApps: []manifest.DXApplication{v2}})
	if err == nil || !strings.Contains(err.Error(), "cannot be converged") {
		t.Fatalf("v3 must be refused with a schema error, got %v", err)
	}
	if metadataCalls.Load() != before {
		t.Fatal("a refused schema change must not call Hasura")
	}
	if registryVersion() != "0.3.0" {
		t.Fatalf("registry version after refusal = %s, want 0.3.0", registryVersion())
	}
	var status, lastErr string
	if err := db.QueryRow(`SELECT status, COALESCE(last_error, '') FROM core.app_deployments WHERE app_id = 'procurement' ORDER BY started_at DESC LIMIT 1`).Scan(&status, &lastErr); err != nil {
		t.Fatal(err)
	}
	if status != "Failed" || !strings.Contains(lastErr, "vendor_name") {
		t.Fatalf("deployment attempt = %s %q, want Failed mentioning vendor_name", status, lastErr)
	}
}

func TestApplyRefusesRemovingAStateStillInUse(t *testing.T) {
	db := testutil.NewDatabase(t)
	app, err := manifest.ParseFile(filepath.Join("..", "..", "..", "apps", "process-fixture", "manifest.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	runner := NewRunner(db, Config{}, nil)
	ctx := context.Background()
	plan, err := planner.BuildPlan([]manifest.DXApplication{*app}, planner.ManagedState{})
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.Run(ctx, plan, []string{app.Metadata.Name}); err != nil {
		t.Fatal(err)
	}
	user := "22222222-2222-4222-8222-222222222222"
	if _, err := db.Exec(`INSERT INTO core.users (id, email, display_name) VALUES ($1, 'a@example.org', 'A')`, user); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := testutil.AsRuntime(tx, "dxos_hasura", user, "role_review", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO domain.review_requests (vendor_name, item_description, quoted_amount) VALUES ('Acme', 'Pump', 1)`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	// v2 drops the initial state OPEN, which the record above still uses.
	v2 := *app
	v2.Metadata.Version = "0.3.0"
	v2.Spec.StateMachine.Initial = "DRAFT"
	v2.Spec.StateMachine.EditableStates = []string{"DRAFT"}
	v2.Spec.StateMachine.Commands = append([]manifest.CommandDef(nil), app.Spec.StateMachine.Commands...)
	v2.Spec.StateMachine.Commands[0].From = []string{"DRAFT"}
	v2.Spec.StateMachine.Commands[3].From = []string{"DRAFT", "IN_REVIEW"}
	plan2, err := planner.BuildPlan([]manifest.DXApplication{v2}, planner.ManagedState{ActiveApps: []manifest.DXApplication{*app}})
	if err != nil {
		t.Fatal(err)
	}
	err = runner.Run(ctx, plan2, []string{app.Metadata.Name})
	var schemaErr *planner.SchemaError
	if !errors.As(err, &schemaErr) || !strings.Contains(err.Error(), `state "OPEN" is still used`) {
		t.Fatalf("expected a schema error for the state in use, got %v", err)
	}
}
