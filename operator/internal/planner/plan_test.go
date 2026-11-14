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
	"strings"
	"testing"

	"github.com/opendx-os/opendx-os/operator/internal/manifest"
)

func TestBuildPlanDropsGrantsOfRemovedApp(t *testing.T) {
	procurement := *loadApp(t, "procurement")
	review := *loadApp(t, "process-fixture")

	live := ManagedState{ActiveApps: []manifest.DXApplication{procurement, review}}
	plan, err := BuildPlan([]manifest.DXApplication{procurement}, live)
	if err != nil {
		t.Fatalf("BuildPlan failed: %v", err)
	}
	if len(plan.RemovedApps) != 1 || plan.RemovedApps[0] != "process_fixture" {
		t.Fatalf("expected process_fixture in RemovedApps, got %+v", plan.RemovedApps)
	}

	dropped := map[string]bool{}
	for _, op := range plan.DropOps {
		args := op.Args.(map[string]any)
		tbl := args["table"].(TableRef)
		dropped[op.Type+":"+tbl.Schema+"."+tbl.Name+":"+args["role"].(string)] = true
	}
	for _, want := range []string{
		"pg_drop_select_permission:domain.review_requests:role_review",
		"pg_drop_insert_permission:domain.review_requests:role_review",
		"pg_drop_select_permission:core.users:role_review_manager",
	} {
		if !dropped[want] {
			t.Errorf("expected drop %s, got %v", want, dropped)
		}
	}
	for key := range dropped {
		if strings.Contains(key, "role_procurement") {
			t.Errorf("grant of the remaining application dropped: %s", key)
		}
	}
}

func TestBuildPlanRejectsRoleOfAnotherApp(t *testing.T) {
	procurement := *loadApp(t, "procurement")
	review := *loadApp(t, "process-fixture")
	review.Spec.Roles = append(review.Spec.Roles, manifest.RoleDef{Name: "role_procurement_manager"})
	_, err := BuildPlan([]manifest.DXApplication{procurement, review}, ManagedState{})
	if err == nil || !strings.Contains(err.Error(), `role "role_procurement_manager" is declared by both`) {
		t.Fatalf("expected a role ownership conflict, got %v", err)
	}
}

func TestChangedReportsNewAndModifiedApps(t *testing.T) {
	procurement := *loadApp(t, "procurement")
	review := *loadApp(t, "process-fixture")

	// The installed copy comes back from core.apps as JSON.
	raw, err := json.Marshal(procurement.Spec)
	if err != nil {
		t.Fatal(err)
	}
	installed := procurement
	installed.Spec = manifest.AppSpec{}
	if err := json.Unmarshal(raw, &installed.Spec); err != nil {
		t.Fatal(err)
	}
	live := ManagedState{ActiveApps: []manifest.DXApplication{installed}}

	if got := Changed([]manifest.DXApplication{procurement, review}, live); len(got) != 1 || got[0] != "process_fixture" {
		t.Fatalf("Changed = %v, want [process_fixture]", got)
	}
	upgraded := procurement
	upgraded.Metadata.Version = "0.3.0"
	if got := Changed([]manifest.DXApplication{upgraded}, live); len(got) != 1 || got[0] != "procurement" {
		t.Fatalf("Changed = %v, want [procurement]", got)
	}
}

func TestBuildApplyPlanPreservesOtherActivePackages(t *testing.T) {
	first := loadApp(t, "procurement")
	second := loadApp(t, "process-fixture")
	live := ManagedState{ActiveApps: []manifest.DXApplication{*first}}
	plan, err := BuildApplyPlan(*second, live)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.RemovedApps) != 0 || len(plan.Apps) != 2 {
		t.Fatalf("single app apply discarded active package: %+v", plan.RemovedApps)
	}
}

func TestBuildRemovePlanRemovesOnlyThatApp(t *testing.T) {
	first := loadApp(t, "procurement")
	second := loadApp(t, "process-fixture")
	live := ManagedState{ActiveApps: []manifest.DXApplication{*first, *second}}
	if !live.Has("process_fixture") || live.Has("timesheet") {
		t.Fatal("Has must report exactly the installed applications")
	}
	plan, err := BuildRemovePlan("process_fixture", live)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Apps) != 1 || plan.Apps[0].Metadata.Name != "procurement" {
		t.Fatalf("plan keeps %d apps, want only procurement", len(plan.Apps))
	}
	if len(plan.RemovedApps) != 1 || plan.RemovedApps[0] != "process_fixture" {
		t.Fatalf("removed = %v, want [process_fixture]", plan.RemovedApps)
	}
}
