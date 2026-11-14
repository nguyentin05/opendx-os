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
	"fmt"
	"reflect"
	"sort"

	"github.com/opendx-os/opendx-os/operator/internal/manifest"
)

// ManagedState represents the currently active applications managed by the Operator.
type ManagedState struct {
	ActiveApps []manifest.DXApplication
}

// Plan represents the pure, side-effect-free compilation of desired state vs live state.
type Plan struct {
	Apps        []manifest.DXApplication
	DDLs        map[string]string
	HasuraOps   []HasuraMetadataPayload
	DropOps     []HasuraMetadataPayload
	RemovedApps []string
}

type sharedGrantKey struct {
	table string
	role  string
}

// BuildPlan compiles a pure desired/live diff plan across the active manifest set.
func BuildPlan(apps []manifest.DXApplication, live ManagedState) (Plan, error) {
	plan := Plan{
		Apps: make([]manifest.DXApplication, len(apps)),
		DDLs: make(map[string]string, len(apps)),
	}
	copy(plan.Apps, apps)

	seenNames := map[string]bool{}
	seenTables := map[string]string{}
	roleOwner := map[string]string{}
	desiredSharedGrants := map[sharedGrantKey][]string{}

	for i := range apps {
		app := &apps[i]
		if err := manifest.Validate(app); err != nil {
			return Plan{}, fmt.Errorf("app %q: %w", app.Metadata.Name, err)
		}
		if seenNames[app.Metadata.Name] {
			return Plan{}, fmt.Errorf("duplicate application name %q", app.Metadata.Name)
		}
		for _, t := range app.Spec.Tables {
			if owner, taken := seenTables[t.Name]; taken {
				return Plan{}, fmt.Errorf("domain table %q is declared by both %s and %s", t.Name, owner, app.Metadata.Name)
			}
			seenTables[t.Name] = app.Metadata.Name
		}
		seenNames[app.Metadata.Name] = true
		for _, role := range app.RoleNames() {
			if owner, taken := roleOwner[role]; taken {
				return Plan{}, fmt.Errorf("role %q is declared by both %s and %s; a role belongs to one application", role, owner, app.Metadata.Name)
			}
			roleOwner[role] = app.Metadata.Name
		}

		ddl, err := GenerateDDL(app)
		if err != nil {
			return Plan{}, err
		}
		plan.DDLs[app.Metadata.Name] = ddl

		ops, err := GenerateHasuraOperations(app)
		if err != nil {
			return Plan{}, err
		}
		plan.HasuraOps = append(plan.HasuraOps, ops...)

		for _, shared := range app.Spec.SharedAccess {
			cols := append([]string(nil), shared.Columns...)
			sort.Strings(cols)
			for _, role := range shared.Roles {
				k := sharedGrantKey{table: shared.Table, role: role}
				if existing, ok := desiredSharedGrants[k]; ok {
					if !reflect.DeepEqual(existing, cols) {
						return Plan{}, fmt.Errorf("conflicting sharedAccess columns on %s for shared role %s", shared.Table, role)
					}
				} else {
					desiredSharedGrants[k] = cols
				}
			}
		}
	}

	// Calculate excess managed permissions from live state that are no longer desired
	plan.HasuraOps = orderOperations(plan.HasuraOps)

	desiredDomainPerm := map[string]bool{}
	for _, app := range apps {
		for _, t := range app.Spec.Tables {
			for op, rules := range tableRules(t) {
				for _, r := range rules {
					desiredDomainPerm[op+":"+t.Name+":"+r.Role] = true
				}
			}
		}
	}

	droppedShared := map[sharedGrantKey]bool{}
	for _, oldApp := range live.ActiveApps {
		if !seenNames[oldApp.Metadata.Name] {
			plan.RemovedApps = append(plan.RemovedApps, oldApp.Metadata.Name)
		}
		// Permissions of a table or rule that is no longer declared are dropped;
		// the table and its data stay.
		for _, t := range oldApp.Spec.Tables {
			ref := ParseTableRef(t.Name)
			for _, op := range []string{"select", "insert", "update"} {
				for _, r := range tableRules(t)[op] {
					if !desiredDomainPerm[op+":"+t.Name+":"+r.Role] {
						plan.DropOps = append(plan.DropOps, dropPermOp("pg_drop_"+op+"_permission", ref, r.Role))
					}
				}
			}
		}

		for _, shared := range oldApp.Spec.SharedAccess {
			for _, role := range shared.Roles {
				k := sharedGrantKey{table: shared.Table, role: role}
				if _, stillNeeded := desiredSharedGrants[k]; !stillNeeded && !droppedShared[k] {
					droppedShared[k] = true
					plan.DropOps = append(plan.DropOps, dropPermOp("pg_drop_select_permission", ParseTableRef(shared.Table), role))
				}
			}
		}
	}

	return plan, nil
}

// orderOperations puts every table tracking first, then every relationship,
// then every permission, keeping plan order within a phase and dropping exact
// duplicates. Applications share relationships (cap_hr.org_units.staff serves
// every department-scoped application), and a permission filter depends on the
// relationships it traverses, so all of them must exist before any permission.
func orderOperations(ops []HasuraMetadataPayload) []HasuraMetadataPayload {
	phase := func(op HasuraMetadataPayload) int {
		switch op.Type {
		case "pg_track_table":
			return 0
		case "pg_create_object_relationship", "pg_create_array_relationship":
			return 1
		default:
			return 2
		}
	}
	seen := map[string]bool{}
	out := make([]HasuraMetadataPayload, 0, len(ops))
	for want := 0; want <= 2; want++ {
		for _, op := range ops {
			if phase(op) != want {
				continue
			}
			key, ok := Canonical(op)
			if ok && seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, op)
		}
	}
	return out
}

// tableRules returns a table's access rules by operation.
func tableRules(t manifest.TableDef) map[string][]manifest.AccessRule {
	return map[string][]manifest.AccessRule{
		"select": t.Access.Select,
		"insert": t.Access.Insert,
		"update": t.Access.Update,
	}
}

func dropPermOp(opType string, table TableRef, role string) HasuraMetadataPayload {
	return HasuraMetadataPayload{
		Type: opType,
		Args: map[string]any{
			"source": "default",
			"table":  table,
			"role":   role,
		},
	}
}

// BuildApplyPlan installs or upgrades one application and keeps every other
// installed application at its installed definition. Planning one change at a
// time against the live set means an application that is refused cannot hold
// back another.
func BuildApplyPlan(app manifest.DXApplication, live ManagedState) (Plan, error) {
	desired := live.without(app.Metadata.Name)
	desired = append(desired, app)
	return BuildPlan(desired, live)
}

// BuildRemovePlan uninstalls one application and keeps every other installed
// application at its installed definition.
func BuildRemovePlan(name string, live ManagedState) (Plan, error) {
	return BuildPlan(live.without(name), live)
}

// Has reports whether the named application is installed.
func (s ManagedState) Has(name string) bool {
	for _, a := range s.ActiveApps {
		if a.Metadata.Name == name {
			return true
		}
	}
	return false
}

func (s ManagedState) without(name string) []manifest.DXApplication {
	out := make([]manifest.DXApplication, 0, len(s.ActiveApps)+1)
	for _, a := range s.ActiveApps {
		if a.Metadata.Name != name {
			out = append(out, a)
		}
	}
	return out
}

// Changed returns, in input order, the applications of apps that are not
// installed or whose definition differs from the installed one.
func Changed(apps []manifest.DXApplication, live ManagedState) []string {
	installed := make(map[string]string, len(live.ActiveApps))
	for _, a := range live.ActiveApps {
		if c, ok := Canonical(a); ok {
			installed[a.Metadata.Name] = c
		}
	}
	var out []string
	for _, a := range apps {
		c, ok := Canonical(a)
		if !ok || installed[a.Metadata.Name] != c {
			out = append(out, a.Metadata.Name)
		}
	}
	return out
}

// Canonical renders a value as JSON with sorted keys and normalized numbers, so
// a manifest parsed from YAML compares equal to one decoded from the database.
func Canonical(v any) (string, bool) {
	raw, err := json.Marshal(v)
	if err != nil {
		return "", false
	}
	var generic any
	if err := json.Unmarshal(raw, &generic); err != nil {
		return "", false
	}
	out, err := json.Marshal(generic)
	if err != nil {
		return "", false
	}
	return string(out), true
}
