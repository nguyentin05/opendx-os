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
	"github.com/opendx-os/opendx-os/operator/internal/testutil"
)

func TestGenerateDDLForSeveralTables(t *testing.T) {
	app := testutil.WithLabels(loadFixtureApp(t))
	ddl, err := GenerateDDL(app)
	if err != nil {
		t.Fatal(err)
	}
	labels := `"domain"."procurement_labels"`
	create := strings.Index(ddl, `CREATE TABLE IF NOT EXISTS `+labels)
	firstFK := strings.Index(ddl, "FOREIGN KEY")
	if create < 0 || firstFK < create {
		t.Fatal("every table must be created before the first foreign key, so tables can reference each other")
	}
	body := ddl[create:]
	body = body[:strings.Index(body, ");")]
	if strings.Contains(body, `"status"`) {
		t.Errorf("a plain table must not have a status column:\n%s", body)
	}
	for _, want := range []string{`"created_by" uuid NOT NULL`, `"org_unit_id" uuid NOT NULL`, `"version" bigint NOT NULL DEFAULT 1`, `"title" text NOT NULL CHECK (length(trim(title)) > 0)`} {
		if !strings.Contains(body, want) {
			t.Errorf("plain table is missing %s:\n%s", want, body)
		}
	}
	for _, want := range []string{
		`FOREIGN KEY ("label_id") REFERENCES ` + labels + `("id") ON DELETE RESTRICT`,
		`CREATE INDEX IF NOT EXISTS "idx_procurement_requests_label_id"`,
		`"trg_procurement_labels_10_stamp_owner"`,
		`"trg_procurement_labels_20_stamp_org_unit"`,
		`"trg_procurement_labels_30_bump_version"`,
		"AFTER INSERT OR UPDATE ON " + labels + "\nFOR EACH ROW EXECUTE FUNCTION core.record_domain_audit('procurement');",
		`GRANT SELECT, INSERT, UPDATE ON ` + labels + ` TO dxos_hasura, dxos_process;`,
	} {
		if !strings.Contains(ddl, want) {
			t.Errorf("DDL missing %q", want)
		}
	}
	for _, unwanted := range []string{"ck_procurement_labels_status", "idx_procurement_labels_status", "trg_procurement_labels_25_attachments"} {
		if strings.Contains(ddl, unwanted) {
			t.Errorf("DDL of a plain table mentions %q", unwanted)
		}
	}
	// A table of the application is not a shared table Hasura must be granted.
	for _, shared := range SharedTablesRead(app) {
		if strings.HasPrefix(shared, "domain.") {
			t.Errorf("%s listed as a shared table", shared)
		}
	}
}

func TestGenerateHasuraOperationsForSeveralTables(t *testing.T) {
	app := testutil.WithLabels(loadFixtureApp(t))
	ops, err := GenerateHasuraOperations(app)
	if err != nil {
		t.Fatal(err)
	}
	requests := TableRef{Schema: "domain", Name: "procurement_requests"}
	labels := TableRef{Schema: "domain", Name: "procurement_labels"}
	tracked := map[TableRef]bool{}
	relationships := map[string]TableRef{}
	perms := map[string]map[string]any{}
	for _, op := range ops {
		args := op.Args.(map[string]any)
		table := args["table"].(TableRef)
		switch {
		case op.Type == "pg_track_table":
			tracked[table] = true
		case strings.HasSuffix(op.Type, "_relationship"):
			using := args["using"].(map[string]any)["manual_configuration"].(map[string]any)
			relationships[table.Name+"."+args["name"].(string)] = using["remote_table"].(TableRef)
		case strings.HasSuffix(op.Type, "_permission"):
			perms[op.Type+":"+table.Name+":"+args["role"].(string)] = args["permission"].(map[string]any)
		}
	}
	if !tracked[requests] || !tracked[labels] {
		t.Fatal("every table of the application must be tracked")
	}
	if relationships["procurement_requests.label"] != labels || relationships["procurement_labels.procurement_requests_by_label_id"] != requests {
		t.Fatalf("relationships along the in-application foreign key are missing: %v", relationships)
	}

	// A plain table has no state: nothing is preset and updates are not guarded.
	ins := perms["pg_create_insert_permission:procurement_labels:role_procurement_requester"]
	assertJSON(t, "plain insert presets", ins["set"], `{}`)
	assertJSON(t, "plain insert check", ins["check"], `{}`)
	upd := perms["pg_create_update_permission:procurement_labels:role_procurement_requester"]
	assertJSON(t, "plain update filter", upd["filter"], `{"created_by":{"_eq":"X-Hasura-User-Id"}}`)
	assertJSON(t, "plain update check", upd["check"], `{}`)
	sel := perms["pg_create_select_permission:procurement_labels:role_procurement_manager"]
	assertJSON(t, "plain select filter", sel["filter"], `{}`)

	// The process table keeps its guards.
	ins = perms["pg_create_insert_permission:procurement_requests:role_procurement_requester"]
	assertJSON(t, "process insert presets", ins["set"], `{"status":"DRAFT"}`)
	upd = perms["pg_create_update_permission:procurement_requests:role_procurement_requester"]
	assertJSON(t, "process update check", upd["check"], `{"status":{"_in":["DRAFT"]}}`)
}

func TestBuildPlanDropsPermissionsOfARemovedTable(t *testing.T) {
	installed := *testutil.WithLabels(loadFixtureApp(t))
	desired := *loadFixtureApp(t)
	plan, err := BuildPlan([]manifest.DXApplication{desired}, ManagedState{ActiveApps: []manifest.DXApplication{installed}})
	if err != nil {
		t.Fatal(err)
	}
	dropped := map[string]bool{}
	for _, op := range plan.DropOps {
		args := op.Args.(map[string]any)
		dropped[op.Type+":"+args["table"].(TableRef).Name+":"+args["role"].(string)] = true
	}
	for _, want := range []string{
		"pg_drop_select_permission:procurement_labels:role_procurement_requester",
		"pg_drop_select_permission:procurement_labels:role_procurement_finance",
		"pg_drop_insert_permission:procurement_labels:role_procurement_requester",
		"pg_drop_update_permission:procurement_labels:role_procurement_requester",
	} {
		if !dropped[want] {
			t.Errorf("missing %s; dropped %v", want, dropped)
		}
	}
	for key := range dropped {
		if strings.Contains(key, ":procurement_requests:") {
			t.Errorf("a table that is still declared lost %s", key)
		}
	}
	if len(plan.RemovedApps) != 0 {
		t.Fatalf("the application is still installed, got removed apps %v", plan.RemovedApps)
	}
	if strings.Contains(plan.DDLs["procurement"], "DROP TABLE") {
		t.Fatal("a table removed from the manifest keeps its data")
	}
}

func TestBuildPlanRejectsATableOfAnotherApp(t *testing.T) {
	procurement := *loadApp(t, "procurement")
	review := *loadApp(t, "process-fixture")
	review.Spec.Tables[0].Name = procurement.Spec.Tables[0].Name
	_, err := BuildPlan([]manifest.DXApplication{procurement, review}, ManagedState{})
	if err == nil || !strings.Contains(err.Error(), "is declared by both") {
		t.Fatalf("two applications declaring one table must be refused, got %v", err)
	}
}

func TestChildTableDDLAndPermissions(t *testing.T) {
	app := loadFixtureApp(t)
	ddl, err := GenerateDDL(app)
	if err != nil {
		t.Fatal(err)
	}
	items := `"domain"."procurement_items"`
	create := ddl[strings.Index(ddl, "CREATE TABLE IF NOT EXISTS "+items):]
	create = create[:strings.Index(create, ");")]
	if !strings.Contains(create, `"request_id" uuid NOT NULL`) || strings.Contains(create, "org_unit_id") || strings.Contains(create, `"status"`) {
		t.Errorf("a child table holds its parent's id and has no department or status:\n%s", create)
	}
	for _, want := range []string{
		`FOREIGN KEY ("request_id") REFERENCES "domain"."procurement_requests"("id") ON DELETE RESTRICT`,
		`CREATE INDEX IF NOT EXISTS "idx_procurement_items_request_id"`,
		"BEFORE INSERT OR UPDATE OR DELETE ON " + items + "\nFOR EACH ROW EXECUTE FUNCTION core.enforce_parent('domain.procurement_requests', 'request_id', 'DRAFT');",
		"AFTER INSERT OR UPDATE OR DELETE ON " + items + "\nFOR EACH ROW EXECUTE FUNCTION core.touch_parent('domain.procurement_requests', 'request_id');",
		"AFTER INSERT OR UPDATE OR DELETE ON " + items + "\nFOR EACH ROW EXECUTE FUNCTION core.record_domain_audit('procurement');",
		`GRANT SELECT, INSERT, UPDATE, DELETE ON ` + items + ` TO dxos_hasura;`,
		`GRANT SELECT ON ` + items + ` TO dxos_process, dxos_reader;`,
	} {
		if !strings.Contains(ddl, want) {
			t.Errorf("DDL missing %q", want)
		}
	}
	// The parent is checked before the row changes and touched after it.
	if strings.Index(ddl, `"trg_procurement_items_15_parent"`) > strings.Index(ddl, `"trg_procurement_items_80_touch_parent"`) {
		t.Error("triggers must be created in firing order")
	}
	if strings.Contains(ddl, "DELETE ON "+`"domain"."procurement_requests"`) || strings.Contains(ddl, `DELETE ON "domain"."procurement_vendors"`) {
		t.Error("only child rows can be deleted")
	}

	ops, err := GenerateHasuraOperations(app)
	if err != nil {
		t.Fatal(err)
	}
	perms := map[string]map[string]any{}
	deletes := 0
	for _, op := range ops {
		args := op.Args.(map[string]any)
		if !strings.HasSuffix(op.Type, "_permission") {
			continue
		}
		if op.Type == "pg_create_delete_permission" {
			deletes++
		}
		perms[op.Type+":"+args["table"].(TableRef).Name+":"+args["role"].(string)] = args["permission"].(map[string]any)
	}
	const own = `{"created_by":{"_eq":"X-Hasura-User-Id"}}`
	const editable = `{"request":{"_and":[` + own + `,{"status":{"_in":["DRAFT"]}}]}}`
	key := func(op, role string) string { return "pg_create_" + op + "_permission:procurement_items:" + role }

	// Reading follows the role's select rule on the request.
	assertJSON(t, "requester reads", perms[key("select", "role_procurement_requester")]["filter"], `{"request":`+own+`}`)
	assertJSON(t, "manager reads", perms[key("select", "role_procurement_manager")]["filter"],
		`{"request":{"_and":[{"org_unit":{"staff":{"user_id":{"_eq":"X-Hasura-User-Id"}}}},{"status":{"_neq":"DRAFT"}}]}}`)
	assertJSON(t, "finance reads", perms[key("select", "role_procurement_finance")]["filter"], `{"request":{"status":{"_neq":"DRAFT"}}}`)

	// Writing follows the role's update rule on the request and its editable states.
	ins := perms[key("insert", "role_procurement_requester")]
	assertJSON(t, "insert columns", ins["columns"], `["request_id","item","quantity","unit_price"]`)
	assertJSON(t, "insert check", ins["check"], editable)
	upd := perms[key("update", "role_procurement_requester")]
	assertJSON(t, "update columns", upd["columns"], `["item","quantity","unit_price"]`)
	assertJSON(t, "update filter", upd["filter"], editable)
	assertJSON(t, "update check", upd["check"], editable)
	assertJSON(t, "delete filter", perms[key("delete", "role_procurement_requester")]["filter"], editable)
	if deletes != 1 {
		t.Fatalf("delete permissions = %d, want only the requester's on the child table", deletes)
	}
	for _, role := range []string{"role_procurement_manager", "role_procurement_finance"} {
		for _, op := range []string{"insert", "update", "delete"} {
			if perms[key(op, role)] != nil {
				t.Errorf("%s must not %s request lines", role, op)
			}
		}
	}
}
