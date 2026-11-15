//go:build integration

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
package knowledge

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
	"strings"
	"testing"
)

const managerID = "88888888-8888-4888-8888-888888888888"

func restrictedFixture(t *testing.T) (*sql.DB, *Store, *auth.Identity, *auth.Identity, string) {
	db, store, an, _ := packageFixture(t)
	_, e := db.Exec(`INSERT INTO core.users(id,email,display_name) VALUES('88888888-8888-4888-8888-888888888888','manager@test.invalid','Manager');INSERT INTO core.roles(name,app_id) VALUES('role_library_manager','library');INSERT INTO core.user_roles(user_id,role_name) VALUES('88888888-8888-4888-8888-888888888888','role_library_manager');UPDATE core.apps SET spec=jsonb_set(jsonb_set(spec,'{knowledge,sources,0,access}','{"mode":"RESTRICTED","allowedRoles":["role_library_user"],"grantsManagedBy":"hr-managers"}'),'{knowledge,grantPolicies}','{"hr-managers":{"allowedRoles":["role_library_manager"]}}') WHERE app_id='library'`)
	if e != nil {
		t.Fatal(e)
	}
	if e = store.ReconcilePackage(context.Background(), "library", 1, strings.Repeat("b", 64)); e != nil {
		t.Fatal(e)
	}
	var doc string
	db.QueryRow(`SELECT id::text FROM cap_ai.knowledge_documents WHERE source_kind='PACKAGE'`).Scan(&doc)
	return db, store, an, &auth.Identity{UserID: managerID, Role: "role_library_manager"}, doc
}
func TestRestrictedAnBinh(t *testing.T) {
	_, store, an, manager, doc := restrictedFixture(t)
	ctx := context.Background()
	binh := &auth.Identity{UserID: otherID, Role: an.Role}
	page, e := store.GrantManagement(ctx, manager, doc, "")
	if e != nil {
		t.Fatal(e)
	}
	if e = store.GrantRead(ctx, manager, "grant-binh", doc, otherID, page.Revision); e != nil {
		t.Fatal(e)
	}
	if _, e = NewPolicy(store.db).CanRead(ctx, an, doc); e == nil {
		t.Fatal("An without grant read restricted source")
	}
	if _, e = NewPolicy(store.db).CanRead(ctx, binh, doc); e != nil {
		t.Fatal("Binh named grant denied", e)
	}
	if e = store.GrantRead(ctx, manager, "grant-binh", doc, otherID, page.Revision); e != nil {
		t.Fatal("same receipt conflicts with own ETag", e)
	}
	latest, e := store.GrantManagement(ctx, manager, doc, "")
	if e != nil {
		t.Fatal(e)
	}
	if e = store.RevokeRead(ctx, manager, "revoke-binh", doc, otherID, latest.Revision); e != nil {
		t.Fatal(e)
	}
	if _, e = NewPolicy(store.db).CanRead(ctx, binh, doc); e == nil {
		t.Fatal("revoked named grant still readable")
	}
}
func TestManagerNotReader(t *testing.T) {
	_, store, _, manager, doc := restrictedFixture(t)
	ctx := context.Background()
	page, e := store.GrantManagement(ctx, manager, doc, "")
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(page)
	if strings.Contains(string(raw), "title") || strings.Contains(string(raw), "Director") || strings.Contains(string(raw), "artifact_ref") {
		t.Fatal("management leaked source content", string(raw))
	}
	if _, e = NewPolicy(store.db).CanRead(ctx, manager, doc); e == nil {
		t.Fatal("MANAGE implied READ")
	}
}
func TestGrantGenerationABA(t *testing.T) {
	db, store, an, manager, doc := restrictedFixture(t)
	ctx := context.Background()
	page, e := store.GrantManagement(ctx, manager, doc, "")
	if e != nil {
		t.Fatal(e)
	}
	if e = store.GrantRead(ctx, manager, "grant-a", doc, an.UserID, page.Revision); e != nil {
		t.Fatal(e)
	}
	for _, mode := range []string{"ROLE", "RESTRICTED"} {
		_, e = db.Exec(`UPDATE core.apps SET publication_generation=publication_generation+1,spec=jsonb_set(jsonb_set(jsonb_set(spec,'{_publication,generation}',to_jsonb(publication_generation+1)),'{_publication,sources,0,authorization_generation}',to_jsonb(publication_generation+1)),'{knowledge,sources,0,access,mode}',to_jsonb($1::text)) WHERE app_id='library'`, mode)
		if e != nil {
			t.Fatal(e)
		}
	}
	if _, e = NewPolicy(store.db).CanRead(ctx, an, doc); e == nil {
		t.Fatal("historical grant revived before dispatcher caught up")
	}
	if e = store.ReconcilePackage(ctx, "library", 3, strings.Repeat("b", 64)); e != nil {
		t.Fatal(e)
	}
	if _, e = NewPolicy(store.db).CanRead(ctx, an, doc); e == nil {
		t.Fatal("historical grant revived after reconcile")
	}
}
func TestPrivateNeverGrantable(t *testing.T) {
	_, store, owner, manager, _ := restrictedFixture(t)
	ctx := context.Background()
	reg, e := store.Register(ctx, owner, "private", RegisterRequest{FileID: fileID, ParserProfile: "standard-text-v1"})
	if e != nil {
		t.Fatal(e)
	}
	if e = store.GrantRead(ctx, manager, "private-grant", reg.DocumentID, otherID, reg.Revision); e == nil {
		t.Fatal("private document grant allowed")
	}
	if _, e = store.GrantManagement(ctx, owner, reg.DocumentID, ""); e == nil {
		t.Fatal("private owner obtained grant management")
	}
}

func TestGrantRequiresCurrentManagerAndRevision(t *testing.T) {
	db, store, an, manager, doc := restrictedFixture(t)
	ctx := context.Background()
	page, e := store.GrantManagement(ctx, manager, doc, "")
	if e != nil {
		t.Fatal(e)
	}
	if e = store.GrantRead(ctx, manager, "one", doc, an.UserID, page.Revision); e != nil {
		t.Fatal(e)
	}
	if e = store.GrantRead(ctx, manager, "one", doc, otherID, page.Revision); e != ErrConflict {
		t.Fatal("key reused with another target", e)
	}
	if e = store.GrantRead(ctx, manager, "two", doc, otherID, page.Revision); e != ErrConflict {
		t.Fatal("stale revision accepted", e)
	}
	db.Exec(`DELETE FROM core.user_roles WHERE user_id=$1 AND role_name=$2`, manager.UserID, manager.Role)
	if _, e = store.GrantManagement(ctx, manager, doc, ""); e == nil {
		t.Fatal("stale JWT retained management")
	}
	db.Exec(`INSERT INTO core.user_roles(user_id,role_name) VALUES($1,$2)`, manager.UserID, manager.Role)
	page, e = store.GrantManagement(ctx, manager, doc, "")
	if e != nil {
		t.Fatal("fresh role grant required new JWT", e)
	}
	db.Exec(`UPDATE core.apps SET spec=jsonb_set(spec,'{knowledge,grantPolicies,hr-managers,allowedRoles}','[]') WHERE app_id='library'`)
	if e = store.GrantRead(ctx, manager, "three", doc, otherID, page.Revision); e == nil {
		t.Fatal("old manager policy accepted")
	}
}
func TestConcurrentGrantRevision(t *testing.T) {
	db, store, an, manager, doc := restrictedFixture(t)
	ctx := context.Background()
	page, e := store.GrantManagement(ctx, manager, doc, "")
	if e != nil {
		t.Fatal(e)
	}
	results := make(chan error, 2)
	for _, target := range []string{an.UserID, otherID} {
		go func(target string) {
			results <- store.GrantRead(ctx, manager, "grant-"+target, doc, target, page.Revision)
		}(target)
	}
	success := 0
	for range 2 {
		e := <-results
		if e == nil {
			success++
		} else if e != ErrConflict {
			t.Fatal(e)
		}
	}
	if success != 1 {
		t.Fatal("concurrent stale manager write", success)
	}
	for _, query := range []string{`SELECT count(*) FROM cap_ai.document_grants WHERE revoked_at IS NULL`, `SELECT count(*) FROM cap_ai.request_receipts WHERE action='grant-read'`, `SELECT count(*) FROM core.audit_logs WHERE action='knowledge.grant-read'`} {
		var count int
		if e = db.QueryRow(query).Scan(&count); e != nil || count != 1 {
			t.Fatal("non-atomic grant/receipt/audit", count, e)
		}
	}
}
