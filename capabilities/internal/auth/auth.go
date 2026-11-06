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

// Package auth verifies caller identity before exposing runtime context.
package auth

import (
	"errors"
	"net/http"
)

type Identity struct {
	UserID       string   `json:"user_id"`
	WorkspaceID  string   `json:"workspace_id"`
	Role         string   `json:"role"`
	AllowedRoles []string `json:"allowed_roles"`
}
type identityKey struct{}

func ExtractIdentity(r *http.Request) (*Identity, error) {
	id, ok := r.Context().Value(identityKey{}).(*Identity)
	if !ok || id == nil {
		return nil, errors.New("verified identity required")
	}
	copy := *id
	copy.AllowedRoles = append([]string(nil), id.AllowedRoles...)
	return &copy, nil
}
