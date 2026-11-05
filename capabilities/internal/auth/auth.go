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

// Package auth extracts caller identity from HTTP request headers.
package auth

import (
	"errors"
	"net/http"
	"strings"
)

// Identity represents the authenticated caller context.
type Identity struct {
	UserID      string `json:"user_id"`
	WorkspaceID string `json:"workspace_id"`
	Role        string `json:"role"`
}

// ExtractIdentity extracts caller identity from request headers.
// Supports standard DX-OS headers and Hasura forwarding headers.
func ExtractIdentity(r *http.Request) (*Identity, error) {
	workspaceID := r.Header.Get("X-Hasura-Workspace-Id")
	if workspaceID == "" {
		workspaceID = r.Header.Get("X-Workspace-Id")
	}

	role := r.Header.Get("X-Hasura-Role")
	if role == "" {
		role = r.Header.Get("X-Role")
	}

	userID := r.Header.Get("X-Hasura-User-Id")
	if userID == "" {
		userID = r.Header.Get("X-User-Id")
	}

	// Also support Authorization: Bearer <token> fallback
	authHeader := r.Header.Get("Authorization")
	if (userID == "" || role == "") && strings.HasPrefix(authHeader, "Bearer ") {
		// In test and mock mode, token format can be "mock:<role>:<user_id>:<workspace_id>"
		token := strings.TrimPrefix(authHeader, "Bearer ")
		parts := strings.Split(token, ":")
		if len(parts) == 4 && parts[0] == "mock" {
			role = parts[1]
			userID = parts[2]
			if workspaceID == "" {
				workspaceID = parts[3]
			}
		}
	}

	if workspaceID == "" {
		return nil, errors.New("workspace ID is required (missing X-Hasura-Workspace-Id or X-Workspace-Id)")
	}
	if role == "" {
		role = "anonymous"
	}

	return &Identity{
		UserID:      userID,
		WorkspaceID: workspaceID,
		Role:        role,
	}, nil
}
