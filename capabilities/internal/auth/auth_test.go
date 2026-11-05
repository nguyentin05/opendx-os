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

package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestExtractIdentityFromHasuraHeaders(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/cap/process/commands/maintenance/submit", nil)
	req.Header.Set("X-Hasura-Workspace-Id", "ws-123")
	req.Header.Set("X-Hasura-Role", "role_maintenance")
	req.Header.Set("X-Hasura-User-Id", "usr-456")

	iden, err := ExtractIdentity(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if iden.WorkspaceID != "ws-123" {
		t.Errorf("expected ws-123, got %s", iden.WorkspaceID)
	}
	if iden.Role != "role_maintenance" {
		t.Errorf("expected role_maintenance, got %s", iden.Role)
	}
	if iden.UserID != "usr-456" {
		t.Errorf("expected usr-456, got %s", iden.UserID)
	}
}

func TestExtractIdentityMissingWorkspace(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/cap/process/commands/maintenance/submit", nil)
	_, err := ExtractIdentity(req)
	if err == nil {
		t.Fatalf("expected error for missing workspace, got nil")
	}
}
