// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/opendx-os/opendx-os/capabilities/internal/ai/access"
	"github.com/opendx-os/opendx-os/capabilities/internal/ai/models"
	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
)

type verifierStub struct{}

func (verifierStub) VerifySubject(context.Context, string) (auth.Subject, error) {
	return auth.Subject{UserID: "22222222-2222-4222-8222-222222222222", ExpiresAt: time.Now().Add(time.Hour)}, nil
}

type membershipStub struct{}

func (membershipStub) Resolve(_ context.Context, s auth.Subject, role string) (access.Principal, error) {
	if role != "role_reader" {
		return access.Principal{}, access.ErrDenied
	}
	return access.Principal{UserID: s.UserID, Role: role}, nil
}
func testAuth() access.Authenticator {
	return access.Authenticator{Verifier: verifierStub{}, Membership: membershipStub{}}
}
func TestLegacyRAGUnavailable(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "app", "knowledge"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "app", "knowledge", "secret.md"), []byte("PRIVATE-SECRET-PHRASE"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("APPS_DIR", dir)
	h := newHandler(testAuth())
	for _, body := range []string{`{"app_name":"app","query":"PRIVATE"}`, `{"app_name":"../app","query":"PRIVATE"}`, "malformed"} {
		for _, authenticated := range []bool{false, true} {
			r := httptest.NewRequest("POST", "/api/cap/ai/rag", strings.NewReader(body))
			if authenticated {
				r.Header.Set("Authorization", "Bearer token")
				r.Header.Set("X-Hasura-Role", "role_reader")
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != 503 {
				t.Fatalf("%d %s", w.Code, w.Body.String())
			}
			checkUnavailable(t, w)
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/healthz", nil))
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
}
func checkUnavailable(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	var b struct {
		Error struct{ Code, Message string }
	}
	if err := json.Unmarshal(w.Body.Bytes(), &b); err != nil {
		t.Fatal(err)
	}
	if b.Error.Code != "CAP_AI_UNAVAILABLE" || b.Error.Message != "AI content processing is unavailable" {
		t.Fatalf("unexpected body %s", w.Body.String())
	}
}
func TestExtractUnavailable(t *testing.T) {
	h := newHandler(testAuth())
	for _, tc := range []struct {
		token, role string
		status      int
	}{{"", "", 401}, {"Bearer token", "role_reader", 503}, {"Bearer token", "role_other", 403}} {
		r := httptest.NewRequest("POST", "/api/cap/ai/extract", strings.NewReader(`{"file_id":"guessed"}`))
		r.Header.Set("Authorization", tc.token)
		r.Header.Set("X-Hasura-Role", tc.role)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("%d %s", w.Code, w.Body.String())
		}
		if tc.status == 503 {
			checkUnavailable(t, w)
		}
	}
}
func TestLoadConfig(t *testing.T) {
	settings := map[string]string{"DATABASE_URL": "postgres://test:SECRET@localhost/test", "OIDC_ISSUER_URL": "https://issuer.test", "OIDC_JWKS_URL": "https://issuer.test/keys", "OIDC_AUDIENCE": "runtime"}
	getenv := func(k string) string { return settings[k] }
	c, err := loadConfig(getenv)
	if err != nil || c.Port != "8083" || c.DatabaseURL != settings["DATABASE_URL"] || c.Audience != "runtime" {
		t.Fatalf("config failed: %v", err)
	}
	for _, key := range []string{"DATABASE_URL", "OIDC_ISSUER_URL", "OIDC_JWKS_URL", "OIDC_AUDIENCE"} {
		old := settings[key]
		for _, blank := range []string{"", "  "} {
			settings[key] = blank
			if _, err = loadConfig(getenv); err == nil || strings.Contains(err.Error(), "SECRET") {
				t.Fatalf("invalid %s accepted or leaked: %v", key, err)
			}
		}
		settings[key] = old
	}
	settings["PORT"] = "9876"
	c, err = loadConfig(getenv)
	if err != nil || c.Port != "9876" {
		t.Fatal("explicit port lost")
	}
	for _, bad := range []string{"0", "-1", "65536", "invalid"} {
		settings["PORT"] = bad
		if _, err = loadConfig(getenv); err == nil {
			t.Fatal(errors.New("invalid port accepted"))
		}
	}
}

func TestMetadataRoutesDefaultClosed(t *testing.T) {
	h := newHandler(testAuth())
	for _, path := range []string{"/api/cap/ai/documents", "/api/cap/ai/collections?kind=PERSONAL", "/api/cap/ai/ingestions/00000000-0000-0000-0000-000000000001"} {
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("Authorization", "Bearer token")
		r.Header.Set("X-Hasura-Role", "role_reader")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 503 {
			t.Fatalf("unqualified metadata route open: %s %d", path, w.Code)
		}
	}
}

func TestAppKnowledgeFrontDoorIsAuthenticatedAndDefaultClosed(t *testing.T) {
	h := newHandler(testAuth())
	for _, test := range []struct {
		token string
		want  int
	}{{"", 401}, {"Bearer token", 503}} {
		req := httptest.NewRequest("GET", "/api/cap/ai/app-knowledge?app_id=example", nil)
		req.Header.Set("Authorization", test.token)
		req.Header.Set("X-Hasura-Role", "role_reader")
		response := httptest.NewRecorder()
		h.ServeHTTP(response, req)
		if response.Code != test.want {
			t.Fatalf("app knowledge front door: got %d want %d", response.Code, test.want)
		}
	}
}

func TestChatHTTPDefaultClosedIndependentlyOfNativeKnowledge(t *testing.T) {
	h := newHandler(testAuth())
	for _, path := range []string{"/api/cap/ai/conversations", "/api/cap/ai/conversations/22222222-2222-4222-8222-222222222222"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 503 || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("disabled chat route %s: got %d %s", path, w.Code, w.Body.String())
		}
	}
	for _, endpoint := range []struct{ method, path string }{
		{"POST", "/api/cap/ai/search"},
		{"POST", "/api/cap/ai/answers"},
		{"GET", "/api/cap/ai/documents/22222222-2222-4222-8222-222222222222/citations/22222222-2222-4222-8222-222222222222"},
	} {
		r := httptest.NewRequest(endpoint.method, endpoint.path, nil)
		r.Header.Set("Authorization", "Bearer token")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatalf("native route accepted absent selected role: %s, got %d", endpoint.path, w.Code)
		}
	}
}

// The retired AI_AGENT_BILLING_APP_ID setting is unsupported: it must never be
// accepted as a fallback for the current AI_AGENT_PROCESSING_APP_ID name.
func TestProcessingAppConfiguration(t *testing.T) {
	settings := map[string]string{"AI_AGENT_PROCESSING_APP_ID": "personal_library"}
	getenv := func(k string) string { return settings[k] }
	if got := processingAppID(getenv); got != "personal_library" {
		t.Fatalf("processing app from configuration: %q", got)
	}
	delete(settings, "AI_AGENT_PROCESSING_APP_ID")
	settings["AI_AGENT_BILLING_APP_ID"] = "legacy_library"
	if got := processingAppID(getenv); got != "" {
		t.Fatalf("legacy billing setting was accepted as a fallback: %q", got)
	}
	settings["AI_AGENT_PROCESSING_APP_ID"] = "  "
	if got := processingAppID(getenv); got != "" {
		t.Fatalf("blank processing app accepted: %q", got)
	}
}

func TestChatHTTPRequiresFeatureAndQualifiedMetadata(t *testing.T) {
	flags := map[string]string{
		"RAG_AGENT_ENABLED":      "true",
		"RAG_METADATA_ENABLED":   "true",
		"RAG_SOURCE_QUALIFIED":   "true",
		"RAG_METADATA_QUALIFIED": "true",
		"RAG_PARSER_QUALIFIED":   "true",
		"RAG_PACKAGE_ENABLED":    "true",
	}
	getenv := func(name string) string { return flags[name] }
	if !chatEnabled(getenv) {
		t.Fatal("qualified chat remains unavailable")
	}
	for flag := range flags {
		flags[flag] = ""
		if chatEnabled(getenv) {
			t.Errorf("chat opens without %s", flag)
		}
		flags[flag] = "true"
	}
}

func TestChatHTTPRequiresAnEffectiveSourceKind(t *testing.T) {
	flags := map[string]string{
		"RAG_AGENT_ENABLED":      "true",
		"RAG_METADATA_ENABLED":   "true",
		"RAG_SOURCE_QUALIFIED":   "true",
		"RAG_METADATA_QUALIFIED": "true",
		"RAG_PARSER_QUALIFIED":   "true",
	}
	getenv := func(name string) string { return flags[name] }
	if chatEnabled(getenv) {
		t.Error("chat opens with both source kinds disabled")
	}
	flags["RAG_PERSONAL_ENABLED"] = "true"
	delete(flags, "RAG_PARSER_QUALIFIED")
	if chatEnabled(getenv) {
		t.Error("chat opens with an unqualified PERSONAL source and APP disabled")
	}
	flags["RAG_PARSER_QUALIFIED"] = "true"
	if !chatEnabled(getenv) {
		t.Error("chat remains unavailable with qualified PERSONAL sources")
	}
}

func TestChatHTTPCachePolicyDuringPathNormalization(t *testing.T) {
	h := newHandler(testAuth())
	for _, path := range []string{
		"/api/cap/ai/conversations//test?cursor=private",
		"/api/cap/ai/conversations/./test",
		"/api/cap/ai/./conversations",
		"/api/cap/ai/%2e/../%63onversations//test?cursor=private",
	} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("chat path %q returned status %d without no-store", path, w.Code)
		}
	}
}

// TestRuntimeModelGateKeepsBootAvailable fixes the composition contract of the
// service start-up: the model gateway is optional, and a valid declaration whose
// live state is not reconciled leaves the process booting with model calls
// unavailable rather than failing. Only a declaration the runtime cannot accept is
// a startup error. With no gateway the search and answer paths keep their
// authorized lexical behaviour, which the knowledge and agent suites cover.
func TestRuntimeModelGateKeepsBootAvailable(t *testing.T) {
	valid := "enabled: true\nembedding:\n  model: 'ollama/embed-model'\n  execution: local\n  api_base: http://ollama:11434\n  revision: 'embed-revision'\n  dimension: 8\n"
	disabled := "enabled: false\n" + strings.TrimPrefix(valid, "enabled: true\n")
	base := map[string]string{
		"RAG_MODELS_ENABLED":         "true",
		"RAG_SOURCE_QUALIFIED":       "true",
		"RAG_METADATA_QUALIFIED":     "true",
		"RAG_PARSER_QUALIFIED":       "true",
		"RAG_LOCAL_MODELS_QUALIFIED": "true",
		"RAG_MODEL_LITELLM_ENDPOINT": "http://litellm:4000",
		"LITELLM_MASTER_KEY":         "runtime-proxy-credential",
	}
	write := func(t *testing.T, declaration string) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), "models.yaml")
		if err := os.WriteFile(path, []byte(declaration), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	for _, tc := range []struct {
		name        string
		declaration string
		gatewayDSN  string
	}{
		{"no model configuration", "", ""},
		{"disabled declaration", disabled, ""},
		{"enabled declaration without a binding", valid, ""},
		{"enabled declaration with an unreachable authority", valid, "postgres://gateway@127.0.0.1:1/test"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := map[string]string{}
			for key, value := range base {
				env[key] = value
			}
			env["AI_GATEWAY_DATABASE_URL"] = tc.gatewayDSN
			if tc.declaration != "" {
				env["RAG_MODEL_DEPLOYMENT_FILE"] = write(t, tc.declaration)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			gateway, embed, generate, closeRuntime, err := models.RuntimeFromEnvironment(ctx, func(key string) string { return env[key] })
			defer closeRuntime()
			if err != nil || gateway != nil || embed.ID != "" || generate.ID != "" {
				t.Fatalf("startup gate: %v %v %q %q", err, gateway, embed.ID, generate.ID)
			}
		})
	}
	t.Run("an invalid declaration is still a startup error", func(t *testing.T) {
		env := map[string]string{}
		for key, value := range base {
			env[key] = value
		}
		env["RAG_MODEL_DEPLOYMENT_FILE"] = write(t, "not a declaration at all")
		if _, _, _, closeRuntime, err := models.RuntimeFromEnvironment(context.Background(), func(key string) string { return env[key] }); err == nil {
			closeRuntime()
			t.Fatal("an invalid declaration booted")
		}
	})
}
