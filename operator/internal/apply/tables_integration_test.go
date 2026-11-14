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
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/opendx-os/opendx-os/operator/internal/manifest"
	"github.com/opendx-os/opendx-os/operator/internal/planner"
	"github.com/opendx-os/opendx-os/operator/internal/testutil"
)

// An upgrade adds a table to an installed application; a later upgrade that
// stops declaring it removes its permissions and keeps the table and its rows.
func TestApplyAddsAndRetiresATable(t *testing.T) {
	db := testutil.NewDatabase(t)
	hasura := testutil.NewFakeHasura()
	srv := httptest.NewServer(hasura)
	defer srv.Close()
	runner := NewRunner(db, Config{HasuraURL: srv.URL}, srv.Client())
	ctx := context.Background()
	install := func(app *manifest.DXApplication) error {
		live, err := runner.ReadManagedState(ctx)
		if err != nil {
			t.Fatal(err)
		}
		plan, err := planner.BuildApplyPlan(*app, live)
		if err != nil {
			return err
		}
		return runner.Run(ctx, plan, []string{app.Metadata.Name})
	}
	tableExists := func(name string) bool {
		var ok bool
		if err := db.QueryRow(`SELECT to_regclass($1) IS NOT NULL`, name).Scan(&ok); err != nil {
			t.Fatal(err)
		}
		return ok
	}

	v1 := testutil.LoadApp(t, "procurement")
	if err := install(v1); err != nil {
		t.Fatalf("v1: %v", err)
	}
	labels := testutil.LabelsTable(v1)
	if tableExists(labels) {
		t.Fatal("the labels table must not exist before it is declared")
	}

	// v2 declares a second table and a nullable foreign key to it.
	v2 := testutil.WithLabels(v1)
	v2.Metadata.Version = "0.3.0"
	if err := install(v2); err != nil {
		t.Fatalf("v2: %v", err)
	}
	if !tableExists(labels) {
		t.Fatal("v2 published without creating the declared table")
	}
	var fk int
	if err := db.QueryRow(`SELECT count(*) FROM pg_constraint WHERE conname = 'fk_procurement_requests_label_id'`).Scan(&fk); err != nil || fk != 1 {
		t.Fatalf("foreign key to the new table: count %d, err %v", fk, err)
	}
	selectLabels := testutil.PermKey("select", "domain", testutil.TableName(labels), "role_procurement_manager")
	insertLabels := testutil.PermKey("insert", "domain", testutil.TableName(labels), "role_procurement_requester")
	selectRequests := testutil.PermKey("select", "domain", "procurement_requests", "role_procurement_manager")
	if !hasura.Has(selectLabels) || !hasura.Has(insertLabels) || !hasura.Has(selectRequests) {
		t.Fatal("permissions of both tables must be published")
	}
	live, err := runner.ReadManagedState(ctx)
	if err != nil || len(live.ActiveApps) != 1 || len(live.ActiveApps[0].Spec.Tables) != 2 {
		t.Fatalf("the registry must hold both tables: %+v %v", live.ActiveApps, err)
	}

	user := "22222222-2222-4222-8222-222222222222"
	if err := testutil.ExecStatements(db, `
		INSERT INTO core.users (id, email, display_name) VALUES ($1, 'a@example.org', 'A');
		INSERT INTO cap_hr.org_units (id, code, name) VALUES ('aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa', 'A', 'Department A');
		INSERT INTO cap_hr.staff (user_id, org_unit_id) VALUES ($1, 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa');
	`, user); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := testutil.AsRuntime(tx, "dxos_hasura", user, "role_procurement_requester", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO ` + labels + ` (title) VALUES ('Urgent')`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	// v3 no longer declares the table (the column stays, as a plain uuid).
	v3 := v2.Clone()
	v3.Metadata.Version = "0.4.0"
	v3.Spec.Tables = v3.Spec.Tables[:1]
	fks := v3.Spec.Tables[0].ForeignKeys
	v3.Spec.Tables[0].ForeignKeys = fks[:len(fks)-1]
	if err := install(v3); err != nil {
		t.Fatalf("v3: %v", err)
	}
	if hasura.Has(selectLabels) || hasura.Has(insertLabels) {
		t.Fatal("a table that is no longer declared must lose its permissions")
	}
	if !hasura.Has(selectRequests) {
		t.Fatal("the table that is still declared must keep its permissions")
	}
	var rows int
	if err := db.QueryRow(`SELECT count(*) FROM ` + labels).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("the retired table must keep its rows: count %d, err %v", rows, err)
	}

	// Dropping the column itself is not additive and is refused.
	v4 := v1.Clone()
	v4.Metadata.Version = "0.5.0"
	err = install(v4)
	if err == nil || !strings.Contains(err.Error(), `"label_id" exists but is not declared`) {
		t.Fatalf("removing a column must be refused, got %v", err)
	}
}
