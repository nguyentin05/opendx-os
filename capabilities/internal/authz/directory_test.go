// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package authz

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
)

type fakeAccounts struct {
	people []Person
	reads  int
	err    error
}

func (f *fakeAccounts) List(context.Context) ([]Person, error) {
	f.reads++
	return f.people, f.err
}

const (
	hoa     = "44444444-4444-4444-8444-444444444444"
	lan     = "55555555-5555-4555-8555-555555555555"
	worker  = "33333333-3333-4333-8333-000000000001"
	deleted = "66666666-6666-4666-8666-666666666666"
)

func person(id, email string, enabled, service bool) Person {
	return Person{Principal: auth.Principal{Subject: id, Email: email, Name: id[:4]}, Enabled: enabled, Service: service}
}

// The platform follows the identity provider: everyone read is mirrored with
// their status, accounts gone from a complete read are disabled, and a failed
// read changes nothing.
func TestAccountSyncFollowsTheIdentityProvider(t *testing.T) {
	store := &fakeStore{status: "ACTIVE", grants: []Grant{{Role: OperatorRole}}}
	accounts := &fakeAccounts{people: []Person{
		person(hoa, "hoa@example.org", true, false),
		person(lan, "lan@example.org", false, false),
		person(worker, "", true, true),
	}}
	dir := &fakeDirectory{recorded: []string{alice, hoa, lan, worker, deleted}}
	r := NewResolver(&fakeAuthn{name: "Alice"}, store, time.Minute)
	now := time.Date(2026, 11, 26, 9, 0, 0, 0, time.UTC)
	r.now = func() time.Time { return now }
	r.Administer(dir, "")
	r.MirrorAccounts(accounts)
	h := Handler(r)

	if code := do(t, h, "GET", "/api/auth/users", "good"); code != http.StatusOK {
		t.Fatalf("list: %d", code)
	}
	// The operator's own request, then Hoa and Lan; the service account is not a person.
	if accounts.reads != 1 || store.mirrors != 3 {
		t.Fatalf("reads=%d mirrors=%d, want 1 and 3", accounts.reads, store.mirrors)
	}
	want := []string{"status:" + hoa + ":true", "status:" + lan + ":false", "status:" + alice + ":false", "status:" + deleted + ":false"}
	if fmt.Sprint(dir.calls) != fmt.Sprint(want) {
		t.Fatalf("status calls = %v, want %v", dir.calls, want)
	}

	now = now.Add(accountSync / 2)
	do(t, h, "GET", "/api/auth/users", "good")
	if accounts.reads != 1 {
		t.Fatalf("read again within %s", accountSync)
	}

	now = now.Add(accountSync)
	dir.calls = nil
	accounts.err = errors.New("keycloak down")
	if code := do(t, h, "GET", "/api/auth/users", "good"); code != http.StatusOK || accounts.reads != 2 {
		t.Fatalf("a failed read must still list what is recorded: %d, reads=%d", code, accounts.reads)
	}
	if len(dir.calls) != 0 {
		t.Fatalf("a failed read changed statuses: %v", dir.calls)
	}

	// Without role_operator the identity provider is never read on request.
	other := &fakeAccounts{}
	r2 := NewResolver(&fakeAuthn{name: "Alice"}, &fakeStore{status: "ACTIVE", grants: []Grant{{Role: "role_review", AppID: "process_fixture"}}}, time.Minute)
	r2.Administer(&fakeDirectory{}, "")
	r2.MirrorAccounts(other)
	if code := do(t, Handler(r2), "GET", "/api/auth/users", "good"); code != http.StatusForbidden || other.reads != 0 {
		t.Fatalf("non-operator: %d, reads=%d", code, other.reads)
	}
}

// The periodic sync runs at once, then every interval, until stopped.
func TestRunAccountSync(t *testing.T) {
	accounts := &fakeAccounts{}
	r := NewResolver(&fakeAuthn{}, &fakeStore{status: "ACTIVE"}, time.Minute)
	r.Administer(&fakeDirectory{}, "")
	r.MirrorAccounts(accounts)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.RunAccountSync(ctx, time.Hour); close(done) }()
	for i := 0; i < 100 && func() bool { r.syncMu.Lock(); defer r.syncMu.Unlock(); return r.synced.IsZero() }(); i++ {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	if accounts.reads != 1 {
		t.Fatalf("reads = %d, want 1 at start", accounts.reads)
	}
}

// The Keycloak reader pages through the whole realm, and refuses to call a
// partial read complete.
func TestKeycloakAccountsList(t *testing.T) {
	var pages []string
	kc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer svc" || r.URL.Path != "/admin/realms/dxos/users" || r.Method != "GET" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		first, _ := strconv.Atoi(r.URL.Query().Get("first"))
		pages = append(pages, r.URL.Query().Get("first"))
		w.Header().Set("Content-Type", "application/json")
		if first > 0 {
			_, _ = io.WriteString(w, `[{"id":"88888888-8888-4888-8888-888888888888","username":"an","email":"an@example.org","enabled":true}]`)
			return
		}
		// A full page: the reader must ask for the next one.
		users := []string{
			`{"id":"44444444-4444-4444-8444-44444444444A","username":"hoa.pham","email":"hoa@example.org","firstName":"Phạm Thị","lastName":"Hoa","enabled":true}`,
			`{"id":"55555555-5555-4555-8555-555555555555","username":"service-account-dxos-rag-worker","email":"","enabled":true}`,
			`{"id":"66666666-6666-4666-8666-666666666666","username":"no.mail","enabled":true}`,
			`{"id":"77777777-7777-4777-8777-777777777777","username":"left","email":"left@example.org","enabled":false}`,
		}
		for i := len(users); i < keycloakPage; i++ {
			users = append(users, fmt.Sprintf(`{"id":"not-a-uuid-%d","username":"x%d","email":"x%d@example.org","enabled":true}`, i, i, i))
		}
		_, _ = io.WriteString(w, "["+strings.Join(users, ",")+"]")
	}))
	defer kc.Close()
	k := &KeycloakAccounts{BaseURL: kc.URL, Realm: "dxos", Client: kc.Client(),
		Token: func(context.Context) (string, error) { return "svc", nil }}
	people, err := k.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []Person{
		{Principal: auth.Principal{Subject: "44444444-4444-4444-8444-44444444444a", Email: "hoa@example.org", Name: "Phạm Thị Hoa"}, Enabled: true},
		{Principal: auth.Principal{Subject: "55555555-5555-4555-8555-555555555555", Name: "service-account-dxos-rag-worker"}, Enabled: true, Service: true},
		{Principal: auth.Principal{Subject: "66666666-6666-4666-8666-666666666666", Name: "no.mail"}, Enabled: true},
		{Principal: auth.Principal{Subject: "77777777-7777-4777-8777-777777777777", Email: "left@example.org", Name: "left"}},
		{Principal: auth.Principal{Subject: "88888888-8888-4888-8888-888888888888", Email: "an@example.org", Name: "an"}, Enabled: true},
	}
	if fmt.Sprint(people) != fmt.Sprint(want) {
		t.Fatalf("people = %v, want %v", people, want)
	}
	if fmt.Sprint(pages) != "[0 200]" {
		t.Fatalf("pages = %v", pages)
	}

	k.Token = func(context.Context) (string, error) { return "wrong", nil }
	if _, err := k.List(context.Background()); err == nil {
		t.Fatal("a refused read must fail")
	}

	// A realm that never ends a page is never reported as complete.
	full := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		users := make([]string, keycloakPage)
		for i := range users {
			users[i] = `{"id":"88888888-8888-4888-8888-888888888888","username":"same","email":"s@example.org","enabled":true}`
		}
		_, _ = io.WriteString(w, "["+strings.Join(users, ",")+"]")
	}))
	defer full.Close()
	k = &KeycloakAccounts{BaseURL: full.URL, Realm: "dxos", Client: full.Client(),
		Token: func(context.Context) (string, error) { return "svc", nil }}
	if _, err := k.List(context.Background()); !errors.Is(err, errTooManyAccounts) {
		t.Fatalf("an endless realm: %v, want errTooManyAccounts", err)
	}
}
