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

package manifest

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"time"
)

var (
	identifier   = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	uuidPattern  = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	statePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)
	workflowPath = regexp.MustCompile(`^workflows/[a-z][a-z0-9_]*\.yaml$`)
)

// Validate checks a DXApplication against all C0 v1 structural and semantic invariants.
func Validate(app *DXApplication) error {
	if app == nil {
		return errors.New("manifest is nil")
	}
	if app.APIVersion != "dxos.io/v1alpha1" {
		return fmt.Errorf("invalid apiVersion %q (expected dxos.io/v1alpha1)", app.APIVersion)
	}
	if app.Kind != "DXApplication" {
		return fmt.Errorf("invalid kind %q (expected DXApplication)", app.Kind)
	}
	if !identifier.MatchString(app.Metadata.Name) || strings.TrimSpace(app.Metadata.DisplayName) == "" || strings.TrimSpace(app.Metadata.Version) == "" {
		return errors.New("invalid metadata: name, displayName, and version are required")
	}

	s := &app.Spec
	if !identifier.MatchString(s.Role) {
		return errors.New("invalid spec.role identifier")
	}
	if !strings.HasPrefix(s.DomainTable.Name, "domain.") || !identifier.MatchString(strings.TrimPrefix(s.DomainTable.Name, "domain.")) {
		return errors.New("invalid spec.domainTable.name: must be domain.<identifier>")
	}
	if len(s.SharedAccess) == 0 || len(s.DomainTable.Columns) == 0 || len(s.DomainTable.CompositeForeignKeys) == 0 || len(s.Capabilities) == 0 || len(s.Workflows) == 0 || len(s.Attachments) == 0 {
		return errors.New("missing required core specification sections")
	}

	cols := map[string]string{
		"id":           "uuid",
		"workspace_id": "uuid",
		"work_item_id": "uuid",
		"status":       "text",
		"version":      "integer",
		"updated_at":   "timestamp",
	}
	for _, c := range s.DomainTable.Columns {
		if !identifier.MatchString(c.Name) || cols[c.Name] != "" || !oneOf(c.Type, "uuid", "text", "date", "numeric(14,2)", "numeric(4,2)") {
			return fmt.Errorf("invalid or duplicate domain column %q", c.Name)
		}
		cols[c.Name] = c.Type
		if c.Check != "" {
			valid := c.Type == "text" && c.Check == "length(trim("+c.Name+")) > 0"
			valid = valid || (strings.HasPrefix(c.Type, "numeric(") && (c.Check == c.Name+" > 0" || c.Check == c.Name+" > 0 AND "+c.Check+" <= 24" || c.Check == c.Name+" > 0 AND "+c.Name+" <= 24"))
			if c.Check == c.Name+" > 0 AND "+c.Check+" <= 24" {
				valid = false
			}
			if !valid {
				return fmt.Errorf("unsupported check expression on column %q", c.Name)
			}
		}
	}

	shared := map[string][]string{
		"core.employees":    {"id", "workspace_id", "full_name", "email", "department", "created_at"},
		"core.work_items":   {"id", "workspace_id", "title", "description", "priority", "assigned_employee_id", "created_at"},
		"core.file_objects": {"id", "workspace_id", "bucket", "object_key", "file_name", "scan_status", "sha256_hash", "uploaded_by", "scanned_at"},
	}
	seenTables := map[string]bool{}
	for _, g := range s.SharedAccess {
		if shared[g.Table] == nil || seenTables[g.Table] || !uniqueNames(g.Roles, identifier) || !uniqueNames(g.Columns, identifier) {
			return fmt.Errorf("invalid sharedAccess entry for table %q", g.Table)
		}
		seenTables[g.Table] = true
		for _, c := range g.Columns {
			if !oneOf(c, shared[g.Table]...) {
				return fmt.Errorf("unknown shared column %q on %s", c, g.Table)
			}
		}
	}

	foreign := map[string]string{}
	for _, f := range s.DomainTable.CompositeForeignKeys {
		if len(f.Columns) != 2 || f.Columns[0] != "workspace_id" || cols[f.Columns[1]] != "uuid" || shared[f.ReferencesTable] == nil || !reflect.DeepEqual(f.ReferencesColumns, []string{"workspace_id", "id"}) || foreign[f.Columns[1]] != "" {
			return errors.New("invalid compositeForeignKeys declaration")
		}
		foreign[f.Columns[1]] = f.ReferencesTable
	}
	if foreign["work_item_id"] != "core.work_items" {
		return errors.New("missing composite foreign key for work_item_id -> core.work_items")
	}
	if cols["requester_id"] != "" && foreign["requester_id"] != "core.employees" {
		return errors.New("missing composite foreign key for requester_id -> core.employees")
	}

	roles := map[string]bool{}
	for op, grants := range map[string][]AccessRule{
		"select": s.DomainAccess.Select,
		"insert": s.DomainAccess.Insert,
		"update": s.DomainAccess.Update,
	} {
		if len(grants) == 0 {
			return fmt.Errorf("missing domainAccess.%s rules", op)
		}
		seen := map[string]bool{}
		for _, p := range grants {
			if op == "insert" && cols["requester_id"] != "" && p.Presets["requester_id"] != "X-Hasura-User-Id" {
				return errors.New("requester_id must be preset from X-Hasura-User-Id on insert")
			}
			if !identifier.MatchString(p.Role) || seen[p.Role] || !uniqueNames(p.Columns, identifier) {
				return fmt.Errorf("invalid permission rule for role %q in %s", p.Role, op)
			}
			seen[p.Role] = true
			roles[p.Role] = true
			for _, c := range p.Columns {
				if cols[c] == "" {
					return fmt.Errorf("unknown permission column %q", c)
				}
				if op != "select" && (oneOf(c, "id", "workspace_id", "status", "version", "updated_at", "requester_id") || (op == "update" && c == "work_item_id")) {
					return fmt.Errorf("system or immutable column %q cannot be writable in %s", c, op)
				}
				if _, ok := p.Presets[c]; ok {
					return fmt.Errorf("preset column %q cannot also be in writable columns", c)
				}
			}
			if op != "insert" && len(p.Presets) > 0 {
				return errors.New("presets are only supported on insert")
			}
			for c, v := range p.Presets {
				if cols[c] != "uuid" || !oneOf(v, "X-Hasura-User-Id", "X-Hasura-Workspace-Id") || oneOf(c, "id", "work_item_id") {
					return fmt.Errorf("unsupported preset on column %q", c)
				}
			}
			if err := validateFilter(p.RecordFilter, cols, 0); err != nil {
				return err
			}
		}
	}
	if !roles[s.Role] {
		return errors.New("primary spec.role has no domainAccess grants")
	}

	sm := s.StateMachine
	if !statePattern.MatchString(sm.Initial) || !uniqueNames(sm.EditableStates, statePattern) || !uniqueNames(sm.TerminalStates, statePattern) || len(sm.Commands) == 0 {
		return errors.New("invalid stateMachine definition")
	}
	states := map[string]bool{sm.Initial: true}
	commands := map[string]bool{}
	for _, c := range sm.Commands {
		if !identifier.MatchString(c.Name) || commands[c.Name] || !statePattern.MatchString(c.To) || !uniqueNames(c.From, statePattern) || !uniqueNames(c.AllowedRoles, identifier) {
			return fmt.Errorf("invalid command %q", c.Name)
		}
		commands[c.Name] = true
		states[c.To] = true
		for _, st := range c.From {
			states[st] = true
			if oneOf(st, sm.TerminalStates...) {
				return fmt.Errorf("terminal state %q cannot have outgoing command %q", st, c.Name)
			}
		}
		for _, r := range c.AllowedRoles {
			if !roles[r] {
				return fmt.Errorf("command %q references undeclared role %q", c.Name, r)
			}
		}
		if err := validateFilter(c.RecordFilter, cols, 0); err != nil {
			return err
		}
	}
	for _, st := range append(append([]string{}, sm.EditableStates...), sm.TerminalStates...) {
		if !states[st] {
			return fmt.Errorf("undeclared state %q in editableStates or terminalStates", st)
		}
	}
	for _, st := range sm.EditableStates {
		if oneOf(st, sm.TerminalStates...) {
			return fmt.Errorf("state %q cannot be both editable and terminal", st)
		}
	}

	attachments := map[string]bool{}
	for _, at := range s.Attachments {
		if cols[at.Column] != "uuid" || foreign[at.Column] != "core.file_objects" || attachments[at.Column] || !uniqueNames(at.RequireCleanOnCommands, identifier) {
			return fmt.Errorf("invalid attachment declaration on column %q", at.Column)
		}
		attachments[at.Column] = true
		for _, c := range at.RequireCleanOnCommands {
			if !commands[c] {
				return fmt.Errorf("attachment %q references unknown command %q", at.Column, c)
			}
		}
	}

	flows := map[string]bool{}
	steps := map[string]bool{}
	for _, f := range s.Workflows {
		if !identifier.MatchString(f.ID) || flows[f.ID] || !workflowPath.MatchString(f.File) || !reflect.DeepEqual(f.SubscribedEvents, []string{"command.committed"}) || len(f.Effects) == 0 {
			return fmt.Errorf("unsupported workflow declaration %q", f.ID)
		}
		flows[f.ID] = true
		for _, e := range f.Effects {
			if !identifier.MatchString(e.StepName) || steps[e.StepName] || e.Kind != "create_work_item" || !uniqueNames(e.WhenCommands, identifier) || strings.TrimSpace(e.Fields.Title) == "" || strings.TrimSpace(e.Fields.Description) == "" || !oneOf(e.Fields.Priority, "LOW", "MEDIUM", "HIGH", "CRITICAL") || (e.Fields.AssignedEmployeeID != "" && !uuidPattern.MatchString(e.Fields.AssignedEmployeeID)) {
				return fmt.Errorf("invalid workflow effect %q", e.StepName)
			}
			steps[e.StepName] = true
			for _, c := range e.WhenCommands {
				if !commands[c] {
					return fmt.Errorf("workflow effect %q references unknown command %q", e.StepName, c)
				}
			}
		}
	}

	if !reflect.DeepEqual(s.Capabilities, []string{"cap-process"}) {
		return errors.New("unsupported capabilities: only [cap-process] is supported in core")
	}

	ui := s.UISchema
	if !uniqueNames(ui.ListColumns, identifier) || !uniqueNames(ui.Actions, identifier) || len(ui.FormFields) == 0 {
		return errors.New("invalid uiSchema")
	}
	for _, c := range ui.ListColumns {
		if cols[c] == "" {
			return fmt.Errorf("unknown uiSchema.listColumns column %q", c)
		}
	}
	for _, c := range ui.Actions {
		if !commands[c] {
			return fmt.Errorf("unknown uiSchema.actions command %q", c)
		}
	}
	fields := map[string]bool{}
	for _, f := range ui.FormFields {
		if cols[f.Name] == "" || fields[f.Name] || strings.TrimSpace(f.Label) == "" || !oneOf(f.Type, "uuid", "text", "number", "file", "date") || (f.Type == "file" && !attachments[f.Name]) {
			return fmt.Errorf("invalid uiSchema.formFields entry %q", f.Name)
		}
		fields[f.Name] = true
	}

	return nil
}

func oneOf(s string, values ...string) bool {
	for _, v := range values {
		if s == v {
			return true
		}
	}
	return false
}

func uniqueNames(v []string, p *regexp.Regexp) bool {
	if len(v) == 0 {
		return false
	}
	seen := map[string]bool{}
	for _, s := range v {
		if !p.MatchString(s) || seen[s] {
			return false
		}
		seen[s] = true
	}
	return true
}

func validateFilter(f map[string]any, cols map[string]string, depth int) error {
	if f == nil {
		return nil
	}
	if len(f) == 0 || depth > 32 {
		return errors.New("empty or overly deep recordFilter")
	}
	for c, value := range f {
		if c == "_and" || c == "_or" {
			var list []any
			switch seq := value.(type) {
			case []any:
				list = seq
			case []map[string]any:
				for _, item := range seq {
					list = append(list, item)
				}
			default:
				return errors.New("invalid boolean filter list")
			}
			if len(list) == 0 {
				return errors.New("empty boolean filter list")
			}
			for _, child := range list {
				m, ok := child.(map[string]any)
				if !ok {
					return errors.New("invalid nested filter object")
				}
				if err := validateFilter(m, cols, depth+1); err != nil {
					return err
				}
			}
			continue
		}
		typ := cols[c]
		if typ == "" {
			return fmt.Errorf("unknown filter column %q", c)
		}
		ops, ok := value.(map[string]any)
		if !ok || len(ops) == 0 {
			return errors.New("invalid scalar filter")
		}
		for op, v := range ops {
			if op != "_eq" && op != "_neq" {
				return fmt.Errorf("unsupported filter operator %q", op)
			}
			if v == nil {
				continue
			}
			switch x := v.(type) {
			case string:
				if strings.HasPrefix(x, "X-Hasura-") {
					if typ != "uuid" || !oneOf(x, "X-Hasura-User-Id", "X-Hasura-Workspace-Id") {
						return errors.New("unsupported session variable comparison")
					}
				} else if typ == "uuid" {
					if !uuidPattern.MatchString(x) {
						return errors.New("invalid UUID literal in filter")
					}
				} else if typ == "date" {
					if _, err := time.Parse("2006-01-02", x); err != nil {
						return err
					}
				} else if typ != "text" && typ != "timestamp" {
					return errors.New("incompatible string literal in filter")
				}
			case int, int64, float64, json.Number:
				if typ != "integer" && !strings.HasPrefix(typ, "numeric(") {
					return errors.New("incompatible numeric literal in filter")
				}
			default:
				return errors.New("unsupported filter value type")
			}
		}
	}
	return nil
}

func checkShape(v any, t reflect.Type) error {
	if t.Kind() == reflect.Pointer {
		if v == nil {
			return nil
		}
		return checkShape(v, t.Elem())
	}
	if v == nil {
		return errors.New("null required field")
	}
	switch t.Kind() {
	case reflect.Struct:
		m, ok := v.(map[string]any)
		if !ok {
			return errors.New("expected object")
		}
		known := map[string]bool{}
		for i := 0; i < t.NumField(); i++ {
			field := t.Field(i)
			tag := strings.Split(field.Tag.Get("json"), ",")
			name := tag[0]
			known[name] = true
			child, exists := m[name]
			optional := len(tag) > 1 && tag[1] == "omitempty"
			if !exists {
				if optional {
					continue
				}
				return fmt.Errorf("missing required field %q", name)
			}
			if err := checkShape(child, field.Type); err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
		}
		for name := range m {
			if !known[name] {
				return fmt.Errorf("unknown field %q", name)
			}
		}
	case reflect.Slice:
		values, ok := v.([]any)
		if !ok {
			return errors.New("expected array")
		}
		for _, child := range values {
			if err := checkShape(child, t.Elem()); err != nil {
				return err
			}
		}
	case reflect.Map:
		if _, ok := v.(map[string]any); !ok {
			return errors.New("expected map/object")
		}
	case reflect.String:
		if _, ok := v.(string); !ok {
			return errors.New("expected string")
		}
	case reflect.Bool:
		if _, ok := v.(bool); !ok {
			return errors.New("expected boolean")
		}
	}
	return nil
}
