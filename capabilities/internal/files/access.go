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

// Package process implements work item state commands and their request and response types.
package files

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
	"github.com/opendx-os/opendx-os/capabilities/internal/registry"
	"regexp"
)

var uuid = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func (s *Service) application(ctx context.Context, id *auth.Identity, app string) (*registry.AppDefinition, error) {
	if id == nil || !uuid.MatchString(id.UserID) || !uuid.MatchString(id.WorkspaceID) {
		return nil, ErrDenied
	}
	granted := false
	for _, r := range id.AllowedRoles {
		granted = granted || r == id.Role
	}
	if !granted {
		return nil, ErrDenied
	}
	a, e := s.apps.Load(ctx, id.WorkspaceID, app)
	if e != nil {
		return nil, ErrDenied
	}
	for _, p := range append(append(append([]registry.Permission{}, a.ManifestSpec.DomainAccess.Select...), a.ManifestSpec.DomainAccess.Insert...), a.ManifestSpec.DomainAccess.Update...) {
		if p.Role == id.Role {
			return a, nil
		}
	}
	return nil, ErrDenied
}
func (s *Service) readable(ctx context.Context, id *auth.Identity, file string) (string, string, string, error) {
	if id == nil || !uuid.MatchString(id.WorkspaceID) || !uuid.MatchString(file) {
		return "", "", "", ErrDenied
	}
	var app, owner, key, hash, status string
	e := s.db.QueryRowContext(ctx, `SELECT app_id,COALESCE(uploaded_by::text,'') FROM core.file_objects WHERE id=$1 AND workspace_id=$2 AND bucket=$3`, file, id.WorkspaceID, s.bucket).Scan(&app, &owner)
	if e != nil {
		return "", "", "", ErrDenied
	}
	a, e := s.application(ctx, id, app)
	if e != nil {
		return "", "", "", ErrDenied
	}
	allowed := owner == id.UserID
	if !allowed {
		types := map[string]string{"id": "uuid", "workspace_id": "uuid", "work_item_id": "uuid", "status": "text", "version": "bigint", "updated_at": "timestamptz"}
		for _, c := range a.ManifestSpec.DomainTable.Columns {
			types[c.Name] = c.Type
		}
		for _, attachment := range a.ManifestSpec.Attachments {
			rows, e := s.db.QueryContext(ctx, `SELECT to_jsonb(d) FROM `+a.DomainTable+` d WHERE workspace_id=$1 AND `+attachment.Column+`=$2`, id.WorkspaceID, file)
			if e != nil {
				return "", "", "", ErrDenied
			}
			for rows.Next() {
				var raw []byte
				if rows.Scan(&raw) != nil {
					continue
				}
				var row map[string]any
				d := json.NewDecoder(bytes.NewReader(raw))
				d.UseNumber()
				if d.Decode(&row) != nil {
					continue
				}
				for _, p := range a.ManifestSpec.DomainAccess.Select {
					if p.Role != id.Role {
						continue
					}
					column := false
					for _, c := range p.Columns {
						column = column || c == attachment.Column
					}
					if !column {
						continue
					}
					ok, e := registry.Matches(p.RecordFilter, row, id, types)
					allowed = allowed || e == nil && ok
				}
			}
			e = rows.Err()
			_ = rows.Close()
			if e != nil {
				return "", "", "", ErrDenied
			}
		}
	}
	if !allowed {
		return "", "", "", ErrDenied
	}
	e = s.db.QueryRowContext(ctx, `SELECT object_key,sha256_hash,scan_status FROM core.file_objects WHERE id=$1 AND workspace_id=$2 AND app_id=$3 AND bucket=$4`, file, id.WorkspaceID, app, s.bucket).Scan(&key, &hash, &status)
	return key, hash, status, e
}
