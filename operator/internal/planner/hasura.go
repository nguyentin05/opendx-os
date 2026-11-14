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
	"maps"
	"slices"
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

// Session filters for the role scopes. A department filter walks from the
// record to its department and on to the department's staff, so a caller sees
// the record when they are staff of the record's department.
var (
	ownFilter        = map[string]any{"created_by": map[string]any{"_eq": "X-Hasura-User-Id"}}
	departmentFilter = map[string]any{"org_unit": map[string]any{"staff": map[string]any{"user_id": map[string]any{"_eq": "X-Hasura-User-Id"}}}}
)

// ScopeFilter returns the Hasura row filter of a role scope; organization scope
// has no filter.
func ScopeFilter(scope string) map[string]any {
	switch scope {
	case manifest.ScopeOwn:
		return ownFilter
	case manifest.ScopeDepartment:
		return departmentFilter
	default:
		return nil
	}
}

// GenerateHasuraOperations generates metadata API commands to track tables,
// configure relationships and set permissions. Relationships come before
// permissions because department filters traverse them.
func GenerateHasuraOperations(app *manifest.DXApplication) ([]HasuraMetadataPayload, error) {
	if err := manifest.Validate(app); err != nil {
		return nil, fmt.Errorf("validate manifest before Hasura generation: %w", err)
	}

	var ops []HasuraMetadataPayload

	// 1. Track the domain tables and the shared tables they read.
	for i := range app.Spec.Tables {
		ops = append(ops, trackTable(ParseTableRef(app.Spec.Tables[i].Name)))
	}
	for _, t := range SharedTablesRead(app) {
		ops = append(ops, trackTable(ParseTableRef(t)))
	}

	// 2. Relationships along foreign keys (to shared tables and to other tables
	// of the application) and from a record to its department.
	for i := range app.Spec.Tables {
		tbl := &app.Spec.Tables[i]
		ref := ParseTableRef(tbl.Name)
		for _, fk := range tbl.ForeignKeys {
			remote := ParseTableRef(fk.References)
			ops = append(ops,
				relationship("pg_create_object_relationship", ref, strings.TrimSuffix(fk.Column, "_id"), remote, map[string]string{fk.Column: "id"}),
				relationship("pg_create_array_relationship", remote, ref.Name+"_by_"+fk.Column, ref, map[string]string{"id": fk.Column}),
			)
		}
		if tableUsesDepartmentScope(tbl) {
			orgUnits := TableRef{Schema: "cap_hr", Name: "org_units"}
			ops = append(ops,
				relationship("pg_create_object_relationship", ref, "org_unit", orgUnits, map[string]string{"org_unit_id": "id"}),
				relationship("pg_create_array_relationship", orgUnits, "staff", TableRef{Schema: "cap_hr", Name: "staff"}, map[string]string{"id": "org_unit_id"}),
			)
		}
	}

	for i := range app.Spec.Tables {
		tbl := &app.Spec.Tables[i]
		ref := ParseTableRef(tbl.Name)

		// 3. SELECT permissions: the role scope narrowed by the rule's filter.
		for _, sel := range tbl.Access.Select {
			ops = append(ops, permission("pg_create_select_permission", ref, sel.Role, map[string]any{
				"columns":            sel.Columns,
				"filter":             nonNil(selectFilter(sel)),
				"allow_aggregations": false,
			}))
		}

		// 4. INSERT permissions. Creator and department are stamped by database
		// triggers; a process record starts in the initial state.
		for _, ins := range tbl.Access.Insert {
			presets := map[string]string{}
			var initial map[string]any
			if tbl.IsProcess() {
				presets["status"] = tbl.StateMachine.Initial
				initial = map[string]any{"status": map[string]any{"_eq": tbl.StateMachine.Initial}}
			}
			maps.Copy(presets, ins.Presets)
			ops = append(ops, permission("pg_create_insert_permission", ref, ins.Role, map[string]any{
				"columns":      ins.Columns,
				"check":        nonNil(conjoinFilters(initial, ins.RecordFilter)),
				"set":          presets,
				"backend_only": false,
			}))
		}

		// 5. UPDATE permissions; a process record is editable only in its
		// editable states.
		for _, upd := range tbl.Access.Update {
			var editable map[string]any
			if tbl.IsProcess() {
				editable = map[string]any{"status": map[string]any{"_in": tbl.StateMachine.EditableStates}}
			}
			ops = append(ops, permission("pg_create_update_permission", ref, upd.Role, map[string]any{
				"columns": upd.Columns,
				"filter":  nonNil(conjoinFilters(conjoinFilters(ScopeFilter(upd.Scope), upd.RecordFilter), editable)),
				"check":   nonNil(editable),
			}))
		}
	}

	// 6. Shared tables SELECT permissions. Rows of app-scoped tables are limited
	// to the application's own rows.
	for _, shared := range app.Spec.SharedAccess {
		for _, role := range shared.Roles {
			ops = append(ops, permission("pg_create_select_permission", ParseTableRef(shared.Table), role, map[string]any{
				"columns":            shared.Columns,
				"filter":             nonNil(sharedFilter(app, shared.Table, role)),
				"allow_aggregations": false,
			}))
		}
	}

	return ops, nil
}

func trackTable(t TableRef) HasuraMetadataPayload {
	return HasuraMetadataPayload{Type: "pg_track_table", Args: map[string]any{"source": "default", "table": t}}
}

// selectFilter is the row filter of a domain select rule: its scope narrowed
// by its record filter.
func selectFilter(rule manifest.AccessRule) map[string]any {
	return conjoinFilters(ScopeFilter(rule.Scope), rule.RecordFilter)
}

// sharedFilter limits what role reads of a shared table. App-scoped tables
// show only the application's rows. Files are narrower still: a role sees the
// files its user uploaded, and files attached to domain records the role may
// read (through the array relationships from core.files to the domain tables),
// so knowing the application is not enough to list other people's files.
func sharedFilter(app *manifest.DXApplication, table, role string) map[string]any {
	if !manifest.SharedTables[table].AppScoped {
		return nil
	}
	filter := map[string]any{"app_id": map[string]any{"_eq": app.Metadata.Name}}
	if table != "core.files" {
		return filter
	}
	visible := []any{map[string]any{"uploaded_by": map[string]any{"_eq": "X-Hasura-User-Id"}}}
	for i := range app.Spec.Tables {
		tbl := &app.Spec.Tables[i]
		for _, fk := range tbl.ForeignKeys {
			if fk.References != "core.files" {
				continue
			}
			for _, sel := range tbl.Access.Select {
				if sel.Role == role && slices.Contains(sel.Columns, fk.Column) {
					visible = append(visible, map[string]any{ParseTableRef(tbl.Name).Name + "_by_" + fk.Column: nonNil(selectFilter(sel))})
				}
			}
		}
	}
	return conjoinFilters(filter, map[string]any{"_or": visible})
}

func relationship(kind string, table TableRef, name string, remote TableRef, mapping map[string]string) HasuraMetadataPayload {
	return HasuraMetadataPayload{
		Type: kind,
		Args: map[string]any{
			"source": "default",
			"table":  table,
			"name":   name,
			"using": map[string]any{
				"manual_configuration": map[string]any{
					"remote_table":   remote,
					"column_mapping": mapping,
				},
			},
		},
	}
}

func permission(kind string, table TableRef, role string, body map[string]any) HasuraMetadataPayload {
	return HasuraMetadataPayload{
		Type: kind,
		Args: map[string]any{
			"source":     "default",
			"table":      table,
			"role":       role,
			"permission": body,
		},
	}
}

func nonNil(f map[string]any) map[string]any {
	if f == nil {
		return map[string]any{}
	}
	return f
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
