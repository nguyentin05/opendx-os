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

package authz

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
)

const alice = "22222222-2222-4222-8222-222222222222"

// fakeAuthn accepts the token "good" as alice.
type fakeAuthn struct{ name string }

func (f *fakeAuthn) Authenticate(_ context.Context, token string) (*auth.Principal, error) {
	if token != "good" {
		return nil, errors.New("bad token")
	}
	return &auth.Principal{Subject: alice, Email: "alice@example.test", Name: f.name}, nil
}

type fakeStore struct {
	status  string
	grants  []Grant
	mirrors int
	reads   int
	err     error
}

func (f *fakeStore) Mirror(context.Context, auth.Principal) (string, error) {
	f.mirrors++
	return f.status, f.err
}

func (f *fakeStore) Grants(context.Context, string) ([]Grant, error) {
	f.reads++
	return f.grants, f.err
}

func call(t *testing.T, h http.Handler, path, token, role string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if role != "" {
		req.Header.Set("X-Hasura-Role", role)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	return w.Code, body
}

func TestHasuraWebhookActsOnlyAsHeldRoles(t *testing.T) {
	store := &fakeStore{status: "ACTIVE", grants: []Grant{{Role: "role_procurement_manager", AppID: "procurement"}, {Role: "role_procurement_requester", AppID: "procurement"}}}
	h := Handler(NewResolver(&fakeAuthn{name: "Alice"}, store, time.Minute))

	code, body := call(t, h, "/hasura", "good", "role_procurement_manager")
	if code != http.StatusOK || body["X-Hasura-User-Id"] != alice || body["X-Hasura-Role"] != "role_procurement_manager" {
		t.Fatalf("held role: %d %v", code, body)
	}
	for name, tc := range map[string]struct{ token, role string }{
		"role not held":     {"good", "role_procurement_finance"},
		"no role, several":  {"good", ""},
		"bad token":         {"forged", "role_procurement_manager"},
		"no token":          {"", "role_procurement_manager"},
		"platform role ask": {"good", "role_operator"},
	} {
		if code, _ := call(t, h, "/hasura", tc.token, tc.role); code != http.StatusUnauthorized {
			t.Fatalf("%s: status %d, want 401", name, code)
		}
	}

	// A caller holding exactly one role acts as it without naming it.
	one := &fakeStore{status: "ACTIVE", grants: []Grant{{Role: "role_review"}}}
	if code, body := call(t, Handler(NewResolver(&fakeAuthn{name: "Alice"}, one, time.Minute)), "/hasura", "good", ""); code != http.StatusOK || body["X-Hasura-Role"] != "role_review" {
		t.Fatalf("single role: %d %v", code, body)
	}
}

func TestDisabledAccountAndStoreFailure(t *testing.T) {
	disabled := &fakeStore{status: "DISABLED", grants: []Grant{{Role: "role_review"}}}
	if code, _ := call(t, Handler(NewResolver(&fakeAuthn{name: "Alice"}, disabled, time.Minute)), "/hasura", "good", "role_review"); code != http.StatusUnauthorized {
		t.Fatalf("disabled account: status %d, want 401", code)
	}
	broken := &fakeStore{err: errors.New("connection refused")}
	if code, _ := call(t, Handler(NewResolver(&fakeAuthn{name: "Alice"}, broken, time.Minute)), "/hasura", "good", "role_review"); code != http.StatusServiceUnavailable {
		t.Fatalf("store failure: status %d, want 503", code)
	}
}

func TestGrantsAreCachedForTheTTLOnly(t *testing.T) {
	store := &fakeStore{status: "ACTIVE", grants: []Grant{{Role: "role_review"}}}
	authn := &fakeAuthn{name: "Alice"}
	r := NewResolver(authn, store, 5*time.Second)
	now := time.Unix(1_800_000_000, 0)
	r.now = func() time.Time { return now }

	for i := 0; i < 3; i++ {
		if _, err := r.Resolve(context.Background(), "good"); err != nil {
			t.Fatal(err)
		}
	}
	if store.reads != 1 || store.mirrors != 1 {
		t.Fatalf("reads=%d mirrors=%d within the TTL, want 1 each", store.reads, store.mirrors)
	}

	// A revoked role stops working once the entry expires.
	store.grants = nil
	now = now.Add(6 * time.Second)
	s, err := r.Resolve(context.Background(), "good")
	if err != nil || s.Holds("role_review") {
		t.Fatalf("revocation must apply after the TTL: %+v, %v", s, err)
	}

	// A changed name in the token is mirrored at once.
	authn.name = "Alice Tran"
	if _, err := r.Resolve(context.Background(), "good"); err != nil || store.mirrors != 3 {
		t.Fatalf("mirrors = %d, want 3 after the name changed (%v)", store.mirrors, err)
	}
}

func TestMeListsIdentityAndRoles(t *testing.T) {
	store := &fakeStore{status: "ACTIVE", grants: []Grant{{Role: "role_review", AppID: "process_fixture", Description: "Creates review requests"}}}
	code, body := call(t, Handler(NewResolver(&fakeAuthn{name: "Alice"}, store, time.Minute)), "/api/auth/me", "good", "")
	if code != http.StatusOK || body["id"] != alice || body["display_name"] != "Alice" {
		t.Fatalf("me: %d %v", code, body)
	}
	roles, _ := body["roles"].([]any)
	if len(roles) != 1 || roles[0].(map[string]any)["app_id"] != "process_fixture" {
		t.Fatalf("roles = %v", body["roles"])
	}
}
