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
	"strings"

	"github.com/opendx-os/opendx-os/operator/internal/manifest"
)

// HasuraMetadataPayload represents an operation for Hasura v1/metadata endpoint.
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

	role := app.Spec.Role

	// 2. Domain table SELECT permission
	for _, sel := range app.Spec.DomainAccess.Select {
		ops = append(ops, HasuraMetadataPayload{
			Type: "pg_create_select_permission",
			Args: map[string]any{
				"source": sourceName,
				"table":  domainTableRef,
				"role":   role,
				"permission": map[string]any{
					"columns": sel.Columns,
					"filter":  sel.Filter,
				},
			},
		})
	}

	// 3. Domain table INSERT permission
	for _, ins := range app.Spec.DomainAccess.Insert {
		ops = append(ops, HasuraMetadataPayload{
			Type: "pg_create_insert_permission",
			Args: map[string]any{
				"source": sourceName,
				"table":  domainTableRef,
				"role":   role,
				"permission": map[string]any{
					"columns": ins.Columns,
					"check":   ins.Check,
				},
			},
		})
	}

	// 4. Domain table UPDATE permission
	for _, upd := range app.Spec.DomainAccess.Update {
		ops = append(ops, HasuraMetadataPayload{
			Type: "pg_create_update_permission",
			Args: map[string]any{
				"source": sourceName,
				"table":  domainTableRef,
				"role":   role,
				"permission": map[string]any{
					"columns": upd.Columns,
					"filter":  upd.Filter,
					"check":   upd.Check,
				},
			},
		})
	}

	// 5. Shared tables SELECT permissions & relationships
	for _, shared := range app.Spec.SharedAccess {
		sharedRef := ParseTableRef(shared.Table)
		ops = append(ops, HasuraMetadataPayload{
			Type: "pg_create_select_permission",
			Args: map[string]any{
				"source": sourceName,
				"table":  sharedRef,
				"role":   role,
				"permission": map[string]any{
					"columns": shared.Columns,
					"filter":  shared.Filter,
				},
			},
		})

		for _, rel := range shared.Relationships {
			targetRef := ParseTableRef(rel.TargetTable)
			ops = append(ops, HasuraMetadataPayload{
				Type: "pg_create_array_relationship",
				Args: map[string]any{
					"source": sourceName,
					"table":  sharedRef,
					"name":   rel.Name,
					"using": map[string]any{
						"manual_configuration": map[string]any{
							"remote_table":   targetRef,
							"column_mapping": rel.Mapping,
						},
					},
				},
			})
		}
	}

	return ops, nil
}
