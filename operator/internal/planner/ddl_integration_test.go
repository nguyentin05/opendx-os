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
	"path/filepath"
	"testing"

	"github.com/opendx-os/opendx-os/operator/internal/manifest"
	"github.com/opendx-os/opendx-os/operator/internal/testutil"
)

func TestDDLIntegrationVersionBumpAndAuditTrigger(t *testing.T) {
	db := testutil.NewDatabase(t)

	path := filepath.Join("..", "..", "..", "contracts", "fixtures", "procurement.yaml")
	app, err := manifest.ParseFile(path)
	if err != nil {
		t.Fatal(err)
	}
	ddl, err := GenerateDDL(app)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ddl); err != nil {
		t.Fatalf("execute generated DDL: %v", err)
	}

	wsID := "11111111-1111-4111-8111-111111111111"
	empID := "22222222-2222-4222-8222-222222222222"
	wiID := "33333333-3333-4333-8333-333333333333"
	recID := "44444444-4444-4444-8444-444444444444"

	err = testutil.ExecStatements(db, `
		INSERT INTO core.workspaces (id, code, name) VALUES ($1, 'ws-a', 'Workspace A');
		INSERT INTO core.employees (id, workspace_id, full_name, email, department) VALUES ($2, $1, 'Alice', 'alice@example.org', 'Ops');
		INSERT INTO core.work_items (id, workspace_id, title, description, priority, assigned_employee_id) VALUES ($3, $1, 'Task', 'Desc', 'MEDIUM', $2);
		INSERT INTO domain.procurement_requests (id, workspace_id, work_item_id, requester_id, vendor_name, item_description, quoted_amount)
		VALUES ($4, $1, $3, $2, 'Acme', 'Pump', 100.00);
	`, wsID, empID, wiID, recID)
	if err != nil {
		t.Fatalf("seed records: %v", err)
	}

	// 1. Updating without actor context must fail
	if _, err := db.Exec(`UPDATE domain.procurement_requests SET vendor_name = 'Beta' WHERE id = $1`, recID); err == nil {
		t.Fatal("expected domain update without actor context to be rejected")
	}

	// 2. Updating with transaction-local actor context increments version once and writes one audit row
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }() // Best-effort cleanup; a committed transaction is already closed.

	if _, err := tx.Exec(`SELECT set_config('dxos.actor_id', $1, true), set_config('dxos.action', 'UPDATE', true)`, empID); err != nil {
		t.Fatal(err)
	}
	var newVersion int
	if err := tx.QueryRow(`UPDATE domain.procurement_requests SET vendor_name = 'Beta' WHERE id = $1 RETURNING version`, recID).Scan(&newVersion); err != nil {
		t.Fatalf("update with actor context: %v", err)
	}
	if newVersion != 2 {
		t.Fatalf("expected version 2, got %d", newVersion)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	var auditCount int
	if err := db.QueryRow(`SELECT count(*) FROM core.audit_logs WHERE workspace_id = $1 AND entity_id = $2 AND entity_version = 2`, wsID, recID).Scan(&auditCount); err != nil || auditCount != 1 {
		t.Fatalf("expected 1 audit log row for version 2, got %d (err=%v)", auditCount, err)
	}
}
