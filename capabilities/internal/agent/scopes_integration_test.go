//go:build integration

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package agent

import (
	"context"
	"database/sql"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
)

type subjectVerifier struct{ subject auth.Subject }

func (v subjectVerifier) VerifySubject(context.Context, string) (auth.Subject, error) {
	return v.subject, nil
}

func scopeDB(t *testing.T) (*sql.DB, *sql.DB) {
	t.Helper()
	db := agentDB(t)
	b, err := os.ReadFile("../../../platform/postgres/migrations/cap_ai/0007_agent_scopes.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(string(b)); err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO core.apps(app_id,display_name,version,status,spec) VALUES
 ('hr','HR','1','Ready','{}'),('procurement','Procurement','1','Ready','{}'),('finance','Finance','1','Ready','{}'),
 ('library','Library','1','Ready','{"knowledge":{"version":1,"personalLibrary":{"enabled":true,"allowedRoles":["role_library"]}}}'),
 ('pending','Pending','1','Pending','{}');
 INSERT INTO core.app_capabilities(app_id,cap_id) VALUES ('hr','cap-ai'),('procurement','cap-ai'),('finance','cap-ai'),('library','cap-ai'),('pending','cap-ai');
 INSERT INTO core.roles(name,app_id) VALUES ('role_hr','hr'),('role_procurement','procurement'),('role_finance','finance'),('role_library','library'),('role_pending','pending');
 INSERT INTO core.user_roles(user_id,role_name) VALUES ('11111111-1111-1111-1111-111111111111','role_hr'),('11111111-1111-1111-1111-111111111111','role_procurement'),('11111111-1111-1111-1111-111111111111','role_library'),('11111111-1111-1111-1111-111111111111','role_pending'),('11111111-1111-1111-1111-111111111111','role_operator')`)
	if err != nil {
		t.Fatal(err)
	}
	var name string
	if err = db.QueryRow(`SELECT current_database()`).Scan(&name); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	u.User = url.User("dxos_ai_api")
	apiDB, err := sql.Open("postgres", u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { apiDB.Close() })
	return db, apiDB
}
func TestAgentScopeListUsesAllCurrentRoles(t *testing.T) {
	_, db := scopeDB(t)
	got, err := (&PostgresScopeResolver{DB: db}).List(t.Context(), Actor{alice})
	if err != nil {
		t.Fatal(err)
	}
	want := []SourceScope{{"APP", "hr", "role_hr"}, {"APP", "library", "role_library"}, {"APP", "procurement", "role_procurement"}, {"PERSONAL", "library", "role_library"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("scopes: %+v", got)
	}
}
func TestAgentScopeListDropsRevokedRole(t *testing.T) {
	admin, db := scopeDB(t)
	resolver := &PostgresScopeResolver{DB: db}
	if _, err := resolver.List(t.Context(), Actor{alice}); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(`DELETE FROM core.user_roles WHERE user_id=$1 AND role_name='role_hr'`, alice); err != nil {
		t.Fatal(err)
	}
	got, err := resolver.List(t.Context(), Actor{alice})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range got {
		if s.ActingRole == "role_hr" {
			t.Fatal(got)
		}
	}
}
func TestAgentChatAuthenticationRequiresActiveUser(t *testing.T) {
	admin, db := scopeDB(t)
	a := ChatAuthenticator{Verifier: subjectVerifier{auth.Subject{UserID: alice, ExpiresAt: time.Now().Add(time.Hour)}}, DB: db}
	r := httptest.NewRequest("POST", "/chat", nil)
	r.Header.Set("Authorization", "Bearer token")
	r.Header.Set("X-Hasura-Role", "role_finance")
	actor, err := a.Authenticate(r)
	if err != nil || actor.UserID != alice {
		t.Fatalf("actor: %+v %v", actor, err)
	}
	if _, err := admin.Exec(`DELETE FROM core.user_roles WHERE user_id=$1`, alice); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Authenticate(r); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(`UPDATE core.users SET status='DISABLED' WHERE id=$1`, alice); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Authenticate(r); err == nil {
		t.Fatal("disabled accepted")
	}
}

func TestAgentScopeListRejectsOverflowAndInactiveApps(t *testing.T) {
	admin, db := scopeDB(t)
	resolver := &PostgresScopeResolver{DB: db}
	if _, err := admin.Exec(`DELETE FROM core.app_capabilities WHERE cap_id='cap-ai'`); err != nil {
		t.Fatal(err)
	}
	got, err := resolver.List(t.Context(), Actor{alice})
	if err != nil || len(got) != 0 {
		t.Fatalf("disabled capability: %+v %v", got, err)
	}
	if _, err := admin.Exec(`INSERT INTO core.app_capabilities(app_id,cap_id) VALUES ('hr','cap-ai');
 INSERT INTO core.roles(name,app_id) SELECT 'role_bulk_' || n,'hr' FROM generate_series(1,257) n;
 INSERT INTO core.user_roles(user_id,role_name) SELECT '11111111-1111-1111-1111-111111111111', 'role_bulk_' || n FROM generate_series(1,257) n`); err != nil {
		t.Fatal(err)
	}
	if got, err := resolver.List(t.Context(), Actor{alice}); err == nil {
		t.Fatalf("overflow silently returned %d scopes", len(got))
	}
	if _, err := admin.Exec(`SET ROLE dxos_ai_worker`); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(`RESET ROLE`)
	if _, err := admin.Exec(`SELECT cap_ai.agent_authorized_scopes($1)`, alice); err == nil {
		t.Fatal("worker can enumerate scopes")
	}
}
