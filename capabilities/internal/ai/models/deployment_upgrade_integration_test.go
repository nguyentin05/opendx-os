//go:build integration

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

// Upgrade and migration-path evidence for deployment-bound model authority:
// a plain FTS installation never selects 0013, a model installation applies the
// existing prerequisites plus 0013 once, a second run changes nothing and the
// checksums stay intact, and an installation that already carries model history,
// approvals, embeddings, transcripts and unrelated data preserves every row.

package models

import (
	"database/sql"
	"strings"
	"testing"
)

// legacyModelHistory is a plausible pre-0013 installation: two enabled revisions
// of one purpose from the legacy promote flow, a slot binding with audit history,
// a candidate declaration, a PERSONAL processing approval with its event, a queued
// EMBED job, a built embedding generation, a transcript and unrelated rows.
const legacyModelHistory = `
INSERT INTO core.apps(app_id,display_name,version,status,spec) VALUES
 ('legacy','Legacy','1','Ready','{}'),
 ('unrelated','Unrelated','1','Ready','{}');
INSERT INTO core.roles(name,app_id) VALUES('role_legacy','legacy');
INSERT INTO core.users(id,email,display_name) VALUES
 ('11111111-1111-4111-8111-111111111111','legacy@test.invalid','Legacy');
INSERT INTO core.user_roles(user_id,role_name) VALUES
 ('11111111-1111-4111-8111-111111111111','role_legacy'),
 ('11111111-1111-4111-8111-111111111111','role_operator');
INSERT INTO core.files(id,app_id,bucket,object_key,file_name,scan_status,sha256_hash,uploaded_by,mime_type,size_bytes,read_policy)
 VALUES('33333333-3333-4333-8333-333333333333','legacy','test','file','private.pdf','CLEAN',repeat('a',64),
   '11111111-1111-4111-8111-111111111111','application/pdf',4,'OWNER_ONLY');
INSERT INTO cap_ai.knowledge_collections(id,app_id,kind,owner_user_id)
 VALUES('44444444-4444-4444-8444-444444444444','legacy','PERSONAL','11111111-1111-4111-8111-111111111111');
INSERT INTO cap_ai.knowledge_documents(id,collection_id,source_kind,owner_user_id,initial_file_id,title,access_mode,processing_policy_id,processing_policy_version)
 VALUES('55555555-5555-4555-8555-555555555555','44444444-4444-4444-8444-444444444444','UPLOAD',
   '11111111-1111-4111-8111-111111111111','33333333-3333-4333-8333-333333333333','Private','PRIVATE','local-only',1);
INSERT INTO cap_ai.document_versions(id,document_id,version_no,source_kind,source_file_id,sha256,media_type,size_bytes,parser_profile,state)
 VALUES('66666666-6666-4666-8666-666666666666','55555555-5555-4555-8555-555555555555',1,'UPLOAD',
   '33333333-3333-4333-8333-333333333333',repeat('a',64),'application/pdf',4,'standard-text-v1','READY');
INSERT INTO cap_ai.ingestion_jobs(id,version_id,kind,profile_key) VALUES
 ('77777777-7777-4777-8777-777777777777','66666666-6666-4666-8666-666666666666','PARSE','standard-text-v1');
INSERT INTO cap_ai.processing_runs(id,version_id,ingestion_job_id,fencing_token,attempt_no,state)
 VALUES('88888888-8888-4888-8888-888888888888','66666666-6666-4666-8666-666666666666',
   '77777777-7777-4777-8777-777777777777',1,1,'COMPLETE');
UPDATE cap_ai.document_versions SET active_run_id='88888888-8888-4888-8888-888888888888';
UPDATE cap_ai.knowledge_documents SET active_version_id='66666666-6666-4666-8666-666666666666',
   desired_version_id='66666666-6666-4666-8666-666666666666';
-- Derived writes need a live parse lease; this is historical fixture data, so the
-- guard is bypassed exactly for these rows and restored immediately after.
SET session_replication_role=replica;
INSERT INTO cap_ai.knowledge_chunks(run_id,ordinal,text,section_path,content_hash,token_count)
 VALUES('88888888-8888-4888-8888-888888888888',0,'Legacy policy text','[]',repeat('a',64),3);
INSERT INTO cap_ai.model_profiles(id,revision,purpose,execution,provider_id,model_id,dimension,limits,enabled) VALUES
 ('legacy-embed',1,'EMBED','HOSTED','legacy','embed-one',3,'{"max_input_tokens":4096,"max_output_tokens":20,"max_bytes":8192,"timeout_ms":1000}',true),
 ('legacy-embed',2,'EMBED','HOSTED','legacy','embed-two',3,'{"max_input_tokens":4096,"max_output_tokens":20,"max_bytes":8192,"timeout_ms":1000}',true),
 ('legacy-gen',1,'GENERATE','HOSTED','legacy','gen-one',NULL,'{"max_input_tokens":4096,"max_output_tokens":20,"max_bytes":8192,"timeout_ms":1000}',true);
INSERT INTO cap_ai.model_candidates(profile_id,revision,adapter_key,declaration_digest,declaration) VALUES
 ('legacy-gen',1,'bounded-http-v1',repeat('9',64),jsonb_build_object('id','legacy-gen','revision',1,
   'purpose','GENERATE','execution','HOSTED','provider_id','legacy','model_id','gen-one',
   'credential_ref',NULL,'dimension',NULL,
   'limits',jsonb_build_object('max_input_tokens',4096,'max_output_tokens',20,'max_bytes',8192,'timeout_ms',1000)));
UPDATE cap_ai.global_model_slots SET profile_id='legacy-gen',profile_revision=1,slot_revision=4
 WHERE capability='LLM';
UPDATE cap_ai.global_model_slots SET profile_id='legacy-embed',profile_revision=2,slot_revision=7
 WHERE capability='EMBEDDING';
INSERT INTO cap_ai.model_slot_events(capability,slot_revision,profile_id,profile_revision,actor_id,action,review) VALUES
 ('LLM',4,'legacy-gen',1,'11111111-1111-4111-8111-111111111111','PROMOTE',jsonb_build_object('reason','legacy review')),
 ('EMBEDDING',7,'legacy-embed',2,'11111111-1111-4111-8111-111111111111','PROMOTE',jsonb_build_object('reason','legacy review'));
INSERT INTO cap_ai.processing_policy_profiles(policy_id,policy_version,model_profile_id,profile_revision)
 VALUES('local-only',1,'legacy-embed',1);
INSERT INTO cap_ai.agent_processing_approvals(review_digest,profile_id,profile_revision,provider_id,purpose,
  source_policy_id,source_policy_version,source_classification,query_app_id,query_classification,
  retention_evidence,training_use_evidence,region_evidence,approved_by,approved_session)
 VALUES(sha256('legacy-review'::bytea),'legacy-embed',1,'legacy','EMBED','local-only',1,'PERSONAL','legacy','PERSONAL',
   'review/retention','review/no-training','review/region','11111111-1111-4111-8111-111111111111','dxos_authz');
INSERT INTO cap_ai.agent_processing_approval_events(approval_id,action,actor_id,session_identity,reason)
 SELECT id,'APPROVE','11111111-1111-4111-8111-111111111111','dxos_authz','legacy review'
   FROM cap_ai.agent_processing_approvals WHERE profile_id='legacy-embed';
INSERT INTO cap_ai.ingestion_jobs(id,version_id,kind,profile_key) VALUES
 ('99999999-9999-4999-8999-999999999999','66666666-6666-4666-8666-666666666666','EMBED','legacy-embed@1');
INSERT INTO cap_ai.embedding_generations(id,run_id,model_profile_id,profile_revision,dimension,state,expected_count,completed_count,job_id,fencing_token)
 VALUES('aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa','88888888-8888-4888-8888-888888888888','legacy-embed',1,3,'READY',1,1,
   '99999999-9999-4999-8999-999999999999',1);
INSERT INTO cap_ai.chunk_embeddings(generation_id,run_id,chunk_id,content_hash,embedding)
 SELECT 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa','88888888-8888-4888-8888-888888888888',id,content_hash,'[1,0,0]'::public.vector
   FROM cap_ai.knowledge_chunks WHERE run_id='88888888-8888-4888-8888-888888888888';
SET session_replication_role=origin;
INSERT INTO cap_ai.agent_conversations(id,owner_user_id,title)
 VALUES('bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb','11111111-1111-4111-8111-111111111111','Legacy transcript');
INSERT INTO cap_ai.agent_messages(conversation_id,seq,role,content,status,client_key,body_hash)
 VALUES('bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb',1,'USER','legacy question','PENDING',repeat('c',64),repeat('d',64));
INSERT INTO cap_ai.agent_messages(conversation_id,seq,role,content,status,response)
 VALUES('bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb',2,'ASSISTANT','legacy answer','COMPLETE',jsonb_build_object('text','legacy answer'));
INSERT INTO cap_ai.model_operations(operation_key,actor_id,app_id,profile_id,profile_revision,purpose,fingerprint,state,error_code,finished_at)
 VALUES('legacy-operation','11111111-1111-4111-8111-111111111111','legacy','legacy-gen',1,'GENERATE',repeat('e',64),
   'FAILED_UNSENT','MODEL_UNAVAILABLE',now());
`

// preservedRows is every row an upgrade must keep, with the live switch as the one
// column that is allowed to change.
const preservedRows = `
SELECT jsonb_build_object(
 'profiles', (SELECT jsonb_agg(to_jsonb(p) - 'enabled' ORDER BY p.id, p.revision) FROM cap_ai.model_profiles p),
 'candidates', (SELECT count(*) FROM cap_ai.model_candidates),
 'slot_events', (SELECT count(*) FROM cap_ai.model_slot_events),
 'slots', (SELECT jsonb_agg(to_jsonb(s) ORDER BY s.capability) FROM cap_ai.global_model_slots s),
 'approvals', (SELECT count(*) FROM cap_ai.agent_processing_approvals),
 'approval_events', (SELECT count(*) FROM cap_ai.agent_processing_approval_events),
 'documents', (SELECT count(*) FROM cap_ai.knowledge_documents),
 'versions', (SELECT count(*) FROM cap_ai.document_versions),
 'chunks', (SELECT count(*) FROM cap_ai.knowledge_chunks),
 'generations', (SELECT jsonb_agg(to_jsonb(g) ORDER BY g.id) FROM cap_ai.embedding_generations g),
 'embeddings', (SELECT count(*) FROM cap_ai.chunk_embeddings),
 'jobs', (SELECT jsonb_agg(jsonb_build_object('id',j.id,'kind',j.kind,'profile_key',j.profile_key,'state',j.state) ORDER BY j.id) FROM cap_ai.ingestion_jobs j),
 'conversations', (SELECT count(*) FROM cap_ai.agent_conversations),
 'messages', (SELECT count(*) FROM cap_ai.agent_messages),
 'operations', (SELECT jsonb_agg(to_jsonb(o) - 'updated_at' ORDER BY o.operation_key) FROM cap_ai.model_operations o),
 'policies', (SELECT jsonb_agg(to_jsonb(p) ORDER BY p.id, p.version) FROM cap_ai.processing_policies p),
 'unrelated_apps', (SELECT count(*) FROM core.apps WHERE app_id='unrelated'),
 'files', (SELECT count(*) FROM core.files))::text`

func snapshot(t *testing.T, db *sql.DB, query string) string {
	t.Helper()
	var value string
	if err := db.QueryRow(query).Scan(&value); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return value
}

// TestDeploymentUpgradePreservesHistory applies 0013 to an installation that
// already holds model history, approvals, embeddings, transcripts and unrelated
// data, and asserts that nothing is lost, that the old authority is denied until
// the deployment reconciles, and that unreviewed historical routes are never
// reused.
func TestDeploymentUpgradePreservesHistory(t *testing.T) {
	db, dsn := platformDB(t)
	if out, err := runMigrations(t, dsn, "0012", modelSchemaFlags...); err != nil {
		t.Fatalf("migrate to 0012: %v: %s", err, out)
	}
	if _, err := db.Exec(legacyModelHistory); err != nil {
		t.Fatalf("legacy fixture: %v", err)
	}
	before := snapshot(t, db, preservedRows)
	ledgerBefore := snapshot(t, db, `SELECT string_agg(scope||':'||version||':'||checksum, ',' ORDER BY scope,version)
		FROM core.schema_migrations`)

	// The upgrade itself.
	if out, err := runMigrations(t, dsn, "0013", modelSchemaFlags...); err != nil {
		t.Fatalf("migrate to 0013: %v: %s", err, out)
	}
	if snapshot(t, db, preservedRows) != before {
		t.Fatal("the upgrade changed a preserved row")
	}
	if countRows(t, db, `SELECT count(*) FROM cap_ai.model_profiles WHERE enabled`) != 0 {
		t.Fatal("the upgrade left a live profile before the deployment reconciled")
	}
	// The earlier checksums are unchanged; only 0013 is appended.
	ledgerAfter := snapshot(t, db, `SELECT string_agg(scope||':'||version||':'||checksum, ',' ORDER BY scope,version)
		FROM core.schema_migrations`)
	if !strings.HasPrefix(ledgerAfter, ledgerBefore) {
		t.Fatal("the upgrade rewrote an applied migration checksum")
	}
	if !strings.Contains(ledgerAfter, "cap-ai:0013:") {
		t.Fatal("the upgrade did not record 0013")
	}

	// No historical route is usable: neither enabled profile can authorize a call.
	gateway := personalLogin(t, db, "dxos_ai_gateway")
	var profile string
	if err := gateway.QueryRow(`SELECT coalesce(cap_ai.model_profile('legacy-gen',1)::text,'')`).Scan(&profile); err != nil || profile != "" {
		t.Fatalf("an unreconciled profile was readable: %s %v", profile, err)
	}
	var allowed bool
	if err := gateway.QueryRow(`SELECT coalesce((cap_ai.model_policy($1,'role_legacy','legacy','PERSONAL','legacy-embed',1,'[]')->>'allowed')::boolean,false)`,
		deploymentOperator).Scan(&allowed); err != nil || allowed {
		t.Fatalf("a historical profile authorized a call: allowed=%t err=%v", allowed, err)
	}
	// The reviewed approval record is preserved but is not authority on its own:
	// it is only one input, and the exact deployed profile is missing.
	if countRows(t, db, `SELECT count(*) FROM cap_ai.agent_processing_approvals WHERE profile_id='legacy-embed' AND enabled`) != 1 {
		t.Fatal("the historical approval record was lost")
	}
	var approval sql.NullInt64
	if err := db.QueryRow(`SELECT cap_ai.personal_processing_approval('legacy-embed',1,'legacy','local-only',1)`).Scan(&approval); err != nil {
		t.Fatal(err)
	}
	if approval.Valid {
		t.Fatal("a historical approval still authorizes processing")
	}
	// The legacy job for the historical profile is never claimed.
	var lease sql.NullString
	worker := personalLogin(t, db, "dxos_ai_worker")
	if err := worker.QueryRow(`SELECT cap_ai.claim_embedding($1,'legacy-embed',1)`, deploymentOperator).Scan(&lease); err != nil || lease.Valid {
		t.Fatalf("a historical profile claimed embedding work: %s %v", lease.String, err)
	}
	// At most one enabled profile per purpose is enforced from here on.
	_, err := db.Exec(`UPDATE cap_ai.model_profiles SET enabled=true WHERE purpose='EMBED'`)
	wantSQLState(t, err, "23505")

	// Reconciling the deployment authorizes exactly the declared revision and
	// leaves every historical row in place.
	embedTwo := readModelProfile(t, db, "legacy-embed", 2)
	generateOne := readModelProfile(t, db, "legacy-gen", 1)
	report := applySelection(t, db, DeploymentSelection{Embedding: &embedTwo, Generation: &generateOne})
	if !report.Changed || len(report.Profiles) != 2 {
		t.Fatalf("post-upgrade reconciliation: %+v", report)
	}
	if !profileEnabled(t, db, "legacy-embed", 2) || profileEnabled(t, db, "legacy-embed", 1) {
		t.Fatal("the reconciled embedding revision is not the single live one")
	}
	if snapshot(t, db, `SELECT jsonb_agg(to_jsonb(p) - 'enabled' ORDER BY p.id,p.revision)::text FROM cap_ai.model_profiles p`) == "" {
		t.Fatal("profile history disappeared")
	}
	if countRows(t, db, `SELECT count(*) FROM cap_ai.chunk_embeddings`) != 1 ||
		countRows(t, db, `SELECT count(*) FROM cap_ai.agent_messages`) != 2 ||
		countRows(t, db, `SELECT count(*) FROM cap_ai.model_slot_events`) != 2 ||
		countRows(t, db, `SELECT count(*) FROM core.apps WHERE app_id='unrelated'`) != 1 {
		t.Fatal("reconciliation removed historical or unrelated data")
	}
	// The reconciled revision is readable again, and only that revision.
	if err := gateway.QueryRow(`SELECT coalesce(cap_ai.model_profile('legacy-gen',1)::text,'')`).Scan(&profile); err != nil || profile == "" {
		t.Fatalf("the reconciled profile is not readable: %v", err)
	}

	// A second run of the same selection is a no-op and appends no event.
	events := deploymentEvents(t, db)
	again := applySelection(t, db, DeploymentSelection{Embedding: &embedTwo, Generation: &generateOne})
	if again.Changed || deploymentEvents(t, db) != events {
		t.Fatalf("re-applying the reconciled selection was not idempotent: %+v", again)
	}
}

// TestDeploymentMigrationPaths covers the four selection paths: a fresh FTS-only
// installation never selects 0013 and never needs pgvector, upgrading that same
// database to models applies the existing prerequisites plus 0013, and a second
// run changes nothing.
func TestDeploymentMigrationPaths(t *testing.T) {
	db, dsn := platformDB(t)
	// 1. Fresh FTS-only installation.
	if out, err := runMigrations(t, dsn, "0007"); err != nil {
		t.Fatalf("FTS-only install: %v: %s", err, out)
	}
	if schemaBool(t, db, `SELECT EXISTS(SELECT FROM pg_extension WHERE extname='vector')`) {
		t.Fatal("an FTS-only installation installed pgvector")
	}
	if schemaBool(t, db, `SELECT to_regclass('cap_ai.model_profiles') IS NOT NULL`) {
		t.Fatal("an FTS-only installation installed the model schema")
	}
	if schemaBool(t, db, `SELECT to_regprocedure('cap_ai.reconcile_deployment_models(jsonb)') IS NOT NULL`) {
		t.Fatal("an FTS-only installation installed deployment authority")
	}
	if countRows(t, db, `SELECT count(*) FROM core.schema_migrations WHERE scope='cap-ai' AND version='0013'`) != 0 {
		t.Fatal("an FTS-only installation recorded 0013")
	}
	ftsLedger := snapshot(t, db, `SELECT string_agg(version||':'||checksum, ',' ORDER BY version)
		FROM core.schema_migrations WHERE scope='cap-ai'`)

	// 2. Upgrade that same database to a model installation.
	if out, err := runMigrations(t, dsn, "0013", modelSchemaFlags...); err != nil {
		t.Fatalf("model upgrade: %v: %s", err, out)
	}
	for _, required := range []string{
		"cap_ai.model_profiles", "cap_ai.global_model_slots", "cap_ai.model_deployment_events",
	} {
		if !schemaBool(t, db, `SELECT to_regclass($1) IS NOT NULL`, required) {
			t.Fatalf("model upgrade is missing %s", required)
		}
	}
	if !schemaBool(t, db, `SELECT to_regprocedure('cap_ai.reconcile_deployment_models(jsonb)') IS NOT NULL`) {
		t.Fatal("model upgrade did not install the deployment interface")
	}
	for _, version := range []string{"0004", "0005", "0008", "0010", "0011", "0012", "0013"} {
		if countRows(t, db, `SELECT count(*) FROM core.schema_migrations WHERE scope='cap-ai' AND version=$1`, version) != 1 {
			t.Fatalf("model upgrade did not record %s exactly once", version)
		}
	}
	// The FTS-era checksums are untouched.
	for _, entry := range strings.Split(ftsLedger, ",") {
		parts := strings.SplitN(entry, ":", 2)
		if len(parts) != 2 {
			continue
		}
		if countRows(t, db, `SELECT count(*) FROM core.schema_migrations WHERE scope='cap-ai' AND version=$1 AND checksum=$2`,
			parts[0], parts[1]) != 1 {
			t.Fatalf("the model upgrade rewrote checksum %s", parts[0])
		}
	}
	ledger := snapshot(t, db, `SELECT string_agg(version||':'||checksum, ',' ORDER BY version)
		FROM core.schema_migrations WHERE scope='cap-ai'`)

	// 3. Second run: nothing is applied or rewritten.
	if out, err := runMigrations(t, dsn, "0013", modelSchemaFlags...); err != nil {
		t.Fatalf("second run: %v: %s", err, out)
	}
	if snapshot(t, db, `SELECT string_agg(version||':'||checksum, ',' ORDER BY version)
		FROM core.schema_migrations WHERE scope='cap-ai'`) != ledger {
		t.Fatal("the second run changed the migration ledger")
	}
	if deploymentEvents(t, db) != 0 {
		t.Fatal("a migration run recorded a deployment selection")
	}
	if countRows(t, db, `SELECT count(*) FROM pg_indexes WHERE schemaname='cap_ai' AND indexname='model_profiles_enabled_purpose'`) != 1 {
		t.Fatal("the live-profile index is missing or duplicated")
	}

	// 4. The same file set applied through the bootstrap script is identical.
	bootDB, bootDSN := platformDB(t)
	if out, err := runBootstrap(t, bootDSN, modelSchemaFlags...); err != nil {
		t.Fatalf("bootstrap: %v: %s", err, out)
	}
	if !schemaBool(t, bootDB, `SELECT to_regprocedure('cap_ai.reconcile_deployment_models(jsonb)') IS NOT NULL`) {
		t.Fatal("the bootstrap did not apply deployment authority")
	}
	if countRows(t, bootDB, `SELECT count(*) FROM core.schema_migrations WHERE scope='cap-ai' AND version='0013'`) != 1 {
		t.Fatal("the bootstrap did not record 0013 exactly once")
	}
	// The bootstrap refuses a model installation without the agent opt-in.
	plainDB, plainDSN := platformDB(t)
	if _, err := runBootstrap(t, plainDSN, modelSchemaDisabled...); err != nil {
		t.Fatalf("bootstrap with the model schema disabled: %v", err)
	}
	if schemaBool(t, plainDB, `SELECT to_regclass('cap_ai.model_deployment_events') IS NOT NULL`) {
		t.Fatal("a bootstrap without the model schema installed deployment authority")
	}
}
