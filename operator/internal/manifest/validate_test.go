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

package manifest

import "testing"

func TestValidateRejectsManagedInsertColumns(t *testing.T) {
	app := loadFixture(t, "procurement")
	app.Spec.DomainAccess.Insert[0].Columns = []string{"status", "version"}
	if err := Validate(app); err == nil {
		t.Fatal("managed insert must be rejected")
	}
}

func TestValidateRejectsWrongAPIVersion(t *testing.T) {
	app := loadFixture(t, "procurement")
	app.APIVersion = "core.dxos.org/v1alpha1"
	if err := Validate(app); err == nil {
		t.Fatal("wrong apiVersion must be rejected")
	}
}

func TestValidateRejectsMissingCommandTargetsAndUndeclaredFileColumns(t *testing.T) {
	app := loadFixture(t, "procurement")
	app.Spec.Attachments[0].RequireCleanOnCommands = []string{"nonexistent_cmd"}
	if err := Validate(app); err == nil {
		t.Fatal("unknown command in attachment must be rejected")
	}

	app = loadFixture(t, "procurement")
	app.Spec.Attachments[0].Column = "vendor_name"
	if err := Validate(app); err == nil {
		t.Fatal("non-file column in attachment must be rejected")
	}
}

func TestValidateRejectsSQLIdentifierTypeCheckAndFilterInjection(t *testing.T) {
	// 1. Hostile column identifier
	app := loadFixture(t, "procurement")
	app.Spec.DomainTable.Columns[0].Name = "vendor_name; DROP TABLE core.users;--"
	if err := Validate(app); err == nil {
		t.Fatal("SQL injection in column name must be rejected")
	}

	// 2. Hostile SQL type
	app = loadFixture(t, "procurement")
	app.Spec.DomainTable.Columns[0].Type = "text; DROP TABLE core.users"
	if err := Validate(app); err == nil {
		t.Fatal("unsupported column type must be rejected")
	}

	// 3. Hostile CHECK expression
	app = loadFixture(t, "procurement")
	app.Spec.DomainTable.Columns[0].Check = "1=1 OR length(trim(vendor_name)) > 0"
	if err := Validate(app); err == nil {
		t.Fatal("arbitrary CHECK expression must be rejected")
	}

	// 4. Unsupported filter operator (_like)
	app = loadFixture(t, "procurement")
	app.Spec.StateMachine.Commands[1].RecordFilter = map[string]any{
		"created_by": map[string]any{"_like": "%"},
	}
	if err := Validate(app); err == nil {
		t.Fatal("unsupported filter operator _like must be rejected")
	}

	// 5. Path traversal in workflow file
	app = loadFixture(t, "procurement")
	app.Spec.Workflows[0].File = "../etc/passwd"
	if err := Validate(app); err == nil {
		t.Fatal("workflow path outside workflows/ must be rejected")
	}
}

func TestValidateRolesScopesAndCapabilities(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*DXApplication)
	}{
		{"cap-process missing", func(a *DXApplication) { a.Spec.Capabilities = []string{CapHR, CapWork} }},
		{"unknown capability", func(a *DXApplication) { a.Spec.Capabilities = append(a.Spec.Capabilities, "cap-mail") }},
		{"no roles", func(a *DXApplication) { a.Spec.Roles = nil }},
		{"duplicate role", func(a *DXApplication) { a.Spec.Roles = append(a.Spec.Roles, a.Spec.Roles[0]) }},
		{"reserved role", func(a *DXApplication) { a.Spec.Roles[0].Name = "role_operator" }},
		{"undeclared role in select", func(a *DXApplication) { a.Spec.DomainAccess.Select[0].Role = "role_other_app" }},
		{"undeclared role in command", func(a *DXApplication) { a.Spec.StateMachine.Commands[0].AllowedRoles = []string{"role_other_app"} }},
		{"undeclared role in shared access", func(a *DXApplication) { a.Spec.SharedAccess[0].Roles = []string{"role_other_app"} }},
		{"select without scope", func(a *DXApplication) { a.Spec.DomainAccess.Select[0].Scope = "" }},
		{"unknown scope", func(a *DXApplication) { a.Spec.DomainAccess.Select[0].Scope = "everyone" }},
		{"scope on insert", func(a *DXApplication) { a.Spec.DomainAccess.Insert[0].Scope = ScopeOwn }},
		{"department without cap-hr", func(a *DXApplication) {
			a.Spec.Capabilities = []string{CapProcess, CapWork}
			a.Spec.SharedAccess = a.Spec.SharedAccess[:1]
		}},
		{"writable created_by", func(a *DXApplication) {
			a.Spec.DomainAccess.Update[0].Columns = append(a.Spec.DomainAccess.Update[0].Columns, "created_by")
		}},
		{"writable org_unit_id", func(a *DXApplication) {
			a.Spec.DomainAccess.Insert[0].Columns = append(a.Spec.DomainAccess.Insert[0].Columns, "org_unit_id")
		}},
		{"business column named like a system column", func(a *DXApplication) { a.Spec.DomainTable.Columns[0].Name = "created_by" }},
		{"shared table outside the catalog", func(a *DXApplication) { a.Spec.SharedAccess[0].Table = "core.audit_logs" }},
		{"unknown shared column", func(a *DXApplication) { a.Spec.SharedAccess[0].Columns = []string{"password_hash"} }},
		{"foreign key to unknown table", func(a *DXApplication) { a.Spec.DomainTable.ForeignKeys[0].References = "core.audit_logs" }},
		{"foreign key to a capability not declared", func(a *DXApplication) {
			a.Spec.DomainTable.ForeignKeys[0].References = "cap_data.devices"
			a.Spec.Attachments = nil
		}},
		{"work item effect without cap-work", func(a *DXApplication) { a.Spec.Capabilities = []string{CapProcess, CapHR} }},
		{"workspace session variable", func(a *DXApplication) {
			a.Spec.StateMachine.Commands[1].RecordFilter = map[string]any{"created_by": map[string]any{"_eq": "X-Hasura-Workspace-Id"}}
		}},
		{"preset outside X-Hasura-User-Id", func(a *DXApplication) {
			a.Spec.DomainAccess.Insert[0].Columns = []string{"vendor_name", "item_description", "quoted_amount"}
			a.Spec.DomainAccess.Insert[0].Presets = map[string]string{"quote_file_id": "X-Hasura-Role"}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			app := loadFixture(t, "procurement")
			tc.mutate(app)
			if err := Validate(app); err == nil {
				t.Fatal("expected the manifest to be rejected")
			}
		})
	}
}

func TestValidateAcceptsOptionalSections(t *testing.T) {
	app := loadFixture(t, "procurement")
	app.Spec.SharedAccess = nil
	app.Spec.DomainAccess.Update = nil
	app.Spec.Workflows = nil
	if err := Validate(app); err != nil {
		t.Fatalf("optional sections must not be required: %v", err)
	}
}

func TestSystemColumnsFollowCapabilities(t *testing.T) {
	hr := loadFixture(t, "procurement")
	if SystemColumns(hr)["org_unit_id"] != "uuid" {
		t.Fatal("a cap-hr application must have org_unit_id")
	}
	plain := loadFixture(t, "process-fixture")
	if _, ok := SystemColumns(plain)["org_unit_id"]; ok {
		t.Fatal("an application without cap-hr must not have org_unit_id")
	}
}

func TestStatesListsEveryState(t *testing.T) {
	app := loadFixture(t, "procurement")
	got := States(app.Spec.StateMachine)
	want := []string{"DRAFT", "SUBMITTED", "BUDGET_REVIEW", "APPROVED", "REJECTED", "CANCELLED"}
	if len(got) != len(want) {
		t.Fatalf("States = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("States = %v, want %v", got, want)
		}
	}
}
