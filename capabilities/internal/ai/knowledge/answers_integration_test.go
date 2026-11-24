//go:build integration

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
package knowledge

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/opendx-os/opendx-os/capabilities/internal/ai/models"
	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
	"github.com/opendx-os/opendx-os/capabilities/internal/testutil"
)

func TestRegistryAnswerCurrentDatabaseRevocation(t *testing.T) {
	db, s, a, profile := semanticFixture(t)
	// The new revision is independently approved for GENERATE, with no vectors.
	if _, e := db.Exec(`INSERT INTO cap_ai.model_profiles(id,revision,purpose,execution,provider_id,model_id,limits,enabled) SELECT 'gen',revision,'GENERATE',execution,provider_id,model_id,limits,enabled FROM cap_ai.model_profiles WHERE id='embed';INSERT INTO cap_ai.processing_policy_profiles VALUES('reviewed',1,'gen',1,now());`); e != nil {
		t.Fatal(e)
	}
	profile.ID = "gen"
	profile.Purpose = "GENERATE"
	profile.Dimension = 0
	var called bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if _, e := db.Exec(`DELETE FROM core.user_roles WHERE user_id=$1`, a.UserID); e != nil {
			t.Error(e)
		}
		json.NewEncoder(w).Encode(models.Result{Text: "Director approval.", CitationIDs: []string{"E1"}, Usage: &models.Usage{InputTokens: 10, OutputTokens: 5}})
	}))
	defer server.Close()
	provider, e := models.NewHTTPProvider(server.URL, "", true)
	if e != nil {
		t.Fatal(e)
	}
	g := &models.Gateway{Policy: &models.Policy{Enabled: true, Authority: models.DatabaseAuthority{DB: db}}, Operations: &models.OperationStore{DB: gatewayLogin(t, db)}, Providers: map[string]models.Provider{"fake": provider}}
	service := AnswerService{Search: s, Reader: NewPolicy(db), Policy: g.Policy, Gateway: g, Resolver: &fixedResolver{profile: profile}}
	response, e := service.Answer(context.Background(), a, AnswerRequest{Scope: Scope{Kind: "APP", AppID: "library"}, Query: "Director"})
	if e != nil || !called || response.Reason != "AUTHORIZATION_CHANGED" || response.Answer != nil || len(response.Results) != 0 || len(response.Citations) != 0 {
		t.Fatal(response, e, called)
	}
}
func TestEmbeddingLeaseLossPreservesLexical(t *testing.T) {
	db, s, a, profile := semanticFixture(t)
	provider := &embeddingProvider{before: func() {
		_, e := db.Exec(`UPDATE cap_ai.ingestion_jobs SET lease_until=now()-interval '1 second' WHERE kind='EMBED'`)
		if e != nil {
			t.Error(e)
		}
	}}
	s.ConfigureModels(&models.Gateway{Policy: &models.Policy{Enabled: true, Authority: models.DatabaseAuthority{DB: db}}, Operations: &models.OperationStore{DB: gatewayLogin(t, db)}, Providers: map[string]models.Provider{"fake": provider}}, profile, models.Profile{})
	if e := s.EnqueueEmbeddings(context.Background(), a, profile); e != nil {
		t.Fatal(e)
	}
	lease, e := s.ClaimEmbedding(context.Background(), ownerID, models.Profile{ID: "embed", Revision: 1})
	if e != nil || lease == nil {
		t.Fatal(e)
	}
	if e = s.BuildEmbeddings(context.Background(), *lease, profile); e == nil {
		t.Fatal("stale vector published")
	}
	if _, e = db.Exec(`UPDATE cap_ai.ingestion_jobs SET attempts=max_attempts WHERE kind='EMBED'`); e != nil {
		t.Fatal(e)
	}
	if e = s.Recover(context.Background(), time.Now()); e != nil {
		t.Fatal(e)
	}
	result, e := s.Search(context.Background(), a, SearchRequest{Scope: Scope{Kind: "APP", AppID: "library"}, Query: "Director"})
	if e != nil || len(result.Results) != 1 {
		t.Fatal("embedding recovery destroyed lexical readiness", e, result)
	}
}
func TestAnswersHTTPContractAndPolicyErrors(t *testing.T) {
	_, s, a, _ := semanticFixture(t)
	s.personalEnabled = true
	for _, test := range []struct {
		body   string
		status int
	}{{`{"scope":{"kind":"APP","app_id":"library"},"query":"Director"}`, 200}, {`{"scope":{"kind":"APP","app_id":"library"},"query":"Director","require_generation":true}`, 403}, {`{"scope":{"kind":"APP","app_id":"library"},"query":"Director","query_classification":"NON_PERSONAL"}`, 400}} {
		r := httptest.NewRequest("POST", "/api/cap/ai/answers", strings.NewReader(test.body))
		r = r.WithContext(auth.WithIdentity(r.Context(), a))
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != test.status {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}

// usageAnswerProvider reports exactly the usage presence the test asks for.
type usageAnswerProvider struct {
	usage *models.Usage
	calls int
}

func (p *usageAnswerProvider) Invoke(context.Context, models.Profile, models.Payload) (models.Result, error) {
	p.calls++
	return models.Result{Text: "Director approval", CitationIDs: []string{"E1"}, Usage: p.usage}, nil
}

// The live HTTP answer path must keep the zero-versus-nil usage distinction the
// provider boundary decodes: a reported zero pair is serialized, absent usage is
// omitted from the public response body.
func TestAnswersUsageResponseShape(t *testing.T) {
	db, s, a, _ := semanticFixture(t)
	if _, e := db.Exec(`INSERT INTO cap_ai.model_profiles(id,revision,purpose,execution,provider_id,model_id,limits,enabled) SELECT 'local-gen',revision,'GENERATE','LOCAL',provider_id,model_id,limits,enabled FROM cap_ai.model_profiles WHERE id='embed';INSERT INTO cap_ai.processing_policy_profiles VALUES('reviewed',1,'local-gen',1,now());`); e != nil {
		t.Fatal(e)
	}
	generate := models.Profile{ID: "local-gen", Revision: 1, Purpose: "GENERATE", Execution: "LOCAL",
		ProviderID: "fake", ModelID: "fake", Enabled: true,
		Limits: models.Limits{MaxInputTokens: 8192, MaxOutputTokens: 512, MaxBytes: 16384, TimeoutMS: 500}}
	for _, test := range []struct {
		name     string
		usage    *models.Usage
		contains string
	}{
		{name: "reported zero", usage: &models.Usage{}, contains: `"usage":{"input_tokens":0,"output_tokens":0}`},
		{name: "no usage", usage: nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			provider := &usageAnswerProvider{usage: test.usage}
			g := &models.Gateway{Policy: &models.Policy{Enabled: true, Authority: models.DatabaseAuthority{DB: db}}, Operations: &models.OperationStore{DB: gatewayLogin(t, db)}, Providers: map[string]models.Provider{"fake": provider}}
			s.ConfigureModels(g, models.Profile{}, generate)
			r := httptest.NewRequest("POST", "/api/cap/ai/answers", strings.NewReader(`{"scope":{"kind":"APP","app_id":"library"},"query":"Director"}`))
			r = r.WithContext(auth.WithIdentity(r.Context(), a))
			w := httptest.NewRecorder()
			s.Handler().ServeHTTP(w, r)
			body := w.Body.String()
			if w.Code != 200 || provider.calls != 1 {
				t.Fatalf("live answer: code=%d calls=%d body=%s", w.Code, provider.calls, body)
			}
			if !strings.Contains(body, `"status":"ANSWERED"`) {
				t.Fatalf("answer not generated: %s", body)
			}
			if test.contains == "" {
				if strings.Contains(body, `"usage"`) {
					t.Fatalf("absent usage serialized: %s", body)
				}
				return
			}
			if !strings.Contains(body, test.contains) {
				t.Fatalf("usage shape lost in response: %s", body)
			}
		})
	}
}

type uncertainAnswerProvider struct{ calls int }

func (p *uncertainAnswerProvider) Invoke(context.Context, models.Profile, models.Payload) (models.Result, error) {
	p.calls++
	return models.Result{}, errors.New("provider timeout")
}

// A possibly sent answer generation is public uncertainty: optional generation
// returns 200 SEARCH_ONLY with only the current authorized results, and
// require_generation reaches the same sanitized 503 code.
func TestAnswersUncertaintyPublicContract(t *testing.T) {
	db, s, a, _ := semanticFixture(t)
	if _, e := db.Exec(`INSERT INTO cap_ai.model_profiles(id,revision,purpose,execution,provider_id,model_id,limits,enabled) SELECT 'local-gen',revision,'GENERATE','LOCAL',provider_id,model_id,limits,enabled FROM cap_ai.model_profiles WHERE id='embed';INSERT INTO cap_ai.processing_policy_profiles VALUES('reviewed',1,'local-gen',1,now());`); e != nil {
		t.Fatal(e)
	}
	generate := models.Profile{ID: "local-gen", Revision: 1, Purpose: "GENERATE", Execution: "LOCAL",
		ProviderID: "fake", ModelID: "fake", Enabled: true,
		Limits: models.Limits{MaxInputTokens: 8192, MaxOutputTokens: 512, MaxBytes: 16384, TimeoutMS: 500}}
	provider := &uncertainAnswerProvider{}
	g := &models.Gateway{Policy: &models.Policy{Enabled: true, Authority: models.DatabaseAuthority{DB: db}}, Operations: &models.OperationStore{DB: gatewayLogin(t, db)}, Providers: map[string]models.Provider{"fake": provider}}
	s.ConfigureModels(g, models.Profile{}, generate)
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"optional generation", `{"scope":{"kind":"APP","app_id":"library"},"query":"Director"}`, 200},
		{"required generation", `{"scope":{"kind":"APP","app_id":"library"},"query":"Director","require_generation":true}`, 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/api/cap/ai/answers", strings.NewReader(tc.body))
			r = r.WithContext(auth.WithIdentity(r.Context(), a))
			w := httptest.NewRecorder()
			s.Handler().ServeHTTP(w, r)
			body := w.Body.String()
			if w.Code != tc.status || strings.Contains(body, "MODEL_UNAVAILABLE") {
				t.Fatalf("status=%d body=%s", w.Code, body)
			}
			if tc.status == 503 {
				if !strings.Contains(body, `"code":"MODEL_CALL_UNCERTAIN"`) {
					t.Fatalf("sanitized uncertainty missing: %s", body)
				}
				return
			}
			for _, want := range []string{`"status":"SEARCH_ONLY"`, `"reason":"MODEL_CALL_UNCERTAIN"`, `"answer":null`, `"citations":[]`, `"chunk_id":"`} {
				if !strings.Contains(body, want) {
					t.Fatalf("missing %s in %s", want, body)
				}
			}
			if strings.Contains(body, `"usage"`) {
				t.Fatalf("uncertain answer exposed usage: %s", body)
			}
		})
	}
	if provider.calls != 2 {
		t.Fatalf("provider calls=%d", provider.calls)
	}
}

type localAnswerProvider struct{ calls int }

func (p *localAnswerProvider) Invoke(context.Context, models.Profile, models.Payload) (models.Result, error) {
	p.calls++
	return models.Result{Text: "Director approval", CitationIDs: []string{"E1"}, Usage: &models.Usage{InputTokens: 10, OutputTokens: 5}}, nil
}
func TestQualifiedLocalPersonalAnswer(t *testing.T) {
	db, s, a, profile := semanticFixture(t)
	activateText(t, s, workerStore(t, db), a, fileID, "local-personal", "Director approval private evidence")
	if _, e := db.Exec(`INSERT INTO cap_ai.model_profiles(id,revision,purpose,execution,provider_id,model_id,limits,enabled) SELECT 'local-gen',revision,'GENERATE','LOCAL',provider_id,model_id,limits,enabled FROM cap_ai.model_profiles WHERE id='embed';INSERT INTO cap_ai.processing_policy_profiles VALUES('local-only',1,'local-gen',1,now());`); e != nil {
		t.Fatal(e)
	}
	profile.ID = "local-gen"
	profile.Purpose = "GENERATE"
	profile.Execution = "LOCAL"
	profile.Dimension = 0
	provider := &localAnswerProvider{}
	g := &models.Gateway{Policy: &models.Policy{Enabled: true, Authority: models.DatabaseAuthority{DB: db}}, Operations: &models.OperationStore{DB: gatewayLogin(t, db)}, Providers: map[string]models.Provider{"fake": provider}}
	service := AnswerService{Search: s, Reader: NewPolicy(db), Policy: g.Policy, Gateway: g, Resolver: &fixedResolver{profile: profile}}
	response, e := service.Answer(context.Background(), a, AnswerRequest{Scope: Scope{Kind: "PERSONAL"}, Query: "Director", RequireGeneration: true})
	if e != nil || response.Status != "ANSWERED" || provider.calls != 1 {
		t.Fatal("qualified local personal answer failed", response, e, provider.calls)
	}
}

// answerRunReplacement changes the real database snapshot at the assembly READ.
type answerRunReplacement struct {
	AnswerReader
	db      *sql.DB
	changed bool
}

func (r *answerRunReplacement) CanRead(ctx context.Context, actor *auth.Identity, doc string) (ReadScope, error) {
	if !r.changed {
		previous, err := r.AnswerReader.CanRead(ctx, actor, doc)
		if err != nil {
			return ReadScope{}, err
		}
		result, err := r.db.ExecContext(ctx, `WITH replacement AS (
            INSERT INTO cap_ai.processing_runs(version_id,ingestion_job_id,attempt_no,fencing_token,state)
            SELECT run.version_id,run.ingestion_job_id,run.attempt_no+1,run.fencing_token+1,'COMPLETE'
            FROM cap_ai.knowledge_documents d
            JOIN cap_ai.document_versions v ON v.id=d.active_version_id
            JOIN cap_ai.processing_runs run ON run.id=v.active_run_id
            WHERE d.id=$1
            RETURNING id,version_id
        )
        UPDATE cap_ai.document_versions v SET active_run_id=r.id FROM replacement r WHERE v.id=r.version_id`, doc)
		if err != nil {
			return ReadScope{}, err
		}
		rows, err := result.RowsAffected()
		if err != nil || rows != 1 {
			return ReadScope{}, fmt.Errorf("replaced %d active runs: %v", rows, err)
		}
		current, err := r.AnswerReader.CanRead(ctx, actor, doc)
		r.changed = err == nil && current.VersionID == previous.VersionID && current.RunID != previous.RunID
		return current, err
	}
	return r.AnswerReader.CanRead(ctx, actor, doc)
}

func TestHostedPersonalAnswerAuditedApproval(t *testing.T) {
	db, s, a, _ := semanticFixture(t)
	activateText(t, s, workerStore(t, db), a, fileID, "hosted-personal", "Director approval private evidence")
	for _, migration := range []string{"0005_model_registry.sql", "0008_personal_model_processing.sql"} {
		raw, err := os.ReadFile("../../../platform/postgres/migrations/cap_ai/" + migration)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(string(raw)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO cap_ai.model_profiles(id,revision,purpose,execution,provider_id,model_id,limits,enabled) SELECT 'hosted-gen',revision,'GENERATE','HOSTED',provider_id,model_id,limits,enabled FROM cap_ai.model_profiles WHERE id='embed';
 INSERT INTO cap_ai.model_candidates(profile_id,revision,adapter_key,declaration_digest,declaration) VALUES('hosted-gen',1,'bounded-http-v1',repeat('a',64),'{}');
 UPDATE cap_ai.global_model_slots SET profile_id='hosted-gen',profile_revision=1,slot_revision=1 WHERE capability='LLM';
 INSERT INTO core.user_roles(user_id,role_name) VALUES('11111111-1111-4111-8111-111111111111','role_operator');`); err != nil {
		t.Fatal(err)
	}
	var name string
	if err := db.QueryRow(`SELECT current_database()`).Scan(&name); err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(os.Getenv("TEST_DATABASE_URL"))
	u.Path = "/" + name
	gateway := testutil.As(t, u.String(), "dxos_ai_gateway")
	admin := testutil.As(t, u.String(), "dxos_authz")
	api := testutil.As(t, u.String(), "dxos_ai_api")
	provider := &localAnswerProvider{}
	g := &models.Gateway{Policy: &models.Policy{Enabled: true, Authority: models.DatabaseAuthority{DB: gateway}}, Operations: &models.OperationStore{DB: gateway}, Providers: map[string]models.Provider{"fake": provider}}
	service := AnswerService{Search: s, Reader: NewPolicy(api), Policy: g.Policy, Gateway: g, Resolver: &models.Registry{DB: gateway}}
	req := AnswerRequest{Scope: Scope{Kind: "PERSONAL"}, Query: "Director", RequireGeneration: true}
	if _, err := service.Answer(context.Background(), a, req); err != models.ErrPolicy || provider.calls != 0 {
		t.Fatal("slot alone permitted egress", err, provider.calls)
	}
	var approval string
	if err := admin.QueryRow(`SELECT cap_ai.approve_agent_processing($1,'{"profile_id":"hosted-gen","profile_revision":1,"provider_id":"fake","purpose":"GENERATE","source_policy_id":"local-only","source_policy_version":1,"source_classification":"PERSONAL","query_app_id":"library","query_classification":"PERSONAL","retention_evidence":"review-retention","training_use_evidence":"review-training","region_evidence":"review-region"}')`, a.UserID).Scan(&approval); err != nil {
		t.Fatal(err)
	}
	response, err := service.Answer(context.Background(), a, req)
	if err != nil || response.Status != "ANSWERED" || provider.calls != 1 {
		t.Fatal("approved owner generation denied", response, err, provider.calls)
	}
	if _, err = service.Answer(context.Background(), &auth.Identity{UserID: otherID, Role: a.Role}, req); err != nil {
		t.Fatal(err)
	}
	if provider.calls != 1 {
		t.Fatal("foreign owner content sent")
	}
	if _, err = admin.Exec(`SELECT cap_ai.disable_agent_processing($1,$2,'review withdrawn')`, a.UserID, approval); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Answer(context.Background(), a, req); err != models.ErrPolicy || provider.calls != 1 {
		t.Fatal("disabled review sent content", err, provider.calls)
	}
	if err := admin.QueryRow(`SELECT cap_ai.approve_agent_processing($1,'{"profile_id":"hosted-gen","profile_revision":1,"provider_id":"fake","purpose":"GENERATE","source_policy_id":"local-only","source_policy_version":1,"source_classification":"PERSONAL","query_app_id":"library","query_classification":"PERSONAL","retention_evidence":"renewed-retention","training_use_evidence":"review-training","region_evidence":"review-region"}')`, a.UserID).Scan(&approval); err != nil {
		t.Fatal(err)
	}
	t.Run("same version reparse before hosted PERSONAL assembly", func(t *testing.T) {
		provider.calls = 0
		reg := service.Reader
		reader := &answerRunReplacement{AnswerReader: reg, db: db}
		service.Reader = reader
		defer func() { service.Reader = reg }()
		got, err := service.Answer(t.Context(), a, req)
		if !reader.changed || err != nil || got.Reason != "AUTHORIZATION_CHANGED" || got.Answer != nil ||
			len(got.Results) != 0 || len(got.Citations) != 0 || provider.calls != 0 {
			t.Fatalf("retired run reached the hosted provider or response: %+v err=%v sends=%d", got, err, provider.calls)
		}
	})

}
