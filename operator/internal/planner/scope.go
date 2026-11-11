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
	"sort"

	"github.com/opendx-os/opendx-os/operator/internal/manifest"
)

// Scope separates global application definitions from per-workspace publication.
// Domain tables, triggers and Hasura metadata are global; registry rows are not.
type Scope struct {
	// Desired is the complete global definition set after the change.
	Desired []manifest.DXApplication
	// Upsert maps a workspace ID to the application names (re)published there.
	Upsert map[string][]string
	// Remove maps a workspace ID to the application names unpublished there.
	Remove map[string][]string
}

// ScopeWorkspace computes the global desired definitions and publication changes
// when one workspace's application set changes.
//
// live holds the active global definitions and published maps an application name
// to the workspaces where it is currently published. apps are applied to
// workspaceID; an upgraded definition is republished in every workspace that
// already uses it, because they share one table and one permission set. When
// exclusive is true, applications published in workspaceID but absent from apps
// are unpublished there, and a definition is removed only when no workspace still
// publishes it.
func ScopeWorkspace(live ManagedState, published map[string][]string, workspaceID string, apps []manifest.DXApplication, exclusive bool) Scope {
	defs := make(map[string]manifest.DXApplication, len(live.ActiveApps)+len(apps))
	for _, app := range live.ActiveApps {
		defs[app.Metadata.Name] = app
	}

	upsert := map[string]map[string]bool{}
	add := func(ws, name string) {
		if upsert[ws] == nil {
			upsert[ws] = map[string]bool{}
		}
		upsert[ws][name] = true
	}

	wanted := make(map[string]bool, len(apps))
	for _, app := range apps {
		name := app.Metadata.Name
		wanted[name] = true
		previous, existed := defs[name]
		defs[name] = app
		add(workspaceID, name)
		if existed && !reflect.DeepEqual(previous, app) {
			for _, ws := range published[name] {
				add(ws, name)
			}
		}
	}

	remove := map[string]map[string]bool{}
	if exclusive {
		for name, workspaces := range published {
			if wanted[name] || !containsString(workspaces, workspaceID) {
				continue
			}
			if remove[workspaceID] == nil {
				remove[workspaceID] = map[string]bool{}
			}
			remove[workspaceID][name] = true
			stillUsed := false
			for _, ws := range workspaces {
				stillUsed = stillUsed || ws != workspaceID
			}
			if !stillUsed {
				delete(defs, name)
			}
		}
	}

	desired := make([]manifest.DXApplication, 0, len(defs))
	for _, app := range defs {
		desired = append(desired, app)
	}
	sort.Slice(desired, func(i, j int) bool { return desired[i].Metadata.Name < desired[j].Metadata.Name })

	return Scope{Desired: desired, Upsert: sortedSets(upsert), Remove: sortedSets(remove)}
}

func sortedSets(in map[string]map[string]bool) map[string][]string {
	out := make(map[string][]string, len(in))
	for ws, set := range in {
		list := make([]string, 0, len(set))
		for name := range set {
			list = append(list, name)
		}
		sort.Strings(list)
		out[ws] = list
	}
	return out
}

func containsString(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}
