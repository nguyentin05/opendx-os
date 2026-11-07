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
	"strings"

	"github.com/opendx-os/opendx-os/operator/internal/manifest"
)

// HasuraMetadataPayload represents an operation for Hasura /v1/metadata endpoint.
type HasuraMetadataPayload struct {
	Type string `json:"type"`
	Args any    `json:"args"`
}

// TableRef points to a schema-qualified table in Hasura.
type TableRef struct {
	Schema string `json:"schema"`
	Name   string `json:"name"`
}

// ParseTableRef converts "domain.foo" or "core.bar" into TableRef.
func ParseTableRef(name string) TableRef {
	parts := strings.Split(name, ".")
	if len(parts) == 2 {
		return TableRef{Schema: parts[0], Name: parts[1]}
	}
	return TableRef{Schema: "public", Name: name}
}

// GenerateHasuraOperations generates metadata API commands to track tables, set permissions, and configure relationships.
func GenerateHasuraOperations(app *manifest.DXApplication) ([]HasuraMetadataPayload, error) {
	if err := manifest.Validate(app); err != nil {
		return nil, fmt.Errorf("validate manifest before Hasura generation: %w", err)
	}

	var ops []HasuraMetadataPayload
	sourceName := "default"
	domainTableRef := ParseTableRef(app.Spec.DomainTable.Name)

	// 1. Track domain table
	ops = append(ops, HasuraMetadataPayload{
		Type: "pg_track_table",
		Args: map[string]any{
			"source": sourceName,
			"table":  domainTableRef,
		},
	})

	// 2. Domain table SELECT permissions
	for _, sel := range app.Spec.DomainAccess.Select {
		ops = append(ops, HasuraMetadataPayload{
			Type: "pg_create_select_permission",
			Args: map[string]any{
				"source": sourceName,
				"table":  domainTableRef,
				"role":   sel.Role,
				"permission": map[string]any{
					"columns":           sel.Columns,
					"filter":            conjoinWorkspaceFilter(sel.RecordFilter),
					"allow_aggregations": false,
				},
			},
		})
	}

	// 3. Domain table INSERT permissions (presets enforce initial status, workspace_id, and user presets)
	for _, ins := range app.Spec.DomainAccess.Insert {
		presets := map[string]string{
			"workspace_id": "X-Hasura-Workspace-Id",
			"status":       app.Spec.StateMachine.Initial,
		}
		for k, v := range ins.Presets {
			presets[k] = v
		}
		checkFilter := conjoinFilters(
			conjoinWorkspaceFilter(ins.RecordFilter),
			map[string]any{"status": map[string]any{"_eq": app.Spec.StateMachine.Initial}},
		)
		ops = append(ops, HasuraMetadataPayload{
			Type: "pg_create_insert_permission",
			Args: map[string]any{
				"source": sourceName,
				"table":  domainTableRef,
				"role":   ins.Role,
				"permission": map[string]any{
					"columns":      ins.Columns,
					"check":        checkFilter,
					"set":          presets,
					"backend_only": false,
				},
			},
		})
	}

	// 4. Domain table UPDATE permissions (restricted strictly to editableStates)
	for _, upd := range app.Spec.DomainAccess.Update {
		editableGuard := map[string]any{
			"status": map[string]any{"_in": app.Spec.StateMachine.EditableStates},
		}
		updateFilter := conjoinFilters(conjoinWorkspaceFilter(upd.RecordFilter), editableGuard)
		ops = append(ops, HasuraMetadataPayload{
			Type: "pg_create_update_permission",
			Args: map[string]any{
				"source": sourceName,
				"table":  domainTableRef,
				"role":   upd.Role,
				"permission": map[string]any{
					"columns": upd.Columns,
					"filter":  updateFilter,
					"check":   editableGuard,
				},
			},
		})
	}

	// 5. Shared tables SELECT permissions
	for _, shared := range app.Spec.SharedAccess {
		sharedRef := ParseTableRef(shared.Table)
		ops = append(ops, HasuraMetadataPayload{
			Type: "pg_track_table",
			Args: map[string]any{
				"source": sourceName,
				"table":  sharedRef,
			},
		})
		for _, role := range shared.Roles {
			ops = append(ops, HasuraMetadataPayload{
				Type: "pg_create_select_permission",
				Args: map[string]any{
					"source": sourceName,
					"table":  sharedRef,
					"role":   role,
					"permission": map[string]any{
						"columns":            shared.Columns,
						"filter":             conjoinWorkspaceFilter(nil),
						"allow_aggregations": false,
					},
				},
			})
		}
	}

	// 6. Relationships derived from CompositeForeignKeys
	for _, fk := range app.Spec.DomainTable.CompositeForeignKeys {
		fkCol := fk.Columns[1]
		sharedRef := ParseTableRef(fk.ReferencesTable)
		relName := strings.TrimSuffix(fkCol, "_id")
		ops = append(ops, HasuraMetadataPayload{
			Type: "pg_create_object_relationship",
			Args: map[string]any{
				"source": sourceName,
				"table":  domainTableRef,
				"name":   relName,
				"using": map[string]any{
					"manual_configuration": map[string]any{
						"remote_table": sharedRef,
						"column_mapping": map[string]string{
							"workspace_id": "workspace_id",
							fkCol:          "id",
						},
					},
				},
			},
		})
		ops = append(ops, HasuraMetadataPayload{
			Type: "pg_create_array_relationship",
			Args: map[string]any{
				"source": sourceName,
				"table":  sharedRef,
				"name":   domainTableRef.Name + "_by_" + fkCol,
				"using": map[string]any{
					"manual_configuration": map[string]any{
						"remote_table": domainTableRef,
						"column_mapping": map[string]string{
							"workspace_id": "workspace_id",
							"id":           fkCol,
						},
					},
				},
			},
		})
	}

	return ops, nil
}

func conjoinWorkspaceFilter(f map[string]any) map[string]any {
	ws := map[string]any{
		"workspace_id": map[string]any{"_eq": "X-Hasura-Workspace-Id"},
	}
	if len(f) == 0 {
		return ws
	}
	return conjoinFilters(ws, f)
}

func conjoinFilters(a, b map[string]any) map[string]any {
	if len(a) == 0 {
		return b
	}
	if len(b) == 0 {
		return a
	}
	return map[string]any{
		"_and": []any{a, b},
	}
}
