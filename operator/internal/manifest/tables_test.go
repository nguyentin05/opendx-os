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

package manifest_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/opendx-os/opendx-os/operator/internal/manifest"
	"github.com/opendx-os/opendx-os/operator/internal/testutil"
)

// An application declares several tables (ADR 0008): here a process table and
// a plain list it references.
func TestValidateAcceptsSeveralTables(t *testing.T) {
	app := testutil.WithLabels(testutil.LoadApp(t, "procurement"))
	if err := manifest.Validate(app); err != nil {
		t.Fatal(err)
	}
	labels := app.Table(testutil.LabelsTable(app))
	if labels == nil || labels.IsProcess() {
		t.Fatal("the added table must be a plain table")
	}
	if _, ok := manifest.SystemColumns(app, labels)["status"]; ok {
		t.Fatal("a plain table has no status column")
	}
	if manifest.SystemColumns(app, &app.Spec.Tables[0])["status"] != "text" {
		t.Fatal("a process table keeps its status column")
	}
}

func TestValidateRejectsInconsistentTables(t *testing.T) {
	second := func(a *manifest.DXApplication) *manifest.TableDef { return &a.Spec.Tables[1] }
	cases := []struct {
		name   string
		mutate func(*manifest.DXApplication)
		want   string
	}{
		{"no tables", func(a *manifest.DXApplication) { a.Spec.Tables = nil }, "at least one table"},
		{"duplicate table", func(a *manifest.DXApplication) { second(a).Name = a.Spec.Tables[0].Name }, "duplicate table"},
		{"table outside domain", func(a *manifest.DXApplication) { second(a).Name = "core.users" }, "invalid table name"},
		// A command is addressed by application and name alone.
		{"same command on two tables", func(a *manifest.DXApplication) {
			sm := *a.Spec.Tables[0].StateMachine
			second(a).StateMachine = &sm
			second(a).UI.Actions = []string{"submit"}
		}, "duplicate command"},
		{"plain table with actions", func(a *manifest.DXApplication) { second(a).UI.Actions = []string{"submit"} }, "ui.actions"},
		{"process table without actions", func(a *manifest.DXApplication) { a.Spec.Tables[0].UI.Actions = nil }, "ui.actions"},
		{"status as a business column", func(a *manifest.DXApplication) { second(a).Columns[0].Name = "status" }, "system column"},
		{"table without select rules", func(a *manifest.DXApplication) { second(a).Access.Select = nil }, "missing access.select"},
		{"foreign key to an undeclared table", func(a *manifest.DXApplication) {
			fks := a.Spec.Tables[0].ForeignKeys
			fks[len(fks)-1].References = "domain.other_app_records"
		}, "unsupported table"},
		// Only attachment columns are checked against the uploader.
		{"file column outside attachments", func(a *manifest.DXApplication) {
			second(a).Columns = append(second(a).Columns, manifest.ColumnDef{Name: "scan_id", Type: "uuid", Nullable: true})
			second(a).ForeignKeys = []manifest.ForeignKeyDef{{Column: "scan_id", References: "core.files"}}
		}, "must be declared in attachments"},
		{"attachment on a plain table", func(a *manifest.DXApplication) {
			second(a).Columns = append(second(a).Columns, manifest.ColumnDef{Name: "scan_id", Type: "uuid", Nullable: true})
			second(a).ForeignKeys = []manifest.ForeignKeyDef{{Column: "scan_id", References: "core.files"}}
			second(a).Attachments = []manifest.AttachmentDef{{Column: "scan_id", RequireCleanOnCommands: []string{"submit"}}}
		}, "unknown command"},
		{"effect on an unknown command", func(a *manifest.DXApplication) {
			a.Spec.Workflows[0].Effects[0].WhenCommands = []string{"archive"}
		}, "unknown command"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			app := testutil.WithLabels(testutil.LoadApp(t, "procurement"))
			tc.mutate(app)
			err := manifest.Validate(app)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want an error containing %q, got %v", tc.want, err)
			}
		})
	}
}

// The single-table fields are gone from manifests; only a spec stored before
// ADR 0008 is still read, as one process table.
func TestSingleTableShapeIsReadOnlyFromStoredSpecs(t *testing.T) {
	const legacy = `apiVersion: dxos.io/v1alpha1
kind: DXApplication
metadata: {name: legacy, displayName: Legacy, version: 0.1.0}
spec:
  capabilities: [cap-process]
  roles: [{name: role_legacy_user}]
  domainTable:
    name: domain.legacy_records
    columns: [{name: note, type: text, nullable: true}]
`
	if _, err := manifest.Parse(strings.NewReader(legacy)); err == nil || !strings.Contains(err.Error(), `"tables"`) {
		t.Fatalf("a manifest in the single-table shape must be refused, got %v", err)
	}

	app := testutil.LoadApp(t, "process-fixture")
	table := app.Spec.Tables[0]
	stored, err := json.Marshal(map[string]any{
		"capabilities": app.Spec.Capabilities,
		"roles":        app.Spec.Roles,
		"sharedAccess": app.Spec.SharedAccess,
		"workflows":    app.Spec.Workflows,
		"domainTable":  map[string]any{"name": table.Name, "columns": table.Columns, "foreignKeys": table.ForeignKeys},
		"domainAccess": table.Access,
		"stateMachine": table.StateMachine,
		"attachments":  table.Attachments,
		"uiSchema":     table.UI,
	})
	if err != nil {
		t.Fatal(err)
	}
	spec, err := manifest.DecodeStoredSpec(stored)
	if err != nil {
		t.Fatal(err)
	}
	read := &manifest.DXApplication{APIVersion: app.APIVersion, Kind: app.Kind, Metadata: app.Metadata, Spec: spec}
	if err := manifest.Validate(read); err != nil {
		t.Fatalf("a stored single-table spec must read as a valid application: %v", err)
	}
	want, _ := json.Marshal(app.Spec)
	got, _ := json.Marshal(read.Spec)
	if string(got) != string(want) {
		t.Fatal("a stored single-table spec must read as the same application with one process table")
	}

	current, _ := json.Marshal(app.Spec)
	spec, err = manifest.DecodeStoredSpec(current)
	if err != nil || len(spec.Tables) != 1 || spec.Tables[0].Name != table.Name {
		t.Fatalf("a spec stored in the tables shape must be read unchanged: %v", err)
	}
}

// A child table's rows follow their parent record, so its rules name roles and
// columns only, and each role must be able to reach the parent.
func TestValidateChildTables(t *testing.T) {
	app := testutil.LoadApp(t, "procurement")
	items := app.Table("domain.procurement_items")
	if items == nil || !items.IsChild() || items.IsProcess() {
		t.Fatal("the sample application must declare a child table")
	}
	system := manifest.SystemColumns(app, items)
	if system["request_id"] != "uuid" {
		t.Fatal("the parent column is managed by the operator")
	}
	if _, ok := system["org_unit_id"]; ok {
		t.Fatal("a child row has no department of its own")
	}

	child := func(a *manifest.DXApplication) *manifest.TableDef { return a.Table("domain.procurement_items") }
	cases := []struct {
		name   string
		mutate func(*manifest.DXApplication)
		want   string
	}{
		{"unknown parent", func(a *manifest.DXApplication) { child(a).BelongsTo.Table = "domain.other_records" }, "belongsTo names"},
		{"parent of itself", func(a *manifest.DXApplication) { child(a).BelongsTo.Table = child(a).Name }, "belongsTo names"},
		{"child of a child", func(a *manifest.DXApplication) {
			grandchild := *child(a)
			grandchild.Name = "domain.procurement_notes"
			grandchild.BelongsTo = &manifest.BelongsToDef{Table: child(a).Name, Column: "item_id"}
			a.Spec.Tables = append(a.Spec.Tables, grandchild)
		}, "belongsTo names"},
		{"parent column named like a system column", func(a *manifest.DXApplication) { child(a).BelongsTo.Column = "created_by" }, "invalid belongsTo column"},
		{"parent column declared as a business column", func(a *manifest.DXApplication) {
			child(a).Columns = append(child(a).Columns, manifest.ColumnDef{Name: "request_id", Type: "uuid"})
		}, "system column"},
		{"parent column writable", func(a *manifest.DXApplication) {
			child(a).Access.Update[0].Columns = append(child(a).Access.Update[0].Columns, "request_id")
		}, "cannot be writable"},
		{"child with a state machine", func(a *manifest.DXApplication) {
			child(a).StateMachine = &manifest.StateMachineDef{Initial: "OPEN"}
		}, "no stateMachine"},
		{"scope on a child rule", func(a *manifest.DXApplication) { child(a).Access.Select[0].Scope = manifest.ScopeOrganization }, "cannot declare a scope"},
		{"record filter on a child rule", func(a *manifest.DXApplication) {
			child(a).Access.Select[0].RecordFilter = map[string]any{"item": map[string]any{"_eq": "x"}}
		}, "cannot declare a scope"},
		// Finance reads requests but cannot edit them, so it cannot edit their lines.
		{"insert by a role that cannot edit the parent", func(a *manifest.DXApplication) {
			child(a).Access.Insert[0].Role = "role_procurement_finance"
		}, "needs an access.update rule"},
		{"delete by a role that cannot edit the parent", func(a *manifest.DXApplication) {
			child(a).Access.Delete[0].Role = "role_procurement_finance"
		}, "needs an access.update rule"},
		{"select by a role that cannot read the parent", func(a *manifest.DXApplication) {
			requests := a.Table("domain.procurement_requests")
			requests.Access.Select = requests.Access.Select[:2]
		}, "needs an access.select rule"},
		{"delete on a table that is not a child", func(a *manifest.DXApplication) {
			vendors := a.Table("domain.procurement_vendors")
			vendors.Access.Delete = []manifest.DeleteRule{{Role: "role_procurement_finance"}}
		}, "child tables only"},
		{"duplicate delete rule", func(a *manifest.DXApplication) {
			child(a).Access.Delete = append(child(a).Access.Delete, child(a).Access.Delete[0])
		}, "duplicate access.delete"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			app := testutil.LoadApp(t, "procurement")
			tc.mutate(app)
			err := manifest.Validate(app)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want an error containing %q, got %v", tc.want, err)
			}
		})
	}
}
