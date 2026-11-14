// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
package knowledge

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/lib/pq"
)

type Store struct{ db *sql.DB }

func NewStore(db *sql.DB) *Store { return &Store{db: db} }
func decodeRow(row *sql.Row, out any) error {
	var raw []byte
	err := row.Scan(&raw)
	if err != nil {
		return dbError(err)
	}
	if len(raw) == 0 || string(raw) == "null" {
		return ErrNotFound
	}
	if err = json.Unmarshal(raw, out); err != nil {
		return ErrAuthorityUnavailable
	}
	return nil
}
func dbError(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	var p *pq.Error
	if errors.As(err, &p) {
		switch p.Code {
		case "P0404":
			return ErrNotFound
		case "P0409":
			return ErrConflict
		case "P0415":
			return ErrUnsupported
		case "P0422":
			return ErrUnclean
		case "P0400":
			return ErrInvalid
		case "P0410":
			return ErrStaleLease
		}
	}
	return ErrAuthorityUnavailable
}
func (s *Store) ping(ctx context.Context) error {
	if s == nil || s.db == nil {
		return ErrAuthorityUnavailable
	}
	return s.db.PingContext(ctx)
}
