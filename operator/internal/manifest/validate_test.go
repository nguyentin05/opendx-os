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
	app := loadFixture(t, "procurement.yaml")
	app.Spec.DomainAccess.Insert[0].Columns = []string{"status", "version"}
	if err := Validate(app); err == nil {
		t.Fatal("managed insert must be rejected")
	}
}

func TestValidateRejectsWrongAPIVersion(t *testing.T) {
	app := loadFixture(t, "procurement.yaml")
	app.APIVersion = "core.dxos.org/v1alpha1"
	if err := Validate(app); err == nil {
		t.Fatal("wrong apiVersion must be rejected")
	}
}

func TestValidateRejectsMissingCommandTargetsAndUndeclaredFileColumns(t *testing.T) {
	app := loadFixture(t, "procurement.yaml")
	app.Spec.Attachments[0].RequireCleanOnCommands = []string{"nonexistent_cmd"}
	if err := Validate(app); err == nil {
		t.Fatal("unknown command in attachment must be rejected")
	}

	app = loadFixture(t, "procurement.yaml")
	app.Spec.Attachments[0].Column = "vendor_name"
	if err := Validate(app); err == nil {
		t.Fatal("non-file column in attachment must be rejected")
	}
}

func TestValidateRejectsSQLIdentifierTypeCheckAndFilterInjection(t *testing.T) {
	// 1. Hostile column identifier
	app := loadFixture(t, "procurement.yaml")
	app.Spec.DomainTable.Columns[0].Name = "requester_id; DROP TABLE core.workspaces;--"
	if err := Validate(app); err == nil {
		t.Fatal("SQL injection in column name must be rejected")
	}

	// 2. Hostile SQL type
	app = loadFixture(t, "procurement.yaml")
	app.Spec.DomainTable.Columns[0].Type = "text; DROP TABLE core.workspaces"
	if err := Validate(app); err == nil {
		t.Fatal("unsupported column type must be rejected")
	}

	// 3. Hostile CHECK expression
	app = loadFixture(t, "procurement.yaml")
	app.Spec.DomainTable.Columns[1].Check = "1=1 OR length(trim(vendor_name)) > 0"
	if err := Validate(app); err == nil {
		t.Fatal("arbitrary CHECK expression must be rejected")
	}

	// 4. Unsupported filter operator (_like)
	app = loadFixture(t, "procurement.yaml")
	app.Spec.StateMachine.Commands[1].RecordFilter = map[string]any{
		"requester_id": map[string]any{"_like": "%"},
	}
	if err := Validate(app); err == nil {
		t.Fatal("unsupported filter operator _like must be rejected")
	}

	// 5. Path traversal in workflow file
	app = loadFixture(t, "procurement.yaml")
	app.Spec.Workflows[0].File = "../etc/passwd"
	if err := Validate(app); err == nil {
		t.Fatal("workflow path outside workflows/ must be rejected")
	}
}
