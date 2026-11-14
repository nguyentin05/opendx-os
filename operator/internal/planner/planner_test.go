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
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opendx-os/opendx-os/operator/internal/manifest"
)

func loadFixtureApp(t *testing.T) *manifest.DXApplication {
	t.Helper()
	return loadApp(t, "procurement")
}

func loadApp(t *testing.T, name string) *manifest.DXApplication {
	t.Helper()
	app, err := manifest.ParseFile(filepath.Join("..", "..", "..", "apps", name, "manifest.yaml"))
	if err != nil {
		t.Fatalf("failed to parse %s manifest: %v", name, err)
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
		`"created_by" uuid NOT NULL REFERENCES "core"."users"("id")`,
		`"org_unit_id" uuid NOT NULL REFERENCES "cap_hr"."org_units"("id")`,
		`"status" text NOT NULL DEFAULT 'DRAFT'`,
		`"created_at" timestamptz NOT NULL DEFAULT now()`,
		`FOREIGN KEY ("quote_file_id") REFERENCES "core"."files"("id")`,
		`CHECK ("status" IN ('DRAFT', 'SUBMITTED', 'BUDGET_REVIEW', 'APPROVED', 'REJECTED', 'CANCELLED'))`,
		`CREATE INDEX IF NOT EXISTS "idx_procurement_requests_created_by"`,
		`CREATE INDEX IF NOT EXISTS "idx_procurement_requests_org_unit_id"`,
		`CREATE INDEX IF NOT EXISTS "idx_procurement_requests_status"`,
		"BEFORE INSERT ON \"domain\".\"procurement_requests\"\nFOR EACH ROW EXECUTE FUNCTION core.stamp_record_owner();",
		"BEFORE INSERT ON \"domain\".\"procurement_requests\"\nFOR EACH ROW EXECUTE FUNCTION cap_hr.stamp_record_org_unit();",
		"BEFORE UPDATE ON \"domain\".\"procurement_requests\"\nFOR EACH ROW EXECUTE FUNCTION core.bump_domain_record_version();",
		"AFTER INSERT OR UPDATE ON \"domain\".\"procurement_requests\"\nFOR EACH ROW EXECUTE FUNCTION core.record_domain_audit('procurement');",
	}
	for _, want := range checks {
		if !strings.Contains(ddl, want) {
			t.Errorf("DDL missing %q, got:\n%s", want, ddl)
		}
	}
	for _, gone := range []string{"workspace", "work_item_id", "requester_id"} {
		if strings.Contains(ddl, gone) {
			t.Errorf("DDL still mentions %q", gone)
		}
	}
	// PostgreSQL fires BEFORE triggers of one event in name order: the creator
	// must be stamped before the department is derived from it.
	if !strings.Contains(ddl, `"trg_procurement_requests_10_stamp_owner"`) || !strings.Contains(ddl, `"trg_procurement_requests_20_stamp_org_unit"`) {
		t.Error("owner and department triggers must be named so that the owner sorts first")
	}
}

func TestGenerateDDLWithoutHR(t *testing.T) {
	ddl, err := GenerateDDL(loadApp(t, "process-fixture"))
	if err != nil {
		t.Fatal(err)
	}
	for _, unwanted := range []string{"org_unit_id", "cap_hr"} {
		if strings.Contains(ddl, unwanted) {
			t.Errorf("DDL of an application without cap-hr mentions %q", unwanted)
		}
	}
	if !strings.Contains(ddl, "core.record_domain_audit('process_fixture')") {
		t.Error("audit trigger must name the application")
	}
}

func TestGenerateHasuraOperations(t *testing.T) {
	app := loadFixtureApp(t)
	ops, err := GenerateHasuraOperations(app)
	if err != nil {
		t.Fatalf("unexpected error generating Hasura operations: %v", err)
	}

	perms := map[string]map[string]any{}
	firstPermission, lastRelationship := -1, -1
	tracked := map[string]bool{}
	for i, op := range ops {
		args := op.Args.(map[string]any)
		switch {
		case op.Type == "pg_track_table":
			ref := args["table"].(TableRef)
			tracked[ref.Schema+"."+ref.Name] = true
		case strings.HasSuffix(op.Type, "_relationship"):
			lastRelationship = i
		case strings.HasSuffix(op.Type, "_permission"):
			if firstPermission < 0 {
				firstPermission = i
			}
			ref := args["table"].(TableRef)
			perms[op.Type+":"+ref.Schema+"."+ref.Name+":"+args["role"].(string)] = args["permission"].(map[string]any)
		}
		if op.Type == "pg_create_delete_permission" {
			t.Fatal("delete permission must never be generated")
		}
	}
	if lastRelationship > firstPermission {
		t.Fatal("relationships must be created before the permissions that traverse them")
	}
	for _, table := range []string{"domain.procurement_requests", "core.users", "core.files", "cap_hr.org_units", "cap_hr.staff"} {
		if !tracked[table] {
			t.Errorf("table %s is not tracked", table)
		}
	}

	sel := func(role string) any {
		p := perms["pg_create_select_permission:domain.procurement_requests:"+role]
		if p == nil {
			t.Fatalf("missing select permission for %s", role)
		}
		return p["filter"]
	}
	assertJSON(t, "requester filter", sel("role_procurement_requester"), `{"created_by":{"_eq":"X-Hasura-User-Id"}}`)
	assertJSON(t, "manager filter", sel("role_procurement_manager"), `{"org_unit":{"staff":{"user_id":{"_eq":"X-Hasura-User-Id"}}}}`)
	assertJSON(t, "finance filter", sel("role_procurement_finance"), `{"status":{"_neq":"DRAFT"}}`)

	ins := perms["pg_create_insert_permission:domain.procurement_requests:role_procurement_requester"]
	assertJSON(t, "insert presets", ins["set"], `{"status":"DRAFT"}`)
	assertJSON(t, "insert check", ins["check"], `{"status":{"_eq":"DRAFT"}}`)

	upd := perms["pg_create_update_permission:domain.procurement_requests:role_procurement_requester"]
	assertJSON(t, "update filter", upd["filter"], `{"_and":[{"created_by":{"_eq":"X-Hasura-User-Id"}},{"status":{"_in":["DRAFT"]}}]}`)

	files := perms["pg_create_select_permission:core.files:role_procurement_manager"]
	assertJSON(t, "files filter", files["filter"], `{"app_id":{"_eq":"procurement"}}`)
	users := perms["pg_create_select_permission:core.users:role_procurement_manager"]
	assertJSON(t, "users filter", users["filter"], `{}`)
}

func assertJSON(t *testing.T, what string, got any, want string) {
	t.Helper()
	b, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != want {
		t.Errorf("%s = %s, want %s", what, b, want)
	}
}

func TestDDLGrantsDeclaredDomainToRestrictedRuntimeRoles(t *testing.T) {
	app := loadFixtureApp(t)
	ddl, err := GenerateDDL(app)
	if err != nil {
		t.Fatal(err)
	}
	for _, grant := range []string{
		`GRANT SELECT, INSERT, UPDATE ON "domain"."procurement_requests" TO dxos_hasura, dxos_process;`,
		`GRANT SELECT ON "domain"."procurement_requests" TO dxos_reader;`,
		`GRANT SELECT ON "core"."users" TO dxos_hasura;`,
		`GRANT SELECT ON "cap_hr"."staff" TO dxos_hasura;`,
	} {
		if !strings.Contains(ddl, grant) {
			t.Errorf("missing restricted runtime grant: %s", grant)
		}
	}
}
