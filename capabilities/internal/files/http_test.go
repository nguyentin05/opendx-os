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
	"context"
	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type verifier struct{}

func (verifier) Verify(context.Context, string) (*auth.Identity, error) {
	return &auth.Identity{UserID: "22222222-2222-4222-8222-222222222222", WorkspaceID: "11111111-1111-4111-8111-111111111111", Role: "role_procurement", AllowedRoles: []string{"role_procurement"}}, nil
}
func TestOrdinaryUserCannotMarkClean(t *testing.T) {
	s := &Service{}
	r := httptest.NewRequest("POST", "/api/cap/process/files/x/clean", strings.NewReader(`{"scan_status":"CLEAN"}`))
	r.Header.Set("Authorization", "Bearer valid")
	w := httptest.NewRecorder()
	s.AuthenticatedHandler(verifier{}).ServeHTTP(w, r)
	if w.Code != 404 {
		t.Fatalf("status %d", w.Code)
	}
}
func TestUploadRequiresAuthentication(t *testing.T) {
	w := httptest.NewRecorder()
	(&Service{}).AuthenticatedHandler(verifier{}).ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/cap/process/files/upload", nil))
	if w.Code != 401 {
		t.Fatal(w.Code)
	}
}
