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

package hr

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
	"github.com/opendx-os/opendx-os/capabilities/internal/authz"
)

const (
	admin  = "11111111-1111-4111-8111-111111111111"
	alice  = "22222222-2222-4222-8222-222222222222"
	sales  = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	design = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
)

// fakeSessions knows two tokens: "admin" holds role_hr_admin, "alice" does not.
type fakeSessions struct{ err error }

func (f fakeSessions) Resolve(_ context.Context, token string) (*authz.Session, error) {
	if f.err != nil {
		return nil, f.err
	}
	switch token {
	case "admin":
		return &authz.Session{User: auth.Principal{Subject: admin}, Grants: []authz.Grant{{Role: AdminRole, CapID: "cap-hr"}}}, nil
	case "alice":
		return &authz.Session{User: auth.Principal{Subject: alice}, Grants: []authz.Grant{{Role: "role_procurement_requester"}}}, nil
	}
	return nil, authz.ErrUnauthenticated
}

type fakeOrg struct {
	members map[string]*Member
	calls   []string
	err     error
}

func (f *fakeOrg) OrgUnits(context.Context) ([]OrgUnit, error) {
	return []OrgUnit{{ID: sales, Code: "KD", Name: "Sales"}, {ID: design, Code: "TK", Name: "Design"}}, f.err
}
func (f *fakeOrg) OrgUnit(_ context.Context, id string) (*OrgUnit, error) {
	if id != sales && id != design {
		return nil, ErrNotFound
	}
	return &OrgUnit{ID: id, Code: "KD", Name: "Sales"}, f.err
}
func (f *fakeOrg) Staff(_ context.Context, unit string) ([]Member, error) {
	f.calls = append(f.calls, "staff:"+unit)
	return []Member{{UserID: alice, OrgUnitID: sales}}, f.err
}
func (f *fakeOrg) Membership(_ context.Context, user string) (*Member, error) {
	return f.members[user], f.err
}
func (f *fakeOrg) CreateOrgUnit(_ context.Context, actor, code, name string, parent *string) (string, error) {
	p := "-"
	if parent != nil {
		p = *parent
	}
	f.calls = append(f.calls, "create:"+actor+":"+code+":"+name+":"+p)
	return design, f.err
}
func (f *fakeOrg) UpdateOrgUnit(_ context.Context, actor string, u OrgUnit) error {
	p := "-"
	if u.ParentID != nil {
		p = *u.ParentID
	}
	f.calls = append(f.calls, "update:"+actor+":"+u.ID+":"+u.Code+":"+u.Name+":"+p)
	return f.err
}
func (f *fakeOrg) AssignStaff(_ context.Context, actor, user, unit string, position *string) error {
	p := "-"
	if position != nil {
		p = *position
	}
	f.calls = append(f.calls, "assign:"+actor+":"+user+":"+unit+":"+p)
	return f.err
}
func (f *fakeOrg) RemoveStaff(_ context.Context, actor, user string) error {
	f.calls = append(f.calls, "remove:"+actor+":"+user)
	return f.err
}

func request(t *testing.T, h http.Handler, method, path, token, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	var decoded map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &decoded)
	return w.Code, decoded
}

func newHandler() (http.Handler, *fakeOrg) {
	org := &fakeOrg{members: map[string]*Member{alice: {UserID: alice, OrgUnitID: sales}}}
	return Handler(fakeSessions{}, org), org
}

func TestOnlyHRAdministratorsChangeTheOrganization(t *testing.T) {
	h, org := newHandler()
	writes := []struct{ method, path, body string }{
		{"POST", "/api/cap/hr/org-units", `{"code":"DA","name":"Projects"}`},
		{"PATCH", "/api/cap/hr/org-units/" + sales, `{"name":"Sales"}`},
		{"PUT", "/api/cap/hr/staff/" + alice, `{"org_unit_id":"` + design + `"}`},
		{"DELETE", "/api/cap/hr/staff/" + alice, ``},
	}
	for _, c := range writes {
		if code, _ := request(t, h, c.method, c.path, "alice", c.body); code != http.StatusForbidden {
			t.Errorf("%s %s without role_hr_admin: %d, want 403", c.method, c.path, code)
		}
		if code, _ := request(t, h, c.method, c.path, "", c.body); code != http.StatusUnauthorized {
			t.Errorf("%s %s without a token: %d, want 401", c.method, c.path, code)
		}
		if code, _ := request(t, h, c.method, c.path, "forged", c.body); code != http.StatusUnauthorized {
			t.Errorf("%s %s with a bad token: %d, want 401", c.method, c.path, code)
		}
	}
	if len(org.calls) != 0 {
		t.Fatalf("a caller without role_hr_admin reached the store: %v", org.calls)
	}

	if code, body := request(t, h, "POST", "/api/cap/hr/org-units", "admin", `{"code":" DA ","name":"Projects","parent_id":"`+strings.ToUpper(sales)+`"}`); code != http.StatusCreated || body["id"] != design {
		t.Fatalf("create: %d %v", code, body)
	}
	// Only the fields sent change; null makes the department a root.
	if code, _ := request(t, h, "PATCH", "/api/cap/hr/org-units/"+sales, "admin", `{"name":"Sales and Marketing","parent_id":null}`); code != http.StatusNoContent {
		t.Fatalf("update: %d", code)
	}
	if code, _ := request(t, h, "PUT", "/api/cap/hr/staff/"+alice, "admin", `{"org_unit_id":"`+design+`","position":"Lead"}`); code != http.StatusNoContent {
		t.Fatalf("assign: %d", code)
	}
	if code, _ := request(t, h, "DELETE", "/api/cap/hr/staff/"+alice, "admin", ``); code != http.StatusNoContent {
		t.Fatalf("remove: %d", code)
	}
	want := []string{
		"create:" + admin + ":DA:Projects:" + sales,
		"update:" + admin + ":" + sales + ":KD:Sales and Marketing:-",
		"assign:" + admin + ":" + alice + ":" + design + ":Lead",
		"remove:" + admin + ":" + alice,
	}
	if strings.Join(org.calls, "\n") != strings.Join(want, "\n") {
		t.Fatalf("store calls:\n%s\nwant:\n%s", strings.Join(org.calls, "\n"), strings.Join(want, "\n"))
	}
}

func TestMalformedChangesAreRefusedBeforeTheStore(t *testing.T) {
	h, org := newHandler()
	for name, c := range map[string]struct{ method, path, body string }{
		"no name":                  {"POST", "/api/cap/hr/org-units", `{"code":"DA"}`},
		"blank code":               {"POST", "/api/cap/hr/org-units", `{"code":"  ","name":"Projects"}`},
		"null name":                {"POST", "/api/cap/hr/org-units", `{"code":"DA","name":null}`},
		"parent that is not an id": {"POST", "/api/cap/hr/org-units", `{"code":"DA","name":"Projects","parent_id":"KD"}`},
		"an unknown field":         {"POST", "/api/cap/hr/org-units", `{"code":"DA","name":"Projects","id":"` + sales + `"}`},
		"not an object":            {"POST", "/api/cap/hr/org-units", `["DA"]`},
		"an empty update":          {"PATCH", "/api/cap/hr/org-units/" + sales, `{}`},
		"update with a number":     {"PATCH", "/api/cap/hr/org-units/" + sales, `{"name":5}`},
		"assign without a unit":    {"PUT", "/api/cap/hr/staff/" + alice, `{"position":"Lead"}`},
		"assign to null":           {"PUT", "/api/cap/hr/staff/" + alice, `{"org_unit_id":null}`},
		"a name that is too long":  {"POST", "/api/cap/hr/org-units", `{"code":"DA","name":"` + strings.Repeat("x", maxText+1) + `"}`},
	} {
		if code, _ := request(t, h, c.method, c.path, "admin", c.body); code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", name, code)
		}
	}
	for name, c := range map[string]struct{ method, path, body string }{
		"update of an unknown department": {"PATCH", "/api/cap/hr/org-units/cccccccc-cccc-4ccc-8ccc-cccccccccccc", `{"name":"X"}`},
		"a department id that is no id":   {"PATCH", "/api/cap/hr/org-units/sales", `{"name":"X"}`},
		"a user id that is no id":         {"PUT", "/api/cap/hr/staff/alice", `{"org_unit_id":"` + sales + `"}`},
	} {
		if code, _ := request(t, h, c.method, c.path, "admin", c.body); code != http.StatusNotFound {
			t.Errorf("%s: %d, want 404", name, code)
		}
	}
	if len(org.calls) != 0 {
		t.Fatalf("a malformed change reached the store: %v", org.calls)
	}

	// The store's refusals keep their meaning for the caller.
	org.err = conflict{"a department with this code already exists"}
	if code, body := request(t, h, "POST", "/api/cap/hr/org-units", "admin", `{"code":"KD","name":"Sales"}`); code != http.StatusConflict || body["error"] != "a department with this code already exists" {
		t.Fatalf("conflict: %d %v", code, body)
	}
	org.err = ErrNotFound
	if code, _ := request(t, h, "DELETE", "/api/cap/hr/staff/"+alice, "admin", ``); code != http.StatusNotFound {
		t.Fatalf("not found: %d", code)
	}
	org.err = errors.New("connection reset")
	if code, body := request(t, h, "DELETE", "/api/cap/hr/staff/"+alice, "admin", ``); code != http.StatusServiceUnavailable || strings.Contains(body["error"].(string), "connection") {
		t.Fatalf("a store failure must not leak its cause: %d %v", code, body)
	}
}

// Everyone reads the department list and the people of their own department;
// another department's staff is for administrators.
func TestReadsFollowTheCallersDepartment(t *testing.T) {
	h, org := newHandler()
	if code, body := request(t, h, "GET", "/api/cap/hr/org-units", "alice", ""); code != http.StatusOK || len(body["org_units"].([]any)) != 2 {
		t.Fatalf("department list: %d %v", code, body)
	}
	code, body := request(t, h, "GET", "/api/cap/hr/me", "alice", "")
	if code != http.StatusOK || body["user_id"] != alice || body["org_unit"].(map[string]any)["id"] != sales || body["administrator"] != false {
		t.Fatalf("me: %d %v", code, body)
	}
	if code, body := request(t, h, "GET", "/api/cap/hr/me", "admin", ""); code != http.StatusOK || body["org_unit"] != nil || body["administrator"] != true {
		t.Fatalf("me without a department: %d %v", code, body)
	}

	if code, _ := request(t, h, "GET", "/api/cap/hr/staff", "alice", ""); code != http.StatusOK {
		t.Fatalf("own staff: %d", code)
	}
	if code, _ := request(t, h, "GET", "/api/cap/hr/staff?org_unit_id="+strings.ToUpper(sales), "alice", ""); code != http.StatusOK {
		t.Fatalf("own staff by id: %d", code)
	}
	if code, _ := request(t, h, "GET", "/api/cap/hr/staff?org_unit_id="+design, "alice", ""); code != http.StatusForbidden {
		t.Fatalf("another department's staff: %d, want 403", code)
	}
	if code, _ := request(t, h, "GET", "/api/cap/hr/staff?org_unit_id=KD", "alice", ""); code != http.StatusBadRequest {
		t.Fatalf("a department id that is no id: %d, want 400", code)
	}
	if got := strings.Join(org.calls, ","); got != "staff:"+sales+",staff:"+sales {
		t.Fatalf("a member reads only their own department: %s", got)
	}

	org.calls = nil
	request(t, h, "GET", "/api/cap/hr/staff", "admin", "")
	request(t, h, "GET", "/api/cap/hr/staff?org_unit_id="+design, "admin", "")
	if got := strings.Join(org.calls, ","); got != "staff:,staff:"+design {
		t.Fatalf("an administrator reads every department or the one named: %s", got)
	}

	// Someone without a department sees an empty list, not everyone.
	nobody := Handler(fakeSessions{}, &fakeOrg{members: map[string]*Member{}})
	if code, body := request(t, nobody, "GET", "/api/cap/hr/staff", "alice", ""); code != http.StatusOK || len(body["staff"].([]any)) != 0 {
		t.Fatalf("no department: %d %v", code, body)
	}
	down := Handler(fakeSessions{err: errors.New("database down")}, org)
	if code, _ := request(t, down, "GET", "/api/cap/hr/me", "alice", ""); code != http.StatusServiceUnavailable {
		t.Fatalf("authorization failure: %d, want 503", code)
	}
}
