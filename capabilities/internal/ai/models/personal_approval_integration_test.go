//go:build integration

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
package models

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/opendx-os/opendx-os/capabilities/internal/ai/contracts"
	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
	"github.com/opendx-os/opendx-os/capabilities/internal/testutil"
)

const personalActor = "11111111-1111-4111-8111-111111111111"
const personalOther = "22222222-2222-4222-8222-222222222222"
const personalDoc = "55555555-5555-4555-8555-555555555555"

func personalLogin(t *testing.T, db *sql.DB, role string) *sql.DB {
	t.Helper()
	var name string
	if err := db.QueryRow(`SELECT current_database()`).Scan(&name); err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(os.Getenv("TEST_DATABASE_URL"))
	u.Path = "/" + name
	c := testutil.As(t, u.String(), role)
	var safe bool
	if err := c.QueryRow(`SELECT NOT(rolsuper OR rolbypassrls OR rolcreaterole) AND session_user=current_user FROM pg_roles WHERE rolname=session_user`).Scan(&safe); err != nil || !safe {
		t.Fatal("unsafe runtime session", err)
	}
	return c
}
func personalFixture(t *testing.T, purpose string) (*sql.DB, *sql.DB, *Policy, Operation) {
	t.Helper()
	db := modelDB(t, true)
	applyRegistry(t, db)
	raw, err := os.ReadFile("../../../../platform/postgres/migrations/cap_ai/0008_personal_model_processing.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(string(raw)); err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO core.apps(app_id,display_name,version,status,spec) VALUES('library','Library','1','Ready','{"knowledge":{"version":1,"personalLibrary":{"enabled":true,"allowedRoles":["role_library_user"]}}}');
 INSERT INTO core.roles(name,app_id) VALUES('role_library_user','library');
 INSERT INTO core.users(id,email,display_name) VALUES('11111111-1111-4111-8111-111111111111','a@test.invalid','A'),('22222222-2222-4222-8222-222222222222','b@test.invalid','B');
 INSERT INTO core.user_roles(user_id,role_name) VALUES('11111111-1111-4111-8111-111111111111','role_library_user'),('11111111-1111-4111-8111-111111111111','role_operator'),('22222222-2222-4222-8222-222222222222','role_library_user');
 INSERT INTO core.files(id,app_id,bucket,object_key,file_name,scan_status,sha256_hash,uploaded_by,mime_type,size_bytes,read_policy) VALUES('33333333-3333-4333-8333-333333333333','library','test','file','private.pdf','CLEAN',repeat('a',64),'11111111-1111-4111-8111-111111111111','application/pdf',4,'OWNER_ONLY');
 INSERT INTO cap_ai.knowledge_collections(id,app_id,kind,owner_user_id) VALUES('44444444-4444-4444-8444-444444444444','library','PERSONAL','11111111-1111-4111-8111-111111111111');
 INSERT INTO cap_ai.knowledge_documents(id,collection_id,source_kind,owner_user_id,initial_file_id,title,access_mode,processing_policy_id,processing_policy_version) VALUES('55555555-5555-4555-8555-555555555555','44444444-4444-4444-8444-444444444444','UPLOAD','11111111-1111-4111-8111-111111111111','33333333-3333-4333-8333-333333333333','Private','PRIVATE','local-only',1);
 INSERT INTO cap_ai.document_versions(id,document_id,version_no,source_kind,source_file_id,sha256,media_type,size_bytes,parser_profile,state) VALUES('66666666-6666-4666-8666-666666666666','55555555-5555-4555-8555-555555555555',1,'UPLOAD','33333333-3333-4333-8333-333333333333',repeat('a',64),'application/pdf',4,'standard-text-v1','READY');
 INSERT INTO cap_ai.ingestion_jobs(id,version_id,kind,profile_key) VALUES('77777777-7777-4777-8777-777777777777','66666666-6666-4666-8666-666666666666','PARSE','standard-text-v1');
 INSERT INTO cap_ai.processing_runs(id,version_id,ingestion_job_id,fencing_token,attempt_no,state) VALUES('88888888-8888-4888-8888-888888888888','66666666-6666-4666-8666-666666666666','77777777-7777-4777-8777-777777777777',1,1,'COMPLETE');
 UPDATE cap_ai.document_versions SET active_run_id='88888888-8888-4888-8888-888888888888';
 UPDATE cap_ai.knowledge_documents SET active_version_id='66666666-6666-4666-8666-666666666666',desired_version_id='66666666-6666-4666-8666-666666666666';`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO cap_ai.model_profiles(id,revision,purpose,execution,provider_id,model_id,dimension,limits,enabled) VALUES('personal',1,$1,'HOSTED','fake','fake',CASE WHEN $1='EMBED' THEN 3 END,'{"max_input_tokens":4096,"max_output_tokens":20,"max_bytes":8192,"timeout_ms":1000}',true)`, purpose)
	if err != nil {
		t.Fatal(err)
	}
	var scope contracts.ReadScope
	var value []byte
	if err = db.QueryRow(`SELECT cap_ai.read_document($1,'role_library_user',$2)`, personalActor, personalDoc).Scan(&value); err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(value, &scope); err != nil {
		t.Fatal(err)
	}
	gateway := personalLogin(t, db, "dxos_ai_gateway")
	return db, personalLogin(t, db, "dxos_authz"), &Policy{Enabled: true, Authority: DatabaseAuthority{DB: gateway}}, Operation{Key: "personal", AppID: "library", ScopeKind: "PERSONAL", Purpose: purpose, ProfileID: "personal", ProfileRevision: 1, Sources: []contracts.ReadScope{scope}}
}
func personalReview(purpose string) string {
	return `{"profile_id":"personal","profile_revision":1,"provider_id":"fake","purpose":"` + purpose + `","source_policy_id":"local-only","source_policy_version":1,"source_classification":"PERSONAL","query_app_id":"library","query_classification":"PERSONAL","retention_evidence":"review/retention/30-days","training_use_evidence":"review/no-training","region_evidence":"review/region-eu"}`
}
func approvePersonal(t *testing.T, admin *sql.DB, purpose string) string {
	t.Helper()
	var id string
	err := admin.QueryRow(`SELECT cap_ai.approve_agent_processing($1,$2::jsonb)`, personalActor, personalReview(purpose)).Scan(&id)
	if err != nil {
		t.Fatalf("audited approval unavailable: %v", err)
	}
	return id
}
func checkPersonal(p *Policy, op Operation, actor string) error {
	_, err := p.Check(context.Background(), &auth.Identity{UserID: actor, Role: "role_library_user"}, op)
	return err
}
func TestHostedPersonalDeniedByDefault(t *testing.T) {
	db, _, p, op := personalFixture(t, "GENERATE")
	if _, err := db.Exec(`UPDATE cap_ai.global_model_slots SET profile_id='personal',profile_revision=1,slot_revision=1 WHERE capability='LLM'`); err != nil {
		t.Fatal(err)
	}
	if checkPersonal(p, op, personalActor) == nil {
		t.Fatal("slot approved personal egress")
	}
}
func TestHostedPersonalApprovedGenerate(t *testing.T) {
	db, admin, p, op := personalFixture(t, "GENERATE")
	approvePersonal(t, admin, "GENERATE")
	if err := checkPersonal(p, op, personalActor); err != nil {
		t.Fatal("matching approval denied", err)
	}
	if checkPersonal(p, op, personalOther) == nil {
		t.Fatal("approval bypassed owner READ")
	}
	if _, err := db.Exec(`INSERT INTO cap_ai.model_profiles(id,revision,purpose,execution,provider_id,model_id,limits,enabled) SELECT id,2,purpose,execution,'other-provider',model_id,limits,enabled FROM cap_ai.model_profiles WHERE id='personal'`); err != nil {
		t.Fatal(err)
	}
	op.ProfileRevision = 2
	if checkPersonal(p, op, personalActor) == nil {
		t.Fatal("approval transferred revisions")
	}
}
func TestModelPolicyRejectsMissingScopeAndSources(t *testing.T) {
	db, admin, p, op := personalFixture(t, "GENERATE")
	approvePersonal(t, admin, "GENERATE")
	// With a valid APP review, an absent scope would otherwise still classify the query.
	if _, err := db.Exec(
		`INSERT INTO cap_ai.processing_policies(id,version,execution,classification,enabled,approved_by,approved_at,query_app_id,provider_id,retention_approved)
		VALUES('app-query',1,'APPROVED_PROVIDER','NON_PERSONAL',true,'11111111-1111-4111-8111-111111111111',now(),'library','fake',true);
		INSERT INTO cap_ai.processing_policy_profiles(policy_id,policy_version,model_profile_id,profile_revision)
		VALUES('app-query',1,'personal',1)`); err != nil {
		t.Fatal(err)
	}
	gateway := p.Authority.(DatabaseAuthority).DB
	for _, tc := range []struct {
		name, scope, sources string
		wantAllowed          bool
	}{
		{"explicit empty list", "'PERSONAL'", "'[]'", true},
		{"SQL null sources", "'PERSONAL'", "NULL", false},
		{"JSON null sources", "'PERSONAL'", "'null'", false},
		{"object sources", "'PERSONAL'", "'{}'", false},
		{"missing scope", "NULL", "'[]'", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			query := `SELECT coalesce((cap_ai.model_policy($1,'role_library_user',$2,` + tc.scope + `,$3,$4,` + tc.sources + `)->>'allowed')::boolean,false)`
			var allowed bool
			if err := gateway.QueryRow(query, personalActor, op.AppID, op.ProfileID, op.ProfileRevision).Scan(&allowed); err != nil || allowed != tc.wantAllowed {
				t.Fatalf("allowed=%v, want %v, error=%v", allowed, tc.wantAllowed, err)
			}
		})
	}
}

func TestHostedPersonalApprovedEmbed(t *testing.T) {
	db, admin, p, op := personalFixture(t, "EMBED")
	approvePersonal(t, admin, "EMBED")
	if err := checkPersonal(p, op, personalActor); err != nil {
		t.Fatal(err)
	}
	op.Sources = nil
	if err := checkPersonal(p, op, personalActor); err != nil {
		t.Fatal("query-only approval denied", err)
	}
	var queued bool
	if err := db.QueryRow(`SELECT cap_ai.queue_embeddings($1,'role_library_user','personal',1)`, personalActor).Scan(&queued); err != nil {
		t.Fatal(err)
	}
	var lease []byte
	if err := db.QueryRow(`SELECT cap_ai.claim_embedding($1)`, personalActor).Scan(&lease); err != nil || len(lease) == 0 {
		t.Fatal("approved embedding not queued", err)
	}
	var l struct {
		Job   string `json:"job_id"`
		Run   string `json:"run_id"`
		Fence int64  `json:"fence"`
	}
	json.Unmarshal(lease, &l)
	op.Lease = &LeaseRef{JobID: l.Job, WorkerID: personalActor, RunID: l.Run, Fence: l.Fence}
	var raw []byte
	if err := db.QueryRow(`SELECT cap_ai.embedding_scope($1)`, l.Run).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var s contracts.ReadScope
	json.Unmarshal(raw, &s)
	op.Sources = []contracts.ReadScope{s}
	if err := checkPersonal(p, op, personalActor); err != nil {
		t.Fatal("worker approval denied", err)
	}
	t.Run("missing lease source snapshot", func(t *testing.T) {
		gateway := p.Authority.(DatabaseAuthority).DB
		var allowed bool
		err := gateway.QueryRow(`SELECT coalesce((cap_ai.worker_model_policy($1::uuid,$2,$1::text,$3,$4,$5,$6,$7,NULL)->>'allowed')::boolean,false)`,
			personalActor, l.Job, l.Fence, l.Run, op.ProfileID, op.ProfileRevision, op.AppID).Scan(&allowed)
		if err != nil || allowed {
			t.Fatalf("missing snapshot: allowed=%v, error=%v", allowed, err)
		}
	})
	id := approvePersonal(t, admin, "EMBED")
	if _, err := admin.Exec(`SELECT cap_ai.disable_agent_processing($1,$2,'superseded')`, personalActor, id); err != nil {
		t.Fatal(err)
	}
	if checkPersonal(p, op, personalActor) == nil {
		t.Fatal("worker reused disabled approval")
	}
	approvePersonal(t, admin, "EMBED")
	op.Lease.Fence++
	if checkPersonal(p, op, personalActor) == nil {
		t.Fatal("stale fence accepted")
	}
}
func TestHostedPersonalApprovalConcurrentRetry(t *testing.T) {
	db, admin, p, op := personalFixture(t, "GENERATE")
	secondAdmin := personalLogin(t, db, "dxos_authz")
	const query = `SELECT cap_ai.approve_agent_processing($1,$2::jsonb)`
	review := personalReview("GENERATE")
	type result struct {
		id  string
		err error
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	for _, connection := range []*sql.DB{admin, secondAdmin} {
		go func() {
			<-start
			var r result
			r.err = connection.QueryRow(query, personalActor, review).Scan(&r.id)
			results <- r
		}()
	}
	close(start)
	first, second := <-results, <-results
	if first.err != nil || second.err != nil {
		t.Fatalf("concurrent approval errors: %v, %v", first.err, second.err)
	}
	if first.id == "" || first.id != second.id {
		t.Errorf("concurrent approvals returned different UUIDs: %q, %q", first.id, second.id)
	}

	// Treat the initial responses as lost and withdraw the review recovered by retry.
	retried := approvePersonal(t, admin, "GENERATE")
	if retried != first.id {
		t.Errorf("retry returned %q, want %q", retried, first.id)
	}
	var disabled bool
	if err := admin.QueryRow(`SELECT cap_ai.disable_agent_processing($1,$2,'review withdrawn')`, personalActor, retried).Scan(&disabled); err != nil || !disabled {
		t.Fatal("could not withdraw recovered approval", disabled, err)
	}
	if checkPersonal(p, op, personalActor) == nil {
		t.Error("withdrawing the retried approval left PERSONAL processing enabled")
	}
	var approvals, events int
	if err := db.QueryRow(`SELECT (SELECT count(*) FROM cap_ai.agent_processing_approvals),
		(SELECT count(*) FROM cap_ai.agent_processing_approval_events)`).Scan(&approvals, &events); err != nil || approvals != 1 || events != 2 {
		t.Fatalf("expected one approval with two lifecycle events, got %d approvals, %d events: %v", approvals, events, err)
	}
	if renewed := approvePersonal(t, admin, "GENERATE"); renewed == retried {
		t.Fatal("new approval reused the withdrawn revision")
	}
}

func TestDisabledApprovalDeniesBeforeEgress(t *testing.T) {
	db, admin, p, op := personalFixture(t, "GENERATE")
	id := approvePersonal(t, admin, "GENERATE")
	if err := checkPersonal(p, op, personalActor); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(`SELECT cap_ai.disable_agent_processing($1,$2,'review withdrawn')`, personalActor, id); err != nil {
		t.Fatal(err)
	}
	if checkPersonal(p, op, personalActor) == nil {
		t.Fatal("revoked approval reused")
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM cap_ai.agent_processing_approval_events WHERE approval_id=$1`, id).Scan(&n); err != nil || n != 2 {
		t.Fatal("missing lifecycle audit", n, err)
	}
}
func TestGatewayRoleCannotSelfApprove(t *testing.T) {
	db, admin, _, _ := personalFixture(t, "GENERATE")
	for _, role := range []string{"dxos_ai_gateway", "dxos_ai_api", "dxos_ai_worker", "dxos_ai_model_catalog"} {
		c := personalLogin(t, db, role)
		for _, q := range []string{`SELECT cap_ai.approve_agent_processing('11111111-1111-4111-8111-111111111111','{}')`, `SELECT cap_ai.disable_agent_processing('11111111-1111-4111-8111-111111111111',gen_random_uuid(),'bad')`, `UPDATE cap_ai.agent_processing_approvals SET enabled=true`} {
			if _, err := c.Exec(q); err == nil {
				t.Fatal("self approval", role, q)
			}
		}
	}
	if _, err := admin.Exec(`SELECT cap_ai.approve_agent_processing($1,'{}')`, personalOther); err == nil {
		t.Fatal("non-operator approved")
	}
	approvePersonal(t, admin, "GENERATE")
}

func TestHostedPersonalApprovalBindingAndHistory(t *testing.T) {
	db, admin, p, op := personalFixture(t, "GENERATE")
	for _, pair := range [][2]string{{`"fake"`, `"other"`}, {`"GENERATE"`, `"EMBED"`}, {`"profile_revision":1`, `"profile_revision":2`}, {`"source_policy_version":1`, `"source_policy_version":2`}, {`"source_classification":"PERSONAL"`, `"source_classification":"UNKNOWN"`}, {`"query_classification":"PERSONAL"`, `"query_classification":"NON_PERSONAL"`}, {`"review/no-training"`, `""`}, {`"review/retention/30-days"`, `null`}, {`"review/region-eu"`, `" "`}} {
		bad := strings.Replace(personalReview("GENERATE"), pair[0], pair[1], 1)
		if _, err := admin.Exec(`SELECT cap_ai.approve_agent_processing($1,$2::jsonb)`, personalActor, bad); err == nil {
			t.Fatal("invalid review accepted", pair[0])
		}
	}
	id := approvePersonal(t, admin, "GENERATE")
	for _, q := range []string{`UPDATE cap_ai.agent_processing_approvals SET provider_id='other'`, `DELETE FROM cap_ai.agent_processing_approvals`, `UPDATE cap_ai.agent_processing_approval_events SET reason='rewrite'`, `DELETE FROM cap_ai.agent_processing_approval_events`} {
		if _, err := db.Exec(q); err == nil {
			t.Fatal("mutable review history", q)
		}
	}
	var audit bool
	if err := db.QueryRow(`SELECT approved_by=$1 AND approved_session='dxos_authz' AND approved_at IS NOT NULL FROM cap_ai.agent_processing_approvals WHERE id=$2`, personalActor, id).Scan(&audit); err != nil || !audit {
		t.Fatal("missing approval attribution", err)
	}
	other := op
	other.AppID = "other"
	if checkPersonal(p, other, personalActor) == nil {
		t.Fatal("query app mismatch allowed")
	}
	other = op
	other.Purpose = "EMBED"
	if checkPersonal(p, other, personalActor) == nil {
		t.Fatal("purpose mismatch allowed")
	}
	if _, err := db.Exec(`INSERT INTO cap_ai.processing_policies(id,version,execution,classification,enabled) VALUES('other-local',1,'LOCAL_ONLY','PERSONAL',true); UPDATE cap_ai.knowledge_documents SET processing_policy_id='other-local',policy_revision=policy_revision+1`); err != nil {
		t.Fatal(err)
	}
	var raw []byte
	if err := db.QueryRow(`SELECT cap_ai.read_document($1,'role_library_user',$2)`, personalActor, personalDoc).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var scope contracts.ReadScope
	if err := json.Unmarshal(raw, &scope); err != nil {
		t.Fatal(err)
	}
	op.Sources = []contracts.ReadScope{scope}
	if checkPersonal(p, op, personalActor) == nil {
		t.Fatal("one source approval covered another policy")
	}
	if _, err := admin.Exec(`SELECT cap_ai.disable_agent_processing($1,$2,'withdrawn')`, personalActor, id); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE cap_ai.agent_processing_approvals SET enabled=true WHERE id=$1`, id); err == nil {
		t.Fatal("reactivated immutable revision")
	}
}
func TestGatewayApprovalSessionDefense(t *testing.T) {
	db, admin, _, _ := personalFixture(t, "GENERATE")
	// An accidental EXECUTE grant or role membership is insufficient to approve.
	for _, role := range []string{"dxos_ai_gateway", "dxos_ai_api", "dxos_ai_worker", "dxos_ai_model_catalog"} {
		c := personalLogin(t, db, role)
		if _, err := db.Exec(fmt.Sprintf(`GRANT EXECUTE ON FUNCTION cap_ai.approve_agent_processing(uuid,jsonb) TO %s`, role)); err != nil {
			t.Fatal(err)
		}
		if _, err := c.Exec(`SELECT cap_ai.approve_agent_processing($1,$2::jsonb)`, personalActor, personalReview("GENERATE")); err == nil {
			t.Fatal("wrong session approved", role)
		}
	}
	if _, err := admin.Exec(`SELECT cap_ai.approve_agent_processing($1,$2::jsonb)`, personalOther, personalReview("GENERATE")); err == nil {
		t.Fatal("ordinary actor approved")
	}
	if _, err := db.Exec(`UPDATE core.users SET status='DISABLED' WHERE id=$1`, personalActor); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(`SELECT cap_ai.approve_agent_processing($1,$2::jsonb)`, personalActor, personalReview("GENERATE")); err == nil {
		t.Fatal("inactive operator approved")
	}
}

type personalEgressSpy struct{ calls int }

func (s *personalEgressSpy) Invoke(context.Context, Profile, Payload) (Result, error) {
	s.calls++
	return Result{}, nil
}

type disableBeforeSend struct {
	authority CurrentAuthority
	admin     *sql.DB
	approval  string
	checks    int
}

func (a *disableBeforeSend) Current(ctx context.Context, actor *auth.Identity, op Operation) (Decision, error) {
	a.checks++
	if a.checks == 3 {
		if _, err := a.admin.ExecContext(ctx, `SELECT cap_ai.disable_agent_processing($1,$2,'withdrawn before send')`, personalActor, a.approval); err != nil {
			return Decision{}, err
		}
	}
	return a.authority.Current(ctx, actor, op)
}
func TestDisabledApprovalAtIntentPreventsEgress(t *testing.T) {
	db, admin, p, op := personalFixture(t, "GENERATE")
	id := approvePersonal(t, admin, "GENERATE")
	p.Authority = &disableBeforeSend{authority: p.Authority, admin: admin, approval: id}
	spy := &personalEgressSpy{}
	g := Gateway{Policy: p, Operations: &OperationStore{DB: personalLogin(t, db, "dxos_ai_gateway")}, Providers: map[string]Provider{"fake": spy}}
	_, err := g.Call(context.Background(), &auth.Identity{UserID: personalActor, Role: "role_library_user"}, op, Payload{Query: "private query"})
	if err != ErrPolicy || spy.calls != 0 {
		t.Fatal("revoked approval sent content", err, spy.calls)
	}
	var state, code string
	if err = db.QueryRow(`SELECT state,error_code FROM cap_ai.model_operations`).Scan(&state, &code); err != nil || state != "FAILED_UNSENT" || code != "AUTHORITY_CHANGED" {
		t.Fatal("unsent operation not recorded as definitely unsent", state, code, err)
	}
}

func TestHostedPersonalCurrentAuthorityRevocation(t *testing.T) {
	for _, change := range []string{`UPDATE cap_ai.model_profiles SET enabled=false`, `UPDATE cap_ai.processing_policies SET enabled=false WHERE id='local-only'`, `DELETE FROM core.user_roles WHERE user_id='11111111-1111-4111-8111-111111111111' AND role_name='role_library_user'`, `UPDATE core.users SET status='DISABLED' WHERE id='11111111-1111-4111-8111-111111111111'`, `UPDATE core.files SET scan_status='INFECTED'`} {
		t.Run(strings.Fields(change)[1], func(t *testing.T) {
			db, admin, p, op := personalFixture(t, "GENERATE")
			approvePersonal(t, admin, "GENERATE")
			if err := checkPersonal(p, op, personalActor); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(change); err != nil {
				t.Fatal(err)
			}
			if checkPersonal(p, op, personalActor) == nil {
				t.Fatal("revoked authority reused", change)
			}
		})
	}
}

func TestPersonalWithdrawalSerializesAdminRevocation(t *testing.T) {
	db, admin, _, _ := personalFixture(t, "GENERATE")
	for _, migration := range []string{"0011_model_administration.sql", "0012_personal_admin_authority_locks.sql"} {
		raw, err := os.ReadFile("../../../../platform/postgres/migrations/cap_ai/" + migration)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(string(raw)); err != nil {
			t.Fatal(err)
		}
	}
	approval := approvePersonal(t, admin, "GENERATE")
	blocker, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback()
	if _, err = blocker.Exec(`SELECT 1 FROM cap_ai.agent_processing_approvals WHERE id=$1 FOR UPDATE`, approval); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := admin.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var pid int
	if err = conn.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	withdrawal := make(chan error, 1)
	go func() {
		_, e := conn.ExecContext(ctx, `SELECT cap_ai.disable_agent_processing($1,$2,'concurrency fixture')`, personalActor, approval)
		withdrawal <- e
	}()
	deadline := time.Now().Add(3 * time.Second)
	waiting := false
	for time.Now().Before(deadline) {
		if err = db.QueryRow(`SELECT coalesce(wait_event_type='Lock',false) FROM pg_stat_activity WHERE pid=$1`, pid).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !waiting {
		t.Fatal("withdrawal never reached blocked mutation")
	}
	revocation := make(chan error, 1)
	go func() {
		_, e := db.ExecContext(ctx, `DELETE FROM core.user_roles WHERE user_id=$1 AND role_name='role_operator'`, personalActor)
		revocation <- e
	}()
	revokedEarly := false
	select {
	case err = <-revocation:
		if err != nil {
			t.Fatal(err)
		}
		revokedEarly = true
	case <-time.After(100 * time.Millisecond):
	}
	if err = blocker.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err = <-withdrawal; err != nil {
		t.Fatal(err)
	}
	if !revokedEarly {
		if err = <-revocation; err != nil {
			t.Fatal(err)
		}
	}
	if revokedEarly {
		t.Fatal("role revocation committed before the protected withdrawal completed")
	}
	if _, err = admin.Exec(`SELECT cap_ai.disable_agent_processing($1,$2,'revoked actor')`, personalActor, approval); err == nil {
		t.Fatal("revoked operator remained eligible")
	}
}
