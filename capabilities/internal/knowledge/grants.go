// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package knowledge

import (
	"context"
	"encoding/base64"
	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
	"strings"
)

type GrantIdentity struct {
	UserID string `json:"user_id"`
	Status string `json:"status"`
}
type GrantPage struct {
	DocumentID              string `json:"document_id"`
	Revision                int64  `json:"revision"`
	AuthorizationGeneration int64  `json:"authorization_generation"`
	Ceiling                 struct {
		Mode            string   `json:"mode"`
		AllowedRoles    []string `json:"allowedRoles"`
		GrantsManagedBy string   `json:"grantsManagedBy"`
	} `json:"ceiling"`
	PendingPublication bool            `json:"pending_publication"`
	Grants             []GrantIdentity `json:"grants"`
	NextCursor         string          `json:"next_cursor,omitempty"`
}

func (s *Store) GrantManagement(ctx context.Context, actor *auth.Identity, documentID, cursor string) (GrantPage, error) {
	var out GrantPage
	if !s.packageEnabled || !idPattern.MatchString(documentID) {
		return out, ErrNotFound
	}
	if e := CheckIdentity(ctx, s.db, actor); e != nil {
		return out, concealAuthority(e)
	}
	var after any
	if cursor != "" {
		raw, e := base64.RawURLEncoding.DecodeString(cursor)
		if e != nil || len(raw) > 300 {
			return out, ErrInvalid
		}
		parts := strings.Split(string(raw), ":")
		if len(parts) != 4 || parts[0] != actor.UserID || parts[1] != actor.Role || parts[2] != documentID || !idPattern.MatchString(parts[3]) {
			return out, ErrInvalid
		}
		after = parts[3]
	}
	if e := decodeRow(s.db.QueryRowContext(ctx, `SELECT cap_ai.grant_management($1,$2,$3,$4)`, actor.UserID, actor.Role, documentID, after), &out); e != nil {
		return GrantPage{}, e
	}
	var final GrantPage
	if e := decodeRow(s.db.QueryRowContext(ctx, `SELECT cap_ai.management_scope($1,$2,$3)`, actor.UserID, actor.Role, documentID), &final); e != nil {
		return GrantPage{}, e
	}
	if final.Revision != out.Revision || final.AuthorizationGeneration != out.AuthorizationGeneration {
		return GrantPage{}, ErrConflict
	}
	if len(out.Grants) > 50 {
		out.Grants = out.Grants[:50]
		out.NextCursor = base64.RawURLEncoding.EncodeToString([]byte(actor.UserID + ":" + actor.Role + ":" + documentID + ":" + out.Grants[49].UserID))
	}
	return out, nil
}
func (s *Store) GrantRead(ctx context.Context, actor *auth.Identity, key, documentID, targetUserID string, expectedRevision int64) error {
	return s.mutateGrant(ctx, actor, key, documentID, targetUserID, expectedRevision, false)
}
func (s *Store) RevokeRead(ctx context.Context, actor *auth.Identity, key, documentID, targetUserID string, expectedRevision int64) error {
	return s.mutateGrant(ctx, actor, key, documentID, targetUserID, expectedRevision, true)
}
func (s *Store) mutateGrant(ctx context.Context, actor *auth.Identity, key, doc, target string, revision int64, revoke bool) error {
	if !s.packageEnabled || !idPattern.MatchString(doc) || !idPattern.MatchString(target) {
		return ErrNotFound
	}
	if strings.TrimSpace(key) == "" || len(key) > 200 || revision < 1 {
		return ErrInvalid
	}
	if e := CheckIdentity(ctx, s.db, actor); e != nil {
		return concealAuthority(e)
	}
	var ok bool
	return dbErrorOrNil(s.db.QueryRowContext(ctx, `SELECT cap_ai.mutate_read_grant($1,$2,$3,$4,$5,$6,$7)`, actor.UserID, actor.Role, key, doc, target, revision, revoke).Scan(&ok))
}
