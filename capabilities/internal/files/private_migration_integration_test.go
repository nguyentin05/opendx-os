//go:build integration

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package files

import (
	"github.com/opendx-os/opendx-os/capabilities/internal/testutil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrivatePolicyUpgradePreservesAttachments(t *testing.T) {
	db := testutil.Database(t)
	bootstrap, err := os.ReadFile("../../../platform/postgres/init/01_core_schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	// Remove the additive policy from bootstrap to represent the legacy schema.
	legacyBase := strings.ReplaceAll(string(bootstrap), " OR (mime_type='text/markdown' AND read_policy='OWNER_ONLY')", "")
	legacy := strings.Replace(legacyBase, "    read_policy TEXT NOT NULL DEFAULT 'APP_ATTACHMENT' CHECK (read_policy IN ('APP_ATTACHMENT', 'OWNER_ONLY')),\n", "", 1)
	legacy = strings.Replace(legacy, "       OR NEW.read_policy IS DISTINCT FROM OLD.read_policy\n", "", 1)
	if _, err = db.Exec(legacy); err != nil {
		t.Fatal(err)
	}
	var policy string
	migration, err := os.ReadFile(filepath.Join("../../../platform/postgres/migrations/core", "0001_private_file_policy.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO core.users(id,email,display_name) VALUES('00000000-0000-0000-0000-000000000001','a@example.test','A'); INSERT INTO core.files(id,app_id,bucket,object_key,file_name,sha256_hash,uploaded_by,mime_type,size_bytes) VALUES('00000000-0000-0000-0000-000000000002','library','test','one','one.pdf',repeat('a',64),'00000000-0000-0000-0000-000000000001','application/pdf',4)`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(string(migration)); err != nil {
		t.Fatal(err)
	}
	var hash, owner string
	if err = db.QueryRow(`SELECT sha256_hash,uploaded_by::text FROM core.files`).Scan(&hash, &owner); err != nil || hash != strings.Repeat("a", 64) || owner != "00000000-0000-0000-0000-000000000001" {
		t.Fatal("legacy identity changed", err)
	}
	if err = db.QueryRow(`SELECT read_policy FROM core.files`).Scan(&policy); err != nil || policy != "APP_ATTACHMENT" {
		t.Fatalf("legacy policy %q: %v", policy, err)
	}
	for _, q := range []string{`UPDATE core.files SET read_policy='OWNER_ONLY'`, `UPDATE core.files SET sha256_hash=repeat('b',64)`, `UPDATE core.files SET uploaded_by=gen_random_uuid()`} {
		if _, err = db.Exec(q); err == nil {
			t.Fatal("immutable field changed", q)
		}
	}
	if _, err = db.Exec(`INSERT INTO core.files(app_id,bucket,object_key,file_name,sha256_hash,uploaded_by,mime_type,size_bytes,read_policy) SELECT app_id,bucket,'two',file_name,sha256_hash,uploaded_by,mime_type,size_bytes,'OWNER_ONLY' FROM core.files`); err != nil {
		t.Fatal(err)
	}
}

func TestMigrationReplayAndChecksumConflict(t *testing.T) {
	db := testutil.Database(t)
	schema, err := os.ReadFile("../../../platform/postgres/init/01_core_schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(string(schema)); err != nil {
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
	t.Setenv("RAG_MIGRATION_TEST_URL", u.String())
	run := func() error {
		cmd := exec.Command("../../../scripts/migrate-rag.sh", "--database-url-env", "RAG_MIGRATION_TEST_URL", "--scope", "core", "--through", "0001")
		out, e := cmd.CombinedOutput()
		t.Log(string(out))
		return e
	}
	if err = run(); err != nil {
		t.Fatal(err)
	}
	if err = run(); err != nil {
		t.Fatal("replay:", err)
	}
	if _, err = db.Exec(`UPDATE core.schema_migrations SET checksum='changed'`); err != nil {
		t.Fatal(err)
	}
	if err = run(); err == nil {
		t.Fatal("checksum conflict accepted")
	}
	var n int
	if err = db.QueryRow(`SELECT count(*) FROM core.schema_migrations WHERE checksum='changed'`).Scan(&n); err != nil || n != 1 {
		t.Fatal("ledger mutated", n, err)
	}
}

func TestPrivatePolicyBootstrapMatchesUpgrade(t *testing.T) {
	upgraded := testutil.Database(t)
	fresh := testutil.Database(t)
	bootstrap, err := os.ReadFile("../../../platform/postgres/init/01_core_schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	legacyBase := strings.ReplaceAll(string(bootstrap), " OR (mime_type='text/markdown' AND read_policy='OWNER_ONLY')", "")
	legacy := strings.Replace(legacyBase, "    read_policy TEXT NOT NULL DEFAULT 'APP_ATTACHMENT' CHECK (read_policy IN ('APP_ATTACHMENT', 'OWNER_ONLY')),\n", "", 1)
	legacy = strings.Replace(legacy, "       OR NEW.read_policy IS DISTINCT FROM OLD.read_policy\n", "", 1)
	if _, err = upgraded.Exec(legacy); err != nil {
		t.Fatal(err)
	}
	migration, err := os.ReadFile("../../../platform/postgres/migrations/core/0001_private_file_policy.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = upgraded.Exec(string(migration)); err != nil {
		t.Fatal(err)
	}
	core2, err := os.ReadFile("../../../platform/postgres/migrations/core/0002_private_markdown.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = upgraded.Exec(string(core2)); err != nil {
		t.Fatal(err)
	}
	if _, err = fresh.Exec(string(bootstrap)); err != nil {
		t.Fatal(err)
	}
	const query = `SELECT jsonb_build_object('columns',(SELECT jsonb_agg(jsonb_build_array(column_name,data_type,is_nullable,column_default) ORDER BY column_name) FROM information_schema.columns WHERE table_schema='core' AND table_name='files'),'checks',(SELECT jsonb_agg(pg_get_constraintdef(oid) ORDER BY conname) FROM pg_constraint WHERE conrelid='core.files'::regclass),'trigger',pg_get_functiondef('core.enforce_file_immutability()'::regprocedure))::text`
	var a, b string
	if err = upgraded.QueryRow(query).Scan(&a); err != nil {
		t.Fatal(err)
	}
	if err = fresh.QueryRow(query).Scan(&b); err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatal("bootstrap and upgraded file semantics differ")
	}
	contract, err := os.ReadFile("../../../platform/postgres/tests/rag_source_contract.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = upgraded.Exec(string(contract)); err != nil {
		t.Fatal(err)
	}
	if _, err = fresh.Exec(string(contract)); err != nil {
		t.Fatal(err)
	}
}
