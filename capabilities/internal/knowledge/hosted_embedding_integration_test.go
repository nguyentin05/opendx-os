//go:build integration

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package knowledge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/opendx-os/opendx-os/capabilities/internal/knowledge/modeladapter"
	"github.com/opendx-os/opendx-os/capabilities/internal/knowledge/models"
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
	if err = api.EnqueueEmbeddings(ctx, actor, profile); err != nil {
		t.Fatal(err)
	}
	lease, err := worker.ClaimEmbedding(ctx, ownerID)
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
	if _, err = db.Exec(`INSERT INTO cap_ai.model_profiles(id,revision,purpose,execution,provider_id,model_id,dimension,limits,enabled) VALUES('embed',2,'EMBED','HOSTED','fake','fake',5,$1,true)`, string(limits)); err != nil {
		t.Fatal(err)
	}
	newer := profile
	newer.Revision = 2
	newer.Dimension = 5
	if err = api.EnqueueEmbeddings(ctx, actor, newer); err != nil {
		t.Fatal(err)
	}
	if lease, err = worker.ClaimEmbedding(ctx, ownerID); err != nil || lease != nil {
		t.Fatal("unapproved revision scheduled", lease, err)
	}
	if calls.Load() != 1 {
		t.Fatal("unapproved revision dispatched")
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
			service := AnswerService{Search: search, Reader: NewPolicy(db), Policy: gateway.Policy, Gateway: gateway, Resolver: &fixedResolver{profile: profile}}
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
