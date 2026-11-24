//go:build integration

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

// Deployment-bound model authority: the exact selected profile revision replaces
// the legacy slot as model authority, without weakening any source, access or
// processing guarantee. Every case uses the real connection roles; no privileged
// session stands in for the deployment, gateway, worker or administration login.

package models

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/lib/pq"
	"github.com/opendx-os/opendx-os/capabilities/internal/ai/contracts"
	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
	"github.com/opendx-os/opendx-os/capabilities/internal/testutil"
)

const deploymentOperator = "11111111-1111-4111-8111-111111111111"
const deploymentPlain = "12121212-1212-4212-8212-121212121212"
const deploymentInactive = "13131313-1313-4313-8313-131313131313"

// deploymentLogin is the restricted deployment/catalog login that owns live model
// authority. It is never a superuser and never has direct table access.
func deploymentLogin(t *testing.T, db *sql.DB) *sql.DB {
	t.Helper()
	var name string
	if err := db.QueryRow(`SELECT current_database()`).Scan(&name); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	c := testutil.As(t, u.String(), "dxos_ai_model_catalog")
	var safe bool
	if err := c.QueryRow(`SELECT NOT(rolsuper OR rolbypassrls OR rolcreaterole) AND session_user=current_user
		FROM pg_roles WHERE rolname=session_user`).Scan(&safe); err != nil || !safe {
		t.Fatal("unsafe deployment session", err)
	}
	return c
}

// applyModelAuthority applies the personal/agent/administration chain and the
// deployment migration on top of 0004/0005, in ledger order.
func applyModelAuthority(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, migration := range []string{
		"0008_personal_model_processing.sql", "0007_agent_scopes.sql", "0009_personal_hybrid.sql",
		"0010_agent_model_authority.sql",
		"0011_model_administration.sql", "0012_personal_admin_authority_locks.sql",
		"0013_deployment_models.sql",
	} {
		raw, err := os.ReadFile("../../../../platform/postgres/migrations/cap_ai/" + migration)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(string(raw)); err != nil {
			t.Fatalf("%s: %v", migration, err)
		}
	}
}

func readModelProfile(t *testing.T, db *sql.DB, id string, revision int64) Profile {
	t.Helper()
	var p Profile
	var dimension sql.NullInt64
	var credential sql.NullString
	var limits []byte
	if err := db.QueryRow(`SELECT id,revision,purpose,execution,provider_id,model_id,dimension,credential_ref,enabled,limits
		FROM cap_ai.model_profiles WHERE id=$1 AND revision=$2`, id, revision).
		Scan(&p.ID, &p.Revision, &p.Purpose, &p.Execution, &p.ProviderID, &p.ModelID, &dimension, &credential, &p.Enabled, &limits); err != nil {
		t.Fatal(err)
	}
	p.Dimension = int(dimension.Int64)
	p.CredentialRef = credential.String
	if err := json.Unmarshal(limits, &p.Limits); err != nil {
		t.Fatal(err)
	}
	return p
}

// applySelection records the given complete selection through the deployment
// login and the Go wrapper T6/T7 will use.
func applySelection(t *testing.T, db *sql.DB, selection DeploymentSelection) DeploymentSelectionReport {
	t.Helper()
	report, err := ReconcileDeployment(context.Background(), deploymentLogin(t, db), selection)
	if err != nil {
		t.Fatalf("deployment selection: %v", err)
	}
	return report
}

// currentSelection reads the live selection back into a declaration.
func currentSelection(t *testing.T, db *sql.DB) DeploymentSelection {
	t.Helper()
	status, err := ReadDeploymentStatus(context.Background(), deploymentLogin(t, db))
	if err != nil {
		t.Fatalf("deployment status: %v", err)
	}
	selection := DeploymentSelection{}
	for _, entry := range status.Profiles {
		if !entry.Selected || entry.ProfileID == "" {
			continue
		}
		profile := readModelProfile(t, db, entry.ProfileID, entry.Revision)
		switch entry.Purpose {
		case "EMBED":
			selection.Embedding = &profile
		case "GENERATE":
			selection.Generation = &profile
		}
	}
	return selection
}

// selectProfile records one immutable revision as the live selection for its
// purpose, keeping the other purpose exactly as it was.
func selectProfile(t *testing.T, db *sql.DB, id string, revision int64) DeploymentSelectionReport {
	t.Helper()
	selection := currentSelection(t, db)
	profile := readModelProfile(t, db, id, revision)
	switch profile.Purpose {
	case "EMBED":
		selection.Embedding = &profile
	case "GENERATE":
		selection.Generation = &profile
	default:
		t.Fatalf("unexpected purpose %q", profile.Purpose)
	}
	return applySelection(t, db, selection)
}

func deploymentEvents(t *testing.T, db *sql.DB) int {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM cap_ai.model_deployment_events`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func profileEnabled(t *testing.T, db *sql.DB, id string, revision int64) bool {
	t.Helper()
	var enabled bool
	if err := db.QueryRow(`SELECT enabled FROM cap_ai.model_profiles WHERE id=$1 AND revision=$2`, id, revision).Scan(&enabled); err != nil {
		t.Fatal(err)
	}
	return enabled
}

func countRows(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()
	var count int
	if err := db.QueryRow(query, args...).Scan(&count); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return count
}

func wantSQLState(t *testing.T, err error, code string) {
	t.Helper()
	var pg *pq.Error
	if !errors.As(err, &pg) {
		t.Fatalf("error %v is not a PostgreSQL error", err)
	}
	if string(pg.Code) != code {
		t.Fatalf("SQLSTATE %s, want %s (%v)", pg.Code, code, err)
	}
}

func reconcileRaw(t *testing.T, conn *sql.DB, payload string) error {
	t.Helper()
	var out []byte
	return conn.QueryRow(`SELECT cap_ai.reconcile_deployment_models($1::jsonb)`, payload).Scan(&out)
}

// TestDeploymentReconcile covers the reconciliation contract: an identical apply
// is idempotent, a changed identity withdraws the previous live revision while
// keeping its history, an invalid declaration is rejected atomically, and the
// deployment login can neither approve processing nor read documents while the
// retired slot administrator cannot touch live authority.
func TestDeploymentReconcile(t *testing.T) {
	a := Profile{ID: "embed-a", Revision: 1, Purpose: "EMBED", Execution: "LOCAL", ProviderID: "local",
		ModelID: "embed-model", Dimension: 4, Limits: Limits{MaxInputTokens: 1000, MaxOutputTokens: 10, MaxBytes: 1024, TimeoutMS: 1000}}
	b := Profile{ID: "embed-b", Revision: 1, Purpose: "EMBED", Execution: "LOCAL", ProviderID: "local",
		ModelID: "embed-model-2", Dimension: 8, Limits: Limits{MaxInputTokens: 1000, MaxOutputTokens: 10, MaxBytes: 1024, TimeoutMS: 1000}}
	generate := Profile{ID: "gen-a", Revision: 1, Purpose: "GENERATE", Execution: "LOCAL", ProviderID: "local",
		ModelID: "gen-model", Limits: Limits{MaxInputTokens: 1000, MaxOutputTokens: 100, MaxBytes: 1024, TimeoutMS: 1000}}
	db := modelDB(t, true)
	applyRegistry(t, db)
	applyModelAuthority(t, db)
	deployment := deploymentLogin(t, db)

	first := applySelection(t, db, DeploymentSelection{Embedding: &a, Generation: &generate})
	if !first.Changed || first.Created != 2 || len(first.Profiles) != 2 {
		t.Fatalf("first apply: %+v", first)
	}
	states := map[string]DeploymentPurposeState{}
	for _, p := range first.Profiles {
		states[p.Purpose] = p
	}
	if !states["EMBED"].Enabled || states["EMBED"].ProfileID != "embed-a" || !states["GENERATE"].Enabled {
		t.Fatalf("first apply did not select both purposes: %+v", first)
	}
	embedRevision, generateRevision := states["EMBED"].SelectionRevision, states["GENERATE"].SelectionRevision
	if events := deploymentEvents(t, db); events != 2 {
		t.Fatalf("first apply appended %d events, want 2", events)
	}

	// Identical apply: no write, no event, identical selection revisions.
	second := applySelection(t, db, DeploymentSelection{Embedding: &a, Generation: &generate})
	if second.Changed || second.Created != 0 {
		t.Fatalf("identical apply was not idempotent: %+v", second)
	}
	if events := deploymentEvents(t, db); events != 2 {
		t.Fatal("identical apply appended an audit event")
	}
	for _, p := range second.Profiles {
		if p.Changed {
			t.Fatalf("identical apply reported a change: %+v", p)
		}
		if (p.Purpose == "EMBED" && p.SelectionRevision != embedRevision) ||
			(p.Purpose == "GENERATE" && p.SelectionRevision != generateRevision) {
			t.Fatalf("identical apply moved a selection revision: %+v", second)
		}
	}

	// A changed embedding identity withdraws the previous live revision, keeps its
	// immutable history and leaves the generation selection untouched.
	changed := applySelection(t, db, DeploymentSelection{Embedding: &b, Generation: &generate})
	if !changed.Changed || len(changed.Withdrawn) != 1 || changed.Withdrawn[0] != "embed-a@1" {
		t.Fatalf("embedding change: %+v", changed)
	}
	if profileEnabled(t, db, "embed-a", 1) || !profileEnabled(t, db, "embed-b", 1) {
		t.Fatal("embedding change did not move the live revision")
	}
	if countRows(t, db, `SELECT count(*) FROM cap_ai.model_profiles WHERE id='embed-a' AND revision=1`) != 1 {
		t.Fatal("the withdrawn revision lost its immutable history")
	}
	if countRows(t, db, `SELECT count(*) FROM cap_ai.model_deployment_events WHERE action='SELECT' AND profile_id='embed-a'`) != 1 {
		t.Fatal("the withdrawn selection lost its audit event")
	}
	for _, p := range changed.Profiles {
		if p.Purpose == "GENERATE" && (p.Changed || p.SelectionRevision != generateRevision) {
			t.Fatalf("a changed embedding disturbed generation: %+v", changed)
		}
	}
	afterChange := deploymentEvents(t, db)

	// Invalid and duplicate declarations are rejected atomically.
	limits := `"limits":{"max_input_tokens":10,"max_output_tokens":10,"max_bytes":10,"timeout_ms":10}`
	for _, test := range []struct{ name, payload string }{
		{"duplicate purpose", `{"profiles":[
		 {"id":"embed-c","revision":1,"purpose":"EMBED","execution":"LOCAL","provider_id":"local","model_id":"m","dimension":4,` + limits + `},
		 {"id":"gen-c","revision":1,"purpose":"EMBED","execution":"LOCAL","provider_id":"local","model_id":"m","dimension":4,` + limits + `}]}`},
		{"unselected purpose", `{"profiles":[{"id":"rank-c","revision":1,"purpose":"RERANK","execution":"LOCAL","provider_id":"local","model_id":"m",` + limits + `}]}`},
		{"embedding without dimension", `{"profiles":[{"id":"embed-c","revision":1,"purpose":"EMBED","execution":"LOCAL","provider_id":"local","model_id":"m",` + limits + `}]}`},
		{"generation with dimension", `{"profiles":[{"id":"gen-c","revision":1,"purpose":"GENERATE","execution":"LOCAL","provider_id":"local","model_id":"m","dimension":3,` + limits + `}]}`},
		{"limit above the gateway ceiling", `{"profiles":[{"id":"gen-c","revision":1,"purpose":"GENERATE","execution":"LOCAL","provider_id":"local","model_id":"m","limits":{"max_input_tokens":2000000,"max_output_tokens":10,"max_bytes":10,"timeout_ms":10}}]}`},
		{"unknown declaration key", `{"profiles":[{"id":"gen-c","revision":1,"purpose":"GENERATE","execution":"LOCAL","provider_id":"local","model_id":"m",` + limits + `}],"extra":true}`},
		{"unknown profile key", `{"profiles":[{"id":"gen-c","revision":1,"purpose":"GENERATE","execution":"LOCAL","provider_id":"local","model_id":"m","enabled":true,` + limits + `}]}`},
		{"unknown execution", `{"profiles":[{"id":"gen-c","revision":1,"purpose":"GENERATE","execution":"REMOTE","provider_id":"local","model_id":"m",` + limits + `}]}`},
		{"three declarations", `{"profiles":[
		 {"id":"gen-c","revision":1,"purpose":"GENERATE","execution":"LOCAL","provider_id":"local","model_id":"m",` + limits + `},
		 {"id":"gen-d","revision":1,"purpose":"GENERATE","execution":"LOCAL","provider_id":"local","model_id":"m",` + limits + `},
		 {"id":"gen-e","revision":1,"purpose":"GENERATE","execution":"LOCAL","provider_id":"local","model_id":"m",` + limits + `}]}`},
		{"array payload", `[]`},
	} {
		t.Run(test.name, func(t *testing.T) {
			wantSQLState(t, reconcileRaw(t, deployment, test.payload), "22023")
		})
	}
	if !profileEnabled(t, db, "embed-b", 1) || !profileEnabled(t, db, "gen-a", 1) {
		t.Fatal("a rejected declaration withdrew the live selection")
	}
	if countRows(t, db, `SELECT count(*) FROM cap_ai.model_profiles WHERE id IN('embed-c','gen-c','rank-c','gen-d','gen-e')`) != 0 {
		t.Fatal("a rejected declaration created a profile")
	}
	if deploymentEvents(t, db) != afterChange {
		t.Fatal("a rejected declaration appended an audit event")
	}

	// A field paired with the wrong purpose never reaches the database: the payload
	// carries the purpose, so it would otherwise select the other purpose.
	for _, mismatched := range []DeploymentSelection{
		{Embedding: &generate},
		{Generation: &a},
	} {
		if _, err := ReconcileDeployment(context.Background(), deployment, mismatched); !errors.Is(err, ErrPolicy) {
			t.Fatalf("a mismatched selection field = %v, want ErrPolicy", err)
		}
	}
	if !profileEnabled(t, db, "embed-b", 1) || !profileEnabled(t, db, "gen-a", 1) || deploymentEvents(t, db) != afterChange {
		t.Fatal("a mismatched selection field changed live authority")
	}

	// An immutable revision conflict rolls the whole batch back.
	conflict := `{"profiles":[
	 {"id":"embed-d","revision":1,"purpose":"EMBED","execution":"LOCAL","provider_id":"local","model_id":"m","dimension":4,` + limits + `},
	 {"id":"gen-a","revision":1,"purpose":"GENERATE","execution":"LOCAL","provider_id":"local","model_id":"changed",` + limits + `}]}`
	wantSQLState(t, reconcileRaw(t, deployment, conflict), "23505")
	if countRows(t, db, `SELECT count(*) FROM cap_ai.model_profiles WHERE id='embed-d'`) != 0 {
		t.Fatal("a rejected batch left a partial profile")
	}

	// An empty declaration is the installation switch: every purpose is withdrawn
	// and an audit event records it, without deleting any history.
	empty := applySelection(t, db, DeploymentSelection{})
	if empty.Changed == false || len(empty.Withdrawn) != 2 {
		t.Fatalf("empty declaration: %+v", empty)
	}
	if profileEnabled(t, db, "embed-b", 1) || profileEnabled(t, db, "gen-a", 1) {
		t.Fatal("the disabled installation still serves a profile")
	}
	if countRows(t, db, `SELECT count(*) FROM cap_ai.model_deployment_events WHERE action='SUSPEND'`) != 2 {
		t.Fatal("the disabled installation did not record both withdrawals")
	}

	// The deployment login cannot approve or withdraw processing, and holds no
	// direct relation right on any protected table.
	for _, statement := range []string{
		`SELECT cap_ai.approve_agent_processing($1,'{}'::jsonb)`,
		`SELECT cap_ai.disable_agent_processing($1,gen_random_uuid(),'x')`,
	} {
		var out any
		wantSQLState(t, deployment.QueryRow(statement, deploymentOperator).Scan(&out), "42501")
	}
	for _, relation := range []string{
		"cap_ai.knowledge_documents", "cap_ai.model_operations", "cap_ai.model_profiles",
		"cap_ai.model_deployment_events", "cap_ai.agent_processing_approvals", "cap_ai.agent_processing_approval_events",
	} {
		var readable bool
		if err := deployment.QueryRow(`SELECT has_table_privilege($1,$2,'SELECT')`,
			"dxos_ai_model_catalog", relation).Scan(&readable); err != nil {
			t.Fatal(err)
		}
		if readable {
			t.Fatalf("the deployment login can read %s", relation)
		}
	}

	// The retired slot administrator cannot change live model authority.
	admin := testutil.As(t, databaseURL(t, db), "dxos_ai_model_admin")
	events := deploymentEvents(t, db)
	var out any
	wantSQLState(t, admin.QueryRow(`SELECT cap_ai.reconcile_deployment_models('{"profiles":[]}'::jsonb)`).Scan(&out), "42501")
	wantSQLState(t, admin.QueryRow(`SELECT cap_ai.suspend_model_calls($1)`, deploymentOperator).Scan(&out), "42501")
	for _, statement := range []string{
		`UPDATE cap_ai.model_profiles SET enabled=true`,
		`UPDATE cap_ai.model_deployment_events SET action='SUSPEND'`,
		`INSERT INTO cap_ai.model_deployment_events(purpose,action,session_identity) VALUES('EMBED','SUSPEND','x')`,
	} {
		if _, err := admin.Exec(statement); err == nil {
			t.Fatalf("retired slot administrator reached live authority: %s", statement)
		}
	}
	if countRows(t, db, `SELECT count(*) FROM cap_ai.model_profiles WHERE enabled`) != 0 || deploymentEvents(t, db) != events {
		t.Fatal("a denied administration attempt changed live authority")
	}

	// The deployment status is readable by the administration login too and is
	// sanitized: identity and limits only, never a credential reference.
	var raw []byte
	if err := admin.QueryRow(`SELECT cap_ai.model_deployment_status()`).Scan(&raw); err != nil {
		t.Fatalf("administration status read: %v", err)
	}
	for _, secret := range []string{"credential_ref", "LOCAL_KEY", "endpoint", "api_base"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("status leaked %q: %s", secret, raw)
		}
	}
}

// TestDeploymentProcessingPolicy keeps the three effective processing modes on
// the existing source and approval authority, now anchored to the exact deployed
// profile revision.
func TestDeploymentProcessingPolicy(t *testing.T) {
	ctx := context.Background()

	t.Run("unknown or disabled mode denies", func(t *testing.T) {
		db, admin, policy, op := personalFixture(t, "GENERATE")
		approvePersonal(t, admin, "GENERATE")
		actor := &auth.Identity{UserID: personalActor, Role: "role_library_user"}
		if _, err := policy.Check(ctx, actor, op); err != nil {
			t.Fatalf("selected deployment denied an approved call: %v", err)
		}
		applySelection(t, db, DeploymentSelection{})
		if _, err := policy.Check(ctx, actor, op); !errors.Is(err, ErrPolicy) {
			t.Fatalf("a withdrawn installation authorized a model call: %v", err)
		}
		selectProfile(t, db, "personal", 1)
		if _, err := policy.Check(ctx, actor, op); err != nil {
			t.Fatalf("re-selected deployment denied an approved call: %v", err)
		}
	})

	t.Run("local-only source denies hosted even with an approval", func(t *testing.T) {
		f := newAgentFixture(t, "GENERATE")
		agentReview(t, f.admin, "GENERATE", "agent_processing")
		agentReview(t, f.admin, "GENERATE", "library")
		gateway := f.gateway.Policy.Authority.(DatabaseAuthority).DB
		api := personalLogin(t, f.db, "dxos_ai_api")
		readScope := func() contracts.ReadScope {
			var value []byte
			if err := api.QueryRow(`SELECT cap_ai.read_document($1,'role_hr_reader',$2)`,
				personalActor, f.proofs[0].ReadScope.DocumentID).Scan(&value); err != nil {
				t.Fatal(err)
			}
			var scope contracts.ReadScope
			if err := json.Unmarshal(value, &scope); err != nil {
				t.Fatal(err)
			}
			return scope
		}
		allowed := func() bool {
			proofs, err := json.Marshal([]contracts.ReadScope{readScope()})
			if err != nil {
				t.Fatal(err)
			}
			var ok bool
			if err := gateway.QueryRow(`SELECT coalesce((cap_ai.model_policy($1,'role_hr_reader','hr','APP','personal',1,$2)->>'allowed')::boolean,false)`,
				personalActor, string(proofs)).Scan(&ok); err != nil {
				t.Fatal(err)
			}
			return ok
		}
		if !allowed() {
			t.Fatal("the reviewed hosted source was denied unexpectedly")
		}
		// The parser-local processing policy is not hosted consent, whatever the
		// deployment is: the source policy revision is what changes here.
		agentExec(t, f.db, `INSERT INTO cap_ai.processing_policies(id,version,execution,classification,enabled,approved_by,approved_at,query_app_id,provider_id,retention_approved)
			VALUES('hr-local',1,'LOCAL_ONLY','NON_PERSONAL',true,$1,now(),'hr','fake',true)`, personalActor)
		agentExec(t, f.db, `INSERT INTO cap_ai.processing_policy_profiles(policy_id,policy_version,model_profile_id,profile_revision)
			VALUES('hr-local',1,'personal',1)`)
		agentExec(t, f.db, `UPDATE cap_ai.knowledge_documents d SET processing_policy_id='hr-local',processing_policy_version=1
			FROM cap_ai.knowledge_collections c WHERE c.id=d.collection_id AND c.app_id='hr'`)
		if allowed() {
			t.Fatal("a LOCAL_ONLY source authorized a hosted model call")
		}
		agentExec(t, f.db, `UPDATE cap_ai.knowledge_documents d SET processing_policy_id='hr',processing_policy_version=1
			FROM cap_ai.knowledge_collections c WHERE c.id=d.collection_id AND c.app_id='hr'`)
		if !allowed() {
			t.Fatal("restoring the reviewed source policy did not restore authority")
		}
	})

	t.Run("hosted requires the exact current approval", func(t *testing.T) {
		db, admin, policy, op := personalFixture(t, "GENERATE")
		actor := &auth.Identity{UserID: personalActor, Role: "role_library_user"}
		old := approvePersonal(t, admin, "GENERATE")
		if _, err := policy.Check(ctx, actor, op); err != nil {
			t.Fatal(err)
		}
		// A new reviewed source policy revision: the old approval is still present
		// and enabled, and must not authorize the new source snapshot.
		agentExec(t, db, `INSERT INTO cap_ai.processing_policies(id,version,execution,classification,enabled,approved_by,approved_at,query_app_id,provider_id,retention_approved)
			VALUES('local-only',2,'LOCAL_ONLY','PERSONAL',true,$1,now(),'library','fake',true)`, personalActor)
		agentExec(t, db, `INSERT INTO cap_ai.processing_policy_profiles(policy_id,policy_version,model_profile_id,profile_revision)
			VALUES('local-only',2,'personal',1)`)
		agentExec(t, db, `UPDATE cap_ai.knowledge_documents SET processing_policy_version=2 WHERE id=$1`, personalDoc)
		var approval sql.NullInt64
		if err := db.QueryRow(`SELECT cap_ai.personal_processing_approval('personal',1,'library','local-only',1)`).Scan(&approval); err != nil {
			t.Fatal(err)
		}
		if !approval.Valid {
			t.Fatal("the fixture lost its historical approval")
		}
		var value []byte
		if err := db.QueryRow(`SELECT cap_ai.read_document($1,'role_library_user',$2)`, personalActor, personalDoc).Scan(&value); err != nil {
			t.Fatal(err)
		}
		var scope contracts.ReadScope
		if err := json.Unmarshal(value, &scope); err != nil {
			t.Fatal(err)
		}
		revised := op
		revised.Sources = []contracts.ReadScope{scope}
		if _, err := policy.Check(ctx, actor, revised); !errors.Is(err, ErrPolicy) {
			t.Fatalf("a stale approval authorized the new source revision: %v", err)
		}
		review := personalReview("GENERATE")
		review = strings.Replace(review, `"source_policy_version":1`, `"source_policy_version":2`, 1)
		var approved string
		if err := admin.QueryRow(`SELECT cap_ai.approve_agent_processing($1,$2::jsonb)`, personalActor, review).Scan(&approved); err != nil {
			t.Fatalf("re-approval: %v", err)
		}
		if approved == old {
			t.Fatal("the re-approval reused the previous review")
		}
		if _, err := policy.Check(ctx, actor, revised); err != nil {
			t.Fatalf("the exact current approval was refused: %v", err)
		}
	})

	t.Run("query-only global embedding needs its own personal review", func(t *testing.T) {
		f := newAgentFixture(t, "EMBED")
		op := f.op
		op.AppID, op.ScopeKind, op.QueryClassification = "hr", "APP", "NON_PERSONAL"
		payload := Payload{Query: "A global question can carry personal terms"}
		if _, err := f.gateway.Call(ctx, f.actor, op, payload); err != nil {
			t.Fatalf("ordinary APP query changed: %v", err)
		}
		op.Key = "deployment-global-query"
		op.RequirePersonalQueryApproval = true
		if _, err := f.gateway.Call(ctx, f.actor, op, payload); !errors.Is(err, ErrPolicy) || f.provider.calls != 1 {
			t.Fatalf("flagged query without a personal review was sent: calls=%d err=%v", f.provider.calls, err)
		}
		agentReview(t, f.admin, "EMBED", "hr")
		if _, err := f.gateway.Call(ctx, f.actor, op, payload); err != nil || f.provider.calls != 2 {
			t.Fatalf("reviewed flagged query denied: calls=%d err=%v", f.provider.calls, err)
		}
		applySelection(t, f.db, DeploymentSelection{})
		op.Key = "deployment-global-query-suspended"
		if _, err := f.gateway.Call(ctx, f.actor, op, payload); !errors.Is(err, ErrPolicy) || f.provider.calls != 2 {
			t.Fatalf("a suspended installation sent a flagged query: calls=%d err=%v", f.provider.calls, err)
		}
	})

	t.Run("AGENT generation needs the per-source and consumer reviews", func(t *testing.T) {
		f := newAgentFixture(t, "GENERATE")
		agentReview(t, f.admin, "GENERATE", "hr")
		if err := f.generate(t, f.op, f.proofs[:2]); !errors.Is(err, ErrPolicy) || f.provider.calls != 0 {
			t.Fatalf("APP-only generation bypassed its consumer review: calls=%d err=%v", f.provider.calls, err)
		}
		agentReview(t, f.admin, "GENERATE", "agent_processing")
		agentReview(t, f.admin, "GENERATE", "library")
		if err := f.generate(t, f.op, f.proofs); err != nil || f.provider.calls != 1 {
			t.Fatalf("fully reviewed generation denied: calls=%d err=%v", f.provider.calls, err)
		}
		// A different profile revision is not the deployed authority.
		stale := f.op
		stale.Key = "deployment-stale-revision"
		stale.ProfileRevision = 2
		if err := f.generate(t, stale, f.proofs); !errors.Is(err, ErrPolicy) || f.provider.calls != 1 {
			t.Fatalf("an undeployed revision was authorized: calls=%d err=%v", f.provider.calls, err)
		}
	})

	t.Run("PERSONAL stays owner-only", func(t *testing.T) {
		_, admin, policy, op := personalFixture(t, "GENERATE")
		approvePersonal(t, admin, "GENERATE")
		if err := checkPersonal(policy, op, personalActor); err != nil {
			t.Fatal(err)
		}
		if err := checkPersonal(policy, op, personalOther); !errors.Is(err, ErrPolicy) {
			t.Fatalf("a non-owner was authorized: %v", err)
		}
	})

	t.Run("profile or revision mismatch denies", func(t *testing.T) {
		db, admin, policy, op := personalFixture(t, "GENERATE")
		approvePersonal(t, admin, "GENERATE")
		agentExec(t, db, `INSERT INTO cap_ai.model_profiles(id,revision,purpose,execution,provider_id,model_id,limits,enabled)
			VALUES('personal',2,'GENERATE','HOSTED','fake','fake','{"max_input_tokens":4096,"max_output_tokens":20,"max_bytes":8192,"timeout_ms":1000}',false)`)
		actor := &auth.Identity{UserID: personalActor, Role: "role_library_user"}
		stale := op
		stale.ProfileRevision = 2
		if _, err := policy.Check(ctx, actor, stale); !errors.Is(err, ErrPolicy) {
			t.Fatalf("an undeployed revision was authorized: %v", err)
		}
		other := op
		other.ProfileID = "invented"
		if _, err := policy.Check(ctx, actor, other); !errors.Is(err, ErrPolicy) {
			t.Fatalf("an invented profile was authorized: %v", err)
		}
		if _, err := policy.Check(ctx, actor, op); err != nil {
			t.Fatalf("the deployed revision was denied: %v", err)
		}
	})
}

// TestDeploymentSuspend covers the atomic emergency withdrawal: one authorized
// call withdraws every purpose, the audit row names the actor, and no other
// connection role may suspend.
func TestDeploymentSuspend(t *testing.T) {
	ctx := context.Background()
	db, admin, policy, generateOp := personalFixture(t, "GENERATE")
	agentExec(t, db, `INSERT INTO cap_ai.model_profiles(id,revision,purpose,execution,provider_id,model_id,dimension,limits,enabled)
		VALUES('embed-live',1,'EMBED','HOSTED','fake','fake',3,'{"max_input_tokens":4096,"max_output_tokens":20,"max_bytes":8192,"timeout_ms":1000}',false)`)
	embed := readModelProfile(t, db, "embed-live", 1)
	selection := currentSelection(t, db)
	applySelection(t, db, DeploymentSelection{Embedding: &embed, Generation: selection.Generation})
	if !profileEnabled(t, db, "embed-live", 1) || !profileEnabled(t, db, "personal", 1) {
		t.Fatal("both purposes must be live before the suspension")
	}
	approvePersonal(t, admin, "GENERATE")
	actor := &auth.Identity{UserID: personalActor, Role: "role_library_user"}
	if _, err := policy.Check(ctx, actor, generateOp); err != nil {
		t.Fatalf("approved call denied before the suspension: %v", err)
	}

	// Only the deployment login reaches the interface at all.
	deployment := deploymentLogin(t, db)
	for _, role := range []string{"dxos_ai_gateway", "dxos_ai_worker", "dxos_ai_api", "dxos_authz", "dxos_ai_model_admin"} {
		runtime := personalLogin(t, db, role)
		var out any
		wantSQLState(t, runtime.QueryRow(`SELECT cap_ai.suspend_model_calls($1)`, deploymentOperator).Scan(&out), "42501")
	}

	// Unauthenticated or non-administrator actors are refused by the deployment
	// session at the database, with no state change and no audit event.
	events := deploymentEvents(t, db)
	agentExec(t, db, `INSERT INTO core.users(id,email,display_name) VALUES($1,'plain@test.invalid','Plain')`, deploymentPlain)
	agentExec(t, db, `INSERT INTO core.users(id,email,display_name,status) VALUES($1,'inactive@test.invalid','Inactive','DISABLED')`, deploymentInactive)
	for _, test := range []struct{ name, actor, want string }{
		{"plain user", deploymentPlain, "42501"},
		{"inactive operator", deploymentInactive, "42501"},
		{"unknown actor", "99999999-9999-4999-8999-999999999999", "42501"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var out any
			wantSQLState(t, deployment.QueryRow(`SELECT cap_ai.suspend_model_calls($1)`, test.actor).Scan(&out), test.want)
		})
	}
	if _, err := SuspendModelCalls(ctx, deployment, ""); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("an empty actor was accepted: %v", err)
	}
	if deploymentEvents(t, db) != events || !profileEnabled(t, db, "embed-live", 1) {
		t.Fatal("a refused suspension changed live authority")
	}
	// The gateway connection role can no longer call the slot-based policy pair.
	gateway := personalLogin(t, db, "dxos_ai_gateway")
	var ignored []byte
	if err := gateway.QueryRow(`SELECT cap_ai.agent_model_policy($1,$2,$3,$4,$5,$6)`,
		personalActor, "agent_processing", "personal", 1, 1, "[]").Scan(&ignored); err == nil {
		t.Fatal("the retired slot-based agent policy is still reachable")
	}
	if err := gateway.QueryRow(`SELECT cap_ai.agent_query_model_policy($1,$2,$3,$4,$5,$6,$7)`,
		personalActor, "role_hr_reader", "hr", "APP", "personal", 1, 1).Scan(&ignored); err == nil {
		t.Fatal("the retired slot-based query policy is still reachable")
	}

	// An authorized suspension withdraws both purposes in one transaction.
	suspension, err := SuspendModelCalls(ctx, deployment, deploymentOperator)
	if err != nil {
		t.Fatalf("authorized suspension: %v", err)
	}
	if suspension.Suspended != 2 || len(suspension.Purposes) != 2 || suspension.ActorID != deploymentOperator || suspension.OccurredAt == "" {
		t.Fatalf("suspension report: %+v", suspension)
	}
	if profileEnabled(t, db, "embed-live", 1) || profileEnabled(t, db, "personal", 1) {
		t.Fatal("the suspension left a live profile")
	}
	if countRows(t, db, `SELECT count(*) FROM cap_ai.model_deployment_events WHERE action='SUSPEND' AND actor_id=$1`, deploymentOperator) != 2 {
		t.Fatal("the suspension did not record one audited event per purpose")
	}
	status, err := ReadDeploymentStatus(ctx, deployment)
	if err != nil || status.Enabled || status.LiveProfiles != 0 {
		t.Fatalf("status after suspension: %+v %v", status, err)
	}
	// The next Gateway check is denied.
	if _, err := policy.Check(ctx, actor, generateOp); !errors.Is(err, ErrPolicy) {
		t.Fatalf("a suspended installation authorized a model call: %v", err)
	}
	// The job queue stops too: no authority, no queued embedding work.
	var queued bool
	if err := db.QueryRow(`SELECT cap_ai.queue_embeddings($1,'role_library_user','personal',1)`, personalActor).Scan(&queued); err != nil {
		t.Fatal(err)
	}
	if countRows(t, db, `SELECT count(*) FROM cap_ai.ingestion_jobs WHERE kind='EMBED'`) != 0 {
		t.Fatal("a suspended installation queued embedding work")
	}
}

// TestEmbeddingClaimDeploymentProfile covers the worker claim interface: only the
// current deployment profile may be claimed, a job queued for a superseded
// profile is neither dispatched nor able to block the current one, and a wrong
// fence is rejected.
func TestEmbeddingClaimDeploymentProfile(t *testing.T) {
	db, admin, _, _ := personalFixture(t, "EMBED")
	approvePersonal(t, admin, "EMBED")
	// The parsed run must carry chunks for an embedding generation to be built.
	agentExec(t, db, `SET session_replication_role=replica;
INSERT INTO cap_ai.knowledge_chunks(run_id,ordinal,text,section_path,content_hash,token_count)
	VALUES('88888888-8888-4888-8888-888888888888',0,'Private policy text','[]',repeat('a',64),3);
SET session_replication_role=origin`)
	// A second, newer embedding revision becomes the live deployment profile. It
	// needs its own exact processed-source approval before it can queue PERSONAL
	// embedding work.
	agentExec(t, db, `INSERT INTO cap_ai.model_profiles(id,revision,purpose,execution,provider_id,model_id,dimension,limits,enabled)
		VALUES('personal-next',1,'EMBED','HOSTED','fake','fake',3,'{"max_input_tokens":4096,"max_output_tokens":20,"max_bytes":8192,"timeout_ms":1000}',false)`)
	var nextApproval string
	if err := admin.QueryRow(`SELECT cap_ai.approve_agent_processing($1,$2::jsonb)`, personalActor,
		strings.Replace(personalReview("EMBED"), `"profile_id":"personal"`, `"profile_id":"personal-next"`, 1)).Scan(&nextApproval); err != nil {
		t.Fatalf("approval for the newer embedding revision: %v", err)
	}
	var queued bool
	if err := db.QueryRow(`SELECT cap_ai.queue_embeddings($1,'role_library_user','personal',1)`, personalActor).Scan(&queued); err != nil || !queued {
		t.Fatalf("queue for the old profile: %v", err)
	}
	oldKey := "personal@1"
	if countRows(t, db, `SELECT count(*) FROM cap_ai.ingestion_jobs WHERE kind='EMBED' AND profile_key LIKE $1`, oldKey+"%") != 1 {
		t.Fatal("the old-profile job was not queued")
	}
	selectProfile(t, db, "personal-next", 1)
	if !profileEnabled(t, db, "personal-next", 1) || profileEnabled(t, db, "personal", 1) {
		t.Fatal("the newer embedding revision is not the live selection")
	}

	worker := personalLogin(t, db, "dxos_ai_worker")
	claim := func(profile string, revision int64) sql.NullString {
		var lease sql.NullString
		if err := worker.QueryRow(`SELECT cap_ai.claim_embedding($1,$2,$3)`, personalActor, profile, revision).Scan(&lease); err != nil {
			t.Fatalf("claim %s@%d: %v", profile, revision, err)
		}
		return lease
	}
	// Neither the superseded profile nor the wrong revision can claim.
	if lease := claim("personal", 1); lease.Valid {
		t.Fatalf("a superseded profile claimed work: %s", lease.String)
	}
	if lease := claim("personal-next", 2); lease.Valid {
		t.Fatal("an invented revision claimed work")
	}
	if lease := claim("unknown", 1); lease.Valid {
		t.Fatal("an unknown profile claimed work")
	}
	// The old pending job must not consume the worker's lease budget.
	if countRows(t, db, `SELECT count(*) FROM cap_ai.ingestion_jobs WHERE kind='EMBED' AND profile_key LIKE $1`, oldKey+"%") != 1 {
		t.Fatal("the old job was dispatched or deleted")
	}

	// Queue and claim for the live profile.
	if err := db.QueryRow(`SELECT cap_ai.queue_embeddings($1,'role_library_user','personal-next',1)`, personalActor).Scan(&queued); err != nil || !queued {
		t.Fatalf("queue for the live profile: %v", err)
	}
	if countRows(t, db, `SELECT count(*) FROM cap_ai.ingestion_jobs WHERE kind='EMBED' AND profile_key LIKE 'personal-next@1%'`) != 1 {
		t.Fatal("the live-profile job was not queued")
	}
	lease := claim("personal-next", 1)
	if !lease.Valid || lease.String == "" {
		t.Fatal("the live profile did not claim its own job")
	}
	var claimed struct {
		Job   string `json:"job_id"`
		Run   string `json:"run_id"`
		Fence int64  `json:"fence"`
	}
	if err := json.Unmarshal([]byte(lease.String), &claimed); err != nil {
		t.Fatal(err)
	}
	// A second claim cannot lease the same job.
	if again := claim("personal-next", 1); again.Valid {
		t.Fatal("the same job was leased twice")
	}

	// The leased work is authorized under the real fence.
	var raw []byte
	if err := worker.QueryRow(`SELECT cap_ai.embedding_work($1,$2,$3,$4,'personal-next',1)`,
		claimed.Job, personalActor, claimed.Fence, claimed.Run).Scan(&raw); err != nil {
		t.Fatalf("authorized embedding work: %v", err)
	}
	var work struct {
		GenerationID string `json:"generation_id"`
	}
	if err := json.Unmarshal(raw, &work); err != nil || work.GenerationID == "" {
		t.Fatalf("authorized embedding work: %s %v", raw, err)
	}
	// A wrong fence is rejected by every worker interface.
	for _, statement := range []string{
		`SELECT cap_ai.embedding_work($1,$2,$3,$4,'personal-next',1)`,
		`SELECT cap_ai.save_embedding($1,$2,$3,$4,$5,gen_random_uuid(),repeat('a',64),'[1,0,0]'::public.vector)`,
		`SELECT cap_ai.finish_embedding($1,$2,$3,$4,$5,true)`,
	} {
		var out any
		args := []any{claimed.Job, personalActor, claimed.Fence + 1, claimed.Run}
		if strings.Contains(statement, "$5") {
			args = append(args, work.GenerationID)
		}
		wantSQLState(t, worker.QueryRow(statement, args...).Scan(&out), "P0410")
	}
	// The superseded, unbound claim interface is closed to the worker.
	var ignored []byte
	wantSQLState(t, worker.QueryRow(`SELECT cap_ai.claim_embedding($1)`, personalActor).Scan(&ignored), "42501")
	// The gateway and the API never reach the worker claim either.
	for _, role := range []string{"dxos_ai_gateway", "dxos_ai_api"} {
		runtime := personalLogin(t, db, role)
		wantSQLState(t, runtime.QueryRow(`SELECT cap_ai.claim_embedding($1,$2,$3)`, personalActor, "personal-next", 1).Scan(&ignored), "42501")
	}
}
