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
	"strings"
	"testing"
	"time"

	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
)

const alice = "22222222-2222-4222-8222-222222222222"

// fakeAuthn accepts the token "good" as alice, whose email is verified
// unless unverified is set.
type fakeAuthn struct {
	name       string
	unverified bool
}

func (f *fakeAuthn) Authenticate(_ context.Context, token string) (*auth.Principal, error) {
	if token != "good" {
		return nil, errors.New("bad token")
	}
	return &auth.Principal{Subject: alice, Email: "alice@example.test", Name: f.name, EmailVerified: !f.unverified}, nil
}

type fakeStore struct {
	status  string
	grants  []Grant
	mirrors int
	reads   int
	err     error
	apps    []App
	asked   []string
}

func (f *fakeStore) Apps(_ context.Context, ids []string) ([]App, error) {
	f.asked = ids
	var out []App
	for _, a := range f.apps {
		for _, id := range ids {
			if a.AppID == id {
				out = append(out, a)
			}
		}
	}
	return out, f.err
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

// QA finding: a role named admin is privileged in Hasura; it is never handed
// out, even if a registry row held it.
func TestPrivilegedRoleNamesAreNeverGranted(t *testing.T) {
	store := &fakeStore{status: "ACTIVE", grants: []Grant{{Role: "admin"}, {Role: "role_review"}}}
	h := Handler(NewResolver(&fakeAuthn{name: "Alice"}, store, time.Minute))
	if code, body := call(t, h, "/hasura", "good", "admin"); code != http.StatusUnauthorized {
		t.Fatalf("admin role: %d %v", code, body)
	}
	only := &fakeStore{status: "ACTIVE", grants: []Grant{{Role: "admin"}}}
	if code, _ := call(t, Handler(NewResolver(&fakeAuthn{name: "Alice"}, only, time.Minute)), "/hasura", "good", ""); code != http.StatusUnauthorized {
		t.Fatalf("single admin grant used implicitly: %d", code)
	}
}

// The UI learns which applications to show, and their tables, from the
// registry: only applications the caller holds a role in, with those roles.
func TestAppsListsApplicationsOfHeldRoles(t *testing.T) {
	store := &fakeStore{status: "ACTIVE",
		grants: []Grant{
			{Role: "role_review", AppID: "process_fixture"},
			{Role: "role_review_lead", AppID: "process_fixture"},
			{Role: "role_auditor"},
			{Role: "role_old_user", AppID: "old_app"},
		},
		apps: []App{
			{AppID: "process_fixture", DisplayName: "Review", Version: "0.2.0",
				Spec: json.RawMessage(`{
					"capabilities":["cap-process","cap-work"],
					"tables":[{"name":"domain.review_requests","ui":{"listColumns":["note"]}}],
					"sharedAccess":[
						{"table":"cap_work.work_items","roles":["role_review_lead","role_unheld"],"columns":["id","title"]},
						{"table":"core.users","roles":["role_review"],"columns":["id"]}
					],
					"workflows":[{"name":"private_workflow"}],
					"privateMetadata":{"value":"internal"}
				}`)},
			{AppID: "other_app", DisplayName: "Other", Version: "1", Spec: json.RawMessage(`{"tables":[{"name":"domain.other"}]}`)},
			// Stored before spec.tables: nothing to show until it is reinstalled.
			{AppID: "old_app", DisplayName: "Old", Version: "1", Spec: json.RawMessage(`{"domainTable":{"name":"domain.old"}}`)},
		}}
	h := Handler(NewResolver(&fakeAuthn{name: "Alice"}, store, time.Minute))
	code, body := call(t, h, "/api/auth/apps", "good", "")
	if code != http.StatusOK {
		t.Fatalf("apps: %d %v", code, body)
	}
	if got := strings.Join(store.asked, ","); got != "process_fixture,old_app" {
		t.Fatalf("asked the registry for %q, want only applications of held roles", got)
	}
	apps, _ := body["apps"].([]any)
	if len(apps) != 1 {
		t.Fatalf("apps = %v, want the one application with tables", body["apps"])
	}
	app := apps[0].(map[string]any)
	roles, _ := app["roles"].([]any)
	tables, _ := app["tables"].([]any)
	if app["app_id"] != "process_fixture" || app["version"] != "0.2.0" || len(roles) != 2 || len(tables) != 1 ||
		tables[0].(map[string]any)["name"] != "domain.review_requests" {
		t.Fatalf("app = %v", app)
	}
	workItemRoles, _ := app["work_item_roles"].([]any)
	if len(workItemRoles) != 1 || workItemRoles[0] != "role_review_lead" {
		t.Errorf("work_item_roles = %v, want [role_review_lead]", app["work_item_roles"])
	}
	// Only the caller's own roles are listed: role_unheld stays private.
	shared, _ := json.Marshal(app["shared_access"])
	if want := `[{"columns":["id","title"],"roles":["role_review_lead"],"table":"cap_work.work_items"},` +
		`{"columns":["id"],"roles":["role_review"],"table":"core.users"}]`; string(shared) != want {
		t.Errorf("shared_access = %s, want %s", shared, want)
	}
	for _, field := range []string{"sharedAccess", "columns", "workflows", "privateMetadata"} {
		if _, leaked := app[field]; leaked {
			t.Errorf("apps response exposed %s", field)
		}
	}

	if code, _ := call(t, h, "/api/auth/apps", "", ""); code != http.StatusUnauthorized {
		t.Fatalf("without a token: %d, want 401", code)
	}
	none := Handler(NewResolver(&fakeAuthn{name: "Alice"}, &fakeStore{status: "ACTIVE", grants: []Grant{{Role: "role_auditor"}}}, time.Minute))
	code, body = call(t, none, "/api/auth/apps", "good", "")
	if list, ok := body["apps"].([]any); code != http.StatusOK || !ok || len(list) != 0 {
		t.Fatalf("a caller without application roles: %d %v, want an empty list", code, body)
	}
}

func TestAppsIncludesCapabilityOnlyKnowledge(t *testing.T) {
	store := &fakeStore{status: "ACTIVE", grants: []Grant{{Role: "role_library_user", AppID: "library"}}, apps: []App{{AppID: "library", DisplayName: "Library", Version: "1", Spec: json.RawMessage(`{"capabilities":["cap-process","cap-ai"],"roles":[{"name":"role_library_user"}],"tables":[],"knowledge":{"version":1,"personalLibrary":{"enabled":true,"allowedRoles":["role_library_user"]}}}`)}}}
	code, body := call(t, Handler(NewResolver(&fakeAuthn{}, store, 0)), "/api/auth/apps", "good", "")
	apps, _ := body["apps"].([]any)
	if code != 200 || len(apps) != 1 {
		t.Fatal("capability-only library not discoverable", code, body)
	}
	app := apps[0].(map[string]any)
	if app["knowledge"] == nil {
		t.Fatal("knowledge metadata lost")
	}
	if roles, ok := app["work_item_roles"].([]any); !ok || len(roles) != 0 {
		t.Errorf("work_item_roles = %v, want an empty JSON array", app["work_item_roles"])
	}
	if shared, ok := app["shared_access"].([]any); !ok || len(shared) != 0 {
		t.Errorf("shared_access = %v, want an empty JSON array", app["shared_access"])
	}
}

type fakeDirectory struct {
	calls     []string
	bootstrap []string
	err       error
}

func (d *fakeDirectory) Roles(context.Context) ([]Grant, error) {
	return []Grant{{Role: "role_review", AppID: "process_fixture"}}, d.err
}
func (d *fakeDirectory) Users(context.Context) ([]User, error) {
	return []User{{ID: alice, Email: "alice@example.test", DisplayName: "Alice", Status: "ACTIVE", Roles: []string{"role_review"}}}, d.err
}
func (d *fakeDirectory) GrantRole(_ context.Context, actor, user, role string) error {
	d.calls = append(d.calls, "grant:"+actor+":"+user+":"+role)
	return d.err
}
func (d *fakeDirectory) RevokeRole(_ context.Context, actor, user, role string) error {
	d.calls = append(d.calls, "revoke:"+actor+":"+user+":"+role)
	return d.err
}
func (d *fakeDirectory) BootstrapOperator(_ context.Context, user string) (bool, error) {
	d.bootstrap = append(d.bootstrap, user)
	return len(d.bootstrap) == 1, d.err
}

func do(t *testing.T, h http.Handler, method, path, token string) int {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w.Code
}

// Only a platform operator manages role assignments, and some assignments are
// not theirs to change.
func TestRoleAdministrationIsForOperators(t *testing.T) {
	const bob = "33333333-3333-4333-8333-333333333333"
	path := "/api/auth/users/" + bob + "/roles/role_review"
	admin := func(grants ...Grant) (http.Handler, *fakeDirectory, *fakeStore) {
		store := &fakeStore{status: "ACTIVE", grants: grants}
		dir := &fakeDirectory{}
		r := NewResolver(&fakeAuthn{name: "Alice"}, store, time.Minute)
		r.Administer(dir, "")
		return Handler(r), dir, store
	}

	// Without role_operator every endpoint is closed.
	h, dir, _ := admin(Grant{Role: "role_review", AppID: "process_fixture"})
	for _, c := range []struct{ method, path string }{
		{"GET", "/api/auth/roles"}, {"GET", "/api/auth/users"}, {"PUT", path}, {"DELETE", path},
	} {
		if code := do(t, h, c.method, c.path, "good"); code != http.StatusForbidden {
			t.Errorf("%s %s without role_operator: %d, want 403", c.method, c.path, code)
		}
		if code := do(t, h, c.method, c.path, ""); code != http.StatusUnauthorized {
			t.Errorf("%s %s without a token: %d, want 401", c.method, c.path, code)
		}
	}
	if len(dir.calls) != 0 {
		t.Fatalf("a caller without role_operator reached the directory: %v", dir.calls)
	}

	h, dir, _ = admin(Grant{Role: OperatorRole})
	if code, body := call(t, h, "/api/auth/roles", "good", ""); code != http.StatusOK || len(body["roles"].([]any)) != 1 {
		t.Fatalf("roles: %d %v", code, body)
	}
	if code, body := call(t, h, "/api/auth/users", "good", ""); code != http.StatusOK || body["users"].([]any)[0].(map[string]any)["email"] != "alice@example.test" {
		t.Fatalf("users: %d %v", code, body)
	}
	if code := do(t, h, "PUT", path, "good"); code != http.StatusNoContent {
		t.Fatalf("grant: %d", code)
	}
	if code := do(t, h, "DELETE", path, "good"); code != http.StatusNoContent {
		t.Fatalf("revoke: %d", code)
	}
	if got := strings.Join(dir.calls, " "); got != "grant:"+alice+":"+bob+":role_review revoke:"+alice+":"+bob+":role_review" {
		t.Fatalf("directory calls = %q (the operator is the recorded actor)", got)
	}

	// Assignments nobody changes here, and names that are not roles or users.
	for name, c := range map[string]struct {
		method, path string
		want         int
	}{
		"the workflow role is granted":  {"PUT", "/api/auth/users/" + bob + "/roles/role_workflow", http.StatusConflict},
		"the workflow role is revoked":  {"DELETE", "/api/auth/users/" + bob + "/roles/role_workflow", http.StatusConflict},
		"an operator revokes their own": {"DELETE", "/api/auth/users/" + alice + "/roles/" + OperatorRole, http.StatusConflict},
		"a privileged Hasura name":      {"PUT", "/api/auth/users/" + bob + "/roles/admin", http.StatusNotFound},
		"a user id that is not a UUID":  {"PUT", "/api/auth/users/bob/roles/role_review", http.StatusNotFound},
		"another operator is revoked":   {"DELETE", "/api/auth/users/" + bob + "/roles/" + OperatorRole, http.StatusNoContent},
		"an operator grants operator":   {"PUT", "/api/auth/users/" + bob + "/roles/" + OperatorRole, http.StatusNoContent},
	} {
		if code := do(t, h, c.method, c.path, "good"); code != c.want {
			t.Errorf("%s: %d, want %d", name, code, c.want)
		}
	}
	dir.err = ErrNotFound
	if code := do(t, h, "PUT", path, "good"); code != http.StatusNotFound {
		t.Fatalf("a role that is not in force: %d, want 404", code)
	}
	dir.err = errors.New("database down")
	if code := do(t, h, "PUT", path, "good"); code != http.StatusServiceUnavailable {
		t.Fatalf("a directory failure: %d, want 503", code)
	}

	// Without administration configured the endpoints do not exist.
	plain := Handler(NewResolver(&fakeAuthn{name: "Alice"}, &fakeStore{status: "ACTIVE", grants: []Grant{{Role: OperatorRole}}}, time.Minute))
	if code := do(t, plain, "GET", "/api/auth/users", "good"); code != http.StatusNotFound {
		t.Fatalf("admin endpoint without administration: %d, want 404", code)
	}
}

// A change applies to the user's next request: their cached grants are dropped.
func TestRoleChangeDropsTheCachedGrants(t *testing.T) {
	store := &fakeStore{status: "ACTIVE", grants: []Grant{{Role: OperatorRole}}}
	r := NewResolver(&fakeAuthn{name: "Alice"}, store, time.Minute)
	r.Administer(&fakeDirectory{}, "")
	h := Handler(r)
	if code, _ := call(t, h, "/api/auth/me", "good", ""); code != http.StatusOK || store.reads != 1 {
		t.Fatalf("first request: %d reads=%d", code, store.reads)
	}
	if code, _ := call(t, h, "/api/auth/me", "good", ""); code != http.StatusOK || store.reads != 1 {
		t.Fatalf("grants must be cached: reads=%d", store.reads)
	}
	// Alice (an operator) grants herself another role: her cache is dropped.
	if code := do(t, h, "PUT", "/api/auth/users/"+alice+"/roles/role_review", "good"); code != http.StatusNoContent {
		t.Fatalf("grant: %d", code)
	}
	if code, _ := call(t, h, "/api/auth/me", "good", ""); code != http.StatusOK || store.reads != 2 {
		t.Fatalf("the changed user's grants must be read again: reads=%d", store.reads)
	}
}

// A new installation has no operator. The first sign-in of the configured
// account becomes it; nobody else triggers the bootstrap.
func TestFirstOperatorIsBootstrappedByEmail(t *testing.T) {
	dir := &fakeDirectory{}
	r := NewResolver(&fakeAuthn{name: "Alice"}, &fakeStore{status: "ACTIVE"}, 0)
	r.Administer(dir, "  ALICE@example.test ")
	h := Handler(r)
	if code, _ := call(t, h, "/api/auth/me", "good", ""); code != http.StatusOK || len(dir.bootstrap) != 1 || dir.bootstrap[0] != alice {
		t.Fatalf("the configured account must be bootstrapped: %d %v", code, dir.bootstrap)
	}
	other := &fakeDirectory{}
	r = NewResolver(&fakeAuthn{name: "Alice"}, &fakeStore{status: "ACTIVE"}, 0)
	r.Administer(other, "someone.else@example.org")
	if code, _ := call(t, Handler(r), "/api/auth/me", "good", ""); code != http.StatusOK || len(other.bootstrap) != 0 {
		t.Fatalf("another account must not be bootstrapped: %d %v", code, other.bootstrap)
	}
	none := &fakeDirectory{}
	r = NewResolver(&fakeAuthn{name: "Alice"}, &fakeStore{status: "ACTIVE"}, 0)
	r.Administer(none, "")
	if _, _ = call(t, Handler(r), "/api/auth/me", "good", ""); len(none.bootstrap) != 0 {
		t.Fatal("without BOOTSTRAP_OPERATOR_EMAIL nobody is bootstrapped")
	}
	// A user who types the configured address as their own email gets a
	// token with email_verified false.
	unverified := &fakeDirectory{}
	r = NewResolver(&fakeAuthn{name: "Alice", unverified: true}, &fakeStore{status: "ACTIVE"}, 0)
	r.Administer(unverified, "alice@example.test")
	if code, _ := call(t, Handler(r), "/api/auth/me", "good", ""); code != http.StatusOK || len(unverified.bootstrap) != 0 {
		t.Fatalf("an unverified email must not be bootstrapped: %d %v", code, unverified.bootstrap)
	}
}
