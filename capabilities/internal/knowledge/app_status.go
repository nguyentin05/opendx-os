// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
package knowledge

import (
	"context"
	"encoding/json"
	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
	"net/http"
)

type AppSource struct {
	SourceID               string `json:"source_id"`
	DocumentID             string `json:"document_id,omitempty"`
	Title                  string `json:"title,omitempty"`
	Readable               bool   `json:"readable"`
	Manageable             bool   `json:"manageable"`
	Status                 string `json:"status"`
	ActiveVersionAvailable bool   `json:"active_version_available"`
}
type AppKnowledgePage struct {
	Sources []AppSource `json:"sources"`
}

func (s *Store) AppKnowledge(ctx context.Context, actor *auth.Identity, app string) (AppKnowledgePage, error) {
	var result AppKnowledgePage
	if !s.packageEnabled {
		return result, ErrAuthorityUnavailable
	}
	if !appPattern.MatchString(app) {
		return result, ErrInvalid
	}
	if e := CheckIdentity(ctx, s.db, actor); e != nil {
		return result, concealAuthority(e)
	}
	if e := decodeRow(s.db.QueryRowContext(ctx, `SELECT cap_ai.app_knowledge($1,$2,$3)`, actor.UserID, actor.Role, app), &result); e != nil {
		return result, e
	}
	// Refresh current scopes after materializing metadata; a role/source change
	// discards the whole page rather than retaining a stale title or management UI.
	var final AppKnowledgePage
	if e := decodeRow(s.db.QueryRowContext(ctx, `SELECT cap_ai.app_knowledge($1,$2,$3)`, actor.UserID, actor.Role, app), &final); e != nil {
		return AppKnowledgePage{}, e
	}
	before, _ := json.Marshal(result)
	after, _ := json.Marshal(final)
	if string(before) != string(after) {
		return AppKnowledgePage{}, ErrConflict
	}
	return final, nil
}
func (s *Store) appKnowledgeHTTP(w http.ResponseWriter, r *http.Request) {
	actor, e := auth.ExtractIdentity(r)
	if e != nil {
		httpError(w, ErrNotFound)
		return
	}
	result, e := s.AppKnowledge(r.Context(), actor, r.URL.Query().Get("app_id"))
	if e != nil {
		httpError(w, e)
		return
	}
	writeJSON(w, 200, result)
}
