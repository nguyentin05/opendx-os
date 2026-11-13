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

package planner

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/opendx-os/opendx-os/operator/internal/manifest"
	"github.com/opendx-os/opendx-os/operator/internal/testutil"
)

const (
	alice  = "22222222-2222-4222-8222-222222222222" // staff of department A
	bob    = "33333333-3333-4333-8333-333333333333" // staff of department B
	nobody = "66666666-6666-4666-8666-666666666666" // user without a department
	deptA  = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	deptB  = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
)

// installDomainTable registers app in core.apps (the audit chain references it),
// executes its generated DDL and seeds two departments with one user each.
func installDomainTable(t *testing.T, db *sql.DB, app *manifest.DXApplication) {
	t.Helper()
	ddl, err := GenerateDDL(app)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO core.apps (app_id, display_name, version, spec, status) VALUES ($1, $2, $3, '{}', 'Ready')`,
		app.Metadata.Name, app.Metadata.DisplayName, app.Metadata.Version); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ddl); err != nil {
		t.Fatalf("execute generated DDL: %v", err)
	}
	if err := testutil.ExecStatements(db, `
		INSERT INTO core.users (id, email, display_name) VALUES ($1, 'alice@example.org', 'Alice'), ($2, 'bob@example.org', 'Bob'), ($3, 'nobody@example.org', 'Nobody');
		INSERT INTO cap_hr.org_units (id, code, name) VALUES ($4, 'A', 'Department A'), ($5, 'B', 'Department B');
		INSERT INTO cap_hr.staff (user_id, org_unit_id) VALUES ($1, $4), ($2, $5);
	`, alice, bob, nobody, deptA, deptB); err != nil {
		t.Fatalf("seed users and departments: %v", err)
	}
}

// inTx runs fn in a transaction acting as a runtime connection and rolls back
// unless fn succeeds.
func inTx(t *testing.T, db *sql.DB, connection, actor, action string, fn func(*sql.Tx) error) error {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }() // Best-effort cleanup; a committed transaction is already closed.
	if err := testutil.AsRuntime(tx, connection, actor, "role_procurement_requester", action); err != nil {
		t.Fatal(err)
	}
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func TestDDLIntegrationStampsAuditsAndVersions(t *testing.T) {
	db := testutil.NewDatabase(t)
	installDomainTable(t, db, loadFixtureApp(t))

	// An untrusted connection has no actor and cannot create records.
	if _, err := db.Exec(`INSERT INTO domain.procurement_requests (vendor_name, item_description, quoted_amount) VALUES ('Acme', 'Pump', 100)`); err == nil {
		t.Fatal("insert without an authenticated actor must be rejected")
	}

	// Hasura inserts as Alice: creator, department, state and version are stamped.
	var id, createdBy, orgUnit, status string
	var version int
	err := inTx(t, db, "dxos_hasura", alice, "", func(tx *sql.Tx) error {
		return tx.QueryRow(`INSERT INTO domain.procurement_requests (vendor_name, item_description, quoted_amount)
			VALUES ('Acme', 'Pump', 100) RETURNING id, created_by, org_unit_id, status, version`).Scan(&id, &createdBy, &orgUnit, &status, &version)
	})
	if err != nil {
		t.Fatalf("insert as Alice: %v", err)
	}
	if createdBy != alice || orgUnit != deptA || status != "DRAFT" || version != 1 {
		t.Fatalf("stamped (%s, %s, %s, %d), want (%s, %s, DRAFT, 1)", createdBy, orgUnit, status, version, alice, deptA)
	}

	// A user without a department cannot create records in a cap-hr application.
	err = inTx(t, db, "dxos_hasura", nobody, "", func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO domain.procurement_requests (vendor_name, item_description, quoted_amount) VALUES ('Acme', 'Pump', 1)`)
		return err
	})
	if err == nil || !strings.Contains(err.Error(), "has no department") {
		t.Fatalf("insert by a user without a department: got %v", err)
	}

	// cap-process runs a command: the version is bumped and the command is audited.
	err = inTx(t, db, "dxos_process", alice, "submit", func(tx *sql.Tx) error {
		return tx.QueryRow(`UPDATE domain.procurement_requests SET status = 'SUBMITTED' WHERE id = $1 RETURNING version`, id).Scan(&version)
	})
	if err != nil || version != 2 {
		t.Fatalf("submit: version %d, err %v", version, err)
	}

	rows, err := db.Query(`SELECT chain, seq, action, acting_role, entity_version FROM core.audit_logs WHERE entity_id = $1 ORDER BY seq`, id)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var got []string
	for rows.Next() {
		var chain, action, role string
		var seq, ver int
		if err := rows.Scan(&chain, &seq, &action, &role, &ver); err != nil {
			t.Fatal(err)
		}
		got = append(got, strings.Join([]string{chain, action, role}, "/"))
		if seq != len(got) || ver != len(got) {
			t.Fatalf("audit row %d has seq %d and version %d", len(got), seq, ver)
		}
	}
	want := []string{"procurement/INSERT/role_procurement_requester", "procurement/submit/role_procurement_requester"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("audit chain = %v, want %v", got, want)
	}

	// The creator, department and allowed states are enforced by the database.
	for name, stmt := range map[string]string{
		"undeclared state":    `UPDATE domain.procurement_requests SET status = 'ON_HOLD' WHERE id = $1`,
		"changed creator":     `UPDATE domain.procurement_requests SET created_by = '` + bob + `' WHERE id = $1`,
		"changed department":  `UPDATE domain.procurement_requests SET org_unit_id = '` + deptB + `' WHERE id = $1`,
		"non-positive amount": `UPDATE domain.procurement_requests SET quoted_amount = 0 WHERE id = $1`,
	} {
		err := inTx(t, db, "dxos_process", alice, "edit", func(tx *sql.Tx) error {
			_, err := tx.Exec(stmt, id)
			return err
		})
		if err == nil {
			t.Errorf("%s must be rejected", name)
		}
	}
}

func TestDDLIntegrationIsIdempotentAndScopesByCapability(t *testing.T) {
	db := testutil.NewDatabase(t)
	app := loadFixtureApp(t)
	installDomainTable(t, db, app)
	ddl, err := GenerateDDL(app)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ddl); err != nil {
		t.Fatalf("re-running the DDL must succeed: %v", err)
	}

	review := loadApp(t, "process-fixture")
	reviewDDL, err := GenerateDDL(review)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO core.apps (app_id, display_name, version, spec, status) VALUES ('process_fixture', 'Review', '1', '{}', 'Ready')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(reviewDDL); err != nil {
		t.Fatal(err)
	}
	var hasOrgUnit bool
	if err := db.QueryRow(`SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'domain' AND table_name = 'review_requests' AND column_name = 'org_unit_id')`).Scan(&hasOrgUnit); err != nil {
		t.Fatal(err)
	}
	if hasOrgUnit {
		t.Fatal("an application without cap-hr must not get org_unit_id")
	}
	// A user without a department can use an application without cap-hr.
	err = inTx(t, db, "dxos_hasura", nobody, "", func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO domain.review_requests (vendor_name, item_description, quoted_amount) VALUES ('Acme', 'Pump', 1)`)
		return err
	})
	if err != nil {
		t.Fatalf("insert into an application without cap-hr: %v", err)
	}
}

// Hasura evaluates a department scope by joining cap_hr.staff, so the DDL of a
// department-scoped app must let dxos_hasura reach that schema and table.
func TestDDLIntegrationLetsHasuraReadDepartmentTables(t *testing.T) {
	db := testutil.NewDatabase(t)
	installDomainTable(t, db, loadFixtureApp(t))
	err := inTx(t, db, "dxos_hasura", nobody, "", func(tx *sql.Tx) error {
		_, err := tx.Exec(`SELECT 1 FROM cap_hr.staff s JOIN cap_hr.org_units o ON o.id = s.org_unit_id LIMIT 1`)
		return err
	})
	if err != nil {
		t.Fatalf("dxos_hasura must read the department tables: %v", err)
	}
}
