// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
package knowledge

import (
	"encoding/json"
	"github.com/opendx-os/opendx-os/capabilities/internal/ai/models"
	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPRejectsAuthorityFields(t *testing.T) {
	s := NewStore(nil)
	for _, body := range []string{`{"file_id":"33333333-3333-4333-8333-333333333333","parser_profile":"standard-text-v1","owner_user_id":"other"}`, `{"file_id":"33333333-3333-4333-8333-333333333333","parser_profile":"standard-text-v1"} {}`, `null`} {
		r := httptest.NewRequest("POST", "/api/cap/ai/documents", strings.NewReader(body))
		r.Header.Set("Idempotency-Key", "request")
		r = r.WithContext(auth.WithIdentity(r.Context(), &auth.Identity{UserID: "11111111-1111-4111-8111-111111111111", Role: "role_library_user"}))
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != 400 {
			t.Fatalf("unsafe request accepted %d", w.Code)
		}
	}
}

func TestHTTPErrorUncertaintyMapping(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"uncertain", models.ErrUncertain, 503, "MODEL_CALL_UNCERTAIN"},
		{"operation binding conflict", models.ErrConflict, 503, "MODEL_CALL_UNCERTAIN"},
		{"unavailable", models.ErrUnavailable, 503, "MODEL_UNAVAILABLE"},
		{"resource conflict", ErrConflict, 409, "CONFLICT"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			httpError(w, tc.err)
			var body struct {
				Error struct{ Code string }
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || w.Code != tc.status || body.Error.Code != tc.code {
				t.Fatalf("status=%d body=%s want %d/%s", w.Code, w.Body.String(), tc.status, tc.code)
			}
		})
	}
}

func TestUnsupportedHTTPStatus(t *testing.T) {
	w := httptest.NewRecorder()
	httpError(w, ErrUnsupported)
	if w.Code != 415 || strings.Contains(w.Body.String(), "source_id") {
		t.Fatal("unsupported media response", w.Code, w.Body.String())
	}
}
