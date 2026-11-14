// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
package files

import (
	"context"
	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
	"net/http"
	"net/http/httptest"
	"testing"
)

type privateAuthorizer struct{}

func (privateAuthorizer) Authorize(context.Context, string, string) (*auth.Identity, error) {
	return &auth.Identity{UserID: "00000000-0000-0000-0000-000000000001", Role: "role_library_user"}, nil
}
func TestPrivateUploadDefaultsClosed(t *testing.T) {
	s := &Service{}
	r := httptest.NewRequest(http.MethodPost, "/api/cap/process/files/private/upload", nil)
	r.Header.Set("Authorization", "Bearer fixture")
	w := httptest.NewRecorder()
	s.AuthenticatedHandler(privateAuthorizer{}).ServeHTTP(w, r)
	if w.Code != 503 {
		t.Fatalf("unqualified private upload open: %d", w.Code)
	}
}
