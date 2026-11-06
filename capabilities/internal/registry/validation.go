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

package registry

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"regexp"
	"strings"
	"time"
)

var identifier = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
var statePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)
var workflowPath = regexp.MustCompile(`^workflows/[a-z][a-z0-9_]*\.yaml$`)

func Decode(data []byte) (*AppDefinition, error) {
	var shape any
	shapeDecoder := json.NewDecoder(bytes.NewReader(data))
	shapeDecoder.UseNumber()
	if e := shapeDecoder.Decode(&shape); e != nil {
		return nil, e
	}
	if e := checkShape(shape, reflect.TypeOf(AppDefinition{})); e != nil {
		return nil, e
	}
	var a AppDefinition
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	d.UseNumber()
	if e := d.Decode(&a); e != nil {
		return nil, e
	}
	if e := d.Decode(new(any)); e != io.EOF {
		return nil, errors.New("trailing registry JSON")
	}
	if e := a.Validate(); e != nil {
		return nil, e
	}
	return &a, nil
}
func (a *AppDefinition) Validate() error {
	s := &a.ManifestSpec
	if !uuidPattern.MatchString(a.WorkspaceID) || !identifier.MatchString(a.AppID) || strings.TrimSpace(a.DisplayName) == "" || strings.TrimSpace(a.Version) == "" || a.UpdatedAt.IsZero() || a.Status != "Ready" || a.LastError != nil {
		return errors.New("invalid active registry identity/status")
	}
	if !identifier.MatchString(s.Role) || a.RoleName != s.Role || a.DomainTable != s.DomainTable.Name || !strings.HasPrefix(a.DomainTable, "domain.") || !identifier.MatchString(strings.TrimPrefix(a.DomainTable, "domain.")) {
		return errors.New("invalid domain or role identifier")
	}
	if !reflect.DeepEqual(a.UISchema, s.UISchema) || !reflect.DeepEqual(a.StateMachine, s.StateMachine) || !reflect.DeepEqual(a.DependentCapabilities, s.Capabilities) {
		return errors.New("incoherent active snapshot")
	}
	if len(s.SharedAccess) == 0 || len(s.DomainTable.Columns) == 0 || len(s.DomainTable.CompositeForeignKeys) == 0 || len(s.Capabilities) == 0 || len(s.Workflows) == 0 || len(s.Attachments) == 0 {
		return errors.New("missing core specification")
	}
	cols := map[string]string{"id": "uuid", "workspace_id": "uuid", "work_item_id": "uuid", "status": "text", "version": "integer", "updated_at": "timestamp"}
	for _, c := range s.DomainTable.Columns {
		if !identifier.MatchString(c.Name) || cols[c.Name] != "" || !oneOf(c.Type, "uuid", "text", "date", "numeric(14,2)", "numeric(4,2)") {
			return errors.New("invalid or duplicate column")
		}
		cols[c.Name] = c.Type
		if c.Check != "" {
			valid := c.Type == "text" && c.Check == "length(trim("+c.Name+")) > 0"
			valid = valid || (strings.HasPrefix(c.Type, "numeric(") && (c.Check == c.Name+" > 0" || c.Check == c.Name+" > 0 AND "+c.Name+" <= 24"))
			if !valid {
				return errors.New("unsupported check")
			}
		}
	}
	shared := map[string][]string{"core.employees": {"id", "workspace_id", "full_name", "email", "department", "created_at"}, "core.work_items": {"id", "workspace_id", "title", "description", "priority", "assigned_employee_id", "created_at"}, "core.file_objects": {"id", "workspace_id", "bucket", "object_key", "file_name", "scan_status", "sha256_hash", "uploaded_by", "scanned_at"}}
	seenTables := map[string]bool{}
	for _, g := range s.SharedAccess {
		if shared[g.Table] == nil || seenTables[g.Table] || !uniqueNames(g.Roles, identifier) || !uniqueNames(g.Columns, identifier) {
			return errors.New("invalid shared access")
		}
		seenTables[g.Table] = true
		for _, c := range g.Columns {
			if !oneOf(c, shared[g.Table]...) {
				return errors.New("unknown shared column")
			}
		}
	}
	foreign := map[string]string{}
	for _, f := range s.DomainTable.CompositeForeignKeys {
		if len(f.Columns) != 2 || f.Columns[0] != "workspace_id" || cols[f.Columns[1]] != "uuid" || shared[f.ReferencesTable] == nil || !reflect.DeepEqual(f.ReferencesColumns, []string{"workspace_id", "id"}) || foreign[f.Columns[1]] != "" {
			return errors.New("invalid composite FK")
		}
		foreign[f.Columns[1]] = f.ReferencesTable
	}
	if foreign["work_item_id"] != "core.work_items" {
		return errors.New("missing work item FK")
	}
	if cols["requester_id"] != "" && foreign["requester_id"] != "core.employees" {
		return errors.New("missing requester FK")
	}
	roles := map[string]bool{}
	for op, grants := range map[string][]Permission{"select": s.DomainAccess.Select, "insert": s.DomainAccess.Insert, "update": s.DomainAccess.Update} {
		if len(grants) == 0 {
			return errors.New("missing domain permissions")
		}
		seen := map[string]bool{}
		for _, p := range grants {
			if op == "insert" && cols["requester_id"] != "" && p.Presets["requester_id"] != "X-Hasura-User-Id" {
				return errors.New("requester_id must be preset from signed user identity")
			}
			if !identifier.MatchString(p.Role) || seen[p.Role] || !uniqueNames(p.Columns, identifier) {
				return errors.New("invalid permission")
			}
			seen[p.Role] = true
			roles[p.Role] = true
			for _, c := range p.Columns {
				if cols[c] == "" {
					return errors.New("unknown permission column")
				}
				if op != "select" && (oneOf(c, "id", "workspace_id", "status", "version", "updated_at", "requester_id") || (op == "update" && c == "work_item_id")) {
					return errors.New("writable system or immutable column")
				}
				if _, ok := p.Presets[c]; ok {
					return errors.New("preset also writable")
				}
			}
			if op != "insert" && len(p.Presets) > 0 {
				return errors.New("presets only supported on insert")
			}
			for c, v := range p.Presets {
				if cols[c] != "uuid" || !oneOf(v, "X-Hasura-User-Id", "X-Hasura-Workspace-Id") || oneOf(c, "id", "work_item_id") {
					return errors.New("unsupported preset")
				}
			}
			if e := validateFilter(p.RecordFilter, cols, 0); e != nil {
				return e
			}
		}
	}
	if !roles[s.Role] {
		return errors.New("primary role has no grants")
	}
	sm := s.StateMachine
	if !statePattern.MatchString(sm.Initial) || !uniqueNames(sm.EditableStates, statePattern) || !uniqueNames(sm.TerminalStates, statePattern) || len(sm.Commands) == 0 {
		return errors.New("invalid state machine")
	}
	states := map[string]bool{sm.Initial: true}
	commands := map[string]bool{}
	for _, c := range sm.Commands {
		if !identifier.MatchString(c.Name) || commands[c.Name] || !statePattern.MatchString(c.To) || !uniqueNames(c.From, statePattern) || !uniqueNames(c.AllowedRoles, identifier) {
			return errors.New("invalid command")
		}
		commands[c.Name] = true
		states[c.To] = true
		for _, st := range c.From {
			states[st] = true
			if oneOf(st, sm.TerminalStates...) {
				return errors.New("terminal state has outgoing command")
			}
		}
		for _, r := range c.AllowedRoles {
			if !roles[r] {
				return errors.New("command has undeclared role")
			}
		}
		if e := validateFilter(c.RecordFilter, cols, 0); e != nil {
			return e
		}
	}
	for _, st := range append(append([]string{}, sm.EditableStates...), sm.TerminalStates...) {
		if !states[st] {
			return errors.New("undeclared state")
		}
	}
	for _, st := range sm.EditableStates {
		if oneOf(st, sm.TerminalStates...) {
			return errors.New("terminal editable state")
		}
	}
	attachments := map[string]bool{}
	for _, at := range s.Attachments {
		if cols[at.Column] != "uuid" || foreign[at.Column] != "core.file_objects" || attachments[at.Column] || !uniqueNames(at.RequireCleanOnCommands, identifier) {
			return errors.New("invalid attachment")
		}
		attachments[at.Column] = true
		for _, c := range at.RequireCleanOnCommands {
			if !commands[c] {
				return errors.New("attachment references unknown command")
			}
		}
	}
	flows := map[string]bool{}
	// Receipts and effect requests identify a step across the entire app.
	steps := map[string]bool{}
	for _, f := range s.Workflows {
		if !identifier.MatchString(f.ID) || flows[f.ID] || !workflowPath.MatchString(f.File) || !reflect.DeepEqual(f.SubscribedEvents, []string{"command.committed"}) || len(f.Effects) == 0 {
			return errors.New("unsupported workflow")
		}
		flows[f.ID] = true
		for _, e := range f.Effects {
			if !identifier.MatchString(e.StepName) || steps[e.StepName] || e.Kind != "create_work_item" || !uniqueNames(e.WhenCommands, identifier) || strings.TrimSpace(e.Fields.Title) == "" || !oneOf(e.Fields.Priority, "LOW", "MEDIUM", "HIGH", "CRITICAL") || (e.Fields.AssignedEmployeeID != "" && !uuidPattern.MatchString(e.Fields.AssignedEmployeeID)) {
				return errors.New("invalid workflow effect")
			}
			steps[e.StepName] = true
			for _, c := range e.WhenCommands {
				if !commands[c] {
					return errors.New("effect references unknown command")
				}
			}
		}
	}
	if !reflect.DeepEqual(s.Capabilities, []string{"cap-process"}) {
		return errors.New("unsupported capabilities")
	}
	ui := s.UISchema
	if !uniqueNames(ui.ListColumns, identifier) || !uniqueNames(ui.Actions, identifier) || len(ui.FormFields) == 0 {
		return errors.New("invalid UI schema")
	}
	for _, c := range ui.ListColumns {
		if cols[c] == "" {
			return errors.New("unknown UI column")
		}
	}
	for _, c := range ui.Actions {
		if !commands[c] {
			return errors.New("unknown UI action")
		}
	}
	fields := map[string]bool{}
	for _, f := range ui.FormFields {
		if cols[f.Name] == "" || fields[f.Name] || f.Label == "" || !oneOf(f.Type, "uuid", "text", "number", "file", "date") || (f.Type == "file" && !attachments[f.Name]) {
			return errors.New("invalid UI field")
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
		return errors.New("empty or deep filter")
	}
	for c, value := range f {
		if c == "_and" || c == "_or" {
			list, ok := value.([]any)
			if !ok || len(list) == 0 {
				return errors.New("invalid boolean filter")
			}
			for _, child := range list {
				m, ok := child.(map[string]any)
				if !ok {
					return errors.New("invalid nested filter")
				}
				if e := validateFilter(m, cols, depth+1); e != nil {
					return e
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
				return errors.New("unsupported filter operator")
			}
			if v == nil {
				continue
			}
			switch x := v.(type) {
			case string:
				if strings.HasPrefix(x, "X-Hasura-") {
					if typ != "uuid" || !oneOf(x, "X-Hasura-User-Id", "X-Hasura-Workspace-Id") {
						return errors.New("unsupported session comparison")
					}
				} else if typ == "uuid" {
					if !uuidPattern.MatchString(x) {
						return errors.New("invalid UUID comparison")
					}
				} else if typ == "date" {
					if _, e := time.Parse("2006-01-02", x); e != nil {
						return e
					}
				} else if typ != "text" && typ != "timestamp" {
					return errors.New("incompatible string comparison")
				}
			case json.Number:
				if typ != "integer" && !strings.HasPrefix(typ, "numeric(") {
					return errors.New("incompatible number comparison")
				}
			default:
				return errors.New("unsupported filter value")
			}
		}
	}
	return nil
}

// checkShape supplements encoding/json, which otherwise accepts case-insensitive
// keys, absent booleans, and null scalar values. Optional fields use omitempty.
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
	if t == reflect.TypeOf(time.Time{}) {
		if _, ok := v.(string); !ok {
			return errors.New("invalid timestamp")
		}
		return nil
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
				return fmt.Errorf("missing field %s", name)
			}
			if e := checkShape(child, field.Type); e != nil {
				return fmt.Errorf("%s: %w", name, e)
			}
		}
		for name := range m {
			if !known[name] {
				return fmt.Errorf("unsupported field %s", name)
			}
		}
	case reflect.Slice:
		values, ok := v.([]any)
		if !ok {
			return errors.New("expected array")
		}
		for _, child := range values {
			if e := checkShape(child, t.Elem()); e != nil {
				return e
			}
		}
	case reflect.Map:
		if _, ok := v.(map[string]any); !ok {
			return errors.New("expected object")
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
