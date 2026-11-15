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

// Package hr is the cap-hr service: the organization's departments and who
// works in them. Everyone reads their own place in the organization; users
// holding role_hr_admin maintain it. Changes go through audited database
// functions (platform/postgres/capabilities/cap_hr.sql).
package hr

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/lib/pq"
)

// AdminRole maintains departments and staff.
const AdminRole = "role_hr_admin"

// Errors a caller can act on.
var (
	// ErrNotFound: the department or user does not exist.
	ErrNotFound = errors.New("not found")
	// ErrConflict carries a message for the caller: a duplicate code, a
	// department that would become its own ancestor, a blank value.
	ErrConflict = errors.New("conflict")
)

// conflict wraps ErrConflict with a message safe to show the caller.
type conflict struct{ message string }

func (c conflict) Error() string        { return c.message }
func (c conflict) Is(target error) bool { return target == ErrConflict }

// OrgUnit is a department.
type OrgUnit struct {
	ID         string  `json:"id"`
	ParentID   *string `json:"parent_id"`
	Code       string  `json:"code"`
	Name       string  `json:"name"`
	StaffCount int     `json:"staff_count"`
}

// Member is a user's place in a department.
type Member struct {
	UserID      string    `json:"user_id"`
	DisplayName string    `json:"display_name"`
	Email       string    `json:"email"`
	OrgUnitID   string    `json:"org_unit_id"`
	Position    *string   `json:"position"`
	JoinedAt    time.Time `json:"joined_at"`
}

// Store reads and changes the organization as the dxos_hr connection.
type Store struct{ DB *sql.DB }

// OrgUnits returns every department, parents before children where possible.
func (s Store) OrgUnits(ctx context.Context) ([]OrgUnit, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT u.id::text, u.parent_id::text, u.code, u.name,
		       (SELECT count(*) FROM cap_hr.staff st WHERE st.org_unit_id = u.id)
		  FROM cap_hr.org_units u
		 ORDER BY u.parent_id IS NOT NULL, u.code`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	units := []OrgUnit{}
	for rows.Next() {
		var u OrgUnit
		if err := rows.Scan(&u.ID, &u.ParentID, &u.Code, &u.Name, &u.StaffCount); err != nil {
			return nil, err
		}
		units = append(units, u)
	}
	return units, rows.Err()
}

// Staff returns the members of orgUnit, or of every department when it is empty.
func (s Store) Staff(ctx context.Context, orgUnit string) ([]Member, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT st.user_id::text, u.display_name, u.email, st.org_unit_id::text, st.position, st.joined_at
		  FROM cap_hr.staff st
		  JOIN core.users u ON u.id = st.user_id
		 WHERE $1 = '' OR st.org_unit_id::text = $1
		 ORDER BY lower(u.display_name), st.user_id`, strings.ToLower(orgUnit))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	staff := []Member{}
	for rows.Next() {
		var m Member
		if err := rows.Scan(&m.UserID, &m.DisplayName, &m.Email, &m.OrgUnitID, &m.Position, &m.JoinedAt); err != nil {
			return nil, err
		}
		staff = append(staff, m)
	}
	return staff, rows.Err()
}

// Membership returns the user's place in the organization, or nil when they
// have no department.
func (s Store) Membership(ctx context.Context, user string) (*Member, error) {
	var m Member
	err := s.DB.QueryRowContext(ctx, `
		SELECT st.user_id::text, u.display_name, u.email, st.org_unit_id::text, st.position, st.joined_at
		  FROM cap_hr.staff st
		  JOIN core.users u ON u.id = st.user_id
		 WHERE st.user_id = $1`, user).Scan(&m.UserID, &m.DisplayName, &m.Email, &m.OrgUnitID, &m.Position, &m.JoinedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// OrgUnit returns one department.
func (s Store) OrgUnit(ctx context.Context, id string) (*OrgUnit, error) {
	var u OrgUnit
	err := s.DB.QueryRowContext(ctx, `SELECT id::text, parent_id::text, code, name FROM cap_hr.org_units WHERE id = $1`, id).
		Scan(&u.ID, &u.ParentID, &u.Code, &u.Name)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// CreateOrgUnit adds a department and returns its id.
func (s Store) CreateOrgUnit(ctx context.Context, actor, code, name string, parent *string) (string, error) {
	var id string
	err := s.DB.QueryRowContext(ctx, `SELECT cap_hr.create_org_unit($1, $2, $3, $4, $5)::text`, actor, AdminRole, code, name, parent).Scan(&id)
	return id, explain(err)
}

// UpdateOrgUnit replaces a department's code, name and parent.
func (s Store) UpdateOrgUnit(ctx context.Context, actor string, u OrgUnit) error {
	if _, err := s.OrgUnit(ctx, u.ID); err != nil {
		return err
	}
	_, err := s.DB.ExecContext(ctx, `SELECT cap_hr.update_org_unit($1, $2, $3, $4, $5, $6)`, actor, AdminRole, u.ID, u.Code, u.Name, u.ParentID)
	return explain(err)
}

// AssignStaff places user in orgUnit with a position; a user who already has
// a department is moved.
func (s Store) AssignStaff(ctx context.Context, actor, user, orgUnit string, position *string) error {
	_, err := s.DB.ExecContext(ctx, `SELECT cap_hr.assign_staff($1, $2, $3, $4, $5)`, actor, AdminRole, user, orgUnit, position)
	return explain(err)
}

// RemoveStaff takes user out of their department; ErrNotFound when they have none.
func (s Store) RemoveStaff(ctx context.Context, actor, user string) error {
	var removed bool
	if err := s.DB.QueryRowContext(ctx, `SELECT cap_hr.remove_staff($1, $2, $3)`, actor, AdminRole, user).Scan(&removed); err != nil {
		return explain(err)
	}
	if !removed {
		return ErrNotFound
	}
	return nil
}

// explain turns the database's refusals into errors the caller can act on.
func explain(err error) error {
	var pqErr *pq.Error
	if !errors.As(err, &pqErr) {
		return err
	}
	switch {
	case pqErr.Code == "23505" && strings.Contains(pqErr.Constraint, "code"):
		return conflict{"a department with this code already exists"}
	case pqErr.Code == "23503" && strings.Contains(pqErr.Constraint, "parent"):
		return conflict{"the parent department does not exist"}
	case pqErr.Code == "23503" && strings.Contains(pqErr.Constraint, "org_unit"):
		return ErrNotFound
	case pqErr.Code == "23503" && strings.Contains(pqErr.Constraint, "user"):
		return ErrNotFound
	case pqErr.Code == "23514":
		return conflict{"code, name and position must not be blank, and a department cannot be its own parent"}
	case strings.Contains(pqErr.Message, "cannot be its own ancestor"):
		return conflict{"a department cannot be placed under itself or under one of its own sub-departments"}
	}
	return err
}
