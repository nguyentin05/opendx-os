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
	"strings"
	"testing"

	"github.com/opendx-os/opendx-os/operator/internal/manifest"
)

func loadFixtureApp(t *testing.T) *manifest.DXApplication {
	t.Helper()
	path := filepath.Join("..", "..", "..", "contracts", "fixtures", "procurement.yaml")
	app, err := manifest.ParseFile(path)
	if err != nil {
		t.Fatalf("failed to parse procurement fixture: %v", err)
	}
	return app
}

func TestGenerateDDL(t *testing.T) {
	app := loadFixtureApp(t)
	ddl, err := GenerateDDL(app)
	if err != nil {
		t.Fatalf("unexpected error generating DDL: %v", err)
	}

	checks := []string{
		`CREATE TABLE IF NOT EXISTS "domain"."procurement_requests"`,
		`"id" uuid PRIMARY KEY DEFAULT gen_random_uuid()`,
		`"workspace_id" uuid NOT NULL REFERENCES "core"."workspaces"("id")`,
		`"status" text NOT NULL DEFAULT 'DRAFT'`,
		`FOREIGN KEY ("workspace_id", "work_item_id") REFERENCES "core"."work_items"("workspace_id", "id")`,
		`FOREIGN KEY ("workspace_id", "requester_id") REFERENCES "core"."employees"("workspace_id", "id")`,
		`FOREIGN KEY ("workspace_id", "quote_file_id") REFERENCES "core"."file_objects"("workspace_id", "id")`,
		`core.bump_domain_record_version()`,
		`core.record_domain_update_audit()`,
	}
	for _, want := range checks {
		if !strings.Contains(ddl, want) {
			t.Errorf("DDL missing %q, got:\n%s", want, ddl)
		}
	}
}

func TestGenerateHasuraOperations(t *testing.T) {
	app := loadFixtureApp(t)
	ops, err := GenerateHasuraOperations(app)
	if err != nil {
		t.Fatalf("unexpected error generating Hasura operations: %v", err)
	}

	var foundTrack, foundInsert, foundUpdate bool
	for _, op := range ops {
		switch op.Type {
		case "pg_track_table":
			foundTrack = true
		case "pg_create_insert_permission":
			foundInsert = true
			args := op.Args.(map[string]any)
			perm := args["permission"].(map[string]any)
			presets := perm["set"].(map[string]string)
			if presets["status"] != "DRAFT" || presets["requester_id"] != "X-Hasura-User-Id" || presets["workspace_id"] != "X-Hasura-Workspace-Id" {
				t.Fatalf("unexpected insert presets: %+v", presets)
			}
		case "pg_create_update_permission":
			foundUpdate = true
		case "pg_create_delete_permission":
			t.Fatal("delete permission must never be generated")
		}
	}
	if !foundTrack || !foundInsert || !foundUpdate {
		t.Fatalf("missing expected Hasura ops: track=%v insert=%v update=%v", foundTrack, foundInsert, foundUpdate)
	}
}
