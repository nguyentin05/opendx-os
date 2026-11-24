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
	"sync"
	"testing"
	"time"

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

// adminQualification is the Task 4 ProbeReport shape: identity, verdict, the one
// synthetic citation set and bounded usage counters, with no content or secrets.
func adminQualification(profile string, revision int64, digest, purpose string, dimension, inputTokens, outputTokens int) string {
	q := fmt.Sprintf(`{"profile_id":%q,"revision":%d,"declaration_digest":%q,"purpose":%q,"passed":true,"checked_at":"2026-11-24T10:00:00.123456Z"`,
		profile, revision, digest, purpose)
	if purpose == "EMBED" {
		q += fmt.Sprintf(`,"dimension":%d`, dimension)
	} else {
		q += `,"citation_ids":["E1"]`
	}
	return q + fmt.Sprintf(`,"usage":{"input_tokens":%d,"output_tokens":%d}}`, inputTokens, outputTokens)
}

func adminReview(profile string, revision int64, digest, purpose string, dimension int) string {
	return fmt.Sprintf(`{"declaration_digest":%q,"qualification":%s,"attestation_ref":"review/2026-11-24/model-board-17"}`,
		digest, adminQualification(profile, revision, digest, purpose, dimension, 10, 0))
}

type adminChangeArgs struct {
	Actor    any
	Slot     any
	Expected any
	Action   any
	Profile  any
	Revision any
	Digest   any
	Review   any
}

func adminChange(conn *sql.DB, args adminChangeArgs) (map[string]any, error) {
	var raw sql.NullString
	if err := conn.QueryRow(`SELECT cap_ai.model_admin_change_slot($1,$2::text,$3::bigint,$4::text,$5::text,$6::bigint,$7::text,$8::jsonb)`,
		args.Actor, args.Slot, args.Expected, args.Action, args.Profile, args.Revision, args.Digest, args.Review).Scan(&raw); err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(raw.String), &out); err != nil {
		return nil, err
	}
	return out, nil
}

func adminPromote(conn *sql.DB, slot string, expected int64, profile string, revision int64, digest, review string) (map[string]any, error) {
	return adminChange(conn, adminChangeArgs{
		Actor: adminOperatorID, Slot: slot, Expected: expected, Action: "PROMOTE",
		Profile: profile, Revision: revision, Digest: digest, Review: review,
	})
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

func adminProfiles(t *testing.T, db *sql.DB) string {
	t.Helper()
	var profiled string
	if e := db.QueryRow(`SELECT coalesce(string_agg(to_jsonb(p)::text,'|' ORDER BY p.id,p.revision),'') FROM cap_ai.model_profiles p`).Scan(&profiled); e != nil {
		t.Fatal(e)
	}
	return profiled
}

// adminImmutableProfiles snapshots every profile row except the live switch, so an
// assertion can distinguish "history was rewritten" from "the live revision moved".
func adminImmutableProfiles(t *testing.T, db *sql.DB) string {
	t.Helper()
	var profiled string
	if e := db.QueryRow(`SELECT coalesce(string_agg((to_jsonb(p)-'enabled')::text,'|' ORDER BY p.id,p.revision),'') FROM cap_ai.model_profiles p`).Scan(&profiled); e != nil {
		t.Fatal(e)
	}
	return profiled
}

func adminSlotBinding(t *testing.T, db *sql.DB, capability string) (int64, sql.NullString, sql.NullInt64) {
	t.Helper()
	var revision int64
	var profile sql.NullString
	var profileRevision sql.NullInt64
	if e := db.QueryRow(`SELECT slot_revision,profile_id,profile_revision FROM cap_ai.global_model_slots WHERE capability=$1`, capability).Scan(&revision, &profile, &profileRevision); e != nil {
		t.Fatalf("%s slot: %v", capability, e)
	}
	return revision, profile, profileRevision
}

func adminEventCount(t *testing.T, db *sql.DB) int {
	t.Helper()
	return schemaCount(t, db, `SELECT count(*) FROM cap_ai.model_slot_events`)
}

func adminProfileEnabled(t *testing.T, db *sql.DB, profile string, revision int64) bool {
	t.Helper()
	return schemaBool(t, db, `SELECT enabled FROM cap_ai.model_profiles WHERE id=$1 AND revision=$2`, profile, revision)
}

// adminWaitForLock blocks until the administration mutation is waiting on a
// lock, so a concurrent authority change is committed while it is serialized.
func adminWaitForLock(t *testing.T, db *sql.DB) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		var waiting int
		if e := db.QueryRow(`SELECT count(*) FROM pg_stat_activity WHERE wait_event_type='Lock' AND query LIKE '%model_admin_change_slot%'`).Scan(&waiting); e != nil {
			t.Fatal(e)
		}
		if waiting > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("administration mutation never serialized with the authority change")
		}
		time.Sleep(10 * time.Millisecond)
	}
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
	// Only the dedicated login may execute the interfaces.
	if !schemaBool(t, db, `SELECT has_function_privilege('dxos_ai_model_admin','cap_ai.model_admin_list(uuid,text,bigint,text,bigint,integer)','EXECUTE')
		AND has_function_privilege('dxos_ai_model_admin','cap_ai.model_admin_slots(uuid)','EXECUTE')
		AND has_function_privilege('dxos_ai_model_admin','cap_ai.model_admin_change_slot(uuid,text,bigint,text,text,bigint,text,jsonb)','EXECUTE')`) {
		t.Fatal("administration login cannot execute its own interfaces")
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

func TestModelAdminPromotionAndAudit(t *testing.T) {
	db, dsn := adminFixture(t)
	admin := testutil.As(t, dsn, "dxos_ai_model_admin")
	before := adminState(t, db)
	validLLM := adminReview(adminLLMProfile, 1, adminLLMDigest, "GENERATE", 0)
	validEmbed := adminReview(adminEmbedProfile, 1, adminEmbedDigest, "EMBED", 8)
	denied := []struct {
		name string
		args adminChangeArgs
		code string
	}{
		{"non-operator", adminChangeArgs{adminPlainID, "LLM", int64(0), "PROMOTE", adminLLMProfile, int64(1), adminLLMDigest, validLLM}, "42501"},
		{"inactive-operator", adminChangeArgs{adminInactiveID, "LLM", int64(0), "PROMOTE", adminLLMProfile, int64(1), adminLLMDigest, validLLM}, "42501"},
		{"revoked-operator", adminChangeArgs{adminRevokedID, "LLM", int64(0), "PROMOTE", adminLLMProfile, int64(1), adminLLMDigest, validLLM}, "42501"},
		{"vlm-slot", adminChangeArgs{adminOperatorID, "VLM", int64(0), "PROMOTE", adminLLMProfile, int64(1), adminLLMDigest, validLLM}, "22023"},
		{"reranking-slot", adminChangeArgs{adminOperatorID, "RERANKING", int64(0), "PROMOTE", adminLLMProfile, int64(1), adminLLMDigest, validLLM}, "22023"},
		{"unknown-action", adminChangeArgs{adminOperatorID, "LLM", int64(0), "SET", adminLLMProfile, int64(1), adminLLMDigest, validLLM}, "22023"},
		{"negative-expected-revision", adminChangeArgs{adminOperatorID, "LLM", int64(-1), "PROMOTE", adminLLMProfile, int64(1), adminLLMDigest, validLLM}, "22023"},
		{"missing-expected-revision", adminChangeArgs{adminOperatorID, "LLM", nil, "PROMOTE", adminLLMProfile, int64(1), adminLLMDigest, validLLM}, "22023"},
		{"missing-review", adminChangeArgs{adminOperatorID, "LLM", int64(0), "PROMOTE", adminLLMProfile, int64(1), adminLLMDigest, nil}, "22023"},
		{"review-not-an-object", adminChangeArgs{adminOperatorID, "LLM", int64(0), "PROMOTE", adminLLMProfile, int64(1), adminLLMDigest, "[]"}, "22023"},
		{"extra-review-key", adminChangeArgs{adminOperatorID, "LLM", int64(0), "PROMOTE", adminLLMProfile, int64(1), adminLLMDigest, `{"endpoint":"https://provider.invalid",` + validLLM[1:]}, "22023"},
		{"tampered-review-digest", adminChangeArgs{adminOperatorID, "LLM", int64(0), "PROMOTE", adminLLMProfile, int64(1), adminLLMDigest,
			strings.Replace(validLLM, adminLLMDigest, strings.Repeat("f", 64), 1)}, "22023"},
		{"unreviewed-digest", adminChangeArgs{adminOperatorID, "LLM", int64(0), "PROMOTE", adminLLMProfile, int64(1), strings.Repeat("e", 64),
			strings.Replace(validLLM, adminLLMDigest, strings.Repeat("e", 64), -1)}, "22023"},
		{"unapproved-qualification", adminChangeArgs{adminOperatorID, "LLM", int64(0), "PROMOTE", adminLLMProfile, int64(1), adminLLMDigest,
			strings.Replace(validLLM, `"passed":true`, `"passed":false`, 1)}, "22023"},
		{"purpose-mismatch", adminChangeArgs{adminOperatorID, "EMBEDDING", int64(0), "PROMOTE", adminLLMProfile, int64(1), adminLLMDigest, validLLM}, "22023"},
		{"dimension-mismatch", adminChangeArgs{adminOperatorID, "EMBEDDING", int64(0), "PROMOTE", adminEmbedProfile, int64(1), adminEmbedDigest,
			strings.Replace(validEmbed, `"dimension":8`, `"dimension":4`, 1)}, "22023"},
		{"wrong-citation", adminChangeArgs{adminOperatorID, "LLM", int64(0), "PROMOTE", adminLLMProfile, int64(1), adminLLMDigest,
			strings.Replace(validLLM, `["E1"]`, `["E2"]`, 1)}, "22023"},
		{"usage-over-limit", adminChangeArgs{adminOperatorID, "LLM", int64(0), "PROMOTE", adminLLMProfile, int64(1), adminLLMDigest,
			strings.Replace(validLLM, `"input_tokens":10`, `"input_tokens":5000`, 1)}, "22023"},
		{"unknown-qualification-key", adminChangeArgs{adminOperatorID, "LLM", int64(0), "PROMOTE", adminLLMProfile, int64(1), adminLLMDigest,
			strings.Replace(validLLM, `"passed":true`, `"passed":true,"raw_prompt":"secret"`, 1)}, "22023"},
		{"credential-like-attestation", adminChangeArgs{adminOperatorID, "LLM", int64(0), "PROMOTE", adminLLMProfile, int64(1), adminLLMDigest,
			strings.Replace(validLLM, "review/2026-11-24/model-board-17", "https://board.example/review", 1)}, "22023"},
		{"gateway-ceiling-exceeded", adminChangeArgs{adminOperatorID, "LLM", int64(0), "PROMOTE", adminWideProfile, int64(1), adminWideDigest,
			adminReview(adminWideProfile, 1, adminWideDigest, "GENERATE", 0)}, "22023"},
		{"rollback-without-history", adminChangeArgs{adminOperatorID, "EMBEDDING", int64(0), "ROLLBACK", adminEmbedProfile, int64(2), adminEmbed2Digest,
			adminReview(adminEmbedProfile, 2, adminEmbed2Digest, "EMBED", 8)}, "22023"},
		{"unknown-profile", adminChangeArgs{adminOperatorID, "LLM", int64(0), "PROMOTE", "missing", int64(1), adminLLMDigest, validLLM}, "22023"},
	}
	for _, tc := range denied {
		t.Run(tc.name, func(t *testing.T) {
			_, e := adminChange(admin, tc.args)
			adminRequireCode(t, e, tc.code)
			if adminState(t, db) != before {
				t.Fatal("a denied request changed slot, profile or audit state")
			}
		})
	}
	// A valid, fully reviewed disabled HOSTED candidate binds the LLM slot once.
	result, e := adminPromote(admin, "LLM", 0, adminLLMProfile, 1, adminLLMDigest, validLLM)
	if e != nil {
		t.Fatalf("promotion: %v", e)
	}
	for key, want := range map[string]any{"capability": "LLM", "profile_id": adminLLMProfile, "profile_revision": float64(1), "slot_revision": float64(1), "action": "PROMOTE"} {
		if result[key] != want {
			t.Fatalf("%s=%v, want %v: %v", key, result[key], want, result)
		}
	}
	if id, ok := result["event_id"].(string); !ok || id == "" {
		t.Fatalf("promotion did not report its audit event: %v", result)
	}
	revision, profile, profileRevision := adminSlotBinding(t, db, "LLM")
	if revision != 1 || !profile.Valid || profile.String != adminLLMProfile || profileRevision.Int64 != 1 {
		t.Fatal("promotion did not bind the exact profile revision", revision, profile, profileRevision)
	}
	if !adminProfileEnabled(t, db, adminLLMProfile, 1) {
		t.Fatal("promotion did not enable the reviewed profile")
	}
	if n := adminEventCount(t, db); n != 1 {
		t.Fatal("promotion did not append exactly one audit event", n)
	}
	var eventRevision int64
	var eventActor, eventAction, eventReview string
	if e := db.QueryRow(`SELECT slot_revision,actor_id::text,action,review::text FROM cap_ai.model_slot_events WHERE capability='LLM'`).Scan(&eventRevision, &eventActor, &eventAction, &eventReview); e != nil {
		t.Fatal(e)
	}
	if eventRevision != 1 || eventActor != adminOperatorID || eventAction != "PROMOTE" {
		t.Fatal("audit event does not carry the committed transition", eventRevision, eventActor, eventAction)
	}
	for _, want := range []string{adminLLMDigest, "review/2026-11-24/model-board-17", `"passed": true`} {
		if !strings.Contains(eventReview, want) {
			t.Fatal("audit review omitted human review evidence", want, eventReview)
		}
	}
	for _, secret := range []string{"ADMIN_TEST_KEY", "https://", "credential_ref", "endpoint"} {
		if strings.Contains(eventReview, secret) {
			t.Fatal("audit review stored protected content", secret)
		}
	}
	if n := schemaCount(t, db, `SELECT count(*) FROM cap_ai.global_model_slots WHERE capability IN('EMBEDDING','VLM','RERANKING') AND slot_revision=0 AND profile_id IS NULL`); n != 3 {
		t.Fatal("promotion touched another slot", n)
	}
	// Promotion is not a processing approval: the two authorities stay separate.
	if n := schemaCount(t, db, `SELECT count(*) FROM cap_ai.agent_processing_approvals`); n != 0 {
		t.Fatal("promotion created a processing approval", n)
	}
	// The same review a second time is a stale expectation, never a second event.
	if _, e := adminPromote(admin, "LLM", 0, adminLLMProfile, 1, adminLLMDigest, validLLM); adminCode(e) != "40001" {
		t.Fatal("stale expected revision was accepted", e)
	}
	if n := adminEventCount(t, db); n != 1 {
		t.Fatal("a stale expectation appended an audit event", n)
	}
	// The embedding slot uses the same audited contract with its own purpose.
	if _, e := adminPromote(admin, "EMBEDDING", 0, adminEmbedProfile, 1, adminEmbedDigest, validEmbed); e != nil {
		t.Fatalf("embedding promotion: %v", e)
	}
	revision, _, profileRevision = adminSlotBinding(t, db, "EMBEDDING")
	if revision != 1 || profileRevision.Int64 != 1 {
		t.Fatal("embedding slot not bound", revision, profileRevision)
	}
	// Exact inspection is bounded; page mode is a strict (id, revision) cursor.
	var inspected, page []map[string]any
	if e := admin.QueryRow(`SELECT cap_ai.model_admin_list($1,$2,$3,NULL,NULL,NULL)::text`, adminOperatorID, adminLLMProfile, 1).Scan(jsonList{&inspected}); e != nil {
		t.Fatalf("exact inspect: %v", e)
	}
	if len(inspected) != 1 || inspected[0]["profile_id"] != adminLLMProfile || inspected[0]["revision"] != float64(1) ||
		inspected[0]["adapter_key"] != "bounded-http-v1" || inspected[0]["declaration_digest"] != adminLLMDigest ||
		inspected[0]["enabled"] != true || inspected[0]["purpose"] != "GENERATE" || inspected[0]["execution"] != "HOSTED" {
		t.Fatal("exact inspect is not the bounded reviewed metadata", inspected)
	}
	if e := admin.QueryRow(`SELECT cap_ai.model_admin_list($1,NULL,NULL,NULL,NULL,2)::text`, adminOperatorID).Scan(jsonList{&page}); e != nil {
		t.Fatalf("first page: %v", e)
	}
	if len(page) != 2 || page[0]["profile_id"] != adminEmbedProfile || page[0]["revision"] != float64(1) || page[1]["revision"] != float64(2) {
		t.Fatal("first page is not ordered by profile and revision", page)
	}
	var next []map[string]any
	if e := admin.QueryRow(`SELECT cap_ai.model_admin_list($1,NULL,NULL,$2,$3,2)::text`, adminOperatorID, adminEmbedProfile, 2).Scan(jsonList{&next}); e != nil {
		t.Fatalf("second page: %v", e)
	}
	if len(next) != 2 || next[0]["profile_id"] != adminLLMProfile || next[1]["profile_id"] != adminWideProfile {
		t.Fatal("cursor page is not strictly after the last revision", next)
	}
	for _, bad := range []struct {
		name string
		args []any
	}{
		{"half-exact-cursor", []any{adminOperatorID, adminLLMProfile, nil, nil, nil, nil}},
		{"half-revision-cursor", []any{adminOperatorID, nil, int64(1), nil, nil, nil}},
		{"half-page-cursor", []any{adminOperatorID, nil, nil, adminLLMProfile, nil, nil}},
		{"page-too-small", []any{adminOperatorID, nil, nil, nil, nil, int64(0)}},
		{"page-too-large", []any{adminOperatorID, nil, nil, nil, nil, int64(101)}},
	} {
		var ignored []map[string]any
		e := admin.QueryRow(`SELECT cap_ai.model_admin_list($1,$2,$3,$4,$5,$6)::text`, bad.args...).Scan(jsonList{&ignored})
		if e == nil {
			t.Fatal("invalid list bounds accepted", bad.name)
		}
		adminRequireCode(t, e, "22023")
	}
}

// jsonList keeps a jsonb payload in one Scan destination.
type jsonList struct{ rows *[]map[string]any }

func (j jsonList) Scan(value any) error {
	raw, ok := value.([]byte)
	if !ok {
		if text, isText := value.(string); isText {
			raw = []byte(text)
		} else if value == nil {
			return nil
		} else {
			return fmt.Errorf("unexpected jsonb value %T", value)
		}
	}
	return json.Unmarshal(raw, j.rows)
}

func TestModelAdminConcurrentCAS(t *testing.T) {
	db, dsn := adminFixture(t)
	first := testutil.As(t, dsn, "dxos_ai_model_admin")
	second := testutil.As(t, dsn, "dxos_ai_model_admin")
	review := adminReview(adminLLMProfile, 1, adminLLMDigest, "GENERATE", 0)
	eventCountBefore := adminEventCount(t, db)
	const expectedRevision = int64(0)
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, conn := range []*sql.DB{first, second} {
		wg.Add(1)
		go func(conn *sql.DB) {
			defer wg.Done()
			<-start
			_, e := adminPromote(conn, "LLM", expectedRevision, adminLLMProfile, 1, adminLLMDigest, review)
			results <- e
		}(conn)
	}
	close(start)
	wg.Wait()
	close(results)
	succeeded, conflicted := 0, 0
	for e := range results {
		switch adminCode(e) {
		case "":
			if e == nil {
				succeeded++
			}
		case "40001":
			conflicted++
		default:
			t.Fatalf("unexpected concurrent outcome: %v", e)
		}
	}
	slotRevisionAfter, profile, profileRevision := adminSlotBinding(t, db, "LLM")
	eventCountAfter := adminEventCount(t, db)
	if slotRevisionAfter != expectedRevision+1 || eventCountAfter != eventCountBefore+1 {
		t.Fatal("non-atomic slot transition")
	}
	if succeeded != 1 || conflicted != 1 {
		t.Fatal("stale slot revision was accepted")
	}
	if !profile.Valid || profile.String != adminLLMProfile || profileRevision.Int64 != 1 {
		t.Fatal("the winning request did not bind the exact revision", profile, profileRevision)
	}
}

func TestModelAdminAuthorityRevocation(t *testing.T) {
	t.Run("revoked-before-call", func(t *testing.T) {
		db, dsn := adminFixture(t)
		admin := testutil.As(t, dsn, "dxos_ai_model_admin")
		before := adminState(t, db)
		if _, e := db.Exec(`DELETE FROM core.user_roles WHERE user_id=$1 AND role_name='role_operator'`, adminOperatorID); e != nil {
			t.Fatal(e)
		}
		afterRevoke := adminState(t, db)
		_, e := adminPromote(admin, "LLM", 0, adminLLMProfile, 1, adminLLMDigest, adminReview(adminLLMProfile, 1, adminLLMDigest, "GENERATE", 0))
		adminRequireCode(t, e, "42501")
		if adminState(t, db) != afterRevoke || afterRevoke != before {
			t.Fatal("a revoked operator changed state")
		}
	})
	t.Run("inactive-before-call", func(t *testing.T) {
		db, dsn := adminFixture(t)
		admin := testutil.As(t, dsn, "dxos_ai_model_admin")
		before := adminState(t, db)
		if _, e := db.Exec(`UPDATE core.users SET status='DISABLED' WHERE id=$1`, adminOperatorID); e != nil {
			t.Fatal(e)
		}
		afterDisable := adminState(t, db)
		_, e := adminPromote(admin, "LLM", 0, adminLLMProfile, 1, adminLLMDigest, adminReview(adminLLMProfile, 1, adminLLMDigest, "GENERATE", 0))
		adminRequireCode(t, e, "42501")
		if adminState(t, db) != afterDisable || afterDisable != before {
			t.Fatal("an inactive operator changed state")
		}
	})
	t.Run("role-revoked-while-waiting", func(t *testing.T) {
		db, dsn := adminFixture(t)
		admin := testutil.As(t, dsn, "dxos_ai_model_admin")
		before := adminState(t, db)
		tx, e := db.Begin()
		if e != nil {
			t.Fatal(e)
		}
		defer func() { _ = tx.Rollback() }()
		if _, e = tx.Exec(`DELETE FROM core.user_roles WHERE user_id=$1 AND role_name='role_operator'`, adminOperatorID); e != nil {
			t.Fatal(e)
		}
		results := make(chan error, 1)
		go func() {
			_, err := adminPromote(admin, "LLM", 0, adminLLMProfile, 1, adminLLMDigest, adminReview(adminLLMProfile, 1, adminLLMDigest, "GENERATE", 0))
			results <- err
		}()
		adminWaitForLock(t, db)
		if e = tx.Commit(); e != nil {
			t.Fatal(e)
		}
		adminRequireCode(t, <-results, "42501")
		if adminState(t, db) != before {
			t.Fatal("a concurrently revoked operator changed state")
		}
	})
	t.Run("actor-disabled-while-waiting", func(t *testing.T) {
		db, dsn := adminFixture(t)
		admin := testutil.As(t, dsn, "dxos_ai_model_admin")
		before := adminState(t, db)
		tx, e := db.Begin()
		if e != nil {
			t.Fatal(e)
		}
		defer func() { _ = tx.Rollback() }()
		if _, e = tx.Exec(`UPDATE core.users SET status='DISABLED' WHERE id=$1`, adminOperatorID); e != nil {
			t.Fatal(e)
		}
		results := make(chan error, 1)
		go func() {
			_, err := adminPromote(admin, "LLM", 0, adminLLMProfile, 1, adminLLMDigest, adminReview(adminLLMProfile, 1, adminLLMDigest, "GENERATE", 0))
			results <- err
		}()
		adminWaitForLock(t, db)
		if e = tx.Commit(); e != nil {
			t.Fatal(e)
		}
		adminRequireCode(t, <-results, "42501")
		if adminState(t, db) != before {
			t.Fatal("a concurrently inactive operator changed state")
		}
	})
}

func TestModelAdminRollbackAndDisable(t *testing.T) {
	db, dsn := adminFixture(t)
	admin := testutil.As(t, dsn, "dxos_ai_model_admin")
	first := adminReview(adminEmbedProfile, 1, adminEmbedDigest, "EMBED", 8)
	second := adminReview(adminEmbedProfile, 2, adminEmbed2Digest, "EMBED", 8)
	if _, e := adminPromote(admin, "EMBEDDING", 0, adminEmbedProfile, 1, adminEmbedDigest, first); e != nil {
		t.Fatalf("first promotion: %v", e)
	}
	if _, e := adminPromote(admin, "EMBEDDING", 1, adminEmbedProfile, 2, adminEmbed2Digest, second); e != nil {
		t.Fatalf("second promotion: %v", e)
	}
	// Only one revision of a purpose may be live at a time: the newer promotion
	// withdraws the older live revision and both immutable revisions stay stored.
	if n := schemaCount(t, db, `SELECT count(*) FROM cap_ai.model_profiles WHERE id=$1 AND enabled`, adminEmbedProfile); n != 1 {
		t.Fatal("promotions did not leave exactly one live revision", n)
	}
	if n := schemaCount(t, db, `SELECT count(*) FROM cap_ai.model_profiles WHERE id=$1 AND enabled AND revision=2`, adminEmbedProfile); n != 1 {
		t.Fatal("the newest reviewed revision is not the live one", n)
	}
	events := adminEventCount(t, db)
	profiles := adminImmutableProfiles(t, db)
	// An explicitly reviewed older binding is restored at a new slot revision.
	result, e := adminChange(admin, adminChangeArgs{
		Actor: adminOperatorID, Slot: "EMBEDDING", Expected: int64(2), Action: "ROLLBACK",
		Profile: adminEmbedProfile, Revision: int64(1), Digest: adminEmbedDigest, Review: first,
	})
	if e != nil {
		t.Fatalf("rollback: %v", e)
	}
	if result["action"] != "ROLLBACK" || result["slot_revision"] != float64(3) || result["profile_revision"] != float64(1) {
		t.Fatalf("rollback did not report the new binding: %v", result)
	}
	revision, profile, profileRevision := adminSlotBinding(t, db, "EMBEDDING")
	if revision != 3 || !profile.Valid || profile.String != adminEmbedProfile || profileRevision.Int64 != 1 {
		t.Fatal("rollback did not restore the reviewed revision", revision, profile, profileRevision)
	}
	if n := adminEventCount(t, db); n != events+1 {
		t.Fatal("rollback did not append exactly one event", n)
	}
	// A rollback moves the live switch only: every stored profile revision must be
	// byte-identical apart from `enabled`, which is live state by design.
	if adminImmutableProfiles(t, db) != profiles {
		t.Fatalf("rollback rewrote stored profile revisions: before=%s after=%s", profiles, adminImmutableProfiles(t, db))
	}
	for _, relation := range []string{"cap_ai.embedding_generations", "cap_ai.chunk_embeddings", "cap_ai.model_operations"} {
		if n := schemaCount(t, db, `SELECT count(*) FROM `+relation); n != 0 {
			t.Fatal("rollback touched vectors or operations", relation, n)
		}
	}
	afterRollback := adminState(t, db)
	// Disable requires exactly one bounded operational reason and nothing else.
	for _, bad := range []adminChangeArgs{
		{Actor: adminOperatorID, Slot: "EMBEDDING", Expected: int64(3), Action: "DISABLE", Review: nil},
		{Actor: adminOperatorID, Slot: "EMBEDDING", Expected: int64(3), Action: "DISABLE", Review: `{"reason":""}`},
		{Actor: adminOperatorID, Slot: "EMBEDDING", Expected: int64(3), Action: "DISABLE", Review: `{"reason":"withdrawn","extra":true}`},
		{Actor: adminOperatorID, Slot: "EMBEDDING", Expected: int64(3), Action: "DISABLE", Review: `{"reason":["withdrawn"]}`},
		{Actor: adminOperatorID, Slot: "EMBEDDING", Expected: int64(3), Action: "DISABLE", Profile: adminEmbedProfile, Review: `{"reason":"withdrawn"}`},
		{Actor: adminOperatorID, Slot: "EMBEDDING", Expected: int64(3), Action: "DISABLE", Revision: int64(1), Review: `{"reason":"withdrawn"}`},
		{Actor: adminOperatorID, Slot: "EMBEDDING", Expected: int64(3), Action: "DISABLE", Digest: adminEmbedDigest, Review: `{"reason":"withdrawn"}`},
	} {
		_, e := adminChange(admin, bad)
		adminRequireCode(t, e, "22023")
		if adminState(t, db) != afterRollback {
			t.Fatal("a rejected disable changed state")
		}
	}
	// A stale expectation is a conflict, and a disable never rewinds a revision.
	if _, e := adminChange(admin, adminChangeArgs{
		Actor: adminOperatorID, Slot: "EMBEDDING", Expected: int64(2), Action: "DISABLE", Review: `{"reason":"withdrawn"}`,
	}); adminCode(e) != "40001" {
		t.Fatal("stale expectation was not a conflict", e)
	}
	if adminState(t, db) != afterRollback {
		t.Fatal("a conflicted disable changed state")
	}
	result, e = adminChange(admin, adminChangeArgs{
		Actor: adminOperatorID, Slot: "EMBEDDING", Expected: int64(3), Action: "DISABLE",
		Review: `{"reason":"reviewed withdrawal of the embedding binding"}`,
	})
	if e != nil {
		t.Fatalf("disable: %v", e)
	}
	if result["slot_revision"] != float64(4) || result["action"] != "DISABLE" || result["profile_id"] != nil || result["profile_revision"] != nil {
		t.Fatalf("disable did not report the unbound slot: %v", result)
	}
	revision, profile, profileRevision = adminSlotBinding(t, db, "EMBEDDING")
	if revision != 4 || profile.Valid || profileRevision.Valid {
		t.Fatal("disable did not unbind the slot", revision, profile, profileRevision)
	}
	// Unbinding the slot withdraws the binding only: the live deployment revision
	// stays enabled and the superseded revision stays disabled.
	if !adminProfileEnabled(t, db, adminEmbedProfile, 1) || adminProfileEnabled(t, db, adminEmbedProfile, 2) {
		t.Fatal("disable changed the live profile instead of the binding")
	}
	if n := adminEventCount(t, db); n != events+2 {
		t.Fatal("disable did not append exactly one event", n)
	}
	var reason string
	if e := db.QueryRow(`SELECT review->>'reason' FROM cap_ai.model_slot_events WHERE capability='EMBEDDING' AND action='DISABLE'`).Scan(&reason); e != nil || reason != "reviewed withdrawal of the embedding binding" {
		t.Fatal("disable reason not audited", reason, e)
	}
	if adminImmutableProfiles(t, db) != profiles {
		t.Fatal("disable rewrote stored profile revisions")
	}
}

func TestModelAdminAtomicAuditFailure(t *testing.T) {
	db, dsn := adminFixture(t)
	admin := testutil.As(t, dsn, "dxos_ai_model_admin")
	before := adminState(t, db)
	// A disposable test-only trigger forces the audit insert to fail after the
	// profile enable and the slot rebind have already been applied.
	if _, e := db.Exec(`CREATE FUNCTION cap_ai.test_reject_slot_event() RETURNS trigger
		LANGUAGE plpgsql SET search_path = pg_catalog AS $$
		BEGIN
		  RAISE EXCEPTION 'test audit failure';
		END $$`); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _, _ = db.Exec(`DROP FUNCTION IF EXISTS cap_ai.test_reject_slot_event() CASCADE`) })
	if _, e := db.Exec(`CREATE TRIGGER test_reject_slot_event BEFORE INSERT ON cap_ai.model_slot_events
		FOR EACH ROW EXECUTE FUNCTION cap_ai.test_reject_slot_event()`); e != nil {
		t.Fatal(e)
	}
	_, e := adminPromote(admin, "LLM", 0, adminLLMProfile, 1, adminLLMDigest, adminReview(adminLLMProfile, 1, adminLLMDigest, "GENERATE", 0))
	if e == nil {
		t.Fatal("a failed audit insert was reported as a committed transition")
	}
	if !strings.Contains(e.Error(), "test audit failure") {
		t.Fatalf("unexpected failure: %v", e)
	}
	if adminProfileEnabled(t, db, adminLLMProfile, 1) {
		t.Fatal("the profile enable was not rolled back with the audit insert")
	}
	revision, profile, _ := adminSlotBinding(t, db, "LLM")
	if revision != 0 || profile.Valid {
		t.Fatal("the slot binding was not rolled back with the audit insert", revision, profile)
	}
	if n := adminEventCount(t, db); n != 0 {
		t.Fatal("the failed audit insert left an event", n)
	}
	if adminState(t, db) != before {
		t.Fatal("an atomic failure left partial administration state")
	}
}
