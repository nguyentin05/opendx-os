// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package knowledge

import (
	"context"
	"database/sql"
	"regexp"

	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
)

var idPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

type Policy struct{ db *sql.DB }

func NewPolicy(db *sql.DB) *Policy { return &Policy{db: db} }
func (p *Policy) CanRead(ctx context.Context, actor *auth.Identity, documentID string) (ReadScope, error) {
	var out ReadScope
	if actor == nil || !idPattern.MatchString(actor.UserID) || !idPattern.MatchString(documentID) {
		return out, ErrNotFound
	}
	if err := CheckIdentity(ctx, p.db, actor); err != nil {
		return out, concealAuthority(err)
	}
	err := decodeRow(p.db.QueryRowContext(ctx, `SELECT cap_ai.read_document($1,$2,$3)`, actor.UserID, actor.Role, documentID), &out)
	return out, err
}

func concealAuthority(err error) error {
	if err == auth.ErrUnauthenticated || err == auth.ErrForbidden {
		return ErrNotFound
	}
	return ErrAuthorityUnavailable
}
