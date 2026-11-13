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
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

var identifier = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
var statePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)

// Validate checks what the services rely on at run time. The operator has
// already validated the whole manifest before marking it Ready; this guards
// the identifiers that reach SQL and the rules the services evaluate, so a
// corrupted registry row fails closed instead of being trusted.
func (a *AppDefinition) Validate() error {
	s := &a.Spec
	if !identifier.MatchString(a.AppID) {
		return errors.New("invalid application id")
	}
	if !strings.HasPrefix(s.DomainTable.Name, "domain.") || !identifier.MatchString(strings.TrimPrefix(s.DomainTable.Name, "domain.")) {
		return errors.New("invalid domain table")
	}
	cols := a.ColumnTypes()
	for _, c := range s.DomainTable.Columns {
		if !identifier.MatchString(c.Name) || !oneOf(c.Type, "uuid", "text", "date", "numeric(14,2)", "numeric(4,2)") {
			return fmt.Errorf("invalid column %q", c.Name)
		}
	}
	if len(cols) != len(systemColumns(a))+len(s.DomainTable.Columns) {
		return errors.New("duplicate or reserved column")
	}
	roles := map[string]bool{}
	for _, r := range s.Roles {
		if !identifier.MatchString(r.Name) || roles[r.Name] {
			return errors.New("invalid role")
		}
		roles[r.Name] = true
	}
	for _, rules := range [][]Permission{s.DomainAccess.Select, s.DomainAccess.Insert, s.DomainAccess.Update} {
		for _, p := range rules {
			if !roles[p.Role] {
				return fmt.Errorf("access rule for undeclared role %q", p.Role)
			}
			if !oneOf(p.Scope, "", ScopeOwn, ScopeDepartment, ScopeOrganization) ||
				p.Scope == ScopeDepartment && !a.UsesCapability("cap-hr") {
				return fmt.Errorf("invalid scope %q", p.Scope)
			}
			for _, c := range p.Columns {
				if cols[c] == "" {
					return fmt.Errorf("unknown column %q", c)
				}
			}
			if err := validateFilter(p.RecordFilter, cols, 0); err != nil {
				return err
			}
		}
	}
	sm := s.StateMachine
	if !statePattern.MatchString(sm.Initial) {
		return errors.New("invalid initial state")
	}
	commands := map[string]bool{}
	for _, c := range sm.Commands {
		if !identifier.MatchString(c.Name) || commands[c.Name] || !statePattern.MatchString(c.To) || len(c.From) == 0 || len(c.AllowedRoles) == 0 {
			return fmt.Errorf("invalid command %q", c.Name)
		}
		commands[c.Name] = true
		for _, st := range c.From {
			if !statePattern.MatchString(st) {
				return fmt.Errorf("invalid state in command %q", c.Name)
			}
		}
		for _, r := range c.AllowedRoles {
			if !roles[r] {
				return fmt.Errorf("command %q allows undeclared role %q", c.Name, r)
			}
		}
		if err := validateFilter(c.RecordFilter, cols, 0); err != nil {
			return err
		}
	}
	files := map[string]bool{}
	for _, fk := range s.DomainTable.ForeignKeys {
		if fk.References == "core.files" {
			files[fk.Column] = true
		}
	}
	for _, at := range s.Attachments {
		if cols[at.Column] != "uuid" || !files[at.Column] {
			return fmt.Errorf("attachment %q is not a core.files reference", at.Column)
		}
		for _, c := range at.RequireCleanOnCommands {
			if !commands[c] {
				return fmt.Errorf("attachment %q names unknown command %q", at.Column, c)
			}
		}
	}
	steps := map[string]bool{}
	for _, w := range s.Workflows {
		if !identifier.MatchString(w.ID) {
			return errors.New("invalid workflow id")
		}
		for _, e := range w.Effects {
			if !identifier.MatchString(e.StepName) || steps[e.StepName] || e.Kind != "create_work_item" ||
				strings.TrimSpace(e.Fields.Title) == "" || !oneOf(e.Fields.Priority, "LOW", "MEDIUM", "HIGH", "CRITICAL") ||
				(e.Fields.AssigneeID != "" && !uuidPattern.MatchString(e.Fields.AssigneeID)) {
				return fmt.Errorf("invalid workflow effect %q", e.StepName)
			}
			steps[e.StepName] = true
			for _, c := range e.WhenCommands {
				if !commands[c] {
					return fmt.Errorf("effect %q names unknown command %q", e.StepName, c)
				}
			}
		}
	}
	return nil
}

// UsesCapability reports whether the application declares capability id.
func (a *AppDefinition) UsesCapability(id string) bool {
	return oneOf(id, a.Spec.Capabilities...)
}

// Table is the application's domain table, schema-qualified. Validate has
// checked it is an identifier.
func (a *AppDefinition) Table() string { return a.Spec.DomainTable.Name }

// systemColumns are the operator-managed columns of the domain table, with
// the manifest's type names; org_unit_id exists only with cap-hr.
func systemColumns(a *AppDefinition) map[string]string {
	cols := map[string]string{"id": "uuid", "created_by": "uuid", "status": "text", "version": "integer", "created_at": "timestamp", "updated_at": "timestamp"}
	if a.UsesCapability("cap-hr") {
		cols["org_unit_id"] = "uuid"
	}
	return cols
}

// ColumnTypes maps every column of the domain table to its manifest type.
func (a *AppDefinition) ColumnTypes() map[string]string {
	cols := systemColumns(a)
	for _, c := range a.Spec.DomainTable.Columns {
		cols[c.Name] = c.Type
	}
	return cols
}

func oneOf(s string, values ...string) bool {
	for _, v := range values {
		if s == v {
			return true
		}
	}
	return false
}

// validateFilter accepts the grammar the operator accepts: _and/_or of
// column comparisons with _eq/_neq against a literal, null or X-Hasura-User-Id.
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
					if typ != "uuid" || x != "X-Hasura-User-Id" {
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
