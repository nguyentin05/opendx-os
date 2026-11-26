// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package authz

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const created = "44444444-4444-4444-8444-444444444444"

type fakeAccounts struct {
	made    []NewAccount
	deleted []string
	err     error
}

func (f *fakeAccounts) Create(_ context.Context, a NewAccount, password string) (string, error) {
	if len(password) != 26 {
		return "", errors.New("temporary password is not 26 characters")
	}
	f.made = append(f.made, a)
	return created, f.err
}

func (f *fakeAccounts) Delete(_ context.Context, id string) error {
	f.deleted = append(f.deleted, id)
	return nil
}

func provisioning(accounts Accounts, grants ...Grant) (http.Handler, *fakeDirectory) {
	dir := &fakeDirectory{}
	r := NewResolver(&fakeAuthn{name: "Alice"}, &fakeStore{status: "ACTIVE", grants: grants}, time.Minute)
	r.Administer(dir, "")
	if accounts != nil {
		r.Provision(accounts)
	}
	return Handler(r), dir
}

func post(h http.Handler, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", "/api/auth/users", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer good")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

var operator = Grant{Role: OperatorRole}

func TestAccountCreationIsForOperatorsAndNeedsAProvisioner(t *testing.T) {
	accounts := &fakeAccounts{}
	h, _ := provisioning(accounts, Grant{Role: "role_review", AppID: "process_fixture"})
	if w := post(h, `{"email":"bob@example.org","display_name":"Bob"}`); w.Code != http.StatusForbidden {
		t.Fatalf("without role_operator: %d, want 403", w.Code)
	}
	if len(accounts.made) != 0 {
		t.Fatal("a caller without role_operator created an account")
	}
	h, _ = provisioning(nil, operator)
	if w := post(h, `{"email":"bob@example.org","display_name":"Bob"}`); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("without a provisioner: %d, want 503", w.Code)
	}
}

func TestAccountCreationRecordsTheAccountBeforeTheFirstSignIn(t *testing.T) {
	accounts := &fakeAccounts{}
	h, dir := provisioning(accounts, operator)
	w := post(h, `{"email":"Bob.Tran@Example.org","display_name":"  Trần   Văn Bob "}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Error("the response carrying a password must not be cached")
	}
	var body map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["id"] != created || len(body["temporary_password"]) != 26 || body["username"] != "bob.tran@example.org" ||
		body["display_name"] != "Trần Văn Bob" {
		t.Fatalf("body = %v", body)
	}
	want := "create:" + alice + ":" + created + ":Bob.Tran@Example.org:Trần Văn Bob"
	if len(dir.calls) != 1 || dir.calls[0] != want {
		t.Fatalf("directory calls = %q, want %q (the operator is the recorded actor)", dir.calls, want)
	}
}

func TestAccountCreationRefusesBadInput(t *testing.T) {
	accounts := &fakeAccounts{}
	h, _ := provisioning(accounts, operator)
	for _, body := range []string{
		`{"email":"not an address","display_name":"Bob"}`,
		`{"email":"Bob <bob@example.org>","display_name":"Bob"}`,
		`{"email":"bob@example.org","display_name":"   "}`,
		`{"email":"bob@example.org","display_name":"Bob","username":"B!"}`,
		`{"email":"bob@example.org","display_name":"Bob","roles":["role_operator"]}`,
		`not json`,
	} {
		if w := post(h, body); w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", body, w.Code)
		}
	}
	if len(accounts.made) != 0 {
		t.Fatalf("invalid input reached the identity provider: %v", accounts.made)
	}
}

func TestAccountCreationConflictAndRollback(t *testing.T) {
	h, dir := provisioning(&fakeAccounts{err: ErrExists}, operator)
	if w := post(h, `{"email":"bob@example.org","display_name":"Bob"}`); w.Code != http.StatusConflict {
		t.Fatalf("existing account: %d, want 409", w.Code)
	}
	if len(dir.calls) != 0 {
		t.Fatal("an account that was not created was recorded")
	}

	accounts := &fakeAccounts{}
	h, dir = provisioning(accounts, operator)
	dir.err = errors.New("database down")
	if w := post(h, `{"email":"bob@example.org","display_name":"Bob"}`); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("unrecorded account: %d, want 503", w.Code)
	}
	if len(accounts.deleted) != 1 || accounts.deleted[0] != created {
		t.Fatalf("the unrecorded Keycloak account was not removed: %v", accounts.deleted)
	}
}

// The Keycloak client asks for exactly what a provisioned account needs.
func TestKeycloakAccounts(t *testing.T) {
	var got map[string]any
	var deleted string
	kc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer svc" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case r.Method == "POST" && r.URL.Path == "/admin/realms/dxos/users":
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &got)
			if got["email"] == "taken@example.org" {
				w.WriteHeader(http.StatusConflict)
				return
			}
			w.Header().Set("Location", "http://kc/admin/realms/dxos/users/"+strings.ToUpper(created))
			w.WriteHeader(http.StatusCreated)
		case r.Method == "DELETE" && strings.HasPrefix(r.URL.Path, "/admin/realms/dxos/users/"):
			deleted = strings.TrimPrefix(r.URL.Path, "/admin/realms/dxos/users/")
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer kc.Close()
	k := &KeycloakAccounts{BaseURL: kc.URL, Realm: "dxos", Client: kc.Client(),
		Token: func(context.Context) (string, error) { return "svc", nil }}

	id, err := k.Create(context.Background(), NewAccount{Email: "an@example.org", DisplayName: "Nguyễn Văn An", Username: "an"}, "TmpPassword12345")
	if err != nil || id != created {
		t.Fatalf("create = %q, %v", id, err)
	}
	if got["firstName"] != "Nguyễn Văn" || got["lastName"] != "An" || got["enabled"] != true {
		t.Errorf("profile = %v", got)
	}
	if actions, _ := got["requiredActions"].([]any); len(actions) != 1 || actions[0] != "UPDATE_PASSWORD" {
		t.Errorf("requiredActions = %v", got["requiredActions"])
	}
	creds, _ := got["credentials"].([]any)
	if c, _ := creds[0].(map[string]any); c["temporary"] != true || c["value"] != "TmpPassword12345" {
		t.Errorf("credentials = %v", creds)
	}
	if _, err := k.Create(context.Background(), NewAccount{Email: "taken@example.org", DisplayName: "X"}, "p"); !errors.Is(err, ErrExists) {
		t.Errorf("taken email: %v, want ErrExists", err)
	}
	if err := k.Delete(context.Background(), created); err != nil || deleted != created {
		t.Errorf("delete = %v, %q", err, deleted)
	}
}
