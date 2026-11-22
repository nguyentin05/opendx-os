// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package planner

import (
	"reflect"
	"strings"
	"testing"
)

func TestWorkItemFilterFollowsSourceRecordScope(t *testing.T) {
	app := loadFixtureApp(t)
	prefix := `{"_and":[{"app_id":{"_eq":"procurement"}},{"_or":[{"source_type":{"_is_null":true}},`

	assertJSON(t, "department-scoped work items", sharedFilter(app, "cap_work.work_items", "role_procurement_manager"),
		prefix+`{"source_procurement_requests":{"_and":[{"org_unit":{"staff":{"user_id":{"_eq":"X-Hasura-User-Id"}}}},{"status":{"_neq":"DRAFT"}}]},"source_type":{"_eq":"domain.procurement_requests"}},{"source_procurement_vendors":{},"source_type":{"_eq":"domain.procurement_vendors"}}]}]}`)
	assertJSON(t, "organization work items with a record filter", sharedFilter(app, "cap_work.work_items", "role_procurement_finance"),
		prefix+`{"source_procurement_requests":{"status":{"_neq":"DRAFT"}},"source_type":{"_eq":"domain.procurement_requests"}},{"source_procurement_vendors":{},"source_type":{"_eq":"domain.procurement_vendors"}}]}]}`)
	assertJSON(t, "role without a table rule", sharedFilter(app, "cap_work.work_items", "role_no_table_rule"),
		`{"_and":[{"app_id":{"_eq":"procurement"}},{"_or":[{"source_type":{"_is_null":true}}]}]}`)
}

func TestGeneratedWorkItemPermissionsExcludeUnsharedRoles(t *testing.T) {
	app := loadFixtureApp(t)
	ops, err := GenerateHasuraOperations(app)
	if err != nil {
		t.Fatal(err)
	}
	manager := false
	for _, op := range ops {
		args := op.Args.(map[string]any)
		if op.Type != "pg_create_select_permission" || args["table"] != (TableRef{Schema: "cap_work", Name: "work_items"}) {
			continue
		}
		switch args["role"] {
		case "role_procurement_manager":
			manager = true
		case "role_procurement_finance":
			t.Fatal("finance must not read procurement work items")
		}
	}
	if !manager {
		t.Fatal("the manager's work item select permission is missing")
	}
}

func TestGeneratedWorkItemPermissionAndRelationships(t *testing.T) {
	app := loadFixtureApp(t)
	ops, err := GenerateHasuraOperations(app)
	if err != nil {
		t.Fatal(err)
	}

	var workItemsFilter any
	relationships := map[string]map[string]any{}
	for _, op := range ops {
		args := op.Args.(map[string]any)
		table := args["table"].(TableRef)
		if op.Type == "pg_create_select_permission" && table == (TableRef{Schema: "cap_work", Name: "work_items"}) &&
			args["role"] == "role_procurement_manager" {
			workItemsFilter = args["permission"].(map[string]any)["filter"]
		}
		if strings.HasSuffix(op.Type, "_relationship") && table == (TableRef{Schema: "cap_work", Name: "work_items"}) {
			relationships[args["name"].(string)] = args["using"].(map[string]any)["manual_configuration"].(map[string]any)
		}
	}
	assertJSON(t, "generated work item permission", workItemsFilter,
		`{"_and":[{"app_id":{"_eq":"procurement"}},{"_or":[{"source_type":{"_is_null":true}},{"source_procurement_requests":{"_and":[{"org_unit":{"staff":{"user_id":{"_eq":"X-Hasura-User-Id"}}}},{"status":{"_neq":"DRAFT"}}]},"source_type":{"_eq":"domain.procurement_requests"}},{"source_procurement_vendors":{},"source_type":{"_eq":"domain.procurement_vendors"}}]}]}`)

	link, ok := relationships["source_procurement_requests"]
	if !ok {
		t.Fatalf("work item source relationship is missing: %v", relationships)
	}
	if link["remote_table"] != (TableRef{Schema: "domain", Name: "procurement_requests"}) {
		t.Errorf("remote table = %v", link["remote_table"])
	}
	if !reflect.DeepEqual(link["column_mapping"], map[string]string{"source_id": "id"}) {
		t.Errorf("column mapping = %v", link["column_mapping"])
	}
	if _, ok := relationships["source_procurement_items"]; ok {
		t.Error("a child table must not be a work item source: its rows follow the parent's scope")
	}

	other := loadApp(t, "process-fixture")
	otherOps, err := GenerateHasuraOperations(other)
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range otherOps {
		args := op.Args.(map[string]any)
		if strings.HasSuffix(op.Type, "_relationship") && args["table"] == (TableRef{Schema: "cap_work", Name: "work_items"}) {
			t.Fatalf("an app that does not share work items gets a relationship: %v", op)
		}
	}
}
