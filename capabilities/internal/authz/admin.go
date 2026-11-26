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

package authz

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/lib/pq"
)

// Role administration (ADR 0007): who holds which role is platform data, and
// a platform operator changes it here. Every change is audited by the database
// functions that make it.
const (
	// OperatorRole may assign and revoke roles.
	OperatorRole = "role_operator"
	// workflowRole belongs to the workflow service account and is never
	// assigned to a person.
	workflowRole = "role_workflow"
)

// Errors of role administration.
var (
	// ErrNotFound means the user does not exist, or the role does not exist
	// or is not in force (its application is not installed, or its capability
	// is disabled).
	ErrNotFound = errors.New("user or role not found")
	// ErrReserved means the change is not allowed for anyone.
	ErrReserved = errors.New("reserved role assignment")
)

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// User is an account with the roles it holds, as an operator sees it.
type User struct {
	ID          string   `json:"id"`
	Email       string   `json:"email"`
	DisplayName string   `json:"display_name"`
	Status      string   `json:"status"`
	Roles       []string `json:"roles"`
}

// Directory is what role administration needs from the platform database.
type Directory interface {
	// Roles returns every role in force, with its owner.
	Roles(ctx context.Context) ([]Grant, error)
	// Users returns every mirrored account with the roles it holds.
	Users(ctx context.Context) ([]User, error)
	// GrantRole gives user a role in force; it returns ErrNotFound for an
	// unknown user or a role that is not in force. Granting a held role is a no-op.
	GrantRole(ctx context.Context, actor, user, role string) error
	// RevokeRole takes a role from user; revoking a role that is not held is a no-op.
	RevokeRole(ctx context.Context, actor, user, role string) error
	// CreateUser records an account the identity provider just created (id is
	// its sub), on behalf of actor, so it can hold roles before signing in.
	CreateUser(ctx context.Context, actor, id, email, displayName string) error
	// BootstrapOperator makes user the platform operator when nobody is one
	// yet, and reports whether it did.
	BootstrapOperator(ctx context.Context, user string) (bool, error)
}

// Administer lets operators manage role assignments through dir. With
// bootstrapEmail set, the first sign-in of that account becomes the platform
// operator while there is none.
func (r *Resolver) Administer(dir Directory, bootstrapEmail string) {
	r.directory = dir
	r.bootstrapEmail = strings.TrimSpace(bootstrapEmail)
}

// forget drops a user's cached grants, so a change applies to their next request.
func (r *Resolver) forget(userID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for subject := range r.cache {
		if strings.EqualFold(subject, userID) {
			delete(r.cache, subject)
		}
	}
}

// changeRole grants (or revokes) role for user on behalf of the operator in s.
func (r *Resolver) changeRole(ctx context.Context, s *Session, user, role string, grant bool) error {
	if !uuidPattern.MatchString(user) || !roleName.MatchString(role) {
		return ErrNotFound
	}
	// The workflow role stays with the service account, and an operator cannot
	// remove their own authority: another operator has to.
	if role == workflowRole || (!grant && role == OperatorRole && strings.EqualFold(user, s.User.Subject)) {
		return ErrReserved
	}
	var err error
	if grant {
		err = r.directory.GrantRole(ctx, s.User.Subject, user, role)
	} else {
		err = r.directory.RevokeRole(ctx, s.User.Subject, user, role)
	}
	if err == nil {
		r.forget(user)
	}
	return err
}

// registerAdmin adds the operator endpoints:
//
//	GET    /api/auth/roles                          every role in force
//	GET    /api/auth/users                          accounts and the roles they hold
//	POST   /api/auth/users                          create an account (needs a provisioner)
//	PUT    /api/auth/users/{user_id}/roles/{role}   grant
//	DELETE /api/auth/users/{user_id}/roles/{role}   revoke
func registerAdmin(mux *http.ServeMux, r *Resolver) {
	operator := func(w http.ResponseWriter, req *http.Request) (*Session, bool) {
		s, ok := resolve(w, req, r)
		if !ok {
			return nil, false
		}
		if !s.Holds(OperatorRole) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "role_operator is required"})
			return nil, false
		}
		return s, true
	}
	unavailable := func(w http.ResponseWriter, err error) {
		logf("role administration failed: %v", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "authorization unavailable"})
	}
	mux.HandleFunc("GET /api/auth/roles", func(w http.ResponseWriter, req *http.Request) {
		if _, ok := operator(w, req); !ok {
			return
		}
		roles, err := r.directory.Roles(req.Context())
		if err != nil {
			unavailable(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"roles": roles})
	})
	mux.HandleFunc("GET /api/auth/users", func(w http.ResponseWriter, req *http.Request) {
		if _, ok := operator(w, req); !ok {
			return
		}
		users, err := r.directory.Users(req.Context())
		if err != nil {
			unavailable(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"users": users})
	})
	change := func(grant bool) http.HandlerFunc {
		return func(w http.ResponseWriter, req *http.Request) {
			s, ok := operator(w, req)
			if !ok {
				return
			}
			err := r.changeRole(req.Context(), s, req.PathValue("user_id"), req.PathValue("role"), grant)
			switch {
			case err == nil:
				w.WriteHeader(http.StatusNoContent)
			case errors.Is(err, ErrNotFound):
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown user, or a role that is not in force"})
			case errors.Is(err, ErrReserved):
				writeJSON(w, http.StatusConflict, map[string]string{"error": "this assignment cannot be changed here: the workflow role is reserved, and an operator cannot revoke their own role_operator"})
			default:
				unavailable(w, err)
			}
		}
	}
	mux.HandleFunc("POST /api/auth/users", func(w http.ResponseWriter, req *http.Request) {
		if s, ok := operator(w, req); ok {
			r.createAccount(w, req, s)
		}
	})
	mux.HandleFunc("PUT /api/auth/users/{user_id}/roles/{role}", change(true))
	mux.HandleFunc("DELETE /api/auth/users/{user_id}/roles/{role}", change(false))
}

// Roles implements Directory.
func (s PostgresStore) Roles(ctx context.Context) ([]Grant, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT r.name, coalesce(r.app_id, ''), coalesce(r.cap_id, ''), r.description
		  FROM core.roles r
		  LEFT JOIN core.apps a ON a.app_id = r.app_id
		  LEFT JOIN core.capabilities c ON c.cap_id = r.cap_id
		 WHERE (r.app_id IS NULL OR a.status = 'Ready')
		   AND (r.cap_id IS NULL OR c.enabled)
		 ORDER BY coalesce(r.app_id, r.cap_id, ''), r.name`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	roles := []Grant{}
	for rows.Next() {
		var g Grant
		if err := rows.Scan(&g.Role, &g.AppID, &g.CapID, &g.Description); err != nil {
			return nil, err
		}
		roles = append(roles, g)
	}
	return roles, rows.Err()
}

// Users implements Directory.
func (s PostgresStore) Users(ctx context.Context) ([]User, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT u.id::text, u.email, u.display_name, u.status,
		       coalesce(array_agg(ur.role_name ORDER BY ur.role_name) FILTER (WHERE ur.role_name IS NOT NULL), '{}')
		  FROM core.users u
		  LEFT JOIN core.user_roles ur ON ur.user_id = u.id
		 GROUP BY u.id
		 ORDER BY lower(u.display_name), u.id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	users := []User{}
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Email, &u.DisplayName, &u.Status, pq.Array(&u.Roles)); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

// GrantRole implements Directory.
func (s PostgresStore) GrantRole(ctx context.Context, actor, user, role string) error {
	var known bool
	err := s.DB.QueryRowContext(ctx, `
		SELECT EXISTS (SELECT 1 FROM core.users WHERE id = $1)
		   AND EXISTS (
		       SELECT 1 FROM core.roles r
		         LEFT JOIN core.apps a ON a.app_id = r.app_id
		         LEFT JOIN core.capabilities c ON c.cap_id = r.cap_id
		        WHERE r.name = $2
		          AND (r.app_id IS NULL OR a.status = 'Ready')
		          AND (r.cap_id IS NULL OR c.enabled))`, user, role).Scan(&known)
	if err != nil {
		return err
	}
	if !known {
		return ErrNotFound
	}
	_, err = s.DB.ExecContext(ctx, `SELECT core.grant_role($1, $2, $3, $4)`, actor, OperatorRole, user, role)
	return err
}

// RevokeRole implements Directory.
func (s PostgresStore) RevokeRole(ctx context.Context, actor, user, role string) error {
	var known bool
	if err := s.DB.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM core.users WHERE id = $1)`, user).Scan(&known); err != nil {
		return err
	}
	if !known {
		return ErrNotFound
	}
	_, err := s.DB.ExecContext(ctx, `SELECT core.revoke_role($1, $2, $3, $4)`, actor, OperatorRole, user, role)
	return err
}

// CreateUser implements Directory.
func (s PostgresStore) CreateUser(ctx context.Context, actor, id, email, displayName string) error {
	_, err := s.DB.ExecContext(ctx, `SELECT core.create_user($1, $2, $3, $4, $5)`, actor, OperatorRole, id, email, displayName)
	return err
}

// BootstrapOperator implements Directory.
func (s PostgresStore) BootstrapOperator(ctx context.Context, user string) (bool, error) {
	var made sql.NullBool
	err := s.DB.QueryRowContext(ctx, `SELECT core.bootstrap_operator($1)`, user).Scan(&made)
	return made.Bool, err
}
