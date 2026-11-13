// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package main

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/opendx-os/opendx-os/capabilities/internal/ai/access"
	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
