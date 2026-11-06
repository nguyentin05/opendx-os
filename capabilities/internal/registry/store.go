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
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/lib/pq"
)

var ErrUnavailable = errors.New("active application or dependency unavailable")

type Store struct{ db *sql.DB }

func NewStore(db *sql.DB) *Store { return &Store{db: db} }

// Load reads a coherent Ready snapshot and enabled dependencies in one SQL statement.
// It validates every runtime identifier before returning it to SQL-producing consumers.
func (s *Store) Load(ctx context.Context, workspaceID, appID string) (*AppDefinition, error) {
	if s == nil || s.db == nil || !uuidPattern.MatchString(workspaceID) || !identifier.MatchString(appID) {
		return nil, ErrUnavailable
	}
	var a AppDefinition
	var spec, ui, sm []byte
	e := s.db.QueryRowContext(ctx, `SELECT workspace_id::text,app_id,display_name,version,role_name,domain_table,manifest_spec,ui_schema,state_machine,dependent_capabilities,status,last_error,updated_at FROM core.app_registry a WHERE workspace_id=$1 AND app_id=$2 AND status='Ready' AND NOT EXISTS(SELECT 1 FROM unnest(a.dependent_capabilities) AS dep(cap_id) LEFT JOIN core.capability_registry c ON c.cap_id=dep.cap_id WHERE c.enabled IS DISTINCT FROM true)`, workspaceID, appID).Scan(&a.WorkspaceID, &a.AppID, &a.DisplayName, &a.Version, &a.RoleName, &a.DomainTable, &spec, &ui, &sm, pq.Array(&a.DependentCapabilities), &a.Status, &a.LastError, &a.UpdatedAt)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, ErrUnavailable
	}
	if e != nil {
		return nil, e
	}
	// Decode the same full envelope used by static fixtures, keeping strict nested decoding.
	envelope := map[string]any{"workspace_id": a.WorkspaceID, "app_id": a.AppID, "display_name": a.DisplayName, "version": a.Version, "role_name": a.RoleName, "domain_table": a.DomainTable, "manifest_spec": json.RawMessage(spec), "ui_schema": json.RawMessage(ui), "state_machine": json.RawMessage(sm), "dependent_capabilities": a.DependentCapabilities, "status": a.Status, "last_error": a.LastError, "updated_at": a.UpdatedAt}
	b, e := json.Marshal(envelope)
	if e != nil {
		return nil, e
	}
	return Decode(b)
}
