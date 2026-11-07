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
	seenTables := map[string]bool{}
	desiredSharedGrants := map[sharedGrantKey][]string{}

	for i := range apps {
		app := &apps[i]
		if err := manifest.Validate(app); err != nil {
			return Plan{}, fmt.Errorf("app %q: %w", app.Metadata.Name, err)
		}
		if seenNames[app.Metadata.Name] {
			return Plan{}, fmt.Errorf("duplicate application name %q", app.Metadata.Name)
		}
		if seenTables[app.Spec.DomainTable.Name] {
			return Plan{}, fmt.Errorf("duplicate domain table %q", app.Spec.DomainTable.Name)
		}
		seenNames[app.Metadata.Name] = true
		seenTables[app.Spec.DomainTable.Name] = true

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
	desiredDomainPerm := map[string]bool{}
	for _, app := range apps {
		tbl := app.Spec.DomainTable.Name
		for _, r := range app.Spec.DomainAccess.Select {
			desiredDomainPerm["select:"+tbl+":"+r.Role] = true
		}
		for _, r := range app.Spec.DomainAccess.Insert {
			desiredDomainPerm["insert:"+tbl+":"+r.Role] = true
		}
		for _, r := range app.Spec.DomainAccess.Update {
			desiredDomainPerm["update:"+tbl+":"+r.Role] = true
		}
	}

	droppedShared := map[sharedGrantKey]bool{}
	for _, oldApp := range live.ActiveApps {
		if !seenNames[oldApp.Metadata.Name] {
			plan.RemovedApps = append(plan.RemovedApps, oldApp.Metadata.Name)
		}
		oldTbl := oldApp.Spec.DomainTable.Name
		oldRef := ParseTableRef(oldTbl)

		for _, r := range oldApp.Spec.DomainAccess.Select {
			if !desiredDomainPerm["select:"+oldTbl+":"+r.Role] {
				plan.DropOps = append(plan.DropOps, dropPermOp("pg_drop_select_permission", oldRef, r.Role))
			}
		}
		for _, r := range oldApp.Spec.DomainAccess.Insert {
			if !desiredDomainPerm["insert:"+oldTbl+":"+r.Role] {
				plan.DropOps = append(plan.DropOps, dropPermOp("pg_drop_insert_permission", oldRef, r.Role))
			}
		}
		for _, r := range oldApp.Spec.DomainAccess.Update {
			if !desiredDomainPerm["update:"+oldTbl+":"+r.Role] {
				plan.DropOps = append(plan.DropOps, dropPermOp("pg_drop_update_permission", oldRef, r.Role))
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
