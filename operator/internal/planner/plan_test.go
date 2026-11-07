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
	"testing"

	"github.com/opendx-os/opendx-os/operator/internal/manifest"
)

func TestBuildPlanPreservesSharedGrantsAndDropsExcess(t *testing.T) {
	app1 := *loadFixtureApp(t)
	app2 := *loadFixtureApp(t)
	app2.Metadata.Name = "review_fixture"
	app2.Spec.DomainTable.Name = "domain.review_requests"
	app2.Spec.Role = "role_review"
	// Suppose app2 shares role_procurement on core.employees
	app2.Spec.SharedAccess[0].Roles = []string{"role_review", "role_procurement"}
	app2.Spec.DomainAccess.Select[0].Role = "role_review"
	app2.Spec.DomainAccess.Insert[0].Role = "role_review"
	app2.Spec.DomainAccess.Update[0].Role = "role_review"
	app2.Spec.StateMachine.Commands[0].AllowedRoles = []string{"role_review"}
	app2.Spec.StateMachine.Commands[3].AllowedRoles = []string{"role_review"}

	// Live state has both app1 and app2; desired state removes app2
	live := ManagedState{ActiveApps: []manifest.DXApplication{app1, app2}}
	plan, err := BuildPlan([]manifest.DXApplication{app1}, live)
	if err != nil {
		t.Fatalf("BuildPlan failed: %v", err)
	}

	if len(plan.RemovedApps) != 1 || plan.RemovedApps[0] != "review_fixture" {
		t.Fatalf("expected review_fixture in RemovedApps, got %+v", plan.RemovedApps)
	}

	// Ensure role_procurement on core.employees is NOT dropped because app1 still requires it,
	// while role_review on core.employees IS dropped.
	var droppedReviewShared, droppedProcurementShared bool
	for _, op := range plan.DropOps {
		args := op.Args.(map[string]any)
		tbl := args["table"].(TableRef)
		role := args["role"].(string)
		if tbl.Schema == "core" && tbl.Name == "employees" {
			if role == "role_review" {
				droppedReviewShared = true
			}
			if role == "role_procurement" {
				droppedProcurementShared = true
			}
		}
	}
	if !droppedReviewShared {
		t.Fatal("expected excess shared grant for role_review on core.employees to be dropped")
	}
	if droppedProcurementShared {
		t.Fatal("shared grant for role_procurement on core.employees must be preserved for active app1")
	}
}
