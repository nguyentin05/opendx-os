// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

// Package access authenticates cap-ai callers without granting source READ.
package access

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
)

var ErrDenied = errors.New("membership denied")
var ErrUnavailable = errors.New("membership authority unavailable")
var subjectID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
var selectedRole = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

type Principal struct {
	UserID string
	Role   string
}
type Membership interface {
	Resolve(context.Context, auth.Subject, string) (Principal, error)
}
type DBMembership struct{ db *sql.DB }

func NewDBMembership(db *sql.DB) *DBMembership { return &DBMembership{db: db} }

// Resolve always reads current primary-DB authority. Membership is not source READ.
func (m *DBMembership) Resolve(ctx context.Context, s auth.Subject, role string) (Principal, error) {
	if ctx.Err() != nil || !s.ExpiresAt.After(time.Now()) || !subjectID.MatchString(s.UserID) || !selectedRole.MatchString(role) {
		return Principal{}, ErrDenied
	}
	if m == nil || m.db == nil {
		return Principal{}, ErrUnavailable
	}
	queryCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var p Principal
	err := m.db.QueryRowContext(queryCtx, `SELECT u.id::text,r.name FROM core.users u JOIN core.user_roles ur ON ur.user_id=u.id JOIN core.roles r ON r.name=ur.role_name WHERE u.id=$1 AND u.status='ACTIVE' AND r.name=$2`, strings.ToLower(s.UserID), role).Scan(&p.UserID, &p.Role)
	if ctx.Err() != nil || !s.ExpiresAt.After(time.Now()) {
		return Principal{}, ErrDenied
	}
	if errors.Is(err, sql.ErrNoRows) {
		return Principal{}, ErrDenied
	}
	if err != nil {
		return Principal{}, ErrUnavailable
	}
	return p, nil
}
