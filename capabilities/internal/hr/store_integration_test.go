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

package hr

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/opendx-os/opendx-os/capabilities/internal/testutil"
)

// The store runs as dxos_hr with the grants of 06_runtime_roles.sql: it reads
// the organization and changes it only through the audited functions.
func TestStoreMaintainsTheOrganizationIntegration(t *testing.T) {
	p := testutil.NewPlatform(t)
	store := Store{DB: p.As(t, "dxos_hr")}
	ctx := context.Background()
	hrAdmin := p.User(t, "hr", "", AdminRole)
	an := p.User(t, "an", "")
	conflictWith := func(err error, text string) bool {
		return errors.Is(err, ErrConflict) && strings.Contains(err.Error(), text)
	}

	company, err := store.CreateOrgUnit(ctx, hrAdmin, " CTY ", "Company", nil)
	if err != nil {
		t.Fatal(err)
	}
	sales, err := store.CreateOrgUnit(ctx, hrAdmin, "KD", "Sales", &company)
	if err != nil {
		t.Fatal(err)
	}
	north, err := store.CreateOrgUnit(ctx, hrAdmin, "KD-N", "Sales North", &sales)
	if err != nil {
		t.Fatal(err)
	}

	// Refusals the caller can act on.
	if _, err := store.CreateOrgUnit(ctx, hrAdmin, "KD", "Another", nil); !conflictWith(err, "code already exists") {
		t.Fatalf("duplicate code: %v", err)
	}
	missing := "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	if _, err := store.CreateOrgUnit(ctx, hrAdmin, "X", "Orphan", &missing); !conflictWith(err, "parent department does not exist") {
		t.Fatalf("unknown parent: %v", err)
	}
	if _, err := store.CreateOrgUnit(ctx, hrAdmin, "  ", "Blank", nil); !conflictWith(err, "must not be blank") {
		t.Fatalf("blank code: %v", err)
	}
	// A department cannot move under its own sub-department, or under itself.
	if err := store.UpdateOrgUnit(ctx, hrAdmin, OrgUnit{ID: sales, Code: "KD", Name: "Sales", ParentID: &north}); !conflictWith(err, "under itself or under one of its own sub-departments") {
		t.Fatalf("cycle: %v", err)
	}
	if err := store.UpdateOrgUnit(ctx, hrAdmin, OrgUnit{ID: sales, Code: "KD", Name: "Sales", ParentID: &sales}); !errors.Is(err, ErrConflict) {
		t.Fatalf("own parent: %v", err)
	}
	if err := store.UpdateOrgUnit(ctx, hrAdmin, OrgUnit{ID: missing, Code: "Z", Name: "Z"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update of an unknown department: %v", err)
	}

	// Rename and move to the top; an unchanged update is a no-op.
	if err := store.UpdateOrgUnit(ctx, hrAdmin, OrgUnit{ID: north, Code: "MB", Name: "Northern Region", ParentID: nil}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateOrgUnit(ctx, hrAdmin, OrgUnit{ID: north, Code: "MB", Name: "Northern Region", ParentID: nil}); err != nil {
		t.Fatal(err)
	}

	// A user has one department: assigning again moves them.
	lead := "Lead"
	if err := store.AssignStaff(ctx, hrAdmin, an, sales, &lead); err != nil {
		t.Fatal(err)
	}
	if err := store.AssignStaff(ctx, hrAdmin, an, sales, &lead); err != nil {
		t.Fatalf("an unchanged assignment must be a no-op: %v", err)
	}
	if err := store.AssignStaff(ctx, hrAdmin, an, north, nil); err != nil {
		t.Fatal(err)
	}
	m, err := store.Membership(ctx, an)
	if err != nil || m == nil || m.OrgUnitID != north || m.Position != nil || m.DisplayName != "an" {
		t.Fatalf("membership after the move: %+v %v", m, err)
	}
	if err := store.AssignStaff(ctx, hrAdmin, missing, sales, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("assigning an unknown user: %v", err)
	}
	if err := store.AssignStaff(ctx, hrAdmin, an, missing, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("assigning to an unknown department: %v", err)
	}

	units, err := store.OrgUnits(ctx)
	if err != nil || len(units) != 3 {
		t.Fatalf("departments: %+v %v", units, err)
	}
	byCode := map[string]OrgUnit{}
	for _, u := range units {
		byCode[u.Code] = u
	}
	if byCode["CTY"].ParentID != nil || *byCode["KD"].ParentID != company || byCode["MB"].ParentID != nil || byCode["MB"].StaffCount != 1 || byCode["KD"].StaffCount != 0 {
		t.Fatalf("department tree: %+v", byCode)
	}
	if staff, err := store.Staff(ctx, north); err != nil || len(staff) != 1 || staff[0].UserID != an {
		t.Fatalf("staff of a department: %+v %v", staff, err)
	}
	if staff, err := store.Staff(ctx, sales); err != nil || len(staff) != 0 {
		t.Fatalf("staff of the department she left: %+v %v", staff, err)
	}
	if staff, err := store.Staff(ctx, ""); err != nil || len(staff) != 1 {
		t.Fatalf("all staff: %+v %v", staff, err)
	}

	if err := store.RemoveStaff(ctx, hrAdmin, an); err != nil {
		t.Fatal(err)
	}
	if err := store.RemoveStaff(ctx, hrAdmin, an); !errors.Is(err, ErrNotFound) {
		t.Fatalf("removing someone without a department: %v", err)
	}
	if m, err := store.Membership(ctx, an); err != nil || m != nil {
		t.Fatalf("membership after removal: %+v %v", m, err)
	}

	// Every change that happened is in the core chain with its administrator;
	// refusals and no-ops left nothing.
	var trail sql.NullString
	if err := p.Admin.QueryRow(`SELECT string_agg(action, ',' ORDER BY seq) FROM core.audit_logs
		WHERE chain = 'core' AND entity_type LIKE 'cap_hr.%' AND actor_id = $1 AND acting_role = $2`, hrAdmin, AdminRole).Scan(&trail); err != nil {
		t.Fatal(err)
	}
	want := "CREATE_ORG_UNIT,CREATE_ORG_UNIT,CREATE_ORG_UNIT,UPDATE_ORG_UNIT,ASSIGN_STAFF,ASSIGN_STAFF,REMOVE_STAFF"
	if trail.String != want {
		t.Fatalf("audit trail = %s\nwant          %s", trail.String, want)
	}
	var previous string
	if err := p.Admin.QueryRow(`SELECT payload ->> 'previous_org_unit_id' FROM core.audit_logs
		WHERE action = 'ASSIGN_STAFF' ORDER BY seq DESC LIMIT 1`).Scan(&previous); err != nil || previous != sales {
		t.Fatalf("a move records where the user came from: %s %v", previous, err)
	}

	// The connection cannot write the tables itself, and nobody else may use the functions.
	if _, err := store.DB.ExecContext(ctx, `INSERT INTO cap_hr.org_units (code, name) VALUES ('RAW', 'Raw')`); err == nil {
		t.Fatal("dxos_hr must not write cap_hr.org_units directly")
	}
	if _, err := store.DB.ExecContext(ctx, `DELETE FROM cap_hr.staff`); err == nil {
		t.Fatal("dxos_hr must not delete from cap_hr.staff directly")
	}
	if _, err := store.DB.ExecContext(ctx, `INSERT INTO core.user_roles (user_id, role_name) VALUES ($1, 'role_operator')`, an); err == nil {
		t.Fatal("dxos_hr must not assign roles")
	}
	if _, err := p.Admin.Exec(`SELECT cap_hr.create_org_unit($1, $2, 'ADM', 'Admin', NULL)`, hrAdmin, AdminRole); err == nil || !strings.Contains(err.Error(), "untrusted database connection") {
		t.Fatalf("HR functions must refuse other connections, got %v", err)
	}
	if _, err := p.Process.ExecContext(ctx, `SELECT cap_hr.create_org_unit($1, $2, 'PRC', 'Process', NULL)`, hrAdmin, AdminRole); err == nil {
		t.Fatal("cap-process must not administer the organization")
	}
}
