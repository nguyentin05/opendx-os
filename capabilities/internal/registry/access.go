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
	"context"
	"database/sql"
	"errors"
	"strings"
)

// Viewer is the caller as access rules see them.
type Viewer struct {
	UserID string
	Role   string
	// OrgUnit is the caller's department (cap_hr.staff), or "" when they
	// have none or the application does not use departments.
	OrgUnit string
}

// InScope reports whether row lies within scope for v, with the same meaning
// as the filters the operator generates for Hasura: own records
// (created_by), records of the caller's department (org_unit_id), or any.
func InScope(scope string, row map[string]any, v Viewer) bool {
	switch scope {
	case ScopeOwn:
		owner, _ := row["created_by"].(string)
		return owner != "" && strings.EqualFold(owner, v.UserID)
	case ScopeDepartment:
		unit, _ := row["org_unit_id"].(string)
		return v.OrgUnit != "" && unit != "" && strings.EqualFold(unit, v.OrgUnit)
	default:
		return true
	}
}

// CanSee reports whether one of the select rules for v.Role admits row: its
// scope and its record filter both hold. With column set, the rule must also
// expose that column.
func (a *AppDefinition) CanSee(v Viewer, row map[string]any, column string) (bool, error) {
	types := a.ColumnTypes()
	for _, p := range a.Spec.DomainAccess.Select {
		if p.Role != v.Role || (column != "" && !oneOf(column, p.Columns...)) || !InScope(p.Scope, row, v) {
			continue
		}
		ok, err := Matches(p.RecordFilter, row, v.UserID, types)
		if err != nil {
			return false, err
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}

// UsesDepartmentScope reports whether a select rule for role is department
// scoped, so the caller's department must be looked up.
func (a *AppDefinition) UsesDepartmentScope(role string) bool {
	for _, p := range a.Spec.DomainAccess.Select {
		if p.Role == role && p.Scope == ScopeDepartment {
			return true
		}
	}
	return false
}

// HasRole reports whether role belongs to any access rule of the application.
func (a *AppDefinition) HasRole(role string) bool {
	for _, rules := range [][]Permission{a.Spec.DomainAccess.Select, a.Spec.DomainAccess.Insert, a.Spec.DomainAccess.Update} {
		for _, p := range rules {
			if p.Role == role {
				return true
			}
		}
	}
	return false
}

type rowQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Viewer returns the caller as access rules see them, looking up their
// department only when a rule for role needs it.
func (a *AppDefinition) Viewer(ctx context.Context, q rowQuerier, userID, role string) (Viewer, error) {
	v := Viewer{UserID: userID, Role: role}
	if !a.UsesDepartmentScope(role) {
		return v, nil
	}
	err := q.QueryRowContext(ctx, `SELECT org_unit_id::text FROM cap_hr.staff WHERE user_id = $1`, userID).Scan(&v.OrgUnit)
	if errors.Is(err, sql.ErrNoRows) {
		return v, nil
	}
	return v, err
}
