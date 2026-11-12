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
	"reflect"
	"testing"

	"github.com/opendx-os/opendx-os/operator/internal/manifest"
)

const (
	wsA = "11111111-1111-4111-8111-111111111111"
	wsB = "22222222-2222-4222-8222-222222222222"
)

func names(apps []manifest.DXApplication) []string {
	out := []string{}
	for _, a := range apps {
		out = append(out, a.Metadata.Name)
	}
	return out
}

func TestScopeWorkspaceExclusiveKeepsDefinitionUsedByOtherWorkspace(t *testing.T) {
	app := *loadFixtureApp(t)
	live := ManagedState{ActiveApps: []manifest.DXApplication{app}}
	published := map[string][]string{"procurement": {wsA, wsB}}

	scope := ScopeWorkspace(live, published, wsA, nil, true)

	if !reflect.DeepEqual(names(scope.Desired), []string{"procurement"}) {
		t.Fatalf("definition still published in B must stay desired, got %v", names(scope.Desired))
	}
	if !reflect.DeepEqual(scope.Remove, map[string][]string{wsA: {"procurement"}}) {
		t.Fatalf("expected only workspace A unpublished, got %v", scope.Remove)
	}
	if len(scope.Upsert) != 0 {
		t.Fatalf("expected no publication, got %v", scope.Upsert)
	}

	plan, err := BuildPlan(scope.Desired, live)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.DropOps) != 0 || len(plan.RemovedApps) != 0 {
		t.Fatalf("unpublishing one workspace must not drop global grants: drops=%d removed=%v", len(plan.DropOps), plan.RemovedApps)
	}
}

func TestScopeWorkspaceExclusiveRemovesDefinitionWhenLastWorkspaceLeaves(t *testing.T) {
	app := *loadFixtureApp(t)
	live := ManagedState{ActiveApps: []manifest.DXApplication{app}}
	published := map[string][]string{"procurement": {wsA}}

	scope := ScopeWorkspace(live, published, wsA, nil, true)

	if len(scope.Desired) != 0 {
		t.Fatalf("definition without publications must be removed, got %v", names(scope.Desired))
	}
	plan, err := BuildPlan(scope.Desired, live)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(plan.RemovedApps, []string{"procurement"}) || len(plan.DropOps) == 0 {
		t.Fatalf("expected definition removal with grant drops, got removed=%v drops=%d", plan.RemovedApps, len(plan.DropOps))
	}
}

func TestScopeWorkspaceUpgradeRepublishesEveryWorkspace(t *testing.T) {
	v1 := *loadFixtureApp(t)
	v2 := *loadFixtureApp(t)
	v2.Metadata.Version = "0.2.0"
	live := ManagedState{ActiveApps: []manifest.DXApplication{v1}}
	published := map[string][]string{"procurement": {wsB, wsA}}

	scope := ScopeWorkspace(live, published, wsA, []manifest.DXApplication{v2}, false)

	if len(scope.Desired) != 1 || scope.Desired[0].Metadata.Version != "0.2.0" {
		t.Fatalf("expected upgraded definition, got %+v", scope.Desired)
	}
	want := map[string][]string{wsA: {"procurement"}, wsB: {"procurement"}}
	if !reflect.DeepEqual(scope.Upsert, want) {
		t.Fatalf("an upgraded global definition must be republished everywhere, got %v", scope.Upsert)
	}
}

func TestScopeWorkspaceAddKeepsOtherDefinitions(t *testing.T) {
	first := *loadFixtureApp(t)
	second := *loadFixtureApp(t)
	second.Metadata.Name = "second_app"
	second.Spec.DomainTable.Name = "domain.second_records"
	live := ManagedState{ActiveApps: []manifest.DXApplication{first}}
	published := map[string][]string{"procurement": {wsB}}

	scope := ScopeWorkspace(live, published, wsA, []manifest.DXApplication{second}, false)

	if !reflect.DeepEqual(names(scope.Desired), []string{"procurement", "second_app"}) {
		t.Fatalf("expected both definitions, got %v", names(scope.Desired))
	}
	if !reflect.DeepEqual(scope.Upsert, map[string][]string{wsA: {"second_app"}}) {
		t.Fatalf("unchanged definitions must not be republished, got %v", scope.Upsert)
	}
	if len(scope.Remove) != 0 {
		t.Fatalf("non-exclusive apply must not unpublish, got %v", scope.Remove)
	}
}
