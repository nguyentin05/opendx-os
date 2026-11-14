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
	"regexp"
	"strings"

	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
	"github.com/opendx-os/opendx-os/capabilities/internal/registry"
)

var uuid = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// application returns the app when the caller's role has any access rule in
// it: only such roles may upload files for it.
func (s *Service) application(ctx context.Context, id *auth.Identity, app string) (*registry.AppDefinition, error) {
	if id == nil || !uuid.MatchString(id.UserID) {
		return nil, ErrDenied
	}
	a, e := s.apps.Load(ctx, app)
	if e != nil || !a.HasRole(id.Role) {
		return nil, ErrDenied
	}
	return a, nil
}

// readable returns where a file is stored when the caller may read it: they
// uploaded it, or it is attached to a record their role can read with the
// attachment column visible (scope and record filter included).
func (s *Service) readable(ctx context.Context, id *auth.Identity, file string) (string, string, string, error) {
	if id == nil || !uuid.MatchString(file) {
		return "", "", "", ErrDenied
	}
	var app, owner string
	e := s.db.QueryRowContext(ctx, `SELECT app_id,uploaded_by::text FROM core.files WHERE id=$1 AND bucket=$2`, file, s.bucket).Scan(&app, &owner)
	if e != nil {
		return "", "", "", ErrDenied
	}
	a, e := s.application(ctx, id, app)
	if e != nil {
		return "", "", "", ErrDenied
	}
	allowed := strings.EqualFold(owner, id.UserID)
	if !allowed {
		viewer, e := a.Viewer(ctx, s.db, strings.ToLower(id.UserID), id.Role)
		if e != nil {
			return "", "", "", ErrDenied
		}
		for i := range a.Spec.Tables {
			table := &a.Spec.Tables[i]
			for _, attachment := range table.Attachments {
				rows, e := s.db.QueryContext(ctx, `SELECT to_jsonb(d) FROM `+table.Name+` d WHERE `+attachment.Column+`=$1`, file)
				if e != nil {
					return "", "", "", ErrDenied
				}
				for rows.Next() && !allowed {
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
					ok, e := a.CanSee(table, viewer, row, attachment.Column)
					allowed = e == nil && ok
				}
				e = rows.Err()
				_ = rows.Close()
				if e != nil {
					return "", "", "", ErrDenied
				}
			}
		}
	}
	if !allowed {
		return "", "", "", ErrDenied
	}
	var key, hash, status string
	e = s.db.QueryRowContext(ctx, `SELECT object_key,sha256_hash,scan_status FROM core.files WHERE id=$1 AND app_id=$2 AND bucket=$3`, file, app, s.bucket).Scan(&key, &hash, &status)
	return key, hash, status, e
}
