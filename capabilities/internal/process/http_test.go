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
	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
	"github.com/opendx-os/opendx-os/capabilities/internal/files"
	"net/http"
	"net/http/httptest"
	"strings"
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

func (envelopeVerifier) Authorize(_ context.Context, token, role string) (*auth.Identity, error) {
	if token != "valid" {
		return nil, auth.ErrUnauthenticated
	}
	if role != "user" {
		return nil, auth.ErrForbidden
	}
	return &auth.Identity{Role: "user"}, nil
}
func TestRouteAuthenticationEnvelopes(t *testing.T) {
	e := NewEngine(nil, nil)
	for _, route := range []struct {
		name, method, path string
		handler            http.Handler
	}{
		{"command", "POST", "/api/cap/process/commands/app/submit", e.AuthenticatedHandler(envelopeVerifier{})},
		{"history", "GET", "/api/cap/process/records/app/domain.records/" + "11111111-1111-4111-8111-111111111111" + "/history", e.AuthenticatedHistoryHandler(envelopeVerifier{})},
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

// The workflow engine is a service: it presents the workflow service
// credential, not a user token, and acts as the workflow service account.
func TestEffectEndpointAdmitsOnlyTheWorkflowServiceCredential(t *testing.T) {
	const credential = "workflow-service-credential-0123456789"
	e := NewEngine(nil, nil)
	if _, err := e.EffectHandler(credential); err == nil {
		t.Fatal("an effect handler without a workflow service account must be refused")
	}
	if err := e.ConfigureWorkflowService("22222222-2222-4222-8222-000000000005", "role_workflow"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.EffectHandler("too-short"); err == nil {
		t.Fatal("a short credential must be refused")
	}
	h, err := e.EffectHandler(credential)
	if err != nil {
		t.Fatal(err)
	}
	call := func(authorization, body string) int {
		r := httptest.NewRequest("POST", "/api/cap/process/effects/work-item", strings.NewReader(body))
		if authorization != "" {
			r.Header.Set("Authorization", authorization)
		}
		// A role header means nothing here: the credential decides.
		r.Header.Set("X-Hasura-Role", "role_workflow")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	for name, authorization := range map[string]string{
		"missing":          "",
		"another secret":   "Bearer workflow-service-credential-0123456780",
		"prefix of it":     "Bearer workflow-service-credential",
		"basic scheme":     "Basic " + credential,
		"no scheme":        credential,
		"a user JWT shape": "Bearer eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJ4In0.signature",
	} {
		if code := call(authorization, `{"event_id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","step_name":"create_review"}`); code != 401 {
			t.Errorf("%s: %d, want 401", name, code)
		}
	}
	// With the credential the request is read; a malformed one is a 400, not a 401.
	if code := call("Bearer "+credential, `{"event_id":"x","step_name":"y","extra":true}`); code != 400 {
		t.Fatalf("authenticated malformed request: %d, want 400", code)
	}
}

func TestHistoryHandlerRejectsUnverifiedIdentity(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/cap/process/records/app/domain.records/11111111-1111-4111-8111-111111111111/history", nil)
	w := httptest.NewRecorder()
	NewEngine(nil, nil).HistoryHandler().ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestHistoryHandlerRejectsMalformedIdentifiers(t *testing.T) {
	id := &auth.Identity{UserID: "11111111-1111-4111-8111-111111111111", Role: "user"}
	for name, path := range map[string][3]string{
		"app":    {"App", "domain.records", "11111111-1111-4111-8111-111111111111"},
		"table":  {"app", "records", "11111111-1111-4111-8111-111111111111"},
		"record": {"app", "domain.records", "not-a-uuid"},
	} {
		t.Run(name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/api/cap/process/records/x/y/z/history", nil)
			r = r.WithContext(auth.WithIdentity(r.Context(), id))
			r.SetPathValue("app_id", path[0])
			r.SetPathValue("table", path[1])
			r.SetPathValue("entity_id", path[2])
			w := httptest.NewRecorder()
			NewEngine(nil, nil).HistoryHandler().ServeHTTP(w, r)
			if w.Code != 400 {
				t.Fatalf("status = %d, want 400", w.Code)
			}
		})
	}
}
