//go:build integration

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package models

import (
	"crypto/sha256"
	"database/sql"
	"fmt"
	"github.com/opendx-os/opendx-os/capabilities/internal/testutil"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func modelDB(t *testing.T, vector bool) *sql.DB {
	t.Helper()
	db := testutil.Database(t)
	for _, path := range []string{"init/01_core_schema.sql", "init/02_capability_registry.sql", "init/03_app_registry.sql", "init/05_operator_deployments.sql", "init/06_runtime_roles.sql", "init/07_app_definitions.sql", "capabilities/cap_ai.sql", "migrations/cap_ai/0001_metadata_jobs.sql", "migrations/cap_ai/0002_docir_fts.sql", "migrations/core/0003_publication_generation.sql", "migrations/cap_ai/0003_package_knowledge.sql"} {
		raw, e := os.ReadFile("../../../../platform/postgres/" + path)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = db.Exec(string(raw)); e != nil {
			t.Fatalf("%s: %v", path, e)
		}
	}
	if vector {
		applyModels(t, db)
	}
	return db
}
func applyModels(t *testing.T, db *sql.DB) {
	t.Helper()
	raw, e := os.ReadFile("../../../../platform/postgres/migrations/cap_ai/0004_models_vectors.sql")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(string(raw)); e != nil {
		t.Fatal(e)
	}
}
func TestFTSWithoutVectorExtension(t *testing.T) {
	db := modelDB(t, false)
	var works bool
	if e := db.QueryRow(`SELECT to_tsvector('simple','policy') @@ plainto_tsquery('simple','policy')`).Scan(&works); e != nil || !works {
		t.Fatal("FTS unavailable", e)
	}
	var present bool
	if e := db.QueryRow(`SELECT EXISTS(SELECT FROM pg_extension WHERE extname='vector')`).Scan(&present); e != nil || present {
		t.Fatal("FTS bootstrap requires vector", e)
	}
	var available bool
	if e := db.QueryRow(`SELECT EXISTS(SELECT FROM pg_available_extensions WHERE name='vector')`).Scan(&available); e != nil {
		t.Fatal(e)
	}
	if available {
		applyModels(t, db)
	} else {
		raw, e := os.ReadFile("../../../../platform/postgres/migrations/cap_ai/0004_models_vectors.sql")
		if e != nil {
			t.Fatal(e)
		}
		tx, e := db.Begin()
		if e != nil {
			t.Fatal(e)
		}
		defer tx.Rollback()
		if _, e = tx.Exec(string(raw)); e == nil {
			t.Fatal("model migration accepted missing extension")
		}
	}
}

func applyRegistry(t *testing.T, db *sql.DB) {
	t.Helper()
	raw, e := os.ReadFile("../../../../platform/postgres/migrations/cap_ai/0005_model_registry.sql")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(string(raw)); e != nil {
		t.Fatal(e)
	}
}
func TestRegistryOptionalSchemaAndFTS(t *testing.T) {
	db := modelDB(t, false)
	var n int
	db.QueryRow(`SELECT count(*) FROM pg_tables WHERE schemaname='cap_ai' AND tablename IN('model_profiles','model_candidates','global_model_slots')`).Scan(&n)
	if n != 0 {
		t.Fatal(n)
	}
	raw, e := os.ReadFile("../../../../platform/postgres/migrations/cap_ai/0005_model_registry.sql")
	if e != nil {
		t.Fatal(e)
	}
	tx, e := db.Begin()
	if e != nil {
		t.Fatal(e)
	}
	if _, e = tx.Exec(string(raw)); e == nil {
		t.Fatal("accepted missing 0004")
	}
	tx.Rollback()
	applyModels(t, db)
	applyRegistry(t, db)
	for _, q := range []string{`SELECT count(*) FROM cap_ai.model_profiles`, `SELECT count(*) FROM cap_ai.model_candidates`, `SELECT count(*) FROM cap_ai.model_slot_events`, `SELECT count(*) FROM cap_ai.global_model_slots WHERE profile_id IS NOT NULL`} {
		if e = db.QueryRow(q).Scan(&n); e != nil || n != 0 {
			t.Fatal(q, n, e)
		}
	}
	if e = db.QueryRow(`SELECT count(*) FROM cap_ai.global_model_slots WHERE capability IN('EMBEDDING','LLM','VLM','RERANKING') AND slot_revision=0 AND profile_id IS NULL AND profile_revision IS NULL`).Scan(&n); e != nil || n != 4 {
		t.Fatal(n, e)
	}
}

func TestRegistryExplicitMigrationRunner(t *testing.T) {
	db := modelDB(t, false)
	ledger, e := os.ReadFile("../../../../platform/postgres/migrations/ledger.sql")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(string(ledger)); e != nil {
		t.Fatal(e)
	}
	// modelDB owns the base migrations; record their actual checksums before the runner.
	for _, file := range []string{"0001_metadata_jobs.sql", "0002_docir_fts.sql", "0003_package_knowledge.sql"} {
		raw, e := os.ReadFile("../../../../platform/postgres/migrations/cap_ai/" + file)
		if e != nil {
			t.Fatal(e)
		}
		sum := sha256.Sum256(raw)
		if _, e = db.Exec(`INSERT INTO core.schema_migrations(scope,version,checksum) VALUES('cap-ai',$1,$2)`, file[:4], fmt.Sprintf("%x", sum)); e != nil {
			t.Fatal(e)
		}
	}
	var name string
	if e = db.QueryRow(`SELECT current_database()`).Scan(&name); e != nil {
		t.Fatal(e)
	}
	u, e := url.Parse(os.Getenv("TEST_DATABASE_URL"))
	if e != nil {
		t.Fatal(e)
	}
	u.Path = "/" + name
	run := func(version string, flags ...string) error {
		command := exec.Command("python3", "../../../../scripts/migrate_rag.py", "--database-url-env", "REGISTRY_MIGRATION_TEST_URL", "--scope", "cap-ai", "--through", version)
		command.Env = append(os.Environ(), "REGISTRY_MIGRATION_TEST_URL="+u.String())
		command.Env = append(command.Env, flags...)
		raw, e := command.CombinedOutput()
		if e != nil {
			return fmt.Errorf("runner: %w: %s", e, raw)
		}
		return nil
	}
	if e = run("0007"); e != nil {
		t.Fatal(e)
	}
	var present bool
	if e = db.QueryRow(`SELECT to_regclass('cap_ai.global_model_slots') IS NOT NULL`).Scan(&present); e != nil || present {
		t.Fatal(present, e)
	}
	if e = db.QueryRow(`SELECT EXISTS(SELECT FROM pg_extension WHERE extname='vector')`).Scan(&present); e != nil || present {
		t.Fatal("FTS upgrade installed vector", present, e)
	}
	if e = db.QueryRow(`SELECT to_regclass('cap_ai.agent_conversations') IS NOT NULL AND to_regprocedure('cap_ai.agent_authorized_scopes(uuid)') IS NOT NULL`).Scan(&present); e != nil || !present {
		t.Fatal("FTS agent upgrade missing", present, e)
	}
	if e = run("0010"); e == nil {
		t.Fatal("model-dependent upgrade accepted without gates")
	}
	for i := 0; i < 2; i++ {
		if e = run("0005"); e != nil {
			t.Fatal(e)
		}
	}
	var count int
	if e = db.QueryRow(`SELECT count(*) FROM core.schema_migrations WHERE scope='cap-ai' AND version IN('0004','0005')`).Scan(&count); e != nil || count != 2 {
		t.Fatal(count, e)
	}
	flags := []string{"RAG_MODELS_SCHEMA_ENABLED=true", "RAG_GLOBAL_MODEL_REGISTRY_SCHEMA_ENABLED=true", "RAG_AGENT_MODELS_SCHEMA_ENABLED=true"}
	for i := 0; i < 2; i++ {
		if e = run("0010", flags...); e != nil {
			t.Fatal(e)
		}
	}
	if e = db.QueryRow(`SELECT to_regprocedure('cap_ai.search_personal_vectors(uuid,text,text,bigint,public.vector,uuid[])') IS NOT NULL`).Scan(&present); e != nil || !present {
		t.Fatal("optional PERSONAL vector search missing", present, e)
	}
	if e = db.QueryRow(`SELECT to_regprocedure('cap_ai.agent_model_policy(uuid,text,text,bigint,bigint,jsonb)') IS NOT NULL
		AND to_regprocedure('cap_ai.agent_query_model_policy(uuid,text,text,text,text,bigint,bigint)') IS NOT NULL`).Scan(&present); e != nil || !present {
		t.Fatal("optional AGENT model authority missing", present, e)
	}
	if e = db.QueryRow(`SELECT count(*) FROM cap_ai.agent_processing_approvals`).Scan(&count); e != nil || count != 0 {
		t.Fatal("seeded approval", count, e)
	}
	if _, e = db.Exec(`UPDATE core.schema_migrations SET checksum=repeat('0',64) WHERE scope='cap-ai' AND version='0005'`); e != nil {
		t.Fatal(e)
	}
	if e = run("0005"); e == nil {
		t.Fatal("runner accepted registry checksum mismatch")
	}
}

// The pre-rebase model migrations (git 7b98420) recorded these checksums. A
// coordinated-development ledger that still carries them must never be reused.
const (
	legacyModelChecksum    = "d52636fea0e54273512695169da8834fef8c324f19bf1bc6ce1b61d9f5984161"
	legacyRegistryChecksum = "a99044bac3e679570a9d04146a4133bbf2bfc4705113d9f231428e7efed4380c"
)

var modelSchemaFlags = []string{"RAG_MODELS_SCHEMA_ENABLED=true", "RAG_GLOBAL_MODEL_REGISTRY_SCHEMA_ENABLED=true", "RAG_AGENT_MODELS_SCHEMA_ENABLED=true"}

var modelSchemaDisabled = []string{"RAG_MODELS_SCHEMA_ENABLED=false", "RAG_GLOBAL_MODEL_REGISTRY_SCHEMA_ENABLED=false", "RAG_AGENT_MODELS_SCHEMA_ENABLED=false"}

// platformDB builds the platform bootstrap base and returns its isolated DSN.
func platformDB(t *testing.T) (*sql.DB, string) {
	t.Helper()
	db := testutil.Database(t)
	for _, path := range []string{"init/01_core_schema.sql", "init/02_capability_registry.sql", "init/03_app_registry.sql", "init/05_operator_deployments.sql", "init/06_runtime_roles.sql", "init/07_app_definitions.sql", "capabilities/cap_ai.sql"} {
		raw, e := os.ReadFile("../../../../platform/postgres/" + path)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = db.Exec(string(raw)); e != nil {
			t.Fatalf("%s: %v", path, e)
		}
	}
	return db, databaseURL(t, db)
}

func databaseURL(t *testing.T, db *sql.DB) string {
	t.Helper()
	var name string
	if e := db.QueryRow(`SELECT current_database()`).Scan(&name); e != nil {
		t.Fatal(e)
	}
	u, e := url.Parse(os.Getenv("TEST_DATABASE_URL"))
	if e != nil {
		t.Fatal(e)
	}
	u.Path = "/" + name
	return u.String()
}

// migrationEnv is an isolated libpq environment: no inherited PG* or RAG_*
// settings leak into the disposable fixture the runner connects to.
func migrationEnv(t *testing.T, dsn string) []string {
	t.Helper()
	u, e := url.Parse(dsn)
	if e != nil {
		t.Fatal(e)
	}
	var env []string
	for _, kv := range os.Environ() {
		name := strings.SplitN(kv, "=", 2)[0]
		if strings.HasPrefix(name, "PG") || strings.HasPrefix(name, "RAG_") {
			continue
		}
		env = append(env, kv)
	}
	env = append(env, "PGHOST="+u.Hostname(), "PGPORT="+u.Port(), "PGUSER="+u.User.Username(),
		"PGDATABASE="+strings.TrimPrefix(u.Path, "/"), "PGSSLMODE="+u.Query().Get("sslmode"))
	return env
}

func runBootstrap(t *testing.T, dsn string, flags ...string) (string, error) {
	t.Helper()
	u, e := url.Parse(dsn)
	if e != nil {
		t.Fatal(e)
	}
	command := exec.Command("sh", "../../../../platform/postgres/init/09_rag_migrations.sh")
	command.Env = append(migrationEnv(t, dsn), append(flags, "POSTGRES_USER="+u.User.Username(), "POSTGRES_DB="+strings.TrimPrefix(u.Path, "/"))...)
	raw, e := command.CombinedOutput()
	return string(raw), e
}

func runMigrations(t *testing.T, dsn, through string, flags ...string) (string, error) {
	t.Helper()
	command := exec.Command("python3", "../../../../scripts/migrate_rag.py", "--database-url-env", "MODEL_MIGRATION_TEST_URL", "--scope", "cap-ai", "--through", through)
	command.Env = append(migrationEnv(t, dsn), append(flags, "MODEL_MIGRATION_TEST_URL="+dsn)...)
	raw, e := command.CombinedOutput()
	return string(raw), e
}

func fileChecksum(t *testing.T, name string) string {
	t.Helper()
	raw, e := os.ReadFile("../../../../platform/postgres/migrations/cap_ai/" + name)
	if e != nil {
		t.Fatal(e)
	}
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}

func schemaBool(t *testing.T, db *sql.DB, query string, args ...any) bool {
	t.Helper()
	var value bool
	if e := db.QueryRow(query, args...).Scan(&value); e != nil {
		t.Fatalf("%s: %v", query, e)
	}
	return value
}

// modelOperationFixture is the full optional model bootstrap through 0010 with
// one Ready app, one active actor and one enabled profile.
func modelOperationFixture(t *testing.T) (*sql.DB, string) {
	t.Helper()
	db, dsn := platformDB(t)
	if out, e := runBootstrap(t, dsn, modelSchemaFlags...); e != nil {
		t.Fatalf("bootstrap: %v: %s", e, out)
	}
	if _, e := db.Exec(`INSERT INTO core.apps(app_id,display_name,version,status,spec) VALUES('app','App','1','Ready','{}');
 INSERT INTO core.users(id,email,display_name) VALUES('11111111-1111-4111-8111-111111111111','model@test.invalid','Model');
 INSERT INTO cap_ai.model_profiles(id,revision,purpose,execution,provider_id,model_id,limits,enabled) VALUES('gen',1,'GENERATE','HOSTED','fake','fake','{"max_input_tokens":100,"max_output_tokens":20,"max_bytes":4096,"timeout_ms":1000}',true);`); e != nil {
		t.Fatal(e)
	}
	return db, dsn
}

func TestModelCleanSlateSchema(t *testing.T) {
	db, dsn := modelOperationFixture(t)
	// The five monetary relations and five budget routines are gone.
	if n := len(schemaNames(t, db, `SELECT tablename FROM pg_tables WHERE schemaname='cap_ai' AND tablename IN('budget_accounts','model_reservations','reservation_allocations','usage_ledger','model_calls')`)); n != 0 {
		t.Fatal("legacy model relations retained", n)
	}
	for _, routine := range []string{"cap_ai.reserve_model(jsonb)", "cap_ai.settle_model(uuid,text,jsonb)", "cap_ai.begin_model_call(uuid)", "cap_ai.finish_model_call(uuid,text,text)", "cap_ai.pending_model_charges()"} {
		if schemaBool(t, db, `SELECT to_regprocedure($1) IS NOT NULL`, routine) {
			t.Fatal("legacy model routine retained", routine)
		}
	}
	if !schemaBool(t, db, `SELECT to_regclass('cap_ai.model_operations') IS NOT NULL`) {
		t.Fatal("durable operation table missing")
	}
	for _, routine := range []string{"cap_ai.claim_model_operation(text,uuid,text,text,bigint,text,text)", "cap_ai.begin_model_operation(text,text)", "cap_ai.finish_model_operation(text,text,uuid,text,text,bigint,bigint,text,bigint)", "cap_ai.model_policy(uuid,text,text,text,text,bigint,jsonb)", "cap_ai.worker_model_policy(uuid,uuid,text,bigint,uuid,text,bigint,text,jsonb)", "cap_ai.registry_resolve_slot(text)", "cap_ai.search_vectors(uuid,text,text,text,bigint,public.vector,uuid[])", "cap_ai.search_personal_vectors(uuid,text,text,bigint,public.vector,uuid[])", "cap_ai.agent_model_policy(uuid,text,text,bigint,bigint,jsonb)", "cap_ai.agent_query_model_policy(uuid,text,text,text,text,bigint,bigint)"} {
		if !schemaBool(t, db, `SELECT to_regprocedure($1) IS NOT NULL`, routine) {
			t.Fatal("expected model interface missing", routine)
		}
	}
	if !schemaBool(t, db, `SELECT EXISTS(SELECT FROM pg_extension WHERE extname='vector')`) {
		t.Fatal("vector extension missing")
	}
	// Price fields are gone from the profile row and from the limits contract.
	if n := len(schemaNames(t, db, `SELECT column_name FROM information_schema.columns WHERE table_schema='cap_ai' AND column_name='pricing_revision'`)); n != 0 {
		t.Fatal("price column retained", n)
	}
	if _, e := db.Exec(`INSERT INTO cap_ai.model_profiles(id,revision,purpose,execution,provider_id,model_id,limits,enabled) VALUES('priced',1,'GENERATE','HOSTED','fake','fake','{"max_input_tokens":100,"max_output_tokens":20,"max_bytes":4096,"input_unit_price":1,"output_unit_price":1,"timeout_ms":1000}',true)`); e == nil {
		t.Fatal("profile accepted monetary limits")
	}
	catalog := testutil.As(t, dsn, "dxos_ai_model_catalog")
	if _, e := catalog.Exec(`SELECT cap_ai.registry_import_candidates($1::jsonb)`, strings.Replace(priceFreeCandidate, `"timeout_ms":1000`, `"timeout_ms":1000,"input_unit_price":1`, 1)); e == nil {
		t.Fatal("registry import accepted monetary limits")
	}
	if _, e := catalog.Exec(`SELECT cap_ai.registry_import_candidates($1::jsonb)`, strings.Replace(priceFreeCandidate, `"limits"`, `"pricing_revision":"p1","limits"`, 1)); e == nil {
		t.Fatal("registry import accepted a price revision")
	}
	// A restricted gateway session executes the new boundary.
	gateway := testutil.As(t, dsn, "dxos_ai_gateway")
	key, fingerprint := "schema-gateway", strings.Repeat("a", 64)
	var state string
	if e := gateway.QueryRow(`SELECT cap_ai.claim_model_operation($1,$2,$3,$4,$5,$6,$7)`, key, actorID, "app", "gen", 1, "GENERATE", fingerprint).Scan(&state); e != nil || state != "CLAIMED" {
		t.Fatal("gateway claim", state, e)
	}
	var token sql.NullString
	if e := gateway.QueryRow(`SELECT cap_ai.begin_model_operation($1,$2)`, key, fingerprint).Scan(&token); e != nil || !token.Valid {
		t.Fatal("gateway begin", e)
	}
	if e := gateway.QueryRow(`SELECT cap_ai.finish_model_operation($1,$2,$3,'SUCCEEDED',NULL,4,2,NULL,25)`, key, fingerprint, token.String).Scan(new(bool)); e != nil {
		t.Fatal("gateway finish", e)
	}
	// Direct table access and execution stay closed to unrelated sessions.
	for _, role := range []string{"dxos_ai_api", "dxos_ai_worker"} {
		other := testutil.As(t, dsn, role)
		for _, q := range []string{`SELECT * FROM cap_ai.model_operations`, `UPDATE cap_ai.model_operations SET state='CLAIMED'`} {
			if _, e := other.Exec(q); e == nil {
				t.Fatal("privilege widened", role, q)
			}
		}
		if _, e := other.Exec(`SELECT cap_ai.claim_model_operation('x',$1,'app','gen',1,'GENERATE',$2)`, actorID, strings.Repeat("b", 64)); e == nil {
			t.Fatal("privilege widened", role, "claim")
		}
	}
	stranger := testutil.As(t, dsn, "dxos_reader")
	if _, e := stranger.Exec(`SELECT dispatch_token FROM cap_ai.model_operations`); e == nil {
		t.Fatal("public table access widened")
	}
	if _, e := stranger.Exec(`SELECT cap_ai.begin_model_operation('schema-gateway',$1)`, fingerprint); e == nil {
		t.Fatal("public function execution widened")
	}
	// The explicit runner reaches the same full state with the same flags.
	rdb, rdsn := platformDB(t)
	if out, e := runMigrations(t, rdsn, "0010", modelSchemaFlags...); e != nil {
		t.Fatalf("explicit runner: %v: %s", e, out)
	}
	if !schemaBool(t, rdb, `SELECT to_regclass('cap_ai.model_operations') IS NOT NULL AND to_regprocedure('cap_ai.agent_model_policy(uuid,text,text,bigint,bigint,jsonb)') IS NOT NULL`) {
		t.Fatal("explicit runner did not reach the full model state")
	}
	// FTS plus agent base through 0007 installs nothing optional.
	fdb, fdsn := platformDB(t)
	if out, e := runMigrations(t, fdsn, "0007"); e != nil {
		t.Fatalf("fts runner: %v: %s", e, out)
	}
	if n := len(schemaNames(t, fdb, `SELECT tablename FROM pg_tables WHERE schemaname='cap_ai' AND tablename IN('model_profiles','model_operations','model_candidates','global_model_slots','model_slot_events','agent_processing_approvals','embedding_generations')`)); n != 0 {
		t.Fatal("FTS fixture installed optional model objects", n)
	}
	if schemaBool(t, fdb, `SELECT EXISTS(SELECT FROM pg_extension WHERE extname='vector')`) {
		t.Fatal("FTS fixture installed pgvector")
	}
	if !schemaBool(t, fdb, `SELECT to_regclass('cap_ai.agent_conversations') IS NOT NULL AND to_regprocedure('cap_ai.agent_authorized_scopes(uuid)') IS NOT NULL`) {
		t.Fatal("FTS fixture missing agent base")
	}
}

const actorID = "11111111-1111-4111-8111-111111111111"

const priceFreeCandidate = `{"candidates":[{"adapter_key":"hosted-test","declaration_digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","profile":{"id":"price-free","revision":1,"purpose":"GENERATE","execution":"HOSTED","provider_id":"test","model_id":"model-test","credential_ref":"TEST_KEY","limits":{"max_input_tokens":100,"max_output_tokens":100,"max_bytes":1024,"timeout_ms":1000}}}]}`

func schemaNames(t *testing.T, db *sql.DB, query string) []string {
	t.Helper()
	rows, e := db.Query(query)
	if e != nil {
		t.Fatal(e)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if e = rows.Scan(&name); e != nil {
			t.Fatal(e)
		}
		names = append(names, name)
	}
	if e = rows.Err(); e != nil {
		t.Fatal(e)
	}
	return names
}

func TestModelCleanSlateChecksumConflict(t *testing.T) {
	legacy := []struct{ file, checksum string }{
		{"0004_models_vectors.sql", legacyModelChecksum},
		{"0005_model_registry.sql", legacyRegistryChecksum},
	}
	seed := func(t *testing.T, db *sql.DB, file, checksum string) {
		t.Helper()
		ledger, e := os.ReadFile("../../../../platform/postgres/migrations/ledger.sql")
		if e != nil {
			t.Fatal(e)
		}
		if _, e = db.Exec(string(ledger)); e != nil {
			t.Fatal(e)
		}
		if _, e = db.Exec(`INSERT INTO core.schema_migrations(scope,version,checksum) VALUES('cap-ai',$1,$2)`, file[:4], checksum); e != nil {
			t.Fatal(e)
		}
	}
	// No domain SQL, no ledger insert and no base migration may survive a conflict.
	assertUntouched := func(t *testing.T, db *sql.DB, ledgerRows int) {
		t.Helper()
		if n := schemaCount(t, db, `SELECT count(*) FROM core.schema_migrations`); n != ledgerRows {
			t.Fatal("conflict mutated the ledger", n)
		}
		if n := schemaCount(t, db, `SELECT count(*) FROM core.schema_migrations WHERE scope='core'`); n != 0 {
			t.Fatal("conflict applied pending core migrations", n)
		}
		if schemaBool(t, db, `SELECT to_regnamespace('cap_ai') IS NOT NULL OR to_regclass('cap_ai.embedding_generations') IS NOT NULL`) {
			t.Fatal("conflict applied model SQL")
		}
	}
	requireDiagnostic := func(t *testing.T, out, version string) {
		t.Helper()
		if !strings.Contains(out, "cap-ai") || !strings.Contains(out, version) || !strings.Contains(out, "checksum conflict") {
			t.Fatalf("missing scope/version diagnostic: %q", out)
		}
	}
	for _, item := range legacy {
		item := item
		t.Run("python-disabled-"+item.file[:4], func(t *testing.T) {
			db, dsn := platformDB(t)
			seed(t, db, item.file, item.checksum)
			out, e := runMigrations(t, dsn, "0007", modelSchemaDisabled...)
			if e == nil {
				t.Fatal("runner accepted an old applied model checksum with flags disabled")
			}
			requireDiagnostic(t, out, item.file[:4])
			assertUntouched(t, db, 1)
		})
		t.Run("bootstrap-cap-ai-disabled-"+item.file[:4], func(t *testing.T) {
			db, dsn := platformDB(t)
			if _, e := db.Exec(`UPDATE core.capabilities SET enabled=false WHERE cap_id='cap-ai'`); e != nil {
				t.Fatal(e)
			}
			seed(t, db, item.file, item.checksum)
			out, e := runBootstrap(t, dsn)
			if e == nil {
				t.Fatal("bootstrap accepted an old applied model checksum with cap-ai disabled")
			}
			requireDiagnostic(t, out, item.file[:4])
			assertUntouched(t, db, 1)
		})
		t.Run("bootstrap-flags-enabled-"+item.file[:4], func(t *testing.T) {
			db, dsn := platformDB(t)
			seed(t, db, item.file, item.checksum)
			out, e := runBootstrap(t, dsn, modelSchemaFlags...)
			if e == nil {
				t.Fatal("bootstrap accepted an old applied model checksum with flags enabled")
			}
			requireDiagnostic(t, out, item.file[:4])
			assertUntouched(t, db, 1)
		})
	}
	t.Run("fresh-fts-ledger-passes", func(t *testing.T) {
		db, dsn := platformDB(t)
		if out, e := runMigrations(t, dsn, "0007", modelSchemaDisabled...); e != nil {
			t.Fatalf("fresh FTS ledger rejected: %v: %s", e, out)
		}
		if n := schemaCount(t, db, `SELECT count(*) FROM core.schema_migrations WHERE scope='cap-ai' AND version IN('0001','0002','0003','0006','0007')`); n != 5 {
			t.Fatal("FTS ledger incomplete", n)
		}
		if schemaBool(t, db, `SELECT to_regclass('cap_ai.model_profiles') IS NOT NULL OR to_regclass('cap_ai.model_operations') IS NOT NULL`) {
			t.Fatal("FTS ledger installed optional objects")
		}
	})
	t.Run("matching-model-checksum-passes", func(t *testing.T) {
		db, dsn := platformDB(t)
		seed(t, db, "0004_models_vectors.sql", fileChecksum(t, "0004_models_vectors.sql"))
		if out, e := runMigrations(t, dsn, "0007", modelSchemaDisabled...); e != nil {
			t.Fatalf("matching model checksum rejected: %v: %s", e, out)
		}
		if schemaBool(t, db, `SELECT to_regclass('cap_ai.model_profiles') IS NOT NULL OR to_regclass('cap_ai.model_operations') IS NOT NULL`) {
			t.Fatal("model objects installed without opt-in")
		}
		if n := schemaCount(t, db, `SELECT count(*) FROM core.schema_migrations WHERE scope='cap-ai'`); n != 6 {
			t.Fatal("unexpected ledger", n)
		}
	})
}

func schemaCount(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()
	var n int
	if e := db.QueryRow(query, args...).Scan(&n); e != nil {
		t.Fatalf("%s: %v", query, e)
	}
	return n
}
