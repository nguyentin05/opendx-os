// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package knowledge

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
	"time"
)

type DocumentSummary struct {
	DocumentID           string    `json:"document_id"`
	CollectionID         string    `json:"collection_id"`
	Title                string    `json:"title"`
	Revision             int64     `json:"revision"`
	Status               string    `json:"status"`
	ActiveVersionID      string    `json:"active_version_id,omitempty"`
	DesiredVersionID     string    `json:"desired_version_id,omitempty"`
	ActiveVersionStatus  string    `json:"active_version_status,omitempty"`
	DesiredVersionStatus string    `json:"desired_version_status,omitempty"`
	Warnings             []string  `json:"warnings"`
	CreatedAt            time.Time `json:"created_at"`
	UpdatedAt            time.Time `json:"updated_at"`
}
type DocumentPage struct {
	Documents  []DocumentSummary `json:"documents"`
	NextCursor string            `json:"next_cursor,omitempty"`
}
type pageCursor struct{ Actor, App, After string }

func (s *Store) Delete(ctx context.Context, actor *auth.Identity, key, documentID string, expectedRevision int64) error {
	if !idPattern.MatchString(documentID) || expectedRevision < 1 || len(key) < 1 || len(key) > 200 {
		return ErrInvalid
	}
	if err := CheckIdentity(ctx, s.db, actor); err != nil {
		return concealAuthority(err)
	}
	var ok bool
	return dbErrorOrNil(s.db.QueryRowContext(ctx, `SELECT cap_ai.delete_document($1,$2,$3,$4,$5)`, actor.UserID, actor.Role, key, documentID, expectedRevision).Scan(&ok))
}
func (s *Store) GetDocument(ctx context.Context, actor *auth.Identity, documentID string) (DocumentSummary, error) {
	var result DocumentSummary
	if _, err := NewPolicy(s.db).CanRead(ctx, actor, documentID); err != nil {
		return result, err
	}
	err := decodeRow(s.db.QueryRowContext(ctx, `SELECT cap_ai.document_metadata($1,$2,$3)`, actor.UserID, actor.Role, documentID), &result)
	return result, err
}
func (s *Store) ListDocuments(ctx context.Context, actor *auth.Identity, cursor string, limit int) (DocumentPage, error) {
	out := DocumentPage{Documents: []DocumentSummary{}}
	if limit < 1 || limit > 100 || len(cursor) > 2048 {
		return out, ErrInvalid
	}
	if err := CheckIdentity(ctx, s.db, actor); err != nil {
		return out, concealAuthority(err)
	}
	var c pageCursor
	var after any
	if cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil || json.Unmarshal(raw, &c) != nil || c.Actor != actor.UserID || !idPattern.MatchString(c.After) {
			return out, ErrInvalid
		}
		after = c.After
	}
	var result struct {
		AppID     string            `json:"app_id"`
		Documents []DocumentSummary `json:"documents"`
	}
	err := decodeRow(s.db.QueryRowContext(ctx, `SELECT cap_ai.list_documents($1,$2,$3,$4)`, actor.UserID, actor.Role, after, limit), &result)
	if err != nil {
		return out, err
	}
	if cursor != "" && c.App != result.AppID {
		return out, ErrInvalid
	}
	out.Documents = result.Documents
	if len(out.Documents) > limit {
		out.Documents = out.Documents[:limit]
		raw, _ := json.Marshal(pageCursor{Actor: actor.UserID, App: result.AppID, After: out.Documents[limit-1].DocumentID})
		out.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
	}
	return out, nil
}
func (s *Store) Collections(ctx context.Context, actor *auth.Identity) ([]map[string]any, error) {
	out := []map[string]any{}
	if err := CheckIdentity(ctx, s.db, actor); err != nil {
		return nil, concealAuthority(err)
	}
	err := decodeRow(s.db.QueryRowContext(ctx, `SELECT cap_ai.get_collections($1,$2)`, actor.UserID, actor.Role), &out)
	return out, err
}
func (s *Store) Ingestion(ctx context.Context, actor *auth.Identity, jobID string) (map[string]any, error) {
	var out map[string]any
	if !idPattern.MatchString(jobID) {
		return nil, ErrNotFound
	}
	if err := CheckIdentity(ctx, s.db, actor); err != nil {
		return nil, concealAuthority(err)
	}
	err := decodeRow(s.db.QueryRowContext(ctx, `SELECT cap_ai.ingestion_metadata($1,$2,$3)`, actor.UserID, actor.Role, jobID), &out)
	return out, err
}
func (s *Store) Recover(ctx context.Context, now time.Time) error {
	var ok bool
	return dbErrorOrNil(s.db.QueryRowContext(ctx, `SELECT cap_ai.recover_jobs($1)`, now).Scan(&ok))
}
