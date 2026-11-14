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
package apply

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lib/pq"
	"github.com/opendx-os/opendx-os/operator/internal/manifest"
	"github.com/opendx-os/opendx-os/operator/internal/planner"
	"github.com/opendx-os/opendx-os/operator/internal/testutil"
)

func parseApp(t *testing.T, name string) manifest.DXApplication {
	t.Helper()
	app, err := manifest.ParseFile(filepath.Join("..", "..", "..", "apps", name, "manifest.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return *app
}

func textList(t *testing.T, db *sql.DB, query string, args ...any) []string {
	t.Helper()
	var out []string
	if err := db.QueryRow(`SELECT coalesce(array_agg(x ORDER BY x) FILTER (WHERE x IS NOT NULL), '{}') FROM (`+query+`) q(x)`, args...).Scan(pq.Array(&out)); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestApplyRegistersRolesAndCapabilities(t *testing.T) {
	db := testutil.NewDatabase(t)
	runner := NewRunner(db, Config{}, nil)
	ctx := context.Background()
	install := func(apps []manifest.DXApplication, targets ...string) error {
		live, err := runner.ReadManagedState(ctx)
		if err != nil {
			t.Fatal(err)
		}
		plan, err := planner.BuildPlan(apps, live)
		if err != nil {
			t.Fatal(err)
		}
		return runner.Run(ctx, plan, targets)
	}

	procurement := parseApp(t, "procurement")
	if err := install([]manifest.DXApplication{procurement}, "procurement"); err != nil {
		t.Fatal(err)
	}
	if got := textList(t, db, `SELECT status FROM core.apps WHERE app_id = 'procurement'`); strings.Join(got, ",") != "Ready" {
		t.Fatalf("core.apps status = %v", got)
	}
	if got := textList(t, db, `SELECT name FROM core.roles WHERE app_id = 'procurement'`); strings.Join(got, ",") != "role_procurement_finance,role_procurement_manager,role_procurement_requester" {
		t.Fatalf("roles = %v", got)
	}
	if got := textList(t, db, `SELECT cap_id FROM core.app_capabilities WHERE app_id = 'procurement'`); strings.Join(got, ",") != "cap-hr,cap-process,cap-work" {
		t.Fatalf("capabilities = %v", got)
	}
	if got := textList(t, db, `SELECT status FROM core.app_deployments WHERE app_id = 'procurement'`); strings.Join(got, ",") != "Ready" {
		t.Fatalf("deployments = %v", got)
	}

	// A role dropped from the manifest is deleted with its assignments.
	user := "22222222-2222-4222-8222-222222222222"
	withAuditor := procurement
	withAuditor.Metadata.Version = "0.3.0"
	withAuditor.Spec.Roles = append(append([]manifest.RoleDef(nil), procurement.Spec.Roles...), manifest.RoleDef{Name: "role_procurement_auditor"})
	if err := install([]manifest.DXApplication{withAuditor}, "procurement"); err != nil {
		t.Fatal(err)
	}
	if err := testutil.ExecStatements(db, `
		INSERT INTO core.users (id, email, display_name) VALUES ($1, 'a@example.org', 'A');
		INSERT INTO core.user_roles (user_id, role_name) VALUES ($1, 'role_procurement_auditor'), ($1, 'role_procurement_manager');
	`, user); err != nil {
		t.Fatal(err)
	}
	if err := install([]manifest.DXApplication{procurement}, "procurement"); err != nil {
		t.Fatal(err)
	}
	if got := textList(t, db, `SELECT role_name FROM core.user_roles WHERE user_id = $1`, user); strings.Join(got, ",") != "role_procurement_manager" {
		t.Fatalf("assignments after dropping a role = %v", got)
	}

	// Uninstall: the row stays as Removed, capabilities are released, roles,
	// assignments and the domain table are kept.
	if err := install(nil); err != nil {
		t.Fatal(err)
	}
	if got := textList(t, db, `SELECT status FROM core.apps WHERE app_id = 'procurement'`); strings.Join(got, ",") != "Removed" {
		t.Fatalf("status after uninstall = %v", got)
	}
	if got := textList(t, db, `SELECT cap_id FROM core.app_capabilities WHERE app_id = 'procurement'`); len(got) != 0 {
		t.Fatalf("capabilities after uninstall = %v", got)
	}
	if got := textList(t, db, `SELECT role_name FROM core.user_roles WHERE user_id = $1`, user); len(got) != 1 {
		t.Fatalf("assignments after uninstall = %v", got)
	}
	if got := textList(t, db, `SELECT to_regclass('domain.procurement_requests')::text`); len(got) != 1 {
		t.Fatal("the domain table must survive an uninstall")
	}
	state, err := runner.ReadManagedState(ctx)
	if err != nil || len(state.ActiveApps) != 0 {
		t.Fatalf("a removed application is still active: %+v (err=%v)", state.ActiveApps, err)
	}

	// Reinstall restores the definition; the kept role assignment applies again.
	if err := install([]manifest.DXApplication{procurement}, "procurement"); err != nil {
		t.Fatal(err)
	}
	if got := textList(t, db, `SELECT status FROM core.apps WHERE app_id = 'procurement'`); strings.Join(got, ",") != "Ready" {
		t.Fatalf("status after reinstall = %v", got)
	}
}

func TestApplyRefusesRegistryConflictsBeforeAnySideEffect(t *testing.T) {
	db := testutil.NewDatabase(t)
	runner := NewRunner(db, Config{}, nil)
	ctx := context.Background()
	review := parseApp(t, "process-fixture")
	run := func() error {
		plan, err := planner.BuildPlan([]manifest.DXApplication{review}, planner.ManagedState{})
		if err != nil {
			t.Fatal(err)
		}
		return runner.Run(ctx, plan, []string{"process_fixture"})
	}
	tableExists := func() bool {
		return len(textList(t, db, `SELECT to_regclass('domain.review_requests')::text`)) == 1
	}

	// A role owned by a capability.
	if _, err := db.Exec(`INSERT INTO core.roles (name, cap_id) VALUES ('role_review', 'cap-work')`); err != nil {
		t.Fatal(err)
	}
	err := run()
	var regErr *RegistryError
	if !errors.As(err, &regErr) || !strings.Contains(err.Error(), "role role_review belongs to capability cap-work") {
		t.Fatalf("expected a role ownership conflict, got %v", err)
	}
	if tableExists() {
		t.Fatal("a refused application must not create its domain table")
	}
	if got := textList(t, db, `SELECT status FROM core.apps WHERE app_id = 'process_fixture'`); strings.Join(got, ",") != "Failed" {
		t.Fatalf("status of the refused application = %v, want Failed", got)
	}

	// A capability that is not enabled.
	if _, err := db.Exec(`DELETE FROM core.roles WHERE name = 'role_review'; UPDATE core.capabilities SET enabled = false WHERE cap_id = 'cap-work'`); err != nil {
		t.Fatal(err)
	}
	err = run()
	if !errors.As(err, &regErr) || !strings.Contains(err.Error(), "capability cap-work is not enabled") {
		t.Fatalf("expected a disabled capability conflict, got %v", err)
	}

	// Once the capability is enabled the same manifest installs.
	if _, err := db.Exec(`UPDATE core.capabilities SET enabled = true WHERE cap_id = 'cap-work'`); err != nil {
		t.Fatal(err)
	}
	if err := run(); err != nil {
		t.Fatalf("install after enabling cap-work: %v", err)
	}
	if got := textList(t, db, `SELECT status FROM core.apps WHERE app_id = 'process_fixture'`); strings.Join(got, ",") != "Ready" {
		t.Fatalf("status = %v, want Ready", got)
	}
}
