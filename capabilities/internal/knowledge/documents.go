// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
package knowledge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
	"strings"
)

func (s *Store) Register(ctx context.Context, actor *auth.Identity, key string, req RegisterRequest) (Registration, error) {
	return s.register(ctx, actor, key, "", 0, req)
}
func (s *Store) ReplaceVersion(ctx context.Context, actor *auth.Identity, key, documentID string, expectedRevision int64, req RegisterRequest) (Registration, error) {
	if !idPattern.MatchString(documentID) || expectedRevision < 1 {
		return Registration{}, ErrInvalid
	}
	return s.register(ctx, actor, key, documentID, expectedRevision, req)
}
func (s *Store) register(ctx context.Context, actor *auth.Identity, key, doc string, revision int64, req RegisterRequest) (Registration, error) {
	var out Registration
	if !idPattern.MatchString(req.FileID) || req.CollectionID != "" && !idPattern.MatchString(req.CollectionID) || len(key) < 1 || len(key) > 200 || strings.TrimSpace(key) == "" || len(req.ParserProfile) < 1 || len(req.ParserProfile) > 200 || len(req.Title) > 512 {
		return out, ErrInvalid
	}
	if err := CheckIdentity(ctx, s.db, actor); err != nil {
		return out, concealAuthority(err)
	}
	raw, err := json.Marshal(req)
	if err != nil {
		return out, ErrInvalid
	}
	hash := sha256.Sum256(raw)
	var docArg any
	if doc != "" {
		docArg = doc
	}
	err = decodeRow(s.db.QueryRowContext(ctx, `SELECT cap_ai.register_document($1,$2,$3,$4,$5::jsonb,$6,$7)`, actor.UserID, actor.Role, key, hex.EncodeToString(hash[:]), string(raw), docArg, revision), &out)
	return out, err
}
