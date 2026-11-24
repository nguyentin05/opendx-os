// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package knowledge

import (
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/lib/pq"
	"github.com/opendx-os/opendx-os/capabilities/internal/ai/models"
)

// Store carries the fixed deployment profiles the installation was configured
// with. There is no resolver: a purpose is either configured or unavailable.
type Store struct {
	gateway                       *models.Gateway
	embedProfile, generateProfile models.Profile
	dispatcherStore               *Store
	dispatcherSubject             string
	db                            *sql.DB
	personalEnabled               bool
	packageEnabled                bool
	packageRoot                   string
	fileURL, artifactRoot         string
}

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
	if p, ok := errors.AsType[*pq.Error](err); ok {
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
