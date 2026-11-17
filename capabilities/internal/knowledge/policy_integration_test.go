//go:build integration

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
package knowledge

import (
	"context"
	"database/sql"
	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
	"github.com/opendx-os/opendx-os/capabilities/internal/testutil"
	"net/url"
	"os"
	"os/exec"
	"testing"
)

const ownerID = "11111111-1111-4111-8111-111111111111"
const otherID = "22222222-2222-4222-8222-222222222222"
const fileID = "33333333-3333-4333-8333-333333333333"
const collectionID = "44444444-4444-4444-8444-444444444444"
const documentID = "55555555-5555-4555-8555-555555555555"

func metadataFixture(t *testing.T) (*sql.DB, *sql.DB, *auth.Identity) {
	t.Helper()
	db := testutil.Database(t)
	for _, p := range []string{"init/01_core_schema.sql", "init/02_capability_registry.sql", "init/03_app_registry.sql", "init/05_operator_deployments.sql", "init/06_runtime_roles.sql", "init/07_app_definitions.sql", "capabilities/cap_ai.sql", "migrations/cap_ai/0001_metadata_jobs.sql"} {
		raw, e := os.ReadFile("../../../platform/postgres/" + p)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = db.Exec(string(raw)); e != nil {
			t.Fatalf("%s: %v", p, e)
		}
	}
	_, e := db.Exec(`INSERT INTO core.capabilities(cap_id,display_name,category_hpdi,base_route,enabled) VALUES('cap-process','Process','P','/api/cap/process',true);
 INSERT INTO core.apps(app_id,display_name,version,status,spec) VALUES('library','Library','1','Ready','{"capabilities":["cap-process","cap-ai"],"roles":[{"name":"role_library_user"}],"tables":[],"knowledge":{"version":1,"personalLibrary":{"enabled":true,"allowedRoles":["role_library_user"]}}}');
 INSERT INTO core.app_capabilities(app_id,cap_id) VALUES('library','cap-process'),('library','cap-ai');
 INSERT INTO core.roles(name,app_id) VALUES('role_library_user','library');
 INSERT INTO core.users(id,email,display_name) VALUES('11111111-1111-4111-8111-111111111111','a@test.invalid','A'),('22222222-2222-4222-8222-222222222222','b@test.invalid','B');
 INSERT INTO core.user_roles(user_id,role_name) VALUES('11111111-1111-4111-8111-111111111111','role_library_user'),('22222222-2222-4222-8222-222222222222','role_library_user');
 INSERT INTO core.files(id,app_id,bucket,object_key,file_name,scan_status,sha256_hash,uploaded_by,mime_type,size_bytes,read_policy) VALUES('33333333-3333-4333-8333-333333333333','library','test','file','private.pdf','CLEAN',repeat('a',64),'11111111-1111-4111-8111-111111111111','application/pdf',4,'OWNER_ONLY');`)
	if e != nil {
		t.Fatal(e)
	}
	var name string
	if e = db.QueryRow(`SELECT current_database()`).Scan(&name); e != nil {
		t.Fatal(e)
	}
	u, _ := url.Parse(os.Getenv("TEST_DATABASE_URL"))
	u.Path = "/" + name
	return db, testutil.As(t, u.String(), "dxos_ai_api"), &auth.Identity{UserID: ownerID, Role: "role_library_user"}
}
func seedDocument(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, e := db.Exec(`INSERT INTO cap_ai.knowledge_collections(id,app_id,kind,owner_user_id) VALUES($1,'library','PERSONAL',$2)`, collectionID, ownerID); e != nil {
		t.Fatal(e)
	}
	if _, e := db.Exec(`INSERT INTO cap_ai.knowledge_documents(id,collection_id,source_kind,owner_user_id,initial_file_id,title,access_mode,processing_policy_id,processing_policy_version) VALUES($1,$2,'UPLOAD',$3,$4,'Private title','PRIVATE','local-only',1)`, documentID, collectionID, ownerID, fileID); e != nil {
		t.Fatal(e)
	}
}

func TestMetadataConstraints(t *testing.T) {
	db, _, _ := metadataFixture(t)
	seedDocument(t, db)
	if _, e := db.Exec(`UPDATE cap_ai.knowledge_documents SET owner_user_id=$1 WHERE id=$2`, otherID, documentID); e == nil {
		t.Fatal("owner changed")
	}
	if _, e := db.Exec(`UPDATE cap_ai.knowledge_documents SET active_version_id=gen_random_uuid() WHERE id=$1`, documentID); e == nil {
		t.Fatal("cross-document version allowed")
	}
	if _, e := db.Exec(`INSERT INTO cap_ai.knowledge_collections(app_id,kind,owner_user_id) VALUES('library','APP',$1)`, ownerID); e == nil {
		t.Fatal("APP owner accepted")
	}
}
func TestPolicyOwnerOnly(t *testing.T) {
	db, api, a := metadataFixture(t)
	seedDocument(t, db)
	p := NewPolicy(api)
	if _, e := p.CanRead(context.Background(), a, documentID); e != nil {
		t.Fatal(e)
	}
	b := &auth.Identity{UserID: otherID, Role: a.Role}
	if _, e := p.CanRead(context.Background(), b, documentID); e == nil {
		t.Fatal("foreign private read")
	}
	if _, e := api.Exec(`UPDATE core.users SET display_name='bad'`); e == nil {
		t.Fatal("API can write core.users")
	}
	if _, e := api.Query(`SELECT * FROM cap_ai.knowledge_documents`); e == nil {
		t.Fatal("unscoped metadata SQL privilege")
	}
	db.Exec(`DELETE FROM core.user_roles WHERE user_id=$1`, ownerID)
	if _, e := p.CanRead(context.Background(), a, documentID); e == nil {
		t.Fatal("revoked membership accepted")
	}
}
func TestFreshUpgradeEquivalent(t *testing.T) {
	db, _, _ := metadataFixture(t)
	var count int
	if e := db.QueryRow(`SELECT count(*) FROM information_schema.tables WHERE table_schema='cap_ai' AND table_type='BASE TABLE'`).Scan(&count); e != nil || count != 14 {
		t.Fatal("metadata inventory", count, e)
	}
}

func TestBootstrapMigrationLedger(t *testing.T) {
	db := testutil.Database(t)
	for _, p := range []string{"init/01_core_schema.sql", "init/02_capability_registry.sql", "init/03_app_registry.sql", "init/05_operator_deployments.sql", "init/06_runtime_roles.sql", "init/07_app_definitions.sql", "capabilities/cap_ai.sql"} {
		raw, e := os.ReadFile("../../../platform/postgres/" + p)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = db.Exec(string(raw)); e != nil {
			t.Fatal(e)
		}
	}
	var name string
	if e := db.QueryRow(`SELECT current_database()`).Scan(&name); e != nil {
		t.Fatal(e)
	}
	u, _ := url.Parse(os.Getenv("TEST_DATABASE_URL"))
	u.Path = "/" + name
	env := append(os.Environ(), "PGHOST="+u.Hostname(), "PGPORT="+u.Port(), "PGUSER="+u.User.Username(), "POSTGRES_USER="+u.User.Username(), "POSTGRES_DB="+name, "PGDATABASE="+name, "PGSSLMODE=disable")
	pw, _ := u.User.Password()
	env = append(env, "PGPASSWORD="+pw)
	cmd := exec.Command("../../../platform/postgres/init/09_rag_migrations.sh")
	cmd.Env = env
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("bootstrap: %v %s", e, out)
	}
	t.Setenv("RAG_POLICY_TEST_URL", u.String())
	cmd = exec.Command("../../../scripts/migrate-rag.sh", "--database-url-env", "RAG_POLICY_TEST_URL", "--scope", "cap-ai", "--through", "0001")
	if out, e = cmd.CombinedOutput(); e != nil {
		t.Fatalf("upgrade replay: %v %s", e, out)
	}
	upgraded, _, _ := metadataFixture(t)
	derivedSQL, readErr := os.ReadFile("../../../platform/postgres/migrations/cap_ai/0002_docir_fts.sql")
	if readErr != nil {
		t.Fatal(readErr)
	}
	if _, e = upgraded.Exec(string(derivedSQL)); e != nil {
		t.Fatal(e)
	}
	// Bootstrap also installs the model-independent conversation ledger.
	ledgerSQL, readErr := os.ReadFile("../../../platform/postgres/migrations/cap_ai/0006_agent_conversations.sql")
	if readErr != nil {
		t.Fatal(readErr)
	}
	if _, e = upgraded.Exec(string(ledgerSQL)); e != nil {
		t.Fatal(e)
	}
	const fingerprint = `SELECT jsonb_build_object('columns',(SELECT jsonb_agg(jsonb_build_array(table_name,column_name,data_type,is_nullable,column_default) ORDER BY table_name,ordinal_position) FROM information_schema.columns WHERE table_schema='cap_ai'),'constraints',(SELECT jsonb_agg(jsonb_build_array(t.relname,c.conname,pg_get_constraintdef(c.oid)) ORDER BY t.relname,c.conname) FROM pg_constraint c JOIN pg_class t ON t.oid=c.conrelid JOIN pg_namespace n ON n.oid=t.relnamespace WHERE n.nspname='cap_ai'))::text`
	var freshSchema, upgradeSchema string
	if e = db.QueryRow(fingerprint).Scan(&freshSchema); e != nil {
		t.Fatal(e)
	}
	if e = upgraded.QueryRow(fingerprint).Scan(&upgradeSchema); e != nil {
		t.Fatal(e)
	}
	if freshSchema != upgradeSchema {
		t.Fatal("bootstrap and upgrade constraints differ")
	}
	var n int
	if e = db.QueryRow(`SELECT count(*) FROM core.schema_migrations WHERE version='0001'`).Scan(&n); e != nil || n != 2 {
		t.Fatal("bootstrap ledger differs", n, e)
	}
}
