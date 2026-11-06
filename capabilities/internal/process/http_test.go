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

package process

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
	"github.com/opendx-os/opendx-os/capabilities/internal/files"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSafeErrorEnvelope(t *testing.T) {
	for _, status := range []int{400, 401, 403, 404, 409, 422, 503} {
		w := httptest.NewRecorder()
		WriteError(w, status)
		var body map[string]map[string]string
		if e := json.Unmarshal(w.Body.Bytes(), &body); e != nil || w.Code != status || body["error"]["code"] == "" || body["error"]["message"] == "" {
			t.Fatalf("%d: %s", status, w.Body)
		}
	}
}

type envelopeVerifier struct{}

func (envelopeVerifier) Verify(_ context.Context, token string) (*auth.Identity, error) {
	if token != "valid" {
		return nil, errors.New("invalid token")
	}
	return &auth.Identity{Role: "user", AllowedRoles: []string{"user"}}, nil
}
func TestRouteAuthenticationEnvelopes(t *testing.T) {
	e := NewEngine(nil, nil)
	for _, route := range []struct {
		name, method, path string
		handler            http.Handler
	}{
		{"command", "POST", "/api/cap/process/commands/app/submit", e.AuthenticatedHandler(envelopeVerifier{})},
		{"effect", "POST", "/api/cap/process/effects/work-item", e.AuthenticatedEffectHandler(envelopeVerifier{})},
		{"upload", "POST", "/api/cap/process/files/upload", (&files.Service{}).AuthenticatedHandler(envelopeVerifier{})},
		{"download", "GET", "/api/cap/process/files/file", (&files.Service{}).AuthenticatedHandler(envelopeVerifier{})},
	} {
		for _, tc := range []struct {
			name, token, role string
			status            int
		}{
			{"missing", "", "", 401}, {"invalid", "invalid", "", 401}, {"ungranted", "valid", "admin", 403},
		} {
			t.Run(route.name+"/"+tc.name, func(t *testing.T) {
				r := httptest.NewRequest(route.method, route.path, nil)
				if tc.token != "" {
					r.Header.Set("Authorization", "Bearer "+tc.token)
				}
				r.Header.Set("X-Hasura-Role", tc.role)
				w := httptest.NewRecorder()
				route.handler.ServeHTTP(w, r)
				var body map[string]map[string]string
				if w.Code != tc.status || w.Header().Get("Content-Type") != "application/json" || json.Unmarshal(w.Body.Bytes(), &body) != nil || body["error"]["code"] != http.StatusText(tc.status) || body["error"]["message"] != http.StatusText(tc.status) {
					t.Fatalf("status=%d type=%s body=%s", w.Code, w.Header().Get("Content-Type"), w.Body)
				}
			})
		}
	}
}
