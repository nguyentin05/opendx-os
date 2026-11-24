//go:build integration

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package models

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/lib/pq"

	"github.com/opendx-os/opendx-os/capabilities/internal/testutil"
)

// The audited administration surface is exercised only through the dedicated
// deployment login. Fixtures never call the functions as a superuser: session
// authority is part of the interface under test.
const (
	adminOperatorID = actorID
	adminPlainID    = "22222222-2222-4222-8222-222222222222"
	adminInactiveID = "33333333-3333-4333-8333-333333333333"
	adminRevokedID  = "44444444-4444-4444-8444-444444444444"

	adminLLMProfile   = "admin-llm"
	adminEmbedProfile = "admin-embed"
	adminWideProfile  = "admin-wide"
)

var (
	adminLLMDigest    = strings.Repeat("1", 64)
	adminEmbedDigest  = strings.Repeat("2", 64)
	adminEmbed2Digest = strings.Repeat("3", 64)
	adminWideDigest   = strings.Repeat("4", 64)
)

const (
	adminLimits     = `{"max_input_tokens":1000,"max_output_tokens":100,"max_bytes":1024,"timeout_ms":1000}`
	adminWideLimits = `{"max_input_tokens":2000000,"max_output_tokens":100,"max_bytes":1024,"timeout_ms":1000}`
)

func adminCandidateItem(id string, revision int64, purpose, modelID, digest string, dimension int, limits string) string {
	profile := fmt.Sprintf(`{"id":%q,"revision":%d,"purpose":%q,"execution":"HOSTED","provider_id":"test","model_id":%q,"credential_ref":"ADMIN_TEST_KEY","limits":%s`,
		id, revision, purpose, modelID, limits)
	if purpose == "EMBED" {
		profile += fmt.Sprintf(`,"dimension":%d`, dimension)
	}
	return fmt.Sprintf(`{"adapter_key":"bounded-http-v1","declaration_digest":%q,"profile":%s}`, digest, profile+"}")
}

// adminCatalogPayload declares one candidate per reviewed revision: a valid LLM
// revision, two embed revisions for a rollback, and one revision whose limits
// exceed the Gateway ceiling the promotion must reject.
func adminCatalogPayload() string {
	items := []string{
		adminCandidateItem(adminLLMProfile, 1, "GENERATE", "model-gen", adminLLMDigest, 0, adminLimits),
		adminCandidateItem(adminEmbedProfile, 1, "EMBED", "model-embed-one", adminEmbedDigest, 8, adminLimits),
		adminCandidateItem(adminEmbedProfile, 2, "EMBED", "model-embed-two", adminEmbed2Digest, 8, adminLimits),
		adminCandidateItem(adminWideProfile, 1, "GENERATE", "model-wide", adminWideDigest, 0, adminWideLimits),
	}
	return `{"candidates":[` + strings.Join(items, ",") + `]}`
}

// adminFixture is the full optional model bootstrap through 0011 with active,
// inactive and unprivileged actors and a candidate set imported through the
// catalog login, so every profile starts disabled.
func adminFixture(t *testing.T) (*sql.DB, string) {
	t.Helper()
	db, dsn := platformDB(t)
	if out, e := runBootstrap(t, dsn, modelSchemaFlags...); e != nil {
		t.Fatalf("bootstrap: %v: %s", e, out)
	}
	if _, e := db.Exec(`INSERT INTO core.users(id,email,display_name) VALUES
 ('11111111-1111-4111-8111-111111111111','operator@test.invalid','Operator'),
 ('22222222-2222-4222-8222-222222222222','plain@test.invalid','Plain'),
 ('33333333-3333-4333-8333-333333333333','inactive@test.invalid','Inactive'),
 ('44444444-4444-4444-8444-444444444444','revoked@test.invalid','Revoked');
INSERT INTO core.user_roles(user_id,role_name) VALUES
 ('11111111-1111-4111-8111-111111111111','role_operator'),
 ('33333333-3333-4333-8333-333333333333','role_operator');
UPDATE core.users SET status='DISABLED' WHERE id='33333333-3333-4333-8333-333333333333';`); e != nil {
		t.Fatal(e)
	}
	catalog := testutil.As(t, dsn, "dxos_ai_model_catalog")
	if _, e := catalog.Exec(`SELECT cap_ai.registry_import_candidates($1::jsonb)`, adminCatalogPayload()); e != nil {
		t.Fatalf("candidate import: %v", e)
	}
	return db, dsn
}

func adminCode(err error) string {
	var pqErr *pq.Error
	if errors.As(err, &pqErr) {
		return string(pqErr.Code)
	}
	return ""
}

func adminRequireCode(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil {
		t.Fatalf("administration call was accepted; expected SQLSTATE %s", code)
	}
	if got := adminCode(err); got != code {
		t.Fatalf("expected SQLSTATE %s, got %q: %v", code, got, err)
	}
}

// adminState is the complete audited and bound state a rejected or atomic
// request must never change.
func adminState(t *testing.T, db *sql.DB) string {
	t.Helper()
	var state string
	if e := db.QueryRow(`SELECT jsonb_build_object(
  'slots',(SELECT jsonb_agg(jsonb_build_object('capability',capability,'profile_id',profile_id,'profile_revision',profile_revision,'slot_revision',slot_revision) ORDER BY capability) FROM cap_ai.global_model_slots),
  'events',(SELECT coalesce(jsonb_agg(jsonb_build_object('capability',capability,'slot_revision',slot_revision,'profile_id',profile_id,'profile_revision',profile_revision,'action',action) ORDER BY capability,slot_revision),'[]'::jsonb) FROM cap_ai.model_slot_events),
  'profiles',(SELECT coalesce(jsonb_agg(jsonb_build_object('id',id,'revision',revision,'enabled',enabled) ORDER BY id,revision),'[]'::jsonb) FROM cap_ai.model_profiles))::text`).Scan(&state); e != nil {
		t.Fatal(e)
	}
	return state
}

// adminCatalogSnapshot pins every object the additive migration owns, so a
// repeated bootstrap or migration must leave it byte-identical.
func adminCatalogSnapshot(t *testing.T, db *sql.DB) string {
	t.Helper()
	var snapshot string
	if e := db.QueryRow(`SELECT coalesce(string_agg(x, E'\n' ORDER BY x),'') FROM (
  SELECT p.oid::text||':'||p.proname||':'||pg_get_userbyid(p.proowner)||':'||p.prosecdef::text||':'||p.provolatile::text||':'||coalesce(array_to_string(p.proconfig,','),'') AS x
    FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
    WHERE n.nspname='cap_ai' AND p.proname LIKE 'model_admin%'
  UNION ALL
  SELECT 'column:'||is_nullable||':'||data_type FROM information_schema.columns
    WHERE table_schema='cap_ai' AND table_name='model_slot_events' AND column_name='review'
  UNION ALL
  SELECT 'role:'||rolcanlogin||':'||rolsuper||':'||rolbypassrls||':'||rolcreatedb||':'||rolcreaterole||':'||rolreplication||':'||rolinherit FROM pg_roles
    WHERE rolname='dxos_ai_model_admin'
  UNION ALL
  SELECT 'ledger:'||count(*)::text FROM core.schema_migrations WHERE scope='cap-ai' AND version='0011'
) s`).Scan(&snapshot); e != nil {
		t.Fatal(e)
	}
	return snapshot
}

func adminObjectsPresent(t *testing.T, db *sql.DB) bool {
	t.Helper()
	return schemaBool(t, db, `SELECT to_regprocedure('cap_ai.model_admin_list(uuid,text,bigint,text,bigint,integer)') IS NOT NULL
  OR to_regprocedure('cap_ai.model_admin_slots(uuid)') IS NOT NULL
  OR to_regprocedure('cap_ai.model_admin_change_slot(uuid,text,bigint,text,text,bigint,text,jsonb)') IS NOT NULL
  OR to_regprocedure('cap_ai.model_admin_actor(uuid)') IS NOT NULL
  OR to_regprocedure('cap_ai.model_admin_session()') IS NOT NULL
  OR EXISTS(SELECT FROM information_schema.columns WHERE table_schema='cap_ai' AND table_name='model_slot_events' AND column_name='review')`)
}

func TestModelAdminMigrationGates(t *testing.T) {
	t.Run("fts-only-bootstrap-installs-nothing", func(t *testing.T) {
		db, dsn := platformDB(t)
		roleBefore := schemaBool(t, db, `SELECT EXISTS(SELECT FROM pg_roles WHERE rolname='dxos_ai_model_admin')`)
		if out, e := runBootstrap(t, dsn); e != nil {
			t.Fatalf("fts bootstrap: %v: %s", e, out)
		}
		if adminObjectsPresent(t, db) {
			t.Fatal("FTS bootstrap installed model administration")
		}
		if n := schemaCount(t, db, `SELECT count(*) FROM core.schema_migrations WHERE scope='cap-ai' AND version='0011'`); n != 0 {
			t.Fatal("FTS bootstrap recorded migration 0011", n)
		}
		// Roles are cluster global; only a pristine cluster can assert absence.
		if !roleBefore && schemaBool(t, db, `SELECT EXISTS(SELECT FROM pg_roles WHERE rolname='dxos_ai_model_admin')`) {
			t.Fatal("FTS bootstrap created the administration login")
		}
	})
	t.Run("full-bootstrap-applies-once", func(t *testing.T) {
		db, dsn := platformDB(t)
		if out, e := runBootstrap(t, dsn, modelSchemaFlags...); e != nil {
			t.Fatalf("bootstrap: %v: %s", e, out)
		}
		for _, routine := range []string{
			"cap_ai.model_admin_session()", "cap_ai.model_admin_actor(uuid)",
			"cap_ai.model_admin_list(uuid,text,bigint,text,bigint,integer)",
			"cap_ai.model_admin_slots(uuid)",
			"cap_ai.model_admin_change_slot(uuid,text,bigint,text,text,bigint,text,jsonb)",
		} {
			if !schemaBool(t, db, `SELECT to_regprocedure($1) IS NOT NULL`, routine) {
				t.Fatal("administration interface missing", routine)
			}
		}
		if !schemaBool(t, db, `SELECT EXISTS(SELECT FROM information_schema.columns WHERE table_schema='cap_ai' AND table_name='model_slot_events' AND column_name='review' AND is_nullable='YES')`) {
			t.Fatal("nullable review audit column missing")
		}
		if n := schemaCount(t, db, `SELECT count(*) FROM core.schema_migrations WHERE scope='cap-ai' AND version='0011' AND checksum=$1`, fileChecksum(t, "0011_model_administration.sql")); n != 1 {
			t.Fatal("migration 0011 is not recorded exactly once", n)
		}
		if !schemaBool(t, db, `SELECT rolcanlogin AND NOT rolsuper AND NOT rolbypassrls AND NOT rolcreatedb AND NOT rolcreaterole AND NOT rolreplication AND NOT rolinherit FROM pg_roles WHERE rolname='dxos_ai_model_admin'`) {
			t.Fatal("administration login has the wrong attributes")
		}
		if n := schemaCount(t, db, `SELECT count(*) FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace
			WHERE n.nspname='cap_ai' AND p.proname LIKE 'model_admin%' AND (pg_get_userbyid(p.proowner)<>'dxos_ai_owner' OR p.proconfig IS NULL)`); n != 0 {
			t.Fatal("administration functions are not owned by the isolated owner with a fixed search path", n)
		}
		snapshot := adminCatalogSnapshot(t, db)
		if out, e := runBootstrap(t, dsn, modelSchemaFlags...); e != nil {
			t.Fatalf("repeated bootstrap: %v: %s", e, out)
		}
		if out, e := runMigrations(t, dsn, "0011", modelSchemaFlags...); e != nil {
			t.Fatalf("repeated migration: %v: %s", e, out)
		}
		if adminCatalogSnapshot(t, db) != snapshot {
			t.Fatal("a repeated bootstrap or migration changed the administration schema")
		}
		if n := schemaCount(t, db, `SELECT count(*) FROM core.schema_migrations WHERE scope='cap-ai' AND version='0011'`); n != 1 {
			t.Fatal("repeated runs recorded migration 0011 twice", n)
		}
	})
	t.Run("checksum-mismatch-rejected", func(t *testing.T) {
		db, dsn := platformDB(t)
		if out, e := runBootstrap(t, dsn, modelSchemaFlags...); e != nil {
			t.Fatalf("bootstrap: %v: %s", e, out)
		}
		if _, e := db.Exec(`UPDATE core.schema_migrations SET checksum=repeat('0',64) WHERE scope='cap-ai' AND version='0011'`); e != nil {
			t.Fatal(e)
		}
		out, e := runMigrations(t, dsn, "0011", modelSchemaFlags...)
		if e == nil {
			t.Fatal("runner accepted an administration checksum mismatch")
		}
		if !strings.Contains(out, "0011") || !strings.Contains(out, "checksum conflict") {
			t.Fatalf("missing scope/version diagnostic: %q", out)
		}
		out, e = runBootstrap(t, dsn, modelSchemaFlags...)
		if e == nil {
			t.Fatal("bootstrap accepted an administration checksum mismatch")
		}
		if !strings.Contains(out, "0011") || !strings.Contains(out, "checksum conflict") {
			t.Fatalf("missing bootstrap diagnostic: %q", out)
		}
		if adminObjectsPresent(t, db) != true {
			t.Fatal("a rejected conflict dropped the applied administration schema")
		}
	})
	t.Run("agent-gate-selects-0011", func(t *testing.T) {
		db, dsn := platformDB(t)
		if out, e := runMigrations(t, dsn, "0011", modelSchemaDisabled...); e == nil {
			t.Fatalf("disabled flags selected the agent migration: %s", out)
		}
		if adminObjectsPresent(t, db) {
			t.Fatal("disabled flags installed model administration")
		}
		if out, e := runMigrations(t, dsn, "0011", "RAG_MODELS_SCHEMA_ENABLED=true", "RAG_GLOBAL_MODEL_REGISTRY_SCHEMA_ENABLED=true"); e == nil {
			t.Fatalf("agent opt-in was not required above 0008: %s", out)
		}
		if out, e := runMigrations(t, dsn, "0010", modelSchemaFlags...); e != nil {
			t.Fatalf("through-0010: %v: %s", e, out)
		}
		if adminObjectsPresent(t, db) {
			t.Fatal("through-0010 installed model administration")
		}
		if out, e := runMigrations(t, dsn, "0011", modelSchemaFlags...); e != nil {
			t.Fatalf("through-0011: %v: %s", e, out)
		}
		if !adminObjectsPresent(t, db) {
			t.Fatal("the existing >=0008 opt-in did not select 0011")
		}
	})
}

func TestModelAdminPrivileges(t *testing.T) {
	db, dsn := adminFixture(t)
	admin := testutil.As(t, dsn, "dxos_ai_model_admin")
	before := adminState(t, db)
	// The exact deployment session reads bounded metadata through the functions.
	var listing, slots string
	if e := admin.QueryRow(`SELECT cap_ai.model_admin_list($1,NULL,NULL,NULL,NULL,NULL)::text`, adminOperatorID).Scan(&listing); e != nil {
		t.Fatalf("administration listing: %v", e)
	}
	if !strings.Contains(listing, adminLLMProfile) || !strings.Contains(listing, adminLLMDigest) {
		t.Fatal("listing omitted bounded metadata", listing)
	}
	if e := admin.QueryRow(`SELECT cap_ai.model_admin_slots($1)::text`, adminOperatorID).Scan(&slots); e != nil {
		t.Fatalf("administration slots: %v", e)
	}
	for _, capability := range []string{"EMBEDDING", "LLM", "VLM", "RERANKING"} {
		if !strings.Contains(slots, capability) {
			t.Fatal("slots omitted", capability)
		}
	}
	for _, secret := range []string{"ADMIN_TEST_KEY", "credential_ref", "https://", "endpoint", "dispatch_token"} {
		if strings.Contains(listing, secret) || strings.Contains(slots, secret) {
			t.Fatal("administration output disclosed protected metadata", secret)
		}
	}
	if adminState(t, db) != before {
		t.Fatal("read-only administration mutated state")
	}
	// No direct table access, and no schema create right, for the login.
	for _, q := range []string{
		`SELECT * FROM cap_ai.model_profiles`,
		`SELECT * FROM cap_ai.model_candidates`,
		`SELECT * FROM cap_ai.global_model_slots`,
		`SELECT * FROM cap_ai.model_slot_events`,
		`SELECT * FROM cap_ai.knowledge_chunks`,
		`UPDATE cap_ai.global_model_slots SET slot_revision=9 WHERE capability='LLM'`,
		`UPDATE cap_ai.model_profiles SET enabled=true`,
		`DELETE FROM cap_ai.model_slot_events`,
		`INSERT INTO cap_ai.model_slot_events(capability,slot_revision,actor_id,action) VALUES('LLM',99,'11111111-1111-4111-8111-111111111111','PROMOTE')`,
	} {
		if _, e := admin.Exec(q); e == nil {
			t.Fatal("administration login has direct table access", q)
		}
	}
	if schemaBool(t, db, `SELECT has_table_privilege('dxos_ai_model_admin','cap_ai.model_profiles','SELECT')
		OR has_table_privilege('dxos_ai_model_admin','cap_ai.model_profiles','UPDATE')
		OR has_table_privilege('dxos_ai_model_admin','cap_ai.model_candidates','SELECT')
		OR has_table_privilege('dxos_ai_model_admin','cap_ai.global_model_slots','UPDATE')
		OR has_table_privilege('dxos_ai_model_admin','cap_ai.model_slot_events','INSERT')
		OR has_schema_privilege('dxos_ai_model_admin','cap_ai','CREATE')`) {
		t.Fatal("administration login carries direct relation rights")
	}
	if !schemaBool(t, db, `SELECT has_schema_privilege('dxos_ai_model_admin','cap_ai','USAGE')`) {
		t.Fatal("administration login cannot reach the capability schema")
	}
	// Only the dedicated login may execute the read-only interfaces. The obsolete
	// slot mutator is closed: it keeps its definition for T9 but no role can
	// execute it, so the legacy login cannot move live model authority.
	if !schemaBool(t, db, `SELECT has_function_privilege('dxos_ai_model_admin','cap_ai.model_admin_list(uuid,text,bigint,text,bigint,integer)','EXECUTE')
		AND has_function_privilege('dxos_ai_model_admin','cap_ai.model_admin_slots(uuid)','EXECUTE')`) {
		t.Fatal("administration login cannot execute its own interfaces")
	}
	if schemaBool(t, db, `SELECT has_function_privilege('dxos_ai_model_admin','cap_ai.model_admin_change_slot(uuid,text,bigint,text,text,bigint,text,jsonb)','EXECUTE')`) {
		t.Fatal("the obsolete slot mutator is still executable by the administration login")
	}
	if n := schemaCount(t, db, `SELECT count(*) FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace,
		unnest(ARRAY['dxos_process','dxos_hasura','dxos_authz','dxos_hr','dxos_reader','dxos_caller','dxos_module_test','dxos_ai_api','dxos_ai_worker','dxos_ai_dispatcher','dxos_ai_gateway','dxos_ai_model_catalog']) AS runtime_role
		WHERE n.nspname='cap_ai' AND p.proname LIKE 'model_admin%'
		AND has_function_privilege(runtime_role, p.oid, 'EXECUTE')`); n != 0 {
		t.Fatal("runtime role can execute the administration interfaces", n)
	}
	// Wrong sessions: no grant at all, and a mistaken grant still cannot substitute
	// for the deployment identity.
	for _, role := range []string{"dxos_ai_model_catalog", "dxos_ai_gateway", "dxos_ai_api", "dxos_ai_worker"} {
		c := testutil.As(t, dsn, role)
		for _, q := range []string{
			`SELECT cap_ai.model_admin_list('11111111-1111-4111-8111-111111111111',NULL,NULL,NULL,NULL,NULL)`,
			`SELECT cap_ai.model_admin_slots('11111111-1111-4111-8111-111111111111')`,
			`SELECT cap_ai.model_admin_change_slot('11111111-1111-4111-8111-111111111111','LLM',0,'PROMOTE',NULL,NULL,NULL,NULL)`,
		} {
			if _, e := c.Exec(q); e == nil {
				t.Fatal("runtime session executed administration", role, q)
			}
		}
		if _, e := db.Exec(fmt.Sprintf(`GRANT EXECUTE ON FUNCTION cap_ai.model_admin_list(uuid,text,bigint,text,bigint,integer) TO %s`, role)); e != nil {
			t.Fatal(e)
		}
		var ignored string
		e := c.QueryRow(`SELECT cap_ai.model_admin_list($1,NULL,NULL,NULL,NULL,NULL)::text`, adminOperatorID).Scan(&ignored)
		if e == nil || !strings.Contains(e.Error(), "model administration session required") {
			t.Fatal("an accidental grant bypassed the deployment session check", role, e)
		}
		if _, e := db.Exec(fmt.Sprintf(`REVOKE EXECUTE ON FUNCTION cap_ai.model_admin_list(uuid,text,bigint,text,bigint,integer) FROM %s`, role)); e != nil {
			t.Fatal(e)
		}
	}
	// A privileged session holds every grant but is never the deployment identity.
	if _, e := db.Exec(`SELECT cap_ai.model_admin_list($1,NULL,NULL,NULL,NULL,NULL)`, adminOperatorID); e == nil || !strings.Contains(e.Error(), "model administration session required") {
		t.Fatal("a superuser session was accepted", e)
	}
	if _, e := db.Exec(`SELECT cap_ai.model_admin_change_slot($1,'LLM',0,'PROMOTE',NULL,NULL,NULL,NULL)`, adminOperatorID); e == nil || !strings.Contains(e.Error(), "model administration session required") {
		t.Fatal("a superuser session could administer slots", e)
	}
	// PUBLIC is closed.
	reader := testutil.As(t, dsn, "dxos_reader")
	for _, q := range []string{
		`SELECT cap_ai.model_admin_list('11111111-1111-4111-8111-111111111111',NULL,NULL,NULL,NULL,NULL)`,
		`SELECT cap_ai.model_admin_slots('11111111-1111-4111-8111-111111111111')`,
		`SELECT * FROM cap_ai.model_profiles`,
	} {
		if _, e := reader.Exec(q); e == nil {
			t.Fatal("PUBLIC reached administration", q)
		}
	}
	// Membership plus SET ROLE is insufficient: session_user must be the login.
	if _, e := db.Exec(`GRANT dxos_ai_model_admin TO dxos_ai_api`); e != nil {
		t.Fatal(e)
	}
	api := testutil.As(t, dsn, "dxos_ai_api")
	ctx := context.Background()
	conn, e := api.Conn(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = conn.ExecContext(ctx, `SET ROLE dxos_ai_model_admin`); e != nil {
		t.Fatal(e)
	}
	var substituted string
	e = conn.QueryRowContext(ctx, `SELECT cap_ai.model_admin_slots($1)::text`, adminOperatorID).Scan(&substituted)
	if e == nil || !strings.Contains(e.Error(), "model administration session required") {
		t.Fatal("SET ROLE substitution bypassed session authority", e)
	}
	if _, e = conn.ExecContext(ctx, `RESET ROLE`); e != nil {
		t.Fatal(e)
	}
	conn.Close()
	if _, e = db.Exec(`REVOKE dxos_ai_model_admin FROM dxos_ai_api`); e != nil {
		t.Fatal(e)
	}
	// The login cannot reach an unrelated role either: the revoked catalog grant
	// must not become executable through current_setting('role') substitution.
	if _, e := db.Exec(`GRANT dxos_ai_model_catalog TO dxos_ai_model_admin`); e != nil {
		t.Fatal(e)
	}
	adminConn, e := admin.Conn(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = adminConn.ExecContext(ctx, `SET ROLE dxos_ai_model_catalog`); e != nil {
		t.Fatal(e)
	}
	if e = adminConn.QueryRowContext(ctx, `SELECT cap_ai.model_admin_list($1,NULL,NULL,NULL,NULL,NULL)::text`, adminOperatorID).Scan(&substituted); e == nil {
		t.Fatal("a substituted session executed the administration listing")
	}
	adminConn.Close()
}

// The obsolete slot mutator is closed. A legacy `dxos_ai_model_admin` session
// with a current role_operator could still flip `model_profiles.enabled` for the
// live deployment revision, withdraw the live route or pre-empt a reconciliation
// without ever recording a deployment selection. The definition stays for T9 to
// drop together with its CLI and its slot history; the execute grant does not.
func TestModelAdminLegacySlotMutatorRevoked(t *testing.T) {
	db, dsn := adminFixture(t)
	admin := testutil.As(t, dsn, "dxos_ai_model_admin")
	embed := readModelProfile(t, db, adminEmbedProfile, 1)
	generate := readModelProfile(t, db, adminLLMProfile, 1)
	applySelection(t, db, DeploymentSelection{Embedding: &embed, Generation: &generate})
	if !profileEnabled(t, db, adminEmbedProfile, 1) || !profileEnabled(t, db, adminLLMProfile, 1) {
		t.Fatal("the fixture did not select a live deployment")
	}

	// The definition survives: dropping it belongs to T9, with its CLI.
	if !schemaBool(t, db, `SELECT to_regprocedure('cap_ai.model_admin_change_slot(uuid,text,bigint,text,text,bigint,text,jsonb)') IS NOT NULL`) {
		t.Fatal("the obsolete slot mutator was removed instead of closed")
	}
	// No role reaches it. PUBLIC was revoked by the ownership block and every
	// deployment and administration login lost its grant.
	for _, role := range []string{
		"dxos_ai_model_admin", "dxos_ai_model_catalog", "dxos_ai_gateway", "dxos_ai_worker",
		"dxos_ai_api", "dxos_ai_dispatcher", "dxos_authz", "dxos_process", "dxos_reader",
	} {
		if schemaBool(t, db, `SELECT has_function_privilege($1,'cap_ai.model_admin_change_slot(uuid,text,bigint,text,text,bigint,text,jsonb)','EXECUTE')`, role) {
			t.Fatal("a role can still execute the obsolete slot mutator", role)
		}
	}

	// The legacy administration session cannot enable another revision, withdraw
	// the live route, roll the live revision back or race a reconciliation.
	live := adminState(t, db)
	events := deploymentEvents(t, db)
	for _, statement := range []string{
		`SELECT cap_ai.model_admin_change_slot($1,'EMBEDDING',0,'PROMOTE','admin-embed',2,repeat('3',64),NULL)`,
		`SELECT cap_ai.model_admin_change_slot($1,'LLM',0,'PROMOTE','admin-llm',1,repeat('1',64),NULL)`,
		`SELECT cap_ai.model_admin_change_slot($1,'EMBEDDING',0,'ROLLBACK','admin-embed',1,repeat('2',64),NULL)`,
		`SELECT cap_ai.model_admin_change_slot($1,'LLM',0,'DISABLE',NULL,NULL,NULL,'{"reason":"withdraw the live route"}')`,
	} {
		var out any
		adminRequireCode(t, admin.QueryRow(statement, adminOperatorID).Scan(&out), "42501")
	}
	if adminState(t, db) != live || deploymentEvents(t, db) != events {
		t.Fatal("a revoked administration call changed live authority")
	}
	if !profileEnabled(t, db, adminEmbedProfile, 1) || !profileEnabled(t, db, adminLLMProfile, 1) ||
		profileEnabled(t, db, adminEmbedProfile, 2) {
		t.Fatal("the live deployment revision moved")
	}
	if n := countRows(t, db, `SELECT count(*) FROM cap_ai.global_model_slots
		WHERE slot_revision <> 0 OR profile_id IS NOT NULL OR profile_revision IS NOT NULL`); n != 0 {
		t.Fatal("the obsolete slot bookkeeping was written", n)
	}

	// The administration login keeps its read-only interfaces: the sanitized
	// deployment status still reports the live selection and no secret.
	var raw []byte
	if err := admin.QueryRow(`SELECT cap_ai.model_deployment_status()`).Scan(&raw); err != nil {
		t.Fatalf("administration status read: %v", err)
	}
	var status DeploymentStatus
	if err := json.Unmarshal(raw, &status); err != nil || !status.Enabled || status.LiveProfiles != 2 {
		t.Fatalf("deployment status = %s (%v)", raw, err)
	}
	for _, secret := range []string{"ADMIN_TEST_KEY", "credential_ref", "endpoint", "https://"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("deployment status disclosed %q: %s", secret, raw)
		}
	}

	// A live deployment is unaffected by the closed mutator: reconciliation stays
	// idempotent and never approves processing on its own.
	if again := applySelection(t, db, DeploymentSelection{Embedding: &embed, Generation: &generate}); again.Changed {
		t.Fatalf("an identical reconciliation was not idempotent: %+v", again)
	}
	if n := countRows(t, db, `SELECT count(*) FROM cap_ai.agent_processing_approvals`); n != 0 {
		t.Fatal("a reconciliation created a processing approval", n)
	}
}
