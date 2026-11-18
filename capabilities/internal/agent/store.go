// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package agent

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/lib/pq"
	"github.com/opendx-os/opendx-os/capabilities/internal/knowledge"
)

var uuidPattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
var _ ConversationStore = (*PostgresConversationStore)(nil)

func pageSize(n int) (int, error) {
	if n == 0 {
		return 20, nil
	}
	if n < 0 || n > 50 {
		return 0, knowledge.ErrInvalid
	}
	return n, nil
}
func validText(s string, max int) bool {
	return utf8.ValidString(s) && strings.TrimSpace(s) != "" && utf8.RuneCountInString(s) <= max && !strings.ContainsRune(s, 0)
}
func validID(s string) bool { return uuidPattern.MatchString(s) }
func mapError(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return knowledge.ErrNotFound
	}
	var p *pq.Error
	if errors.As(err, &p) {
		switch p.Code {
		case "P0400":
			return knowledge.ErrInvalid
		case "P0404":
			return knowledge.ErrNotFound
		case "P0409":
			return knowledge.ErrConflict
		}
	}
	return knowledge.ErrAuthorityUnavailable
}
func (s *PostgresConversationStore) query(ctx context.Context, dest any, query string, args ...any) error {
	if s == nil || s.DB == nil {
		return knowledge.ErrAuthorityUnavailable
	}
	var raw []byte
	if err := s.DB.QueryRowContext(ctx, query, args...).Scan(&raw); err != nil {
		return mapError(err)
	}
	if len(raw) == 0 || string(raw) == "null" {
		return knowledge.ErrAuthorityUnavailable
	}
	if err := json.Unmarshal(raw, dest); err != nil {
		return knowledge.ErrAuthorityUnavailable
	}
	return nil
}

type messageRow struct {
	Message
	Sources    []SourceRef `json:"sources"`
	Created    bool        `json:"created"`
	Completion *messageRow `json:"completion"`
}

func (m messageRow) message() Message {
	out := m.Message
	out.Sources = m.Sources
	out.Created = m.Created
	if m.Completion != nil {
		c := m.Completion.message()
		out.Completion = &c
	}
	return out
}
func (s *PostgresConversationStore) Create(ctx context.Context, a Actor, title string) (Conversation, error) {
	var out Conversation
	if !validID(a.UserID) || !validText(title, 512) {
		return out, knowledge.ErrInvalid
	}
	err := s.query(ctx, &out, `SELECT cap_ai.agent_create_conversation($1,$2)`, a.UserID, title)
	return out, err
}
func (s *PostgresConversationStore) List(ctx context.Context, a Actor, limit int, cursor string) ([]Conversation, string, error) {
	n, err := pageSize(limit)
	if err != nil || !validID(a.UserID) || (cursor != "" && !validID(cursor)) {
		return nil, "", knowledge.ErrInvalid
	}
	var before any
	if cursor != "" {
		before = cursor
	}
	var out []Conversation
	// Fetch one extra row to avoid reporting a cursor at the end of the collection.
	err = s.query(ctx, &out, `SELECT cap_ai.agent_list_conversations($1,$2,$3)`, a.UserID, n+1, before)
	if err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) > n {
		out = out[:n]
		next = out[len(out)-1].ID
	}
	return out, next, nil
}
func (s *PostgresConversationStore) Messages(ctx context.Context, a Actor, id string, limit int, after int64) ([]Message, error) {
	n, err := pageSize(limit)
	if err != nil || !validID(a.UserID) || !validID(id) || after < 0 {
		return nil, knowledge.ErrInvalid
	}
	var rows []messageRow
	if err = s.query(ctx, &rows, `SELECT cap_ai.agent_get_messages($1,$2,$3,$4)`, a.UserID, id, n, after); err != nil {
		return nil, err
	}
	out := make([]Message, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.message())
	}
	return out, nil
}
func (s *PostgresConversationStore) BeginTurn(ctx context.Context, a Actor, id, key, query string) (Message, error) {
	if !validID(a.UserID) || !validID(id) || !validText(key, 200) || !validText(query, 2000) {
		return Message{}, knowledge.ErrInvalid
	}
	hash := sha256.Sum256([]byte(query))
	var row messageRow
	err := s.query(ctx, &row, `SELECT cap_ai.agent_begin_turn($1,$2,$3,$4,$5)`, a.UserID, id, key, hex.EncodeToString(hash[:]), query)
	return row.message(), err
}
func (s *PostgresConversationStore) FinishTurn(ctx context.Context, a Actor, id string, result TurnResult) (Message, error) {
	if !validID(a.UserID) || !validID(id) || len(result.Sources) > 100 {
		return Message{}, knowledge.ErrInvalid
	}
	sources := result.Sources
	if sources == nil {
		sources = []SourceRef{}
	}
	payload, err := json.Marshal(struct {
		Response TurnResult  `json:"response"`
		Sources  []SourceRef `json:"sources"`
	}{result, sources})
	if err != nil {
		return Message{}, knowledge.ErrInvalid
	}
	var row messageRow
	err = s.query(ctx, &row, `SELECT cap_ai.agent_finish_turn($1,$2,$3::jsonb)`, a.UserID, id, string(payload))
	return row.message(), err
}
func (s *PostgresConversationStore) Delete(ctx context.Context, a Actor, id string) error {
	if !validID(a.UserID) || !validID(id) {
		return knowledge.ErrInvalid
	}
	var out struct {
		Deleted bool `json:"deleted"`
	}
	if err := s.query(ctx, &out, `SELECT cap_ai.agent_delete_conversation($1,$2)`, a.UserID, id); err != nil {
		return err
	}
	if !out.Deleted {
		return knowledge.ErrAuthorityUnavailable
	}
	return nil
}
