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

func TestPermissionsAndCrossWorkspaceFKIntegration(t *testing.T) {
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
		t.Fatal(err)
	}

	wsA := "11111111-1111-4111-8111-111111111111"
	wsB := "55555555-5555-4555-8555-555555555555"
	empA := "22222222-2222-4222-8222-222222222222"
	wiA := "33333333-3333-4333-8333-333333333333"
	recID := "44444444-4444-4444-8444-444444444444"

	_, err = db.Exec(`
		INSERT INTO core.workspaces (id, code, name) VALUES ($1, 'ws-a', 'Workspace A'), ($2, 'ws-b', 'Workspace B');
		INSERT INTO core.employees (id, workspace_id, full_name, email, department) VALUES ($3, $1, 'Alice', 'alice@example.org', 'Ops');
		INSERT INTO core.work_items (id, workspace_id, title, description, priority, assigned_employee_id) VALUES ($4, $1, 'Work', 'Desc', 'MEDIUM', $3);
	`, wsA, wsB, empA, wiA)
	if err != nil {
		t.Fatal(err)
	}

	// Cross-workspace employee/work-item FK must be rejected by composite FK constraint
	_, err = db.Exec(`
		INSERT INTO domain.procurement_requests (id, workspace_id, work_item_id, requester_id, vendor_name, item_description, quoted_amount)
		VALUES ($1, $2, $3, $4, 'Vendor', 'Item', 50.00)
	`, recID, wsB, wiA, empA)
	if err == nil {
		t.Fatal("expected cross-workspace composite FK insertion to fail")
	}

	// Negative check constraint (quoted_amount <= 0) must be rejected
	_, err = db.Exec(`
		INSERT INTO domain.procurement_requests (id, workspace_id, work_item_id, requester_id, vendor_name, item_description, quoted_amount)
		VALUES ($1, $2, $3, $4, 'Vendor', 'Item', 0)
	`, recID, wsA, wiA, empA)
	if err == nil {
		t.Fatal("expected non-positive quoted_amount to fail CHECK constraint")
	}
}
