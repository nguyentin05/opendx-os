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

package registry

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// readyRow is contracts/fixtures/app-ready.json: the core.apps row the
// operator writes for the procurement sample.
func readyRow(t *testing.T) map[string]any {
	t.Helper()
	b, err := os.ReadFile("../../../contracts/fixtures/app-ready.json")
	if err != nil {
		t.Fatal(err)
	}
	var row map[string]any
	if err := json.Unmarshal(b, &row); err != nil {
		t.Fatal(err)
	}
	return row
}

func decodeRow(row map[string]any) (*AppDefinition, error) {
	spec, _ := json.Marshal(row["spec"])
	return Decode(row["app_id"].(string), row["display_name"].(string), row["version"].(string), spec)
}

func TestDecodeReadyApplication(t *testing.T) {
	a, err := decodeRow(readyRow(t))
	if err != nil {
		t.Fatal(err)
	}
	if a.Table() != "domain.procurement_requests" || len(a.Spec.Workflows) != 1 || len(a.Spec.Attachments) != 1 || a.Spec.StateMachine.Initial != "DRAFT" {
		t.Fatalf("incomplete definition: %+v", a)
	}
	if a.ColumnTypes()["org_unit_id"] != "uuid" || !a.UsesDepartmentScope("role_procurement_manager") || a.UsesDepartmentScope("role_procurement_finance") {
		t.Fatal("cap-hr columns and scopes not recognized")
	}
}

func TestRejectUnsafeDefinitions(t *testing.T) {
	spec := func(m map[string]any) map[string]any { return m["spec"].(map[string]any) }
	firstSelect := func(m map[string]any) map[string]any {
		return spec(m)["domainAccess"].(map[string]any)["select"].([]any)[0].(map[string]any)
	}
	for name, mutate := range map[string]func(map[string]any){
		"table injection": func(m map[string]any) {
			spec(m)["domainTable"].(map[string]any)["name"] = "domain.x;DROP TABLE core.users"
		},
		"non-domain table": func(m map[string]any) { spec(m)["domainTable"].(map[string]any)["name"] = "core.users" },
		"column injection": func(m map[string]any) {
			spec(m)["domainTable"].(map[string]any)["columns"].([]any)[0].(map[string]any)["name"] = "x = x OR true"
		},
		"reserved column": func(m map[string]any) {
			spec(m)["domainTable"].(map[string]any)["columns"].([]any)[0].(map[string]any)["name"] = "created_by"
		},
		"filter operator": func(m map[string]any) {
			firstSelect(m)["recordFilter"] = map[string]any{"status": map[string]any{"_sql": "true"}}
		},
		"filter type": func(m map[string]any) {
			firstSelect(m)["recordFilter"] = map[string]any{"quoted_amount": map[string]any{"_eq": "oops"}}
		},
		"session variable": func(m map[string]any) {
			firstSelect(m)["recordFilter"] = map[string]any{"created_by": map[string]any{"_eq": "X-Hasura-Role"}}
		},
		"undeclared role":     func(m map[string]any) { firstSelect(m)["role"] = "role_admin" },
		"unknown scope":       func(m map[string]any) { firstSelect(m)["scope"] = "everyone" },
		"department, no hr":   func(m map[string]any) { spec(m)["capabilities"] = []any{"cap-process", "cap-work"} },
		"attachment not file": func(m map[string]any) { spec(m)["domainTable"].(map[string]any)["foreignKeys"] = []any{} },
		"bad effect": func(m map[string]any) {
			spec(m)["workflows"].([]any)[0].(map[string]any)["effects"].([]any)[0].(map[string]any)["kind"] = "run_sql"
		},
	} {
		row := readyRow(t)
		mutate(row)
		if _, err := decodeRow(row); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestCanSeeAppliesScopeAndRecordFilter(t *testing.T) {
	a, err := decodeRow(readyRow(t))
	if err != nil {
		t.Fatal(err)
	}
	const alice, carol, deptA, deptB = "22222222-2222-4222-8222-222222222222", "44444444-4444-4444-8444-444444444444",
		"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	row := func(status string) map[string]any {
		return map[string]any{"id": "33333333-3333-4333-8333-333333333333", "created_by": alice, "org_unit_id": deptA, "status": status}
	}
	for _, tc := range []struct {
		name string
		v    Viewer
		row  map[string]any
		want bool
	}{
		{"requester, own", Viewer{UserID: alice, Role: "role_procurement_requester"}, row("DRAFT"), true},
		{"requester, other's", Viewer{UserID: carol, Role: "role_procurement_requester"}, row("SUBMITTED"), false},
		{"manager, same department", Viewer{UserID: carol, Role: "role_procurement_manager", OrgUnit: deptA}, row("SUBMITTED"), true},
		{"manager, other department", Viewer{UserID: carol, Role: "role_procurement_manager", OrgUnit: deptB}, row("SUBMITTED"), false},
		{"manager, no department", Viewer{UserID: carol, Role: "role_procurement_manager"}, row("SUBMITTED"), false},
		{"manager, draft", Viewer{UserID: carol, Role: "role_procurement_manager", OrgUnit: deptA}, row("DRAFT"), false},
		{"finance, organization", Viewer{UserID: carol, Role: "role_procurement_finance"}, row("BUDGET_REVIEW"), true},
		{"finance, draft", Viewer{UserID: carol, Role: "role_procurement_finance"}, row("DRAFT"), false},
		{"no rule for role", Viewer{UserID: alice, Role: "role_review"}, row("DRAFT"), false},
	} {
		got, err := a.CanSee(tc.v, tc.row, "")
		if err != nil || got != tc.want {
			t.Errorf("%s: got %v (%v), want %v", tc.name, got, err, tc.want)
		}
	}
	// The attachment column must be among the rule's visible columns.
	if ok, _ := a.CanSee(Viewer{UserID: alice, Role: "role_procurement_requester"}, row("DRAFT"), "not_a_column"); ok {
		t.Error("a column the rule does not expose must not be readable")
	}
	if !strings.Contains(a.Table(), "procurement") {
		t.Fatal(a.Table())
	}
}
