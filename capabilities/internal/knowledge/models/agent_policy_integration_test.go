//go:build integration

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package models

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
	"github.com/opendx-os/opendx-os/capabilities/internal/knowledge/contracts"
)

type agentFixture struct {
	db, admin *sql.DB
	gateway   Gateway
	actor     *auth.Identity
	provider  *agentProvider
	op        Operation
	proofs    []AgentSourceProof
}

type agentProvider struct{ calls int }

func (s *agentProvider) Invoke(_ context.Context, profile Profile, _ Payload) (Result, error) {
	s.calls++
	if profile.Purpose == "EMBED" {
		return Result{Vector: []float32{1, 0, 0}, Usage: &Usage{InputTokens: 3}}, nil
	}
	return Result{Text: "Grounded answer", CitationIDs: []string{"hr", "procurement", "library"}, Usage: &Usage{InputTokens: 3, OutputTokens: 2}}, nil
}

func agentExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}

func agentPackage(t *testing.T, db, api *sql.DB, app, role string) contracts.ReadScope {
	t.Helper()
	agentExec(t, db, `INSERT INTO core.apps(app_id,display_name,version,status,publication_generation,spec)
	VALUES($1,$1,'1','Ready',1,jsonb_build_object(
	'knowledge',jsonb_build_object('version',1,'sources',jsonb_build_array(jsonb_build_object(
	    'id','policy','path','knowledge/policy.md','mediaType','text/markdown','indexingProfile','standard-text-v1',
	    'access',jsonb_build_object('mode','ROLE','allowedRoles',jsonb_build_array($2::text))))),
	'_publication',jsonb_build_object('generation',1,'spec_digest',repeat('b',64),'package_digest',repeat('c',64),
	    'sources',jsonb_build_array(jsonb_build_object('source_id','policy','source_path','knowledge/policy.md',
	      'incarnation',gen_random_uuid(),'authorization_generation',1,'declaration_hash',repeat('d',64),
	      'sha256',repeat('a',64),'media_type','text/markdown','size_bytes',8,'artifact_ref',$1||'/policy/'||repeat('a',64),
	      'safety_state','APPROVED','parser_profile','standard-text-v1')))))`, app, role)
	agentExec(t, db, `INSERT INTO core.roles(name,app_id) VALUES($1,$2)`, role, app)
	agentExec(t, db, `INSERT INTO core.user_roles(user_id,role_name) VALUES($1,$2)`, personalActor, role)
	agentExec(t, db, `INSERT INTO core.app_capabilities(app_id,cap_id) VALUES($1,'cap-ai')`, app)
	agentExec(t, db, `SELECT cap_ai.reconcile_package($1,1,repeat('b',64))`, app)
	agentExec(t, db, `INSERT INTO cap_ai.processing_policies(id,version,execution,classification,enabled,approved_by,approved_at,query_app_id,provider_id,retention_approved)
	VALUES($1,1,'APPROVED_PROVIDER','NON_PERSONAL',true,$2,now(),$1,'fake',true)`, app, personalActor)
	agentExec(t, db, `INSERT INTO cap_ai.processing_policy_profiles(policy_id,policy_version,model_profile_id,profile_revision)
	VALUES($1,1,'personal',1)`, app)
	var doc, ver, run string
	if err := db.QueryRow(`UPDATE cap_ai.knowledge_documents d SET processing_policy_id=$1,active_version_id=desired_version_id
		FROM cap_ai.knowledge_collections c WHERE c.id=d.collection_id AND c.app_id=$1 RETURNING d.id,d.active_version_id`, app).Scan(&doc, &ver); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`INSERT INTO cap_ai.processing_runs(version_id,ingestion_job_id,fencing_token,attempt_no,state)
	SELECT version_id,id,1,1,'COMPLETE' FROM cap_ai.ingestion_jobs WHERE version_id=$1 RETURNING id`, ver).Scan(&run); err != nil {
		t.Fatal(err)
	}
	agentExec(t, db, `UPDATE cap_ai.document_versions SET state='READY',active_run_id=$1 WHERE id=$2`, run, ver)
	var raw []byte
	if err := api.QueryRow(`SELECT cap_ai.read_document($1,$2,$3)`, personalActor, role, doc).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var scope contracts.ReadScope
	if err := json.Unmarshal(raw, &scope); err != nil {
		t.Fatal(err)
	}
	return scope
}

func newAgentFixture(t *testing.T, purpose string) agentFixture {
	t.Helper()
	db, admin, policy, personal := personalFixture(t, purpose)
	for _, migration := range []string{"0007_agent_scopes.sql", "0009_personal_hybrid.sql", "0010_agent_model_authority.sql"} {
		raw, err := os.ReadFile("../../../../platform/postgres/migrations/cap_ai/" + migration)
		if err != nil {
			t.Fatal(err)
		}
		agentExec(t, db, string(raw))
	}
	agentExec(t, db, `INSERT INTO core.apps(app_id,display_name,version,status,spec)
	VALUES('agent_processing','Agent Processing','1','Ready','{}');
	INSERT INTO core.app_capabilities(app_id,cap_id) VALUES('library','cap-ai')`)
	api := personalLogin(t, db, "dxos_ai_api")
	proofs := []AgentSourceProof{
		{ScopeKind: "APP", AppID: "hr", ActingRole: "role_hr_reader", ReadScope: agentPackage(t, db, api, "hr", "role_hr_reader")},
		{ScopeKind: "APP", AppID: "procurement", ActingRole: "role_proc_reader", ReadScope: agentPackage(t, db, api, "procurement", "role_proc_reader")},
		{ScopeKind: "PERSONAL", AppID: "library", ActingRole: "role_library_user", ReadScope: personal.Sources[0]},
	}
	provider := &agentProvider{}
	op := Operation{Key: "agent-call", AppID: "agent_processing", ScopeKind: "AGENT", Purpose: purpose, ProfileID: "personal", ProfileRevision: 1}
	if purpose == "GENERATE" {
		agentExec(t, db, `UPDATE cap_ai.global_model_slots SET profile_id='personal',
			profile_revision=1,slot_revision=1 WHERE capability='LLM'`)
		op.LLMSlotRevision = 1
	} else {
		agentExec(t, db, `UPDATE cap_ai.global_model_slots SET profile_id='personal',
            profile_revision=1,slot_revision=1 WHERE capability='EMBEDDING'`)
	}
	return agentFixture{
		db: db, admin: admin, actor: &auth.Identity{UserID: personalActor, Role: "role_hr_reader"},
		provider: provider, proofs: proofs,
		gateway: Gateway{Policy: policy, Operations: &OperationStore{DB: policy.Authority.(DatabaseAuthority).DB}, Providers: map[string]Provider{"fake": provider}},
		op:      op,
	}
}

func agentReview(t *testing.T, admin *sql.DB, purpose, app string) string {
	t.Helper()
	raw := strings.Replace(personalReview(purpose), `"query_app_id":"library"`, `"query_app_id":"`+app+`"`, 1)
	var id string
	if err := admin.QueryRow(`SELECT cap_ai.approve_agent_processing($1,$2::jsonb)`, personalActor, raw).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func agentPayload() Payload {
	return Payload{Query: "Find matching policy and library references", Evidence: []Evidence{
		{ID: "hr", Text: "HR policy"}, {ID: "procurement", Text: "Procurement policy"}, {ID: "library", Text: "Private notes"},
	}}
}

func (f agentFixture) generate(t *testing.T, op Operation, proofs []AgentSourceProof) error {
	t.Helper()
	op.AgentSources = proofs
	payload := agentPayload()
	payload.Evidence = payload.Evidence[:len(proofs)]
	_, err := f.gateway.Call(context.Background(), f.actor, op, payload)
	return err
}

func TestAgentModelPolicyChecksEverySource(t *testing.T) {
	f := newAgentFixture(t, "GENERATE")
	agentReview(t, f.admin, "GENERATE", "agent_processing")
	libraryReview := agentReview(t, f.admin, "GENERATE", "library")
	if err := f.generate(t, f.op, f.proofs); err != nil || f.provider.calls != 1 {
		t.Fatalf("mixed sources should send once: calls=%d err=%v", f.provider.calls, err)
	}
	for _, tc := range []struct{ name, revoke, restore string }{
		{"HR membership", `DELETE FROM core.user_roles WHERE role_name='role_hr_reader'`, `INSERT INTO core.user_roles(user_id,role_name) VALUES('11111111-1111-4111-8111-111111111111','role_hr_reader')`},
		{"procurement membership", `DELETE FROM core.user_roles WHERE role_name='role_proc_reader'`, `INSERT INTO core.user_roles(user_id,role_name) VALUES('11111111-1111-4111-8111-111111111111','role_proc_reader')`},
		{"library membership", `DELETE FROM core.user_roles WHERE role_name='role_library_user' AND user_id='11111111-1111-4111-8111-111111111111'`, `INSERT INTO core.user_roles(user_id,role_name) VALUES('11111111-1111-4111-8111-111111111111','role_library_user')`},
		{"procurement egress", `UPDATE cap_ai.processing_policies SET enabled=false WHERE id='procurement'`, `UPDATE cap_ai.processing_policies SET enabled=true WHERE id='procurement'`},
		{"library capability", `DELETE FROM core.app_capabilities WHERE app_id='library' AND cap_id='cap-ai'`, `INSERT INTO core.app_capabilities(app_id,cap_id) VALUES('library','cap-ai')`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			agentExec(t, f.db, tc.revoke)
			t.Cleanup(func() { agentExec(t, f.db, tc.restore) })
			op := f.op
			op.Key = "revoked-" + tc.name
			if err := f.generate(t, op, f.proofs); !errors.Is(err, ErrPolicy) || f.provider.calls != 1 {
				t.Fatalf("revoked source sent: calls=%d err=%v", f.provider.calls, err)
			}
		})
	}
	agentExec(t, f.admin, `SELECT cap_ai.disable_agent_processing($1,$2,'review withdrawn')`, personalActor, libraryReview)
	op := f.op
	op.Key = "library-review-withdrawn"
	if err := f.generate(t, op, f.proofs); !errors.Is(err, ErrPolicy) || f.provider.calls != 1 {
		t.Fatalf("withdrawn PERSONAL review sent: calls=%d err=%v", f.provider.calls, err)
	}
}

func TestAgentModelPolicyRequiresReadyProcessingApp(t *testing.T) {
	f := newAgentFixture(t, "GENERATE")
	agentReview(t, f.admin, "GENERATE", "agent_processing")
	agentReview(t, f.admin, "GENERATE", "library")
	op := f.op
	op.AppID = "invented_consumer"
	if err := f.generate(t, op, f.proofs); !errors.Is(err, ErrPolicy) {
		t.Fatalf("unregistered consumer: %v", err)
	}
	agentExec(t, f.db, `UPDATE core.apps SET status='Removed' WHERE app_id='agent_processing'`)
	if err := f.generate(t, f.op, f.proofs); !errors.Is(err, ErrPolicy) || f.provider.calls != 0 {
		t.Fatalf("unready processing consumer sent: calls=%d err=%v", f.provider.calls, err)
	}
}

func TestAgentModelPolicyRejectsForgedRole(t *testing.T) {
	f := newAgentFixture(t, "GENERATE")
	agentReview(t, f.admin, "GENERATE", "agent_processing")
	agentReview(t, f.admin, "GENERATE", "library")
	for _, tc := range []struct {
		name  string
		alter func(*AgentSourceProof)
	}{
		{"other app's current role", func(p *AgentSourceProof) { p.ActingRole = "role_hr_reader" }},
		{"unheld role", func(p *AgentSourceProof) { p.ActingRole = "invented-role" }},
		{"mislabelled kind", func(p *AgentSourceProof) { p.ScopeKind = "PERSONAL" }},
		{"mislabelled app", func(p *AgentSourceProof) { p.AppID = "agent_processing" }},
		{"stale scope", func(p *AgentSourceProof) { p.ReadScope.PolicyRevision++ }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			proofs := append([]AgentSourceProof(nil), f.proofs...)
			tc.alter(&proofs[1])
			if err := f.generate(t, f.op, proofs); !errors.Is(err, ErrPolicy) || f.provider.calls != 0 {
				t.Fatalf("malformed proof sent: calls=%d err=%v", f.provider.calls, err)
			}
		})
	}
}

func TestAgentModelPolicyBindsActualSourceKind(t *testing.T) {
	f := newAgentFixture(t, "GENERATE")
	agentReview(t, f.admin, "GENERATE", "agent_processing")
	agentReview(t, f.admin, "GENERATE", "library")
	proofs := append([]AgentSourceProof(nil), f.proofs...)
	// The user's library role is eligible for both the APP and PERSONAL tuples.
	proofs[2].ScopeKind = "APP"
	if err := f.generate(t, f.op, proofs); !errors.Is(err, ErrPolicy) || f.provider.calls != 0 {
		t.Fatalf("relabeled owner-only source sent: calls=%d err=%v", f.provider.calls, err)
	}
}

func TestAgentProcessingChargesOnlyConfiguredApp(t *testing.T) {
	f := newAgentFixture(t, "GENERATE")
	agentReview(t, f.admin, "GENERATE", "agent_processing")
	agentReview(t, f.admin, "GENERATE", "library")
	if err := f.generate(t, f.op, f.proofs); err != nil {
		t.Fatal(err)
	}
	if err := f.generate(t, f.op, f.proofs); !errors.Is(err, ErrUncertain) || f.provider.calls != 1 {
		t.Fatalf("same operation replayed: calls=%d err=%v", f.provider.calls, err)
	}
	// The processing consumer, actor and provider-usage counters are recorded once.
	var operations int
	var app, actor, state string
	var input, output int64
	if err := f.db.QueryRow(`SELECT count(*),min(app_id),min(actor_id::text),min(state),min(input_tokens),min(output_tokens)
		FROM cap_ai.model_operations WHERE operation_key=$1`, f.op.Key).Scan(&operations, &app, &actor, &state, &input, &output); err != nil {
		t.Fatal(err)
	}
	if operations != 1 || app != "agent_processing" || actor != personalActor || state != "SUCCEEDED" || input != 3 || output != 2 {
		t.Fatalf("operation binding %d %q %q %q %d/%d", operations, app, actor, state, input, output)
	}
}

func TestAgentModelPolicyRequiresIndependentProcessingQueryReview(t *testing.T) {
	f := newAgentFixture(t, "GENERATE")
	agentReview(t, f.admin, "GENERATE", "hr")
	f.op.QueryClassification = "NON_PERSONAL"
	if err := f.generate(t, f.op, f.proofs[:2]); !errors.Is(err, ErrPolicy) || f.provider.calls != 0 {
		t.Fatalf("APP-only generation bypassed processing consumer query review: calls=%d err=%v", f.provider.calls, err)
	}
	review := agentReview(t, f.admin, "GENERATE", "agent_processing")
	if err := f.generate(t, f.op, f.proofs[:2]); err != nil || f.provider.calls != 1 {
		t.Fatalf("reviewed APP-only generation denied: calls=%d err=%v", f.provider.calls, err)
	}
	agentExec(t, f.admin, `SELECT cap_ai.disable_agent_processing($1,$2,'review withdrawn')`, personalActor, review)
	f.op.Key = "processing-review-withdrawn"
	if err := f.generate(t, f.op, f.proofs[:2]); !errors.Is(err, ErrPolicy) || f.provider.calls != 1 {
		t.Fatalf("withdrawn processing query review accepted: calls=%d err=%v", f.provider.calls, err)
	}
}

func TestAgentModelPolicyGlobalQueryEmbedRequiresPersonalReview(t *testing.T) {
	f := newAgentFixture(t, "EMBED")
	op := f.op
	op.AppID, op.ScopeKind = "hr", "APP"
	op.QueryClassification = "NON_PERSONAL"
	payload := Payload{Query: "Personal terms may occur in global questions"}
	if _, err := f.gateway.Call(context.Background(), f.actor, op, payload); err != nil {
		t.Fatalf("ordinary APP query changed: %v", err)
	}
	op.Key = "global-query"
	op.RequirePersonalQueryApproval = true
	op.EmbeddingSlotRevision = 1
	if _, err := f.gateway.Call(context.Background(), f.actor, op, payload); !errors.Is(err, ErrPolicy) || f.provider.calls != 1 {
		t.Fatalf("APP approval sent flagged query: calls=%d err=%v", f.provider.calls, err)
	}
	agentReview(t, f.admin, "EMBED", "procurement")
	if _, err := f.gateway.Call(context.Background(), f.actor, op, payload); !errors.Is(err, ErrPolicy) || f.provider.calls != 1 {
		t.Fatalf("other app's PERSONAL review sent flagged query: calls=%d err=%v", f.provider.calls, err)
	}
	id := agentReview(t, f.admin, "EMBED", "hr")
	if _, err := f.gateway.Call(context.Background(), f.actor, op, payload); err != nil || f.provider.calls != 2 {
		t.Fatalf("matching PERSONAL review denied: calls=%d err=%v", f.provider.calls, err)
	}
	op.Key = "flagged-text"
	if _, err := f.gateway.Call(context.Background(), f.actor, op, Payload{Texts: []string{"arbitrary source text"}}); !errors.Is(err, ErrPolicy) || f.provider.calls != 2 {
		t.Fatalf("query-only authority sent text: calls=%d err=%v", f.provider.calls, err)
	}
	agentExec(t, f.admin, `SELECT cap_ai.disable_agent_processing($1,$2,'review withdrawn')`, personalActor, id)
	op.Key = "global-query-withdrawn"
	if _, err := f.gateway.Call(context.Background(), f.actor, op, payload); !errors.Is(err, ErrPolicy) || f.provider.calls != 2 {
		t.Fatalf("disabled PERSONAL review sent flagged query: calls=%d err=%v", f.provider.calls, err)
	}
}

func TestAgentModelPolicyRejectsMalformedSQLProofs(t *testing.T) {
	f := newAgentFixture(t, "GENERATE")
	agentReview(t, f.admin, "GENERATE", "agent_processing")
	agentReview(t, f.admin, "GENERATE", "library")
	gateway := f.gateway.Policy.Authority.(DatabaseAuthority).DB
	valid, err := json.Marshal(f.proofs)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		proofs  any
		allowed bool
	}{
		{"valid", string(valid), true},
		{"SQL null", nil, false},
		{"JSON null", "null", false},
		{"object", "{}", false},
		{"empty", "[]", false},
		{"null entry", "[null]", false},
		{"array entry", "[[]]", false},
		{"missing UUID", strings.Replace(string(valid), "document_id", "other_id", 1), false},
		{"non-UUID", strings.Replace(string(valid), f.proofs[0].ReadScope.DocumentID, "invalid", 1), false},
		{"extra proof field", strings.Replace(string(valid), `{"scope_kind"`, `{"extra":null,"scope_kind"`, 1), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var allowed bool
			err := gateway.QueryRow(`SELECT coalesce((cap_ai.agent_model_policy($1,$2,$3,$4,$5,$6)->>'allowed')::boolean,false)`,
				f.actor.UserID, f.op.AppID, f.op.ProfileID, f.op.ProfileRevision, f.op.LLMSlotRevision, tc.proofs).Scan(&allowed)
			if err != nil || allowed != tc.allowed {
				t.Fatalf("allowed=%t, want %t, err=%v", allowed, tc.allowed, err)
			}
		})
	}
}

func TestAgentModelPolicyRequiresGroundedGeneration(t *testing.T) {
	f := newAgentFixture(t, "GENERATE")
	agentReview(t, f.admin, "GENERATE", "agent_processing")
	agentReview(t, f.admin, "GENERATE", "library")
	op := f.op
	op.AgentSources = f.proofs
	for _, tc := range []struct {
		name   string
		change func(*Operation, *Payload)
	}{
		{"missing evidence", func(_ *Operation, p *Payload) { p.Evidence = nil }},
		{"missing question", func(_ *Operation, p *Payload) { p.Query = "" }},
		{"missing LLM slot pin", func(o *Operation, _ *Payload) { o.LLMSlotRevision = 0 }},
		{"stale LLM slot pin", func(o *Operation, _ *Payload) { o.LLMSlotRevision++ }},
		{"legacy sources mixed in", func(o *Operation, _ *Payload) { o.Sources = []contracts.ReadScope{f.proofs[0].ReadScope} }},
		{"worker lease mixed in", func(o *Operation, _ *Payload) { o.Lease = &LeaseRef{} }},
		{"AGENT proofs on APP", func(o *Operation, _ *Payload) { o.ScopeKind, o.AppID = "APP", "hr" }},
		{"missing source proofs", func(o *Operation, _ *Payload) { o.AgentSources = nil }},
		{"too many source proofs", func(o *Operation, _ *Payload) { o.AgentSources = make([]AgentSourceProof, 51) }},
		{"cannot authorize embedding", func(o *Operation, _ *Payload) { o.Purpose = "EMBED" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			current, payload := op, agentPayload()
			tc.change(&current, &payload)
			_, err := f.gateway.Call(context.Background(), f.actor, current, payload)
			if !errors.Is(err, ErrPolicy) || f.provider.calls != 0 {
				t.Fatalf("unbounded or ungrounded call sent: calls=%d err=%v", f.provider.calls, err)
			}
		})
	}
	// Historical provenance can add source proofs beyond the visible evidence.
	payload := agentPayload()
	payload.Texts = []string{"Reauthorized prior context"}
	op.AgentSources = make([]AgentSourceProof, 50)
	for i := range op.AgentSources {
		op.AgentSources[i] = f.proofs[i%len(f.proofs)]
	}
	if _, err := f.gateway.Call(context.Background(), f.actor, op, payload); err != nil || f.provider.calls != 1 {
		t.Fatalf("bounded history and evidence denied: calls=%d err=%v", f.provider.calls, err)
	}
}

func TestAgentModelPolicyRechecksReviewAfterIntent(t *testing.T) {
	f := newAgentFixture(t, "GENERATE")
	agentReview(t, f.admin, "GENERATE", "library")
	revisedEvidence := strings.Replace(personalReview("GENERATE"), "review/no-training", "review/no-training-revision-2", 1)
	var changedReview string
	if err := f.admin.QueryRow(`SELECT cap_ai.approve_agent_processing($1,$2::jsonb)`, personalActor, revisedEvidence).Scan(&changedReview); err != nil {
		t.Fatal(err)
	}
	agentReview(t, f.admin, "GENERATE", "agent_processing")
	f.gateway.Policy.Authority = &disableBeforeSend{authority: f.gateway.Policy.Authority, admin: f.admin, approval: changedReview}
	if err := f.generate(t, f.op, f.proofs); !errors.Is(err, ErrPolicy) || f.provider.calls != 0 {
		t.Fatalf("changed authority fingerprint sent: calls=%d err=%v", f.provider.calls, err)
	}
	op := f.op
	op.AgentSources = f.proofs
	if _, err := f.gateway.Policy.Check(context.Background(), f.actor, op); err != nil {
		t.Fatalf("unchanged older matching review should still authorize a fresh operation: %v", err)
	}
	var state, code string
	var usage int
	if err := f.db.QueryRow(`SELECT state,coalesce(error_code,''),(input_tokens IS NOT NULL)::int
		FROM cap_ai.model_operations WHERE operation_key=$1`, f.op.Key).Scan(&state, &code, &usage); err != nil || state != "FAILED_UNSENT" || code != "AUTHORITY_CHANGED" || usage != 0 {
		t.Fatalf("unsent operation state=%q code=%q usage=%d err=%v", state, code, usage, err)
	}
}

func TestAgentModelPolicyRejectsOtherRuntimeSessions(t *testing.T) {
	f := newAgentFixture(t, "EMBED")
	agentReview(t, f.admin, "EMBED", "hr")
	for _, role := range []string{"dxos_ai_api", "dxos_ai_worker", "dxos_authz"} {
		t.Run(role, func(t *testing.T) {
			runtime := personalLogin(t, f.db, role)
			const query = `SELECT coalesce((cap_ai.agent_query_model_policy($1,'role_hr_reader','hr','APP','personal',1,1)->>'allowed')::boolean,false)`
			var allowed bool
			if err := runtime.QueryRow(query, personalActor).Scan(&allowed); err == nil {
				t.Fatal("unexpected default EXECUTE grant")
			}
			agentExec(t, f.db, `GRANT EXECUTE ON FUNCTION cap_ai.agent_query_model_policy(uuid,text,text,text,text,bigint,bigint) TO `+role)
			if err := runtime.QueryRow(query, personalActor).Scan(&allowed); err != nil || allowed {
				t.Fatalf("accidental grant authorized query: allowed=%t err=%v", allowed, err)
			}
		})
	}
}
