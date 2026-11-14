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

// Package authz resolves what an authenticated caller may act as (ADR 0007).
// Keycloak only says who the caller is; the roles they hold come from
// core.user_roles on every request, so granting or revoking a role takes
// effect within the cache lifetime, without a new token.
package authz

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"time"

	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
)

// ErrUnauthenticated means the token is missing or invalid, or the account is
// disabled.
var ErrUnauthenticated = errors.New("unauthenticated")

// Grant is one role a user holds, with its owner (an application, a
// capability, or neither for platform roles).
type Grant struct {
	Role        string `json:"name"`
	AppID       string `json:"app_id,omitempty"`
	CapID       string `json:"cap_id,omitempty"`
	Description string `json:"description"`
}

// Session is an authenticated caller and the roles they hold now.
type Session struct {
	User   auth.Principal
	Grants []Grant
}

// Holds reports whether the session holds role.
func (s *Session) Holds(role string) bool {
	for _, g := range s.Grants {
		if g.Role == role {
			return true
		}
	}
	return false
}

// Store mirrors identities into core.users and reads role grants.
type Store interface {
	// Mirror records the principal in core.users (id = token subject) and
	// returns the account status. Only email and display name are written;
	// organizational data lives elsewhere and is never touched.
	Mirror(ctx context.Context, p auth.Principal) (status string, err error)
	// Grants returns the roles the user holds that are in force: platform
	// roles, roles of Ready applications and roles of enabled capabilities.
	Grants(ctx context.Context, userID string) ([]Grant, error)
}

// Resolver turns a bearer token into a Session.
type Resolver struct {
	authn auth.Authenticator
	store Store
	ttl   time.Duration
	now   func() time.Time

	mu    sync.Mutex
	cache map[string]cached
}

type cached struct {
	session Session
	expires time.Time
}

// maxCached bounds the cache; it is cleared when full.
const maxCached = 10000

// NewResolver returns a Resolver that reuses a user's grants for ttl.
func NewResolver(authn auth.Authenticator, store Store, ttl time.Duration) *Resolver {
	return &Resolver{authn: authn, store: store, ttl: ttl, now: time.Now, cache: map[string]cached{}}
}

// Resolve authenticates token and returns the caller's current grants. It
// returns ErrUnauthenticated for a bad token or a disabled account; any other
// error is a store failure.
func (r *Resolver) Resolve(ctx context.Context, token string) (*Session, error) {
	p, err := r.authn.Authenticate(ctx, token)
	if err != nil {
		return nil, ErrUnauthenticated
	}
	now := r.now()
	r.mu.Lock()
	hit, ok := r.cache[p.Subject]
	r.mu.Unlock()
	if ok && now.Before(hit.expires) && hit.session.User == *p {
		s := hit.session
		return &s, nil
	}

	status, err := r.store.Mirror(ctx, *p)
	if err != nil {
		return nil, err
	}
	if status != "ACTIVE" {
		return nil, ErrUnauthenticated
	}
	grants, err := r.store.Grants(ctx, p.Subject)
	if err != nil {
		return nil, err
	}
	s := Session{User: *p, Grants: grants}
	r.mu.Lock()
	if len(r.cache) >= maxCached {
		r.cache = map[string]cached{}
	}
	r.cache[p.Subject] = cached{session: s, expires: now.Add(r.ttl)}
	r.mu.Unlock()
	return &s, nil
}

// PostgresStore is the Store over the platform database. Its connection role
// (dxos_authz) may read the registry and grants and write only the identity
// columns of core.users.
type PostgresStore struct{ DB *sql.DB }

// Mirror implements Store.
func (s PostgresStore) Mirror(ctx context.Context, p auth.Principal) (string, error) {
	var status string
	err := s.DB.QueryRowContext(ctx, `
		WITH upserted AS (
		    INSERT INTO core.users (id, email, display_name) VALUES ($1, $2, $3)
		    ON CONFLICT (id) DO UPDATE SET email = EXCLUDED.email, display_name = EXCLUDED.display_name
		     WHERE core.users.email IS DISTINCT FROM EXCLUDED.email
		        OR core.users.display_name IS DISTINCT FROM EXCLUDED.display_name
		    RETURNING status
		)
		SELECT status FROM upserted
		UNION ALL
		SELECT status FROM core.users WHERE id = $1
		LIMIT 1`, p.Subject, p.Email, p.Name).Scan(&status)
	return status, err
}

// Grants implements Store.
func (s PostgresStore) Grants(ctx context.Context, userID string) ([]Grant, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT r.name, coalesce(r.app_id, ''), coalesce(r.cap_id, ''), r.description
		  FROM core.user_roles ur
		  JOIN core.roles r ON r.name = ur.role_name
		  LEFT JOIN core.apps a ON a.app_id = r.app_id
		  LEFT JOIN core.capabilities c ON c.cap_id = r.cap_id
		 WHERE ur.user_id = $1
		   AND (r.app_id IS NULL OR a.status = 'Ready')
		   AND (r.cap_id IS NULL OR c.enabled)
		 ORDER BY r.name`, userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	grants := []Grant{}
	for rows.Next() {
		var g Grant
		if err := rows.Scan(&g.Role, &g.AppID, &g.CapID, &g.Description); err != nil {
			return nil, err
		}
		grants = append(grants, g)
	}
	return grants, rows.Err()
}
