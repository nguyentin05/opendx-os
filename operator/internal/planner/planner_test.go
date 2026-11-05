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
	"strings"
	"testing"

	"github.com/opendx-os/opendx-os/operator/internal/manifest"
)

func sampleApp() *manifest.DXApplication {
	return &manifest.DXApplication{
		APIVersion: "core.dxos.org/v1alpha1",
		Kind:       "DXApplication",
		Metadata: manifest.AppMetadata{
			Name:    "demoapp",
			Version: "1.0.0",
		},
		Spec: manifest.AppSpec{
			Role: "role_demo",
			DomainTable: manifest.DomainTableDef{
				Name: "domain.demo_records",
				Columns: []manifest.ColumnDef{
					{Name: "id", Type: "uuid", PrimaryKey: true},
					{Name: "workspace_id", Type: "uuid", References: "core.workspaces(id)"},
					{Name: "work_item_id", Type: "uuid", References: "core.work_items(workspace_id, id)"},
					{Name: "amount", Type: "numeric"},
					{Name: "status", Type: "text", Default: "'DRAFT'"},
					{Name: "version", Type: "integer", Default: "1"},
					{Name: "updated_at", Type: "timestamptz", Default: "now()"},
				},
			},
			DomainAccess: manifest.DomainAccessDef{
				Select: []manifest.AccessRule{
					{Columns: []string{"id", "workspace_id", "amount", "status", "version"}},
				},
				Insert: []manifest.AccessRule{
					{Columns: []string{"amount"}},
				},
			},
			SharedAccess: []manifest.SharedAccessDef{
				{
					Table:   "core.employees",
					Columns: []string{"id", "workspace_id", "full_name"},
					Relationships: []manifest.RelationshipDef{
						{
							Name:        "demo_records",
							TargetTable: "domain.demo_records",
							Mapping:     map[string]string{"id": "assigned_employee_id"},
						},
					},
				},
			},
		},
	}
}

func TestGenerateDDL(t *testing.T) {
	app := sampleApp()
	ddl, err := GenerateDDL(app)
	if err != nil {
		t.Fatalf("unexpected error generating DDL: %v", err)
	}

	if !strings.Contains(ddl, "CREATE TABLE IF NOT EXISTS domain.demo_records") {
		t.Errorf("expected domain table creation, got:\n%s", ddl)
	}
	if !strings.Contains(ddl, "FOREIGN KEY (workspace_id, work_item_id) REFERENCES core.work_items(workspace_id, id)") {
		t.Errorf("expected composite foreign key, got:\n%s", ddl)
	}
	if !strings.Contains(ddl, "core.bump_domain_record_version()") {
		t.Errorf("expected version bump trigger, got:\n%s", ddl)
	}
	if !strings.Contains(ddl, "INSERT INTO core.app_registry") {
		t.Errorf("expected core.app_registry insert, got:\n%s", ddl)
	}
}

func TestGenerateHasuraOperations(t *testing.T) {
	app := sampleApp()
	ops, err := GenerateHasuraOperations(app)
	if err != nil {
		t.Fatalf("unexpected error generating hasura operations: %v", err)
	}

	foundTrack := false
	foundSelect := false
	foundInsert := false
	foundArrayRel := false

	for _, op := range ops {
		switch op.Type {
		case "pg_track_table":
			foundTrack = true
		case "pg_create_select_permission":
			foundSelect = true
		case "pg_create_insert_permission":
			foundInsert = true
		case "pg_create_array_relationship":
			foundArrayRel = true
		}
	}

	if !foundTrack {
		t.Errorf("missing pg_track_table in hasura operations")
	}
	if !foundSelect {
		t.Errorf("missing pg_create_select_permission in hasura operations")
	}
	if !foundInsert {
		t.Errorf("missing pg_create_insert_permission in hasura operations")
	}
	if !foundArrayRel {
		t.Errorf("missing pg_create_array_relationship in hasura operations")
	}
}
