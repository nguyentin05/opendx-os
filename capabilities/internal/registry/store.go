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

package registry

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

var ErrUnavailable = errors.New("application not installed or a capability it uses is disabled")

type Store struct{ db *sql.DB }

func NewStore(db *sql.DB) *Store { return &Store{db: db} }

// Load returns the Ready definition of appID when every capability it uses is
// enabled. The definition is validated before any of its identifiers reach SQL.
func (s *Store) Load(ctx context.Context, appID string) (*AppDefinition, error) {
	if s == nil || s.db == nil || !identifier.MatchString(appID) {
		return nil, ErrUnavailable
	}
	a := AppDefinition{}
	var spec []byte
	err := s.db.QueryRowContext(ctx, `
		SELECT a.app_id, a.display_name, a.version, a.spec, a.updated_at
		  FROM core.apps a
		 WHERE a.app_id = $1 AND a.status = 'Ready'
		   AND NOT EXISTS (
		       SELECT 1 FROM core.app_capabilities ac
		         JOIN core.capabilities c ON c.cap_id = ac.cap_id
		        WHERE ac.app_id = a.app_id AND NOT c.enabled)`, appID).
		Scan(&a.AppID, &a.DisplayName, &a.Version, &spec, &a.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrUnavailable
	}
	if err != nil {
		return nil, err
	}
	if err := a.decodeSpec(spec); err != nil {
		return nil, err
	}
	return &a, nil
}

// Decode builds a validated definition from a core.apps row's identity and
// spec column.
func Decode(appID, displayName, version string, spec []byte) (*AppDefinition, error) {
	a := &AppDefinition{AppID: appID, DisplayName: displayName, Version: version}
	if err := a.decodeSpec(spec); err != nil {
		return nil, err
	}
	return a, nil
}

func (a *AppDefinition) decodeSpec(spec []byte) error {
	d := json.NewDecoder(bytes.NewReader(spec))
	d.UseNumber()
	if err := d.Decode(&a.Spec); err != nil {
		return fmt.Errorf("decode %s: %w", a.AppID, err)
	}
	if err := a.Validate(); err != nil {
		return fmt.Errorf("%s: %w", a.AppID, err)
	}
	return nil
}
