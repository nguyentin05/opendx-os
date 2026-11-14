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
