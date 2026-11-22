//go:build integration

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/opendx-os/opendx-os/capabilities/internal/knowledge"
	"github.com/opendx-os/opendx-os/capabilities/internal/knowledge/models"
	"github.com/opendx-os/opendx-os/capabilities/internal/testutil"
)

type agentTurnFixture struct {
	db, admin *sql.DB
	actor     Actor
	store     *PostgresConversationStore
	reader    *knowledge.Policy
	retrieval *RetrievalCoordinator
	gateway   *models.Gateway
	resolver  *models.Registry
}

func turnSQL(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}

func newAgentTurnFixture(t *testing.T, provider models.Provider) agentTurnFixture {
	t.Helper()
	db := agentDB(t)
	for _, filename := range []string{
		"0004_models_vectors.sql", "0005_model_registry.sql", "0007_agent_scopes.sql",
		"0008_personal_model_processing.sql", "0009_personal_hybrid.sql", "0010_agent_model_authority.sql",
	} {
		raw, err := os.ReadFile("../../../platform/postgres/migrations/cap_ai/" + filename)
		if err != nil {
			t.Fatal(err)
		}
		turnSQL(t, db, string(raw))
	}
	var name string
	if err := db.QueryRow("SELECT current_database()").Scan(&name); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	apiDB := testutil.As(t, u.String(), "dxos_ai_api")
	gatewayDB := testutil.As(t, u.String(), "dxos_ai_gateway")
	admin := testutil.As(t, u.String(), "dxos_authz")
	turnSQL(t, db, `INSERT INTO core.user_roles(user_id,role_name) VALUES
		('11111111-1111-1111-1111-111111111111','role_operator');
		INSERT INTO core.apps(app_id,display_name,version,status,spec) VALUES('agent_billing','Agent Billing','1','Ready','{}');
		INSERT INTO cap_ai.model_profiles(id,revision,purpose,execution,provider_id,model_id,limits,enabled)
		VALUES('fixture-llm',1,'GENERATE','HOSTED','fixture-adapter','fixture-llm',
		'{"max_input_tokens":4096,"max_output_tokens":64,"max_bytes":8192,"timeout_ms":1500}',true);
		INSERT INTO cap_ai.model_candidates(profile_id,revision,adapter_key,declaration_digest,declaration)
		VALUES('fixture-llm',1,'bounded-http-v1',repeat('a',64),'{}');
		UPDATE cap_ai.global_model_slots SET profile_id='fixture-llm', profile_revision=1, slot_revision=1 WHERE capability='LLM';
`)
	search := knowledge.NewStore(apiDB)
	if err := search.ConfigurePackages(true, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	reader := knowledge.NewPolicy(apiDB)
	gateway := &models.Gateway{
		Policy:     &models.Policy{Authority: models.DatabaseAuthority{DB: gatewayDB}, Enabled: true},
		Operations: &models.OperationStore{DB: gatewayDB}, Providers: map[string]models.Provider{"fixture-adapter": provider},
	}
	return agentTurnFixture{
		db: db, admin: admin, actor: Actor{alice}, store: &PostgresConversationStore{DB: apiDB}, reader: reader,
		retrieval: &RetrievalCoordinator{Scopes: &PostgresScopeResolver{DB: apiDB}, Search: search, Reader: reader},
		gateway:   gateway, resolver: &models.Registry{DB: gatewayDB},
	}
}

func (f agentTurnFixture) approveGeneration(t *testing.T, app, evidence string) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"profile_id": "fixture-llm", "profile_revision": 1, "provider_id": "fixture-adapter", "purpose": "GENERATE",
		"source_policy_id": "local-only", "source_policy_version": 1, "source_classification": "PERSONAL",
		"query_app_id": app, "query_classification": "PERSONAL", "retention_evidence": evidence,
		"training_use_evidence": "reviewed fixture", "region_evidence": "reviewed fixture",
	})
	if err != nil {
		t.Fatal(err)
	}
	var id string
	if err := f.admin.QueryRow(`SELECT cap_ai.approve_agent_processing($1,$2::jsonb)`, f.actor.UserID, string(raw)).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func (f agentTurnFixture) addPackage(t *testing.T, app, role, snippet string) {
	t.Helper()
	turnSQL(t, f.db, `INSERT INTO core.apps(app_id,display_name,version,status,publication_generation,spec)
		VALUES($1,$1,'1','Ready',1,jsonb_build_object(
		'knowledge',jsonb_build_object('version',1,'sources',jsonb_build_array(jsonb_build_object(
			'id','policy','path','knowledge/policy.md','mediaType','text/markdown','indexingProfile','standard-text-v1',
			'access',jsonb_build_object('mode','ROLE','allowedRoles',jsonb_build_array($2::text))))),
		'_publication',jsonb_build_object('generation',1,'spec_digest',repeat('b',64),'package_digest',repeat('c',64),
			'sources',jsonb_build_array(jsonb_build_object('source_id','policy','source_path','knowledge/policy.md',
				'incarnation',gen_random_uuid(),'authorization_generation',1,'declaration_hash',repeat('d',64),
				'sha256',repeat('a',64),'media_type','text/markdown','size_bytes',8,'artifact_ref',$1||'/policy/'||repeat('a',64),
				'safety_state','APPROVED','parser_profile','standard-text-v1')))))`, app, role)
	turnSQL(t, f.db, `INSERT INTO core.roles(name,app_id) VALUES($1,$2)`, role, app)
	turnSQL(t, f.db, `INSERT INTO core.user_roles(user_id,role_name) VALUES($1,$2)`, f.actor.UserID, role)
	turnSQL(t, f.db, `INSERT INTO core.app_capabilities(app_id,cap_id) VALUES($1,'cap-ai')`, app)
	turnSQL(t, f.db, `SELECT cap_ai.reconcile_package($1,1,repeat('b',64))`, app)
	turnSQL(t, f.db, `INSERT INTO cap_ai.processing_policies
		(id,version,execution,classification,enabled,approved_by,approved_at,query_app_id,provider_id,retention_approved)
		VALUES($1,1,'APPROVED_PROVIDER','NON_PERSONAL',true,$2,now(),$1,'fixture-adapter',true)`, app, f.actor.UserID)
	turnSQL(t, f.db, `INSERT INTO cap_ai.processing_policy_profiles(policy_id,policy_version,model_profile_id,profile_revision)
		VALUES($1,1,'fixture-llm',1)`, app)
	var version, run string
	if err := f.db.QueryRow(`UPDATE cap_ai.knowledge_documents d SET processing_policy_id=$1,active_version_id=desired_version_id
		FROM cap_ai.knowledge_collections c WHERE c.id=d.collection_id AND c.app_id=$1 RETURNING d.active_version_id`, app).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRow(`INSERT INTO cap_ai.processing_runs(version_id,ingestion_job_id,fencing_token,attempt_no,state)
		SELECT version_id,id,1,1,'COMPLETE' FROM cap_ai.ingestion_jobs WHERE version_id=$1 RETURNING id`, version).Scan(&run); err != nil {
		t.Fatal(err)
	}
	turnSQL(t, f.db, `UPDATE cap_ai.document_versions SET state='READY',active_run_id=$1 WHERE id=$2`, run, version)
	f.addChunk(t, run, snippet)
}

func (f agentTurnFixture) addChunk(t *testing.T, run, text string) {
	t.Helper()
	// This fixture installs an already-parsed chunk. Ingestion fencing is
	// exercised by the knowledge worker's own integration suite.
	tx, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(t.Context(), "SET LOCAL session_replication_role=replica"); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(t.Context(), `INSERT INTO cap_ai.knowledge_chunks
		(run_id,ordinal,text,section_path,content_hash,token_count) VALUES($1,0,$2,'[]',repeat('a',64),12)`, run, text); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func (f agentTurnFixture) orchestrator(retriever Retriever) *Orchestrator {
	memory := NewWorkingMemory(NewSourcePolicyChecker(f.reader, f.resolver, f.gateway.Policy, "agent_billing"), 0)
	return NewOrchestrator(f.store, memory, retriever, f.reader, f.resolver, f.gateway, "agent_billing")
}

func TestAgentTurnIdempotencyAndUncertainCharge(t *testing.T) {
	for _, uncertain := range []bool{false, true} {
		name := "settled"
		if uncertain {
			name = "uncertain"
		}
		t.Run(name, func(t *testing.T) {
			var sends, searches atomic.Int64
			started := make(chan struct{}, 1)
			release := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				sends.Add(1)
				var body struct {
					Input models.Payload `json:"input"`
				}
				if err := json.NewDecoder(req.Body).Decode(&body); err != nil || body.Input.Query != "laptop" || len(body.Input.Evidence) != 1 {
					t.Errorf("unexpected provider request: %+v, err=%v", body, err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				select {
				case started <- struct{}{}:
				default:
				}
				select {
				case <-release:
				case <-req.Context().Done():
					return
				}
				if uncertain {
					w.WriteHeader(http.StatusBadGateway)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"text":"Standard request","citation_ids":["E1"],"usage":{"input_tokens":3,"output_tokens":2}}`))
			}))
			defer server.Close()
			defer func() {
				select {
				case <-release:
				default:
					close(release)
				}
			}()
			provider, err := models.NewHTTPProvider(server.URL, "", true)
			if err != nil {
				t.Fatal(err)
			}
			f := newAgentTurnFixture(t, provider)
			f.addPackage(t, "procurement", "role_procurement", "New hires can request a laptop.")
			f.approveGeneration(t, "agent_billing", "reviewed fixture")
			o := f.orchestrator(turnRetrieverFunc(func(ctx context.Context, actor Actor, query string) (RetrievalResult, error) {
				searches.Add(1)
				return f.retrieval.Retrieve(ctx, actor, query)
			}))
			conversation, err := f.store.Create(t.Context(), f.actor, "Laptop policy")
			if err != nil {
				t.Fatal(err)
			}
			request := TurnRequest{conversation.ID, "same-logical-intent", "laptop"}
			type outcome struct {
				result TurnResult
				err    error
			}
			replied := make(chan outcome, 1)
			go func() {
				got, err := o.Reply(t.Context(), f.actor, request)
				replied <- outcome{got, err}
			}()
			select {
			case <-started:
			case early := <-replied:
				t.Fatalf("winning turn did not reach the provider: %+v, err=%v", early.result, early.err)
			case <-time.After(5 * time.Second):
				t.Fatal("provider not dispatched")
			}
			pending, err := o.Reply(t.Context(), f.actor, request)
			if err != nil || pending.Reason != "TURN_PENDING" || pending.Answer != nil || len(pending.Citations) != 0 {
				t.Fatalf("losing claim: %+v err=%v", pending, err)
			}
			close(release)
			first := <-replied
			state := "SUCCEEDED"
			if uncertain {
				state = "UNCERTAIN"
				if first.err != nil || first.result.Reason != models.ErrUncertain.Error() || first.result.Answer != nil || first.result.Usage != nil {
					t.Fatalf("possible billed error escaped: %+v err=%v", first.result, first.err)
				}
			} else if first.err != nil || first.result.Status != "ANSWERED" || first.result.Answer == nil || len(first.result.Citations) != 1 ||
				first.result.Usage == nil || first.result.Usage.InputTokens != 3 || first.result.Usage.OutputTokens != 2 ||
				!strings.Contains(first.result.Citations[0].URL, "message_seq=2") {
				t.Fatalf("missing settled answer: %+v err=%v", first.result, first.err)
			}
			if !uncertain {
				// Already delivered owner snapshots survive ordinary role revocation.
				turnSQL(t, f.db, `DELETE FROM core.user_roles WHERE user_id=$1 AND role_name='role_procurement'`, f.actor.UserID)
			}
			replay, err := o.Reply(t.Context(), f.actor, request)
			if err != nil || !reflect.DeepEqual(replay, first.result) || sends.Load() != 1 || searches.Load() != 1 {
				t.Fatalf("completed replay repeated work or changed snapshot: %+v err=%v sends=%d searches=%d", replay, err, sends.Load(), searches.Load())
			}
			if _, err = o.Reply(t.Context(), Actor{bob}, request); !errors.Is(err, knowledge.ErrNotFound) {
				t.Fatalf("foreign owner replay: %v", err)
			}
			request.Query = "changed laptop"
			if _, err = o.Reply(t.Context(), f.actor, request); !errors.Is(err, knowledge.ErrConflict) {
				t.Fatalf("different content on same key: %v", err)
			}
			var operations int
			var gotState, app string
			var input, output sql.NullInt64
			err = f.db.QueryRow(`SELECT count(*),coalesce(min(state),''),coalesce(min(app_id),''),
				min(input_tokens),min(output_tokens) FROM cap_ai.model_operations`).
				Scan(&operations, &gotState, &app, &input, &output)
			if err != nil || operations != 1 || gotState != state || app != "agent_billing" {
				t.Fatalf("operations=%d state=%s app=%s err=%v", operations, gotState, app, err)
			}
			if uncertain && (input.Valid || output.Valid) {
				t.Fatalf("uncertain operation recorded usage: %+v/%+v", input, output)
			}
			if !uncertain && (input.Int64 != 3 || output.Int64 != 2) {
				t.Fatalf("usage counters %+v/%+v", input, output)
			}
		})
	}
}

func TestAgentTurnRechecksCurrentAuthorityAfterProvider(t *testing.T) {
	for _, revoke := range []string{"uncited prior role", "billing review replaced"} {
		t.Run(revoke, func(t *testing.T) {
			var f agentTurnFixture
			var latestReview string
			var sends atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				sends.Add(1)
				var body struct {
					Input models.Payload `json:"input"`
				}
				if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
					t.Errorf("invalid adapter input: %v", err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				answer := "Benefits details from previous HR evidence."
				if body.Input.Query == "laptop" {
					if len(body.Input.Texts) != 1 || !strings.Contains(body.Input.Texts[0], answer) ||
						len(body.Input.Evidence) != 1 || !strings.Contains(body.Input.Evidence[0].Text, "laptop") {
						t.Errorf("missing prior HR context or latest evidence: %+v", body.Input)
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					change := func() error {
						if revoke == "uncited prior role" {
							_, err := f.db.ExecContext(req.Context(), `DELETE FROM core.user_roles WHERE user_id=$1 AND role_name='role_hr'`, f.actor.UserID)
							return err
						}
						var disabled bool
						err := f.admin.QueryRowContext(req.Context(), `SELECT cap_ai.disable_agent_processing($1,$2,'fixture review withdrawn')`, f.actor.UserID, latestReview).Scan(&disabled)
						if err == nil && !disabled {
							return errors.New("current review was not disabled")
						}
						return err
					}
					if err := change(); err != nil {
						t.Errorf("failed to change live authority after dispatch: %v", err)
						w.WriteHeader(http.StatusInternalServerError)
						return
					}
					answer = "Sensitive new answer derived from prior HR context."
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(models.Result{
					Text: answer, CitationIDs: []string{"E1"}, Usage: &models.Usage{InputTokens: 3, OutputTokens: 2},
				})
			}))
			defer server.Close()
			model, err := models.NewHTTPProvider(server.URL, "", true)
			if err != nil {
				t.Fatal(err)
			}
			f = newAgentTurnFixture(t, model)
			f.addPackage(t, "hr", "role_hr", "Benefits details are available through Human Resources.")
			f.addPackage(t, "procurement", "role_procurement", "New hires can request a laptop.")
			f.approveGeneration(t, "agent_billing", "first valid billing review")
			latestReview = f.approveGeneration(t, "agent_billing", "independently reviewed replacement")
			o := f.orchestrator(f.retrieval)
			conversation, err := f.store.Create(t.Context(), f.actor, "Policies")
			if err != nil {
				t.Fatal(err)
			}
			priorReq := TurnRequest{conversation.ID, "first", "benefits"}
			prior, err := o.Reply(t.Context(), f.actor, priorReq)
			if err != nil || prior.Status != "ANSWERED" || prior.Answer == nil {
				t.Fatalf("first turn: %+v, err=%v", prior, err)
			}
			nextReq := TurnRequest{conversation.ID, "next", "laptop"}
			got, err := o.Reply(t.Context(), f.actor, nextReq)
			if err != nil || got.Reason != "AUTHORIZATION_CHANGED" || got.Answer != nil || len(got.Citations) != 0 ||
				got.Usage != nil || sends.Load() != 2 {
				t.Fatalf("live revoke escaped final check: %+v, err=%v, sends=%d", got, err, sends.Load())
			}
			history, err := f.store.Messages(t.Context(), f.actor, conversation.ID, 4, 0)
			if err != nil || len(history) != 4 || history[3].Content != "" || len(history[3].Sources) != 0 ||
				history[1].Content != *prior.Answer {
				t.Fatalf("incorrect persisted transcript: %+v, err=%v", history, err)
			}
			replayed, err := o.Reply(t.Context(), f.actor, priorReq)
			if err != nil || !reflect.DeepEqual(prior, replayed) || sends.Load() != 2 {
				t.Fatalf("owner replay lost immutable history: %+v, err=%v", replayed, err)
			}
		})
	}
}

func TestAgentTurnRequiresCurrentModelBinding(t *testing.T) {
	var sends atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		sends.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	provider, err := models.NewHTTPProvider(server.URL, "", true)
	if err != nil {
		t.Fatal(err)
	}
	f := newAgentTurnFixture(t, provider)
	f.addPackage(t, "procurement", "role_procurement", "New hires can request a laptop.")
	f.approveGeneration(t, "agent_billing", "reviewed fixture")
	turnSQL(t, f.db, `UPDATE cap_ai.global_model_slots SET profile_id=NULL,profile_revision=NULL,slot_revision=slot_revision+1 WHERE capability='LLM'`)
	o := f.orchestrator(f.retrieval)
	conversation, err := f.store.Create(t.Context(), f.actor, "Laptop policy")
	if err != nil {
		t.Fatal(err)
	}
	got, err := o.Reply(t.Context(), f.actor, TurnRequest{conversation.ID, "binding withdrawn", "laptop"})
	if err != nil || got.Status != "SEARCH_ONLY" || got.Reason != models.ErrUnavailable.Error() ||
		got.Answer != nil || len(got.Citations) != 1 || sends.Load() != 0 {
		t.Fatalf("missing binding did not preserve safe search: %+v, err=%v, sends=%d", got, err, sends.Load())
	}
	var operations int
	err = f.db.QueryRow(`SELECT count(*) FROM cap_ai.model_operations`).Scan(&operations)
	if err != nil || operations != 0 {
		t.Fatalf("missing binding left operations: %d, err=%v", operations, err)
	}
}

func TestAgentTurnSlotWithdrawalWhileReserving(t *testing.T) {
	var sends atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		sends.Add(1)
		_, _ = w.Write([]byte(`{"text":"Old slot answer","citation_ids":["E1"],"usage":{"input_tokens":3,"output_tokens":2}}`))
	}))
	defer server.Close()
	provider, err := models.NewHTTPProvider(server.URL, "", true)
	if err != nil {
		t.Fatal(err)
	}
	f := newAgentTurnFixture(t, provider)
	f.addPackage(t, "procurement", "role_procurement", "New hires can request a laptop.")
	f.approveGeneration(t, "agent_billing", "reviewed fixture")
	conversation, err := f.store.Create(t.Context(), f.actor, "Laptop policy")
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	tx, err := f.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `LOCK TABLE cap_ai.model_operations IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	type outcome struct {
		result TurnResult
		err    error
	}
	reply := make(chan outcome, 1)
	o := f.orchestrator(f.retrieval)
	go func() {
		out, err := o.Reply(ctx, f.actor, TurnRequest{conversation.ID, "slot withdrawn", "laptop"})
		reply <- outcome{out, err}
	}()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var waiting bool
		err = f.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity
			WHERE datname=current_database()
			AND wait_event_type='Lock' AND query LIKE '%cap_ai.claim_model_operation(%')`).Scan(&waiting)
		if err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case early := <-reply:
			t.Fatalf("turn finished before entering the durable claim: %+v, err=%v", early.result, early.err)
		case <-ctx.Done():
			t.Fatal("Gateway did not block on the durable claim")
		case <-ticker.C:
		}
	}
	// Withdraw the slot from a separate committed session while the claim is
	// blocked. The old model profile and processing approvals stay enabled.
	if _, err := f.db.ExecContext(ctx, `UPDATE cap_ai.global_model_slots SET
		profile_id=NULL,profile_revision=NULL,slot_revision=slot_revision+1 WHERE capability='LLM'`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	got := <-reply
	if got.err != nil || got.result.Status != "SEARCH_ONLY" || got.result.Reason != "AUTHORIZATION_CHANGED" ||
		got.result.Answer != nil || len(got.result.Citations) != 0 || sends.Load() != 0 {
		t.Fatalf("withdrawn slot reached provider: reply=%+v, err=%v, sends=%d", got.result, got.err, sends.Load())
	}
	var state, code string
	var usage int
	err = f.db.QueryRowContext(ctx, `SELECT state,coalesce(error_code,''),(input_tokens IS NOT NULL)::int FROM cap_ai.model_operations`).Scan(&state, &code, &usage)
	if err != nil || state != "FAILED_UNSENT" || code != "AUTHORITY_CHANGED" || usage != 0 {
		t.Fatalf("withdrawn slot not recorded as unsent: state=%s code=%s usage=%d err=%v", state, code, usage, err)
	}
}
