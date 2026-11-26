// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package authz

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
)

type fakeAccounts struct {
	people []auth.Principal
	reads  int
	err    error
}

func (f *fakeAccounts) List(context.Context) ([]auth.Principal, error) {
	f.reads++
	return f.people, f.err
}

// The operator's account list mirrors people who have not signed in yet, at
// most once per accountSync; the read never blocks the list.
func TestAccountListMirrorsTheIdentityProvider(t *testing.T) {
	store := &fakeStore{status: "ACTIVE", grants: []Grant{{Role: OperatorRole}}}
	accounts := &fakeAccounts{people: []auth.Principal{
		{Subject: "44444444-4444-4444-8444-444444444444", Email: "hoa@example.org", Name: "Phạm Thị Hoa"},
		{Subject: "55555555-5555-4555-8555-555555555555", Email: "binh@example.org", Name: "Binh"},
	}}
	r := NewResolver(&fakeAuthn{name: "Alice"}, store, time.Minute)
	now := time.Date(2026, 11, 26, 9, 0, 0, 0, time.UTC)
	r.now = func() time.Time { return now }
	r.Administer(&fakeDirectory{}, "")
	r.MirrorAccounts(accounts)
	h := Handler(r)

	if code := do(t, h, "GET", "/api/auth/users", "good"); code != http.StatusOK {
		t.Fatalf("list: %d", code)
	}
	// One mirror for the operator's own request, one per person read.
	if accounts.reads != 1 || store.mirrors != 3 {
		t.Fatalf("reads=%d mirrors=%d, want 1 and 3", accounts.reads, store.mirrors)
	}
	now = now.Add(accountSync / 2)
	do(t, h, "GET", "/api/auth/users", "good")
	if accounts.reads != 1 {
		t.Fatalf("read again within %s", accountSync)
	}
	now = now.Add(accountSync)
	accounts.err = errors.New("keycloak down")
	if code := do(t, h, "GET", "/api/auth/users", "good"); code != http.StatusOK || accounts.reads != 2 {
		t.Fatalf("a failed read must still list what is recorded: %d, reads=%d", code, accounts.reads)
	}

	// Without role_operator the identity provider is never read.
	other := &fakeAccounts{}
	r2 := NewResolver(&fakeAuthn{name: "Alice"}, &fakeStore{status: "ACTIVE", grants: []Grant{{Role: "role_review", AppID: "process_fixture"}}}, time.Minute)
	r2.Administer(&fakeDirectory{}, "")
	r2.MirrorAccounts(other)
	if code := do(t, Handler(r2), "GET", "/api/auth/users", "good"); code != http.StatusForbidden || other.reads != 0 {
		t.Fatalf("non-operator: %d, reads=%d", code, other.reads)
	}
}

// The Keycloak reader pages through the realm and keeps only people.
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
		if first == 0 {
			// A full page: the reader must ask for the next one.
			fmt.Fprint(w, "[")
			for i := 0; i < keycloakPage; i++ {
				if i > 0 {
					fmt.Fprint(w, ",")
				}
				switch i {
				case 0:
					fmt.Fprint(w, `{"id":"44444444-4444-4444-8444-44444444444A","username":"hoa.pham","email":"hoa@example.org","firstName":"Phạm Thị","lastName":"Hoa","enabled":true}`)
				case 1:
					fmt.Fprint(w, `{"id":"55555555-5555-4555-8555-555555555555","username":"service-account-dxos-rag-worker","email":"","enabled":true}`)
				case 2:
					fmt.Fprint(w, `{"id":"66666666-6666-4666-8666-666666666666","username":"no.mail","enabled":true}`)
				case 3:
					fmt.Fprint(w, `{"id":"77777777-7777-4777-8777-777777777777","username":"left","email":"left@example.org","enabled":false}`)
				default:
					fmt.Fprintf(w, `{"id":"not-a-uuid-%d","username":"x%d","email":"x%d@example.org","enabled":true}`, i, i, i)
				}
			}
			fmt.Fprint(w, "]")
			return
		}
		fmt.Fprint(w, `[{"id":"88888888-8888-4888-8888-888888888888","username":"an","email":"an@example.org","enabled":true}]`)
	}))
	defer kc.Close()
	k := &KeycloakAccounts{BaseURL: kc.URL, Realm: "dxos", Client: kc.Client(),
		Token: func(context.Context) (string, error) { return "svc", nil }}
	people, err := k.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []auth.Principal{
		{Subject: "44444444-4444-4444-8444-44444444444a", Email: "hoa@example.org", Name: "Phạm Thị Hoa"},
		{Subject: "88888888-8888-4888-8888-888888888888", Email: "an@example.org", Name: "an"},
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
}
