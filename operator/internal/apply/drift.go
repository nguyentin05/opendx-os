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

package apply

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/opendx-os/opendx-os/operator/internal/planner"
)

type exportedPermission struct {
	Role       string         `json:"role"`
	Permission map[string]any `json:"permission"`
}

type exportedRelationship struct {
	Name  string `json:"name"`
	Using any    `json:"using"`
}

type exportedTable struct {
	Table               planner.TableRef       `json:"table"`
	SelectPermissions   []exportedPermission   `json:"select_permissions"`
	InsertPermissions   []exportedPermission   `json:"insert_permissions"`
	UpdatePermissions   []exportedPermission   `json:"update_permissions"`
	ObjectRelationships []exportedRelationship `json:"object_relationships"`
	ArrayRelationships  []exportedRelationship `json:"array_relationships"`
}

type exportedMetadata struct {
	Sources []struct {
		Name   string          `json:"name"`
		Tables []exportedTable `json:"tables"`
	} `json:"sources"`
}

// MetadataDrift compares live Hasura metadata with the plan. It returns one
// description per difference: a desired table, permission or relationship that
// is missing or differs, or a permission the plan drops that still exists. An
// empty result means no metadata write is needed. Without a Hasura URL there is
// nothing to compare.
func (r *Runner) MetadataDrift(ctx context.Context, plan planner.Plan) ([]string, error) {
	if r.cfg.HasuraURL == "" {
		return nil, nil
	}
	raw, err := r.exportHasuraMetadata(ctx)
	if err != nil {
		return nil, fmt.Errorf("export Hasura metadata: %w", err)
	}
	var meta exportedMetadata
	if err := json.Unmarshal(raw, &meta); err != nil {
		return nil, fmt.Errorf("decode Hasura metadata: %w", err)
	}
	tables := map[string]exportedTable{}
	for _, src := range meta.Sources {
		if src.Name != "default" {
			continue
		}
		for _, t := range src.Tables {
			tables[t.Table.Schema+"."+t.Table.Name] = t
		}
	}

	var drift []string
	for _, op := range plan.HasuraOps {
		args, ok := op.Args.(map[string]any)
		if !ok {
			continue
		}
		ref, _ := args["table"].(planner.TableRef)
		key := ref.Schema + "." + ref.Name
		table, tracked := tables[key]
		switch {
		case op.Type == "pg_track_table":
			if !tracked {
				drift = append(drift, "table "+key+" is not tracked")
			}
		case strings.HasPrefix(op.Type, "pg_create_") && strings.HasSuffix(op.Type, "_permission"):
			kind := strings.TrimSuffix(strings.TrimPrefix(op.Type, "pg_create_"), "_permission")
			role, _ := args["role"].(string)
			got, found := findPermission(table, kind, role)
			if !found {
				drift = append(drift, fmt.Sprintf("%s permission for %s on %s is missing", kind, role, key))
				continue
			}
			want, err := normalizePermission(args["permission"])
			if err != nil {
				return nil, err
			}
			have, err := normalizePermission(got)
			if err != nil {
				return nil, err
			}
			if want != have {
				drift = append(drift, fmt.Sprintf("%s permission for %s on %s differs", kind, role, key))
			}
		case op.Type == "pg_create_object_relationship" || op.Type == "pg_create_array_relationship":
			name, _ := args["name"].(string)
			list := table.ObjectRelationships
			if op.Type == "pg_create_array_relationship" {
				list = table.ArrayRelationships
			}
			var have *exportedRelationship
			for i := range list {
				if list[i].Name == name {
					have = &list[i]
				}
			}
			switch {
			case have == nil:
				drift = append(drift, fmt.Sprintf("relationship %s on %s is missing", name, key))
			case relationshipDef(have.Using) != relationshipDef(args["using"]):
				drift = append(drift, fmt.Sprintf("relationship %s on %s differs", name, key))
			}
		}
	}
	for _, op := range plan.DropOps {
		args, ok := op.Args.(map[string]any)
		if !ok || !strings.HasPrefix(op.Type, "pg_drop_") || !strings.HasSuffix(op.Type, "_permission") {
			continue
		}
		ref, _ := args["table"].(planner.TableRef)
		role, _ := args["role"].(string)
		kind := strings.TrimSuffix(strings.TrimPrefix(op.Type, "pg_drop_"), "_permission")
		if _, found := findPermission(tables[ref.Schema+"."+ref.Name], kind, role); found {
			drift = append(drift, fmt.Sprintf("excess %s permission for %s on %s.%s", kind, role, ref.Schema, ref.Name))
		}
	}
	sort.Strings(drift)
	return drift, nil
}

func findPermission(t exportedTable, kind, role string) (map[string]any, bool) {
	var list []exportedPermission
	switch kind {
	case "select":
		list = t.SelectPermissions
	case "insert":
		list = t.InsertPermissions
	case "update":
		list = t.UpdatePermissions
	}
	for _, p := range list {
		if p.Role == role {
			return p.Permission, true
		}
	}
	return nil, false
}

// normalizePermission renders a permission body canonically: column lists are
// sorted, false booleans and empty objects (Hasura defaults it may omit on
// export) are dropped, and keys are ordered by encoding/json.
func normalizePermission(v any) (string, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	var generic map[string]any
	if err := json.Unmarshal(raw, &generic); err != nil {
		return "", err
	}
	for k, val := range generic {
		switch x := val.(type) {
		case bool:
			if !x {
				delete(generic, k)
			}
		case map[string]any:
			if len(x) == 0 {
				delete(generic, k)
			}
		case nil:
			delete(generic, k)
		}
	}
	if cols, ok := generic["columns"].([]any); ok {
		names := make([]string, 0, len(cols))
		for _, c := range cols {
			s, _ := c.(string)
			names = append(names, s)
		}
		sort.Strings(names)
		generic["columns"] = names
	}
	out, err := json.Marshal(generic)
	return string(out), err
}

// relationshipDef renders what a manually configured relationship joins: the
// remote table and the column mapping. Other fields Hasura adds on export
// (insertion_order) are left out.
func relationshipDef(using any) string {
	raw, err := json.Marshal(using)
	if err != nil {
		return ""
	}
	var u struct {
		Manual struct {
			RemoteTable   planner.TableRef  `json:"remote_table"`
			ColumnMapping map[string]string `json:"column_mapping"`
		} `json:"manual_configuration"`
	}
	if err := json.Unmarshal(raw, &u); err != nil {
		return ""
	}
	out, _ := json.Marshal(u.Manual) // map keys are sorted
	return string(out)
}

// relationshipIndex maps "<kind>:<schema>.<table>:<name>" to the definition of
// every relationship in exported metadata.
func relationshipIndex(raw json.RawMessage) map[string]string {
	index := map[string]string{}
	var meta exportedMetadata
	if json.Unmarshal(raw, &meta) != nil {
		return index
	}
	for _, src := range meta.Sources {
		if src.Name != "default" {
			continue
		}
		for _, t := range src.Tables {
			for _, rel := range t.ObjectRelationships {
				index[relationshipKey("pg_create_object_relationship", t.Table, rel.Name)] = relationshipDef(rel.Using)
			}
			for _, rel := range t.ArrayRelationships {
				index[relationshipKey("pg_create_array_relationship", t.Table, rel.Name)] = relationshipDef(rel.Using)
			}
		}
	}
	return index
}

func relationshipKey(opType string, table planner.TableRef, name string) string {
	return opType + ":" + table.Schema + "." + table.Name + ":" + name
}
