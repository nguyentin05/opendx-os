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
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestUnsignedHeadersAreNotIdentity(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("X-Hasura-User-Id", "22222222-2222-4222-8222-222222222222")
	r.Header.Set("X-Hasura-Role", "role_manager_procurement")
	if _, err := ExtractIdentity(r); err == nil {
		t.Fatal("unsigned headers granted identity")
	}
}

// fakeAuthorizer accepts the token "good" for role_user only.
type fakeAuthorizer struct{ err error }

func (f fakeAuthorizer) Authorize(_ context.Context, token, role string) (*Identity, error) {
	if f.err != nil {
		return nil, f.err
	}
	if token != "good" {
		return nil, ErrUnauthenticated
	}
	if role != "role_user" && role != "" {
		return nil, ErrForbidden
	}
	return &Identity{UserID: "22222222-2222-4222-8222-222222222222", Role: "role_user"}, nil
}

func TestMiddlewareMapsAuthorizationOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name, header, role string
		authz              Authorizer
		status             int
	}{
		{"no token", "", "role_user", fakeAuthorizer{}, 401},
		{"not bearer", "Basic good", "role_user", fakeAuthorizer{}, 401},
		{"bad token", "Bearer forged", "role_user", fakeAuthorizer{}, 401},
		{"role not held", "Bearer good", "role_admin", fakeAuthorizer{}, 403},
		{"lookup failed", "Bearer good", "role_user", fakeAuthorizer{err: errors.New("db down")}, 503},
		{"held role", "Bearer good", "role_user", fakeAuthorizer{}, 204},
	} {
		r := httptest.NewRequest("GET", "/", nil)
		if tc.header != "" {
			r.Header.Set("Authorization", tc.header)
		}
		r.Header.Set("X-Hasura-Role", tc.role)
		r.Header.Set("X-Hasura-User-Id", "33333333-3333-4333-8333-333333333333")
		w := httptest.NewRecorder()
		Middleware(tc.authz, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id, err := ExtractIdentity(r)
			if err != nil || id.UserID != "22222222-2222-4222-8222-222222222222" || id.Role != "role_user" {
				t.Errorf("%s: identity %+v %v", tc.name, id, err)
			}
			w.WriteHeader(204)
		})).ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("%s: got %d want %d", tc.name, w.Code, tc.status)
		}
	}
}
