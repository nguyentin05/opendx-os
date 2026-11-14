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

// Capability identifiers an application may declare.
const (
	CapProcess = "cap-process"
	CapHR      = "cap-hr"
	CapWork    = "cap-work"
	CapData    = "cap-data"
	CapAI      = "cap-ai"
)

var knownCapabilities = []string{CapProcess, CapHR, CapWork, CapData, CapAI}

// RoleName is the shape of every role name. The role_ prefix keeps
// application roles away from names Hasura treats as privileged (admin).
var RoleName = regexp.MustCompile(`^role_[a-z0-9_]+$`)

// ReservedRoles belong to the platform (core.roles rows without an owner).
var ReservedRoles = []string{"role_operator", "role_auditor", "role_workflow"}

// SharedTable describes a Tier 1 or Tier 2 table that applications may read
// through sharedAccess or reference from a foreign key.
type SharedTable struct {
	// Capability that owns the table; empty for Tier 1.
	Capability string
	// Columns application roles may read.
	Columns []string
	// AppScoped tables carry app_id; an application's roles see only its rows.
	AppScoped bool
	// Referenceable tables have an id primary key that domain columns may reference.
	Referenceable bool
}

// SharedTables is the closed set of shared tables visible to applications.
var SharedTables = map[string]SharedTable{
	"core.users": {
		Columns:       []string{"id", "email", "display_name", "status"},
		Referenceable: true,
	},
	"core.files": {
		Columns:       []string{"id", "file_name", "mime_type", "size_bytes", "scan_status", "sha256_hash", "uploaded_by", "scanned_at", "created_at"},
		AppScoped:     true,
		Referenceable: true,
	},
	"cap_hr.org_units": {
		Capability:    CapHR,
		Columns:       []string{"id", "parent_id", "code", "name"},
		Referenceable: true,
	},
	"cap_hr.staff": {
		Capability: CapHR,
		Columns:    []string{"user_id", "org_unit_id", "position", "joined_at"},
	},
	"cap_data.devices": {
		Capability:    CapData,
		Columns:       []string{"id", "device_code", "name", "location", "status", "last_reading"},
		Referenceable: true,
	},
	"cap_work.work_items": {
		Capability:    CapWork,
		Columns:       []string{"id", "title", "description", "priority", "board_column", "assignee_id", "source_type", "source_id", "created_by", "created_at", "updated_at"},
		AppScoped:     true,
		Referenceable: true,
	},
}

// SystemColumns returns the operator-managed columns of one of the
// application's tables and their manifest types. org_unit_id exists only when
// the application declares cap-hr and the table is not a child table, status
// only on a process table, and the parent column only on a child table.
func SystemColumns(app *DXApplication, t *TableDef) map[string]string {
	cols := map[string]string{
		"id":         "uuid",
		"created_by": "uuid",
		"version":    "integer",
		"created_at": "timestamp",
		"updated_at": "timestamp",
	}
	if t.IsProcess() {
		cols["status"] = "text"
	}
	if t.IsChild() {
		// A child row belongs to its parent's department; it holds the parent's id.
		cols[t.BelongsTo.Column] = "uuid"
	} else if app.UsesCapability(CapHR) {
		cols["org_unit_id"] = "uuid"
	}
	return cols
}

// UsesCapability reports whether the application declares capability id.
func (app *DXApplication) UsesCapability(id string) bool {
	return oneOf(id, app.Spec.Capabilities...)
}

// RoleNames returns the names of the roles the application owns.
func (app *DXApplication) RoleNames() []string {
	names := make([]string, 0, len(app.Spec.Roles))
	for _, r := range app.Spec.Roles {
		names = append(names, r.Name)
	}
	return names
}

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

	// Capabilities.
	if len(s.Capabilities) == 0 || !uniqueNames(s.Capabilities, regexp.MustCompile(`^cap-[a-z]+$`)) {
		return errors.New("invalid spec.capabilities: a non-empty list of unique capability ids is required")
	}
	for _, c := range s.Capabilities {
		if !oneOf(c, knownCapabilities...) {
			return fmt.Errorf("unknown capability %q", c)
		}
	}
	if !app.UsesCapability(CapProcess) {
		return errors.New("spec.capabilities must include cap-process, which runs the application's commands")
	}

	// Roles owned by the application.
	if len(s.Roles) == 0 {
		return errors.New("spec.roles must declare at least one role")
	}
	roles := map[string]bool{}
	for _, r := range s.Roles {
		if !RoleName.MatchString(r.Name) || roles[r.Name] {
			return fmt.Errorf("invalid or duplicate role %q (role names start with role_)", r.Name)
		}
		if oneOf(r.Name, ReservedRoles...) {
			return fmt.Errorf("role %q is reserved for the platform", r.Name)
		}
		roles[r.Name] = true
	}
	requireRole := func(where, role string) error {
		if !roles[role] {
			return fmt.Errorf("%s uses role %q, which is not declared in spec.roles", where, role)
		}
		return nil
	}

	// Shared tables.
	seenTables := map[string]bool{}
	for _, g := range s.SharedAccess {
		table, ok := SharedTables[g.Table]
		if !ok || seenTables[g.Table] || !uniqueNames(g.Roles, identifier) || !uniqueNames(g.Columns, identifier) {
			return fmt.Errorf("invalid sharedAccess entry for table %q", g.Table)
		}
		if table.Capability != "" && !app.UsesCapability(table.Capability) {
			return fmt.Errorf("sharedAccess on %s requires capability %s", g.Table, table.Capability)
		}
		seenTables[g.Table] = true
		for _, r := range g.Roles {
			if err := requireRole("sharedAccess on "+g.Table, r); err != nil {
				return err
			}
		}
		for _, c := range g.Columns {
			if !oneOf(c, table.Columns...) {
				return fmt.Errorf("unknown shared column %q on %s", c, g.Table)
			}
		}
	}

	// Domain tables.
	if len(s.Tables) == 0 {
		return errors.New("spec.tables must declare at least one table")
	}
	tables := map[string]bool{}
	for i := range s.Tables {
		name := s.Tables[i].Name
		if !strings.HasPrefix(name, "domain.") || !identifier.MatchString(strings.TrimPrefix(name, "domain.")) {
			return fmt.Errorf("invalid table name %q: must be domain.<identifier>", name)
		}
		if tables[name] {
			return fmt.Errorf("duplicate table %q", name)
		}
		tables[name] = true
	}
	// Command names are unique in the application: a command is addressed by
	// application and name alone.
	commands := map[string]bool{}
	for i := range s.Tables {
		if err := validateTable(app, &s.Tables[i], tables, commands, requireRole); err != nil {
			return fmt.Errorf("table %s: %w", s.Tables[i].Name, err)
		}
	}

	// Workflows.
	flows := map[string]bool{}
	steps := map[string]bool{}
	for _, f := range s.Workflows {
		if !identifier.MatchString(f.ID) || flows[f.ID] || !workflowPath.MatchString(f.File) || !reflect.DeepEqual(f.SubscribedEvents, []string{"command.committed"}) || len(f.Effects) == 0 {
			return fmt.Errorf("unsupported workflow declaration %q", f.ID)
		}
		flows[f.ID] = true
		for _, e := range f.Effects {
			if !identifier.MatchString(e.StepName) || steps[e.StepName] || e.Kind != "create_work_item" || !uniqueNames(e.WhenCommands, identifier) || strings.TrimSpace(e.Fields.Title) == "" || strings.TrimSpace(e.Fields.Description) == "" || !oneOf(e.Fields.Priority, "LOW", "MEDIUM", "HIGH", "CRITICAL") || (e.Fields.AssigneeID != "" && !uuidPattern.MatchString(e.Fields.AssigneeID)) {
				return fmt.Errorf("invalid workflow effect %q", e.StepName)
			}
			if !app.UsesCapability(CapWork) {
				return fmt.Errorf("workflow effect %q creates a work item, which requires capability cap-work", e.StepName)
			}
			steps[e.StepName] = true
			for _, c := range e.WhenCommands {
				if !commands[c] {
					return fmt.Errorf("workflow effect %q references unknown command %q", e.StepName, c)
				}
			}
		}
	}

	return nil
}

// validateTable checks one domain table. tables holds every table name of the
// application; commands collects command names across tables.
func validateTable(app *DXApplication, t *TableDef, tables, commands map[string]bool, requireRole func(where, role string) error) error {
	if len(t.Columns) == 0 {
		return errors.New("columns must declare at least one business column")
	}
	// A child table's rows belong to a record of another table of the
	// application, one level deep.
	var parent *TableDef
	if t.IsChild() {
		parent = app.Table(t.BelongsTo.Table)
		if parent == nil || parent == t || parent.IsChild() {
			return fmt.Errorf("belongsTo names %q, which must be another table of the application that is not a child table", t.BelongsTo.Table)
		}
		if !identifier.MatchString(t.BelongsTo.Column) || oneOf(t.BelongsTo.Column, "id", "created_by", "org_unit_id", "status", "version", "created_at", "updated_at") {
			return fmt.Errorf("invalid belongsTo column %q", t.BelongsTo.Column)
		}
		if t.IsProcess() || len(t.Attachments) > 0 {
			return errors.New("a child table has no stateMachine or attachments; it follows its parent record")
		}
	}
	system := SystemColumns(app, t)
	cols := map[string]string{}
	for name, typ := range system {
		cols[name] = typ
	}
	for _, c := range t.Columns {
		// status and org_unit_id stay reserved on every table, so adding a
		// state machine or cap-hr later cannot collide with a business column.
		if !identifier.MatchString(c.Name) || cols[c.Name] != "" || c.Name == "org_unit_id" || c.Name == "status" || !oneOf(c.Type, "uuid", "text", "date", "numeric(14,2)", "numeric(4,2)") {
			return fmt.Errorf("invalid, duplicate or system column %q", c.Name)
		}
		cols[c.Name] = c.Type
		if c.Check != "" && !validCheck(c) {
			return fmt.Errorf("unsupported check expression on column %q", c.Name)
		}
	}

	// A foreign key references a shared table or another table of this
	// application, never a table of another application.
	foreign := map[string]string{}
	for _, f := range t.ForeignKeys {
		if system[f.Column] != "" || cols[f.Column] != "uuid" || foreign[f.Column] != "" {
			return fmt.Errorf("invalid foreign key column %q: must be a business uuid column referenced once", f.Column)
		}
		if target, ok := SharedTables[f.References]; ok && target.Referenceable {
			if target.Capability != "" && !app.UsesCapability(target.Capability) {
				return fmt.Errorf("foreign key %q references %s, which requires capability %s", f.Column, f.References, target.Capability)
			}
		} else if !tables[f.References] {
			return fmt.Errorf("foreign key on %q references unsupported table %q", f.Column, f.References)
		}
		foreign[f.Column] = f.References
	}

	// Access rules.
	for _, group := range []struct {
		op     string
		grants []AccessRule
	}{{"select", t.Access.Select}, {"insert", t.Access.Insert}, {"update", t.Access.Update}} {
		op := group.op
		if len(group.grants) == 0 && op != "update" {
			return fmt.Errorf("missing access.%s rules", op)
		}
		seen := map[string]bool{}
		for _, p := range group.grants {
			if !identifier.MatchString(p.Role) || seen[p.Role] || !uniqueNames(p.Columns, identifier) {
				return fmt.Errorf("invalid permission rule for role %q in %s", p.Role, op)
			}
			if err := requireRole("access."+op, p.Role); err != nil {
				return err
			}
			seen[p.Role] = true
			if parent != nil {
				// A child rule names a role and columns; which rows it reaches
				// is decided by the parent record.
				if p.Scope != "" || p.RecordFilter != nil || len(p.Presets) > 0 {
					return fmt.Errorf("access.%s rule for %q on a child table cannot declare a scope, recordFilter or presets", op, p.Role)
				}
				if err := requireParentRule(parent, op, p.Role); err != nil {
					return err
				}
			} else if err := validateScope(app, op, p); err != nil {
				return err
			}
			for _, c := range p.Columns {
				if cols[c] == "" {
					return fmt.Errorf("unknown permission column %q", c)
				}
				if op != "select" && system[c] != "" {
					return fmt.Errorf("system column %q cannot be writable in %s", c, op)
				}
				if _, ok := p.Presets[c]; ok {
					return fmt.Errorf("preset column %q cannot also be in writable columns", c)
				}
			}
			if op != "insert" && len(p.Presets) > 0 {
				return errors.New("presets are only supported on insert")
			}
			for c, v := range p.Presets {
				if system[c] != "" || cols[c] != "uuid" || v != "X-Hasura-User-Id" {
					return fmt.Errorf("unsupported preset on column %q", c)
				}
			}
			if err := validateFilter(p.RecordFilter, cols, 0); err != nil {
				return err
			}
		}
	}

	seenDelete := map[string]bool{}
	for _, d := range t.Access.Delete {
		if parent == nil {
			return errors.New("access.delete is allowed on child tables only")
		}
		if seenDelete[d.Role] {
			return fmt.Errorf("duplicate access.delete rule for role %q", d.Role)
		}
		seenDelete[d.Role] = true
		if err := requireRole("access.delete", d.Role); err != nil {
			return err
		}
		if err := requireParentRule(parent, "delete", d.Role); err != nil {
			return err
		}
	}

	// State machine: only a process table has one.
	own := map[string]bool{}
	if sm := t.StateMachine; sm != nil {
		if !statePattern.MatchString(sm.Initial) || !uniqueNames(sm.EditableStates, statePattern) || !uniqueNames(sm.TerminalStates, statePattern) || len(sm.Commands) == 0 {
			return errors.New("invalid stateMachine definition")
		}
		for _, c := range sm.Commands {
			if !identifier.MatchString(c.Name) || !statePattern.MatchString(c.To) || !uniqueNames(c.From, statePattern) || !uniqueNames(c.AllowedRoles, identifier) {
				return fmt.Errorf("invalid command %q", c.Name)
			}
			if commands[c.Name] {
				return fmt.Errorf("duplicate command %q: command names are unique within an application", c.Name)
			}
			commands[c.Name] = true
			own[c.Name] = true
			for _, st := range c.From {
				if oneOf(st, sm.TerminalStates...) {
					return fmt.Errorf("terminal state %q cannot have outgoing command %q", st, c.Name)
				}
			}
			for _, r := range c.AllowedRoles {
				if err := requireRole(fmt.Sprintf("command %q", c.Name), r); err != nil {
					return err
				}
			}
			if err := validateFilter(c.RecordFilter, cols, 0); err != nil {
				return err
			}
		}
		states := map[string]bool{}
		for _, st := range States(*sm) {
			states[st] = true
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
	}

	// Attachments must be clean for commands, so only a process table has them.
	attachments := map[string]bool{}
	for _, at := range t.Attachments {
		if foreign[at.Column] != "core.files" || attachments[at.Column] || !uniqueNames(at.RequireCleanOnCommands, identifier) {
			return fmt.Errorf("invalid attachment declaration on column %q: it must be a foreign key to core.files", at.Column)
		}
		attachments[at.Column] = true
		for _, c := range at.RequireCleanOnCommands {
			if !own[c] {
				return fmt.Errorf("attachment %q references unknown command %q", at.Column, c)
			}
		}
	}
	for column, target := range foreign {
		if target == "core.files" && !attachments[column] {
			return fmt.Errorf("file column %q must be declared in attachments", column)
		}
	}

	// UI metadata.
	ui := t.UI
	if !uniqueNames(ui.ListColumns, identifier) || len(ui.FormFields) == 0 {
		return errors.New("invalid ui: listColumns and formFields are required")
	}
	if t.IsProcess() != (len(ui.Actions) > 0) || (len(ui.Actions) > 0 && !uniqueNames(ui.Actions, identifier)) {
		return errors.New("invalid ui.actions: a process table lists its commands, other tables list none")
	}
	for _, c := range ui.ListColumns {
		if cols[c] == "" {
			return fmt.Errorf("unknown ui.listColumns column %q", c)
		}
	}
	for _, c := range ui.Actions {
		if !own[c] {
			return fmt.Errorf("unknown ui.actions command %q", c)
		}
	}
	fields := map[string]bool{}
	for _, f := range ui.FormFields {
		if cols[f.Name] == "" || system[f.Name] != "" || fields[f.Name] || strings.TrimSpace(f.Label) == "" || !oneOf(f.Type, "uuid", "text", "number", "file", "date") || (f.Type == "file" && !attachments[f.Name]) {
			return fmt.Errorf("invalid ui.formFields entry %q", f.Name)
		}
		fields[f.Name] = true
	}
	return nil
}

// States returns every state of the state machine: the initial state followed
// by the targets and sources of the commands, without duplicates.
func States(sm StateMachineDef) []string {
	seen := map[string]bool{}
	var out []string
	add := func(st string) {
		if !seen[st] {
			seen[st] = true
			out = append(out, st)
		}
	}
	add(sm.Initial)
	for _, c := range sm.Commands {
		for _, st := range c.From {
			add(st)
		}
		add(c.To)
	}
	return out
}

// requireParentRule checks that role can reach the parent records its child
// rule depends on: reading a child row needs a select rule on the parent,
// changing one needs an update rule.
func requireParentRule(parent *TableDef, op, role string) error {
	rules, need := parent.Access.Update, "update"
	if op == "select" {
		rules, need = parent.Access.Select, "select"
	}
	for _, r := range rules {
		if r.Role == role {
			return nil
		}
	}
	return fmt.Errorf("access.%s rule for %q needs an access.%s rule for that role on the parent table %s", op, role, need, parent.Name)
}

func validateScope(app *DXApplication, op string, p AccessRule) error {
	if op == "insert" {
		if p.Scope != "" {
			return fmt.Errorf("access.insert rule for %q cannot declare a scope; the creator and department are stamped by the database", p.Role)
		}
		return nil
	}
	switch p.Scope {
	case ScopeOwn, ScopeOrganization:
		return nil
	case ScopeDepartment:
		if !app.UsesCapability(CapHR) {
			return fmt.Errorf("access.%s rule for %q uses scope department, which requires capability cap-hr", op, p.Role)
		}
		return nil
	case "":
		return fmt.Errorf("access.%s rule for %q must declare a scope (own, department or organization)", op, p.Role)
	default:
		return fmt.Errorf("access.%s rule for %q has unknown scope %q", op, p.Role, p.Scope)
	}
}

func validCheck(c ColumnDef) bool {
	switch {
	case c.Type == "text":
		return c.Check == "length(trim("+c.Name+")) > 0"
	case strings.HasPrefix(c.Type, "numeric("):
		return c.Check == c.Name+" > 0" || c.Check == c.Name+" > 0 AND "+c.Name+" <= 24"
	default:
		return false
	}
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
					if typ != "uuid" || x != "X-Hasura-User-Id" {
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
