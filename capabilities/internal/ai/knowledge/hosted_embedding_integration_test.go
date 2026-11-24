//go:build integration

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package knowledge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/opendx-os/opendx-os/capabilities/internal/ai/knowledge/modeladapter"
	"github.com/opendx-os/opendx-os/capabilities/internal/ai/models"
	"github.com/opendx-os/opendx-os/capabilities/internal/testutil"
)

// A revision with a new dimension must build separately and require its own allowlist.
func TestHostedEmbeddingRevisionIsolation(t *testing.T) {
	db, api, actor, profile := semanticFixture(t)
	var calls atomic.Int32
	vendor := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/v1/embeddings" || (r.Header.Get("Authorization") != "Bearer synthetic-vendor" && r.Header.Get("Authorization") != "Bearer synthetic-wide") {
			t.Error("unexpected upstream request")
			w.WriteHeader(400)
			return
		}
		var body struct {
			Model string `json:"model"`
			Input any    `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Model != "fake" || body.Input == nil {
			t.Error("invalid embedding wire request", err)
			w.WriteHeader(400)
			return
		}
		// The reviewed routes select dimensions through distinct upstream paths.
		vector := []float32{1, 0, 0}
		if r.Header.Get("Authorization") == "Bearer synthetic-wide" {
			vector = []float32{1, 0, 0, 0, 0}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"model": "fake", "data": []any{map[string]any{"index": 0, "embedding": vector}}, "usage": map[string]int{"prompt_tokens": 10, "total_tokens": 10}})
	}))
	defer vendor.Close()
	route := modeladapter.Route{ProtocolVersion: 1, Type: "openai-compatible", BaseURL: vendor.URL + "/v1", CredentialRef: "VENDOR_KEY", Models: []modeladapter.ModelRule{{ModelID: "fake", Purpose: "EMBED", Dimension: 3, Limits: profile.Limits}}}
	wideRoute := route
	wideRoute.CredentialRef = "WIDE_KEY"
	wideRoute.Models = []modeladapter.ModelRule{{ModelID: "fake", Purpose: "EMBED", Dimension: 5, Limits: profile.Limits}}
	cfg := modeladapter.Config{SchemaVersion: 1, Routes: []modeladapter.Route{route, wideRoute}}
	upstream, err := modeladapter.NewOpenAIClient(cfg, func(ref string) string {
		if ref == "WIDE_KEY" {
			return "synthetic-wide"
		}
		return "synthetic-vendor"
	}, vendor.Client().Transport)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := modeladapter.NewHandler(cfg, "synthetic-internal", upstream)
	if err != nil {
		t.Fatal(err)
	}
	adapter := httptest.NewTLSServer(handler)
	defer adapter.Close()
	// HTTPProvider uses the platform transport, with only the fixture CA installed.
	previousTransport := http.DefaultTransport
	http.DefaultTransport = adapter.Client().Transport
	defer func() { http.DefaultTransport = previousTransport }()
	path, err := modeladapter.RoutePath(route)
	if err != nil {
		t.Fatal(err)
	}
	provider, err := models.NewHTTPProvider(adapter.URL+path, "synthetic-internal", false)
	if err != nil {
		t.Fatal(err)
	}
	gateway := &models.Gateway{Policy: &models.Policy{Enabled: true, Authority: models.DatabaseAuthority{DB: db}}, Operations: &models.OperationStore{DB: gatewayLogin(t, db)}, Providers: map[string]models.Provider{"fake": provider}}
	api.ConfigureModels(gateway, profile, models.Profile{})
	worker := workerStore(t, db)
	worker.ConfigureModels(gateway, profile, models.Profile{})
	ctx := context.Background()
	if err = api.EnqueueEmbeddings(ctx, actor); err != nil {
		t.Fatal(err)
	}
	lease, err := worker.ClaimEmbedding(ctx, ownerID, models.Profile{ID: "embed", Revision: 1})
	if err != nil || lease == nil {
		t.Fatal(lease, err)
	}
	if err = worker.BuildEmbeddings(ctx, *lease, profile); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("unexpected initial dispatch count", calls.Load())
	}
	// Same model identity, independently versioned profile and vector dimension.
	limits, _ := json.Marshal(profile.Limits)
	if _, err = db.Exec(`INSERT INTO cap_ai.model_profiles(id,revision,purpose,execution,provider_id,model_id,dimension,limits,enabled) VALUES('embed',2,'EMBED','HOSTED','fake','fake',5,$1,false)`, string(limits)); err != nil {
		t.Fatal(err)
	}
	newer := profile
	newer.Revision = 2
	newer.Dimension = 5
	// The new revision is declared and reviewed but is not the deployment selection
	// yet, so nothing is queued for it and the worker cannot claim it.
	api.ConfigureModels(gateway, newer, models.Profile{})
	if err = api.EnqueueEmbeddings(ctx, actor); err != nil {
		t.Fatal(err)
	}
	if lease, err = worker.ClaimEmbedding(ctx, ownerID, newer); err != nil || lease != nil {
		t.Fatal("unselected revision scheduled", lease, err)
	}
	if calls.Load() != 1 {
		t.Fatal("unselected revision dispatched")
	}
	if _, err = db.Exec(`INSERT INTO cap_ai.processing_policy_profiles(policy_id,policy_version,model_profile_id,profile_revision) VALUES('reviewed',1,'embed',2)`); err != nil {
		t.Fatal(err)
	}
	widePath, err := modeladapter.RoutePath(wideRoute)
	if err != nil {
		t.Fatal(err)
	}
	wideProvider, err := models.NewHTTPProvider(adapter.URL+widePath, "synthetic-internal", false)
	if err != nil {
		t.Fatal(err)
	}
	gateway.Providers["fake"] = wideProvider
	// Revision 2 becomes the live selection; revision 1 keeps its own job and
	// vectors but is no longer claimable.
	selectDeployment(t, db, newer)
	worker.ConfigureModels(gateway, newer, models.Profile{})
	if err = (&Worker{Store: worker, WorkerID: ownerID}).Once(ctx); err != nil {
		t.Fatal(err)
	}

	var ready, vectors int
	var newReady int
	if err = db.QueryRow(`SELECT count(*) FROM cap_ai.embedding_generations WHERE state='READY' AND profile_revision=2 AND dimension=5 AND completed_count=expected_count`).Scan(&newReady); err != nil || newReady != 1 {
		t.Fatal("new generation incomplete", newReady, err)
	}
	if err = db.QueryRow(`SELECT count(*) FROM cap_ai.embedding_generations WHERE state='READY' AND profile_revision=1 AND dimension=3`).Scan(&ready); err != nil || ready != 1 {
		t.Fatal(ready, err)
	}
	if err = db.QueryRow(`SELECT count(*) FROM cap_ai.chunk_embeddings e JOIN cap_ai.embedding_generations g ON g.id=e.generation_id WHERE g.profile_revision=2 AND vector_dims(e.embedding)=5`).Scan(&vectors); err != nil || vectors != 1 {
		t.Fatal("mixed vectors", vectors, err)
	}
	if calls.Load() != 2 {
		t.Fatal("expected one send per chunk", calls.Load())
	}
}

// The answer path keeps its final authorization check across both TLS hops.
func TestHostedAdapterGatewayRegression(t *testing.T) {
	for _, scenario := range []string{"grounded", "revoked-before", "revoked-after", "fabricated-citation"} {
		t.Run(scenario, func(t *testing.T) {
			db, search, actor, profile := semanticFixture(t)
			if _, err := db.Exec(`INSERT INTO cap_ai.model_profiles(id,revision,purpose,execution,provider_id,model_id,limits,enabled) SELECT 'gen',revision,'GENERATE',execution,provider_id,model_id,limits,enabled FROM cap_ai.model_profiles WHERE id='embed'; INSERT INTO cap_ai.processing_policy_profiles VALUES('reviewed',1,'gen',1,now())`); err != nil {
				t.Fatal(err)
			}
			profile.ID = "gen"
			profile.Purpose = "GENERATE"
			profile.Dimension = 0
			selectDeployment(t, db, profile)
			var calls atomic.Int32
			vendor := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer synthetic-chat" {
					t.Error("wrong generation destination")
					w.WriteHeader(400)
					return
				}
				var request map[string]any
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request["model"] != "fake" || request["stream"] != false {
					t.Error("wrong generation wire request", err)
					w.WriteHeader(400)
					return
				}
				if scenario == "revoked-after" {
					if _, err := db.Exec(`DELETE FROM core.user_roles WHERE user_id=$1`, actor.UserID); err != nil {
						t.Error(err)
					}
				}
				citation := "E1"
				if scenario == "fabricated-citation" {
					citation = "fabricated"
				}
				content, _ := json.Marshal(map[string]any{"text": "Director approval.", "citation_ids": []string{citation}})
				_ = json.NewEncoder(w).Encode(map[string]any{"model": "fake", "choices": []any{map[string]any{"index": 0, "finish_reason": "stop", "message": map[string]any{"role": "assistant", "content": string(content)}}}, "usage": map[string]int{"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15}})
			}))
			defer vendor.Close()
			route := modeladapter.Route{ProtocolVersion: 1, Type: "openai-compatible", BaseURL: vendor.URL + "/v1", CredentialRef: "CHAT_KEY", Models: []modeladapter.ModelRule{{ModelID: "fake", Purpose: "GENERATE", Limits: profile.Limits}}}
			cfg := modeladapter.Config{SchemaVersion: 1, Routes: []modeladapter.Route{route}}
			upstream, err := modeladapter.NewOpenAIClient(cfg, func(string) string { return "synthetic-chat" }, vendor.Client().Transport)
			if err != nil {
				t.Fatal(err)
			}
			handler, err := modeladapter.NewHandler(cfg, "synthetic-internal", upstream)
			if err != nil {
				t.Fatal(err)
			}
			adapter := httptest.NewTLSServer(handler)
			defer adapter.Close()
			previous := http.DefaultTransport
			http.DefaultTransport = adapter.Client().Transport
			defer func() { http.DefaultTransport = previous }()
			path, err := modeladapter.RoutePath(route)
			if err != nil {
				t.Fatal(err)
			}
			provider, err := models.NewHTTPProvider(adapter.URL+path, "synthetic-internal", false)
			if err != nil {
				t.Fatal(err)
			}
			gateway := &models.Gateway{Policy: &models.Policy{Enabled: true, Authority: models.DatabaseAuthority{DB: db}}, Operations: &models.OperationStore{DB: gatewayLogin(t, db)}, Providers: map[string]models.Provider{"fake": provider}}
			service := AnswerService{Search: search, Reader: NewPolicy(db), Policy: gateway.Policy, Gateway: gateway, Profile: profile}
			if scenario == "revoked-before" {
				if _, err = db.Exec(`DELETE FROM core.user_roles WHERE user_id=$1`, actor.UserID); err != nil {
					t.Fatal(err)
				}
			}
			response, err := service.Answer(context.Background(), actor, AnswerRequest{Scope: Scope{Kind: "APP", AppID: "library"}, Query: "Director"})
			switch scenario {
			case "grounded":
				if err != nil || response.Answer == nil || len(response.Citations) == 0 || calls.Load() != 1 {
					t.Fatal("grounded hosted answer missing", response, err, calls.Load())
				}
			case "revoked-before":
				if calls.Load() != 0 || response.Answer != nil {
					t.Fatal("revoked actor caused egress", response, err, calls.Load())
				}
			default:
				if err != nil || response.Answer != nil || calls.Load() != 1 {
					t.Fatal("invalid hosted answer exposed", response, err, calls.Load())
				}
			}
		})
	}
}

// Exercise provider selection through the deployment selection and a recovered
// PERSONAL lease, with different credentials on both adapter TLS listeners. The
// retired slot path is not used: the live selection is the authority.
func TestHostedRuntimeIndependentProvidersAndPinnedLease(t *testing.T) {
	f := newPersonalHybridFixture(t)
	var oldCalls, newCalls, generateCalls atomic.Int32
	embedVendor := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		vector := []float32{1, 0, 0}
		switch r.URL.Path {
		case "/v1/embeddings":
			oldCalls.Add(1)
			if r.Header.Get("Authorization") != "Bearer vendor-old" {
				t.Error("old embedding credential mismatch")
				w.WriteHeader(401)
				return
			}
		case "/v2/embeddings":
			newCalls.Add(1)
			if r.Header.Get("Authorization") != "Bearer vendor-new" {
				t.Error("new embedding credential mismatch")
				w.WriteHeader(401)
				return
			}
			vector = []float32{1, 0, 0, 0, 0}
		default:
			t.Error("embedding sent to wrong wire path")
			w.WriteHeader(400)
			return
		}
		var body struct {
			Model string `json:"model"`
			Input string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Model != "fake" || body.Input == "" {
			t.Error("invalid embedding request", err)
			w.WriteHeader(400)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"model": "fake", "data": []any{map[string]any{"index": 0, "embedding": vector}}, "usage": map[string]int{"prompt_tokens": 10, "total_tokens": 10}})
	}))
	defer embedVendor.Close()
	generateVendor := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		generateCalls.Add(1)
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer vendor-chat" {
			t.Error("generation routed to wrong provider")
			w.WriteHeader(400)
			return
		}
		var body struct {
			Model    string `json:"model"`
			Messages []any  `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Model != "fake" || len(body.Messages) != 2 {
			t.Error("invalid generation request", err)
			w.WriteHeader(400)
			return
		}
		content := `{"text":"The synthetic project code is BLUE-17.","citation_ids":["E1"]}`
		_ = json.NewEncoder(w).Encode(map[string]any{"model": "fake", "choices": []any{map[string]any{"index": 0, "finish_reason": "stop", "message": map[string]any{"role": "assistant", "content": content}}}, "usage": map[string]int{"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15}})
	}))
	defer generateVendor.Close()
	limits := models.Limits{MaxInputTokens: 8192, MaxOutputTokens: 512, MaxBytes: 16384, TimeoutMS: 1000}
	oldRoute := modeladapter.Route{ProtocolVersion: 1, Type: "openai-compatible", BaseURL: embedVendor.URL + "/v1", CredentialRef: "UPSTREAM_OLD", Models: []modeladapter.ModelRule{{ModelID: "fake", Purpose: "EMBED", Dimension: 3, Limits: limits}}}
	newRoute := modeladapter.Route{ProtocolVersion: 1, Type: "openai-compatible", BaseURL: embedVendor.URL + "/v2", CredentialRef: "UPSTREAM_NEW", Models: []modeladapter.ModelRule{{ModelID: "fake", Purpose: "EMBED", Dimension: 5, Limits: limits}}}
	chatRoute := modeladapter.Route{ProtocolVersion: 1, Type: "openai-compatible", BaseURL: generateVendor.URL + "/v1", CredentialRef: "UPSTREAM_CHAT", Models: []modeladapter.ModelRule{{ModelID: "fake", Purpose: "GENERATE", Limits: limits}}}
	secrets := map[string]string{"UPSTREAM_OLD": "vendor-old", "UPSTREAM_NEW": "vendor-new", "UPSTREAM_CHAT": "vendor-chat", "TOKEN_OLD": "internal-embed", "TOKEN_NEW": "internal-embed", "TOKEN_CHAT": "internal-chat"}
	upstream, err := modeladapter.NewOpenAIClient(modeladapter.Config{SchemaVersion: 1, Routes: []modeladapter.Route{oldRoute, newRoute, chatRoute}}, func(k string) string { return secrets[k] }, embedVendor.Client().Transport)
	if err != nil {
		t.Fatal(err)
	}
	embedConfig := modeladapter.Config{SchemaVersion: 1, Routes: []modeladapter.Route{oldRoute, newRoute}}
	handler, err := modeladapter.NewHandler(embedConfig, "internal-embed", upstream)
	if err != nil {
		t.Fatal(err)
	}
	var embedHandler atomic.Value
	embedHandler.Store(handler)
	embedAdapter := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { embedHandler.Load().(http.Handler).ServeHTTP(w, r) }))
	defer embedAdapter.Close()
	handler, err = modeladapter.NewHandler(modeladapter.Config{SchemaVersion: 1, Routes: []modeladapter.Route{chatRoute}}, "internal-chat", upstream)
	if err != nil {
		t.Fatal(err)
	}
	chatAdapter := httptest.NewTLSServer(handler)
	defer chatAdapter.Close()
	prior := http.DefaultTransport
	http.DefaultTransport = embedAdapter.Client().Transport
	defer func() { http.DefaultTransport = prior }()
	oldPath, err := modeladapter.RoutePath(oldRoute)
	if err != nil {
		t.Fatal(err)
	}
	newPath, err := modeladapter.RoutePath(newRoute)
	if err != nil {
		t.Fatal(err)
	}
	chatPath, err := modeladapter.RoutePath(chatRoute)
	if err != nil {
		t.Fatal(err)
	}
	oldProvider, err := models.NewHTTPProvider(embedAdapter.URL+oldPath, "internal-embed", false)
	if err != nil {
		t.Fatal(err)
	}
	newProvider, err := models.NewHTTPProvider(embedAdapter.URL+newPath, "internal-embed", false)
	if err != nil {
		t.Fatal(err)
	}
	chatProvider, err := models.NewHTTPProvider(chatAdapter.URL+chatPath, "internal-chat", false)
	if err != nil {
		t.Fatal(err)
	}
	oldProfile := models.Profile{ID: "hosted-embed", Revision: 2, Purpose: "EMBED", Execution: "HOSTED", ProviderID: "embed-old", ModelID: "fake", CredentialRef: "TOKEN_OLD", Dimension: 3, Limits: limits}
	newProfile := models.Profile{ID: "hosted-embed", Revision: 3, Purpose: "EMBED", Execution: "HOSTED", ProviderID: "embed-new", ModelID: "fake", CredentialRef: "TOKEN_NEW", Dimension: 5, Limits: limits}
	chatProfile := models.Profile{ID: "hosted-chat", Revision: 1, Purpose: "GENERATE", Execution: "HOSTED", ProviderID: "chat-provider", ModelID: "fake", CredentialRef: "TOKEN_CHAT", Limits: limits}
	// Both purposes are independently configured and independently provided; the
	// embedding purpose starts on the older revision.
	selectDeployment(t, f.db, oldProfile, chatProfile)
	f.db.Exec(`INSERT INTO cap_ai.processing_policy_profiles(policy_id,policy_version,model_profile_id,profile_revision) VALUES('local-only',1,'hosted-embed',2) ON CONFLICT DO NOTHING`)
	var database string
	if err = f.db.QueryRow(`SELECT current_database()`).Scan(&database); err != nil {
		t.Fatal(err)
	}
	gatewayDSN, err := url.Parse(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	gatewayDSN.Path = "/" + database
	gatewayDSN.User = url.User("dxos_ai_gateway")
	gateway := &models.Gateway{
		Policy:     &models.Policy{Enabled: true, Authority: models.DatabaseAuthority{DB: testutil.As(t, gatewayDSN.String(), "dxos_ai_gateway")}},
		Operations: &models.OperationStore{DB: testutil.As(t, gatewayDSN.String(), "dxos_ai_gateway")},
		Providers:  map[string]models.Provider{"embed-old": oldProvider, "embed-new": newProvider, "chat-provider": chatProvider},
	}
	for _, store := range []*Store{f.api, f.worker} {
		store.ConfigureModels(gateway, oldProfile, chatProfile)
	}
	for _, approval := range []struct {
		profile models.Profile
	}{{oldProfile}, {chatProfile}} {
		policy, _ := json.Marshal(map[string]any{"profile_id": approval.profile.ID, "profile_revision": approval.profile.Revision, "provider_id": approval.profile.ProviderID, "purpose": approval.profile.Purpose, "source_policy_id": "local-only", "source_policy_version": 1, "source_classification": "PERSONAL", "query_app_id": "library", "query_classification": "PERSONAL", "retention_evidence": "fixture", "training_use_evidence": "fixture", "region_evidence": "fixture"})
		if _, err = f.admin.Exec(`SELECT cap_ai.approve_agent_processing($1,$2::jsonb)`, f.owner.UserID, string(policy)); err != nil {
			t.Fatal(err)
		}
	}
	f.activate(t, f.owner, "dual-provider", "The synthetic project code is BLUE-17.")
	if err = f.api.EnqueueEmbeddings(context.Background(), f.owner); err != nil {
		t.Fatal(err)
	}
	lease, err := f.worker.ClaimEmbedding(context.Background(), ownerID, oldProfile)
	if err != nil || lease == nil {
		t.Fatal(lease, err)
	}
	var work embeddingWork
	if err = decodeRow(f.worker.db.QueryRow(`SELECT cap_ai.embedding_work($1,$2,$3,$4,$5,$6)`, lease.JobID, lease.WorkerID, lease.Fence, lease.RunID, oldProfile.ID, oldProfile.Revision), &work); err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.Exec(`UPDATE cap_ai.ingestion_jobs SET lease_until=now()-interval '1 second',next_attempt_at=now() WHERE id=$1`, lease.JobID); err != nil {
		t.Fatal(err)
	}
	// The deployment moves to the newer revision while the older lease is free. The
	// worker holding the older profile cannot resume another revision's job, so the
	// old lease is recovered by the old route exactly once.
	if err = (&Worker{Store: f.worker, WorkerID: ownerID}).Once(context.Background()); err != nil {
		t.Fatal("recover old lease", err)
	}
	if oldCalls.Load() != 1 || newCalls.Load() != 0 || generateCalls.Load() != 0 {
		t.Fatal("old lease changed provider", oldCalls.Load(), newCalls.Load(), generateCalls.Load())
	}
	// Selecting the newer revision makes its route the live one, and the older
	// profile can no longer claim or dispatch.
	selectDeployment(t, f.db, newProfile, chatProfile)
	policy, _ := json.Marshal(map[string]any{"profile_id": newProfile.ID, "profile_revision": newProfile.Revision, "provider_id": newProfile.ProviderID, "purpose": newProfile.Purpose, "source_policy_id": "local-only", "source_policy_version": 1, "source_classification": "PERSONAL", "query_app_id": "library", "query_classification": "PERSONAL", "retention_evidence": "fixture", "training_use_evidence": "fixture", "region_evidence": "fixture"})
	if _, err = f.admin.Exec(`SELECT cap_ai.approve_agent_processing($1,$2::jsonb)`, f.owner.UserID, string(policy)); err != nil {
		t.Fatal(err)
	}
	f.db.Exec(`INSERT INTO cap_ai.processing_policy_profiles(policy_id,policy_version,model_profile_id,profile_revision) VALUES('local-only',1,'hosted-embed',3) ON CONFLICT DO NOTHING`)
	if _, err = f.db.Exec(`UPDATE cap_ai.ingestion_jobs SET state='PENDING',next_attempt_at=now() WHERE id=$1`, lease.JobID); err != nil {
		t.Fatal(err)
	}
	if stale, err := f.worker.ClaimEmbedding(context.Background(), ownerID, oldProfile); err != nil || stale != nil {
		t.Fatal("superseded profile claimed a job", stale, err)
	}
	for _, store := range []*Store{f.api, f.worker} {
		store.ConfigureModels(gateway, newProfile, chatProfile)
	}
	if err = (&Worker{Store: f.worker, WorkerID: ownerID}).Once(context.Background()); err != nil {
		t.Fatal("build new revision", err)
	}
	if oldCalls.Load() != 1 || newCalls.Load() != 1 || generateCalls.Load() != 0 {
		t.Fatal("new revision used the old provider", oldCalls.Load(), newCalls.Load(), generateCalls.Load())
	}
	service := AnswerService{Search: f.api, Reader: NewPolicy(f.api.db), Policy: gateway.Policy, Gateway: gateway, Profile: chatProfile}
	answer, err := service.Answer(context.Background(), f.owner, AnswerRequest{Scope: Scope{Kind: "PERSONAL"}, Query: "BLUE", RequireGeneration: true})
	if err != nil || answer.Status != "ANSWERED" || answer.Answer == nil || !strings.Contains(*answer.Answer, "BLUE-17") {
		t.Fatal("runtime answer", answer, err)
	}
	if oldCalls.Load() != 1 || newCalls.Load() != 2 || generateCalls.Load() != 1 {
		t.Fatal("runtime provider dispatch mismatch", oldCalls.Load(), newCalls.Load(), generateCalls.Load())
	}
}
