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
	"testing"
)

func fixture(t *testing.T) []byte {
	t.Helper()
	b, e := os.ReadFile("../../../contracts/fixtures/registry-ready.json")
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func TestDecodeActiveFixture(t *testing.T) {
	a, e := Decode(fixture(t))
	if e != nil {
		t.Fatal(e)
	}
	if a.ManifestSpec.DomainTable.Name != "domain.procurement_requests" || len(a.ManifestSpec.Workflows) != 1 || len(a.ManifestSpec.Attachments) != 1 || a.ManifestSpec.StateMachine.Initial != "DRAFT" {
		t.Fatalf("incomplete typed fixture: %+v", a)
	}
}
func TestRejectUnsafeRegistry(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"unknown", func(m map[string]any) { m["manifest_spec"].(map[string]any)["semanticType"] = "x" }},
		{"identifier", func(m map[string]any) { m["domain_table"] = "domain.x;DROP TABLE core.employees" }},
		{"nested_unknown", func(m map[string]any) {
			m["manifest_spec"].(map[string]any)["domainTable"].(map[string]any)["columns"].([]any)[0].(map[string]any)["semanticType"] = "x"
		}},
		{"filter_operator", func(m map[string]any) {
			m["manifest_spec"].(map[string]any)["domainAccess"].(map[string]any)["select"].([]any)[0].(map[string]any)["recordFilter"] = map[string]any{"workspace_id": map[string]any{"_sql": "true"}}
		}},
		{"filter_type", func(m map[string]any) {
			m["manifest_spec"].(map[string]any)["domainAccess"].(map[string]any)["select"].([]any)[0].(map[string]any)["recordFilter"] = map[string]any{"quoted_amount": map[string]any{"_eq": "oops"}}
		}},
		{"non_ready", func(m map[string]any) { m["status"] = "Applying" }},
		{"incoherent", func(m map[string]any) { m["state_machine"].(map[string]any)["initial"] = "APPROVED" }},
		{"missing", func(m map[string]any) { delete(m["manifest_spec"].(map[string]any), "domainAccess") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var m map[string]any
			json.Unmarshal(fixture(t), &m)
			tc.mutate(m)
			b, _ := json.Marshal(m)
			if _, e := Decode(b); e == nil {
				t.Fatal("unsafe configuration accepted")
			}
		})
	}
}
func TestStrictJSONShape(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"case", func(m map[string]any) {
			s := m["manifest_spec"].(map[string]any)
			s["Role"] = s["role"]
			delete(s, "role")
		}},
		{"missing_bool", func(m map[string]any) {
			delete(m["manifest_spec"].(map[string]any)["domainTable"].(map[string]any)["columns"].([]any)[0].(map[string]any), "nullable")
		}},
		{"null_bool", func(m map[string]any) {
			m["manifest_spec"].(map[string]any)["domainTable"].(map[string]any)["columns"].([]any)[0].(map[string]any)["nullable"] = nil
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var m map[string]any
			json.Unmarshal(fixture(t), &m)
			tc.mutate(m)
			b, _ := json.Marshal(m)
			if _, e := Decode(b); e == nil {
				t.Fatal("malformed JSON shape accepted")
			}
		})
	}
}

func TestRequesterInsertPresetRequiresSignedUser(t *testing.T) {
	for _, value := range []string{"", "X-Hasura-Workspace-Id"} {
		t.Run(value, func(t *testing.T) {
			var m map[string]any
			json.Unmarshal(fixture(t), &m)
			p := m["manifest_spec"].(map[string]any)["domainAccess"].(map[string]any)["insert"].([]any)[0].(map[string]any)["presets"].(map[string]any)
			if value == "" {
				delete(p, "requester_id")
			} else {
				p["requester_id"] = value
			}
			b, _ := json.Marshal(m)
			if _, err := Decode(b); err == nil {
				t.Fatal("requester ownership not bound to signed user")
			}
		})
	}
}

func TestWorkflowStepsUniqueAcrossApp(t *testing.T) {
	a, err := Decode(fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	second := a.ManifestSpec.Workflows[0]
	second.ID = "second_workflow"
	second.File = "workflows/second_workflow.yaml"
	second.Effects = append([]Effect(nil), second.Effects...)
	second.Effects[0].StepName = "second_step"
	a.ManifestSpec.Workflows = append(a.ManifestSpec.Workflows, second)
	if err := a.Validate(); err != nil {
		t.Fatalf("valid two workflows: %v", err)
	}
	a.ManifestSpec.Workflows[1].Effects[0].StepName = a.ManifestSpec.Workflows[0].Effects[0].StepName
	if err := a.Validate(); err == nil {
		t.Fatal("ambiguous app-wide step accepted")
	}
}
