// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package access

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type verifyFunc func(context.Context, string) (auth.Subject, error)

func (f verifyFunc) VerifySubject(c context.Context, s string) (auth.Subject, error) { return f(c, s) }

type memberFunc func(context.Context, auth.Subject, string) (Principal, error)

func (f memberFunc) Resolve(c context.Context, s auth.Subject, r string) (Principal, error) {
	return f(c, s, r)
}

const testUser = "22222222-2222-4222-8222-222222222222"

func TestRAGAuthentication(t *testing.T) {
	for _, tc := range []struct {
		name, token, role string
		status            int
	}{
		{"valid", "Bearer identity-token", "role_reader", 204}, {"missing", "", "role_reader", 401}, {"malformed", "Basic secret", "role_reader", 401}, {"invalid", "Bearer invalid", "role_reader", 401}, {"no-role", "Bearer identity-token", "", 403}, {"ungranted", "Bearer identity-token", "role_admin", 403}, {"outage", "Bearer identity-token", "role_offline", 503}, {"cancelled", "Bearer identity-token", "role_reader", 401}, {"expired", "Bearer identity-token", "role_reader", 401}, {"duplicate-token", "Bearer identity-token", "role_reader", 401}, {"duplicate-role", "Bearer identity-token", "role_reader", 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			verified, resolved, called := 0, 0, false
			a := Authenticator{Verifier: verifyFunc(func(ctx context.Context, token string) (auth.Subject, error) {
				verified++
				if token != "identity-token" {
					return auth.Subject{}, errors.New("SECRET-verifier")
				}
				s := auth.Subject{UserID: testUser, ExpiresAt: time.Now().Add(time.Hour)}
				if tc.name == "expired" {
					s.ExpiresAt = time.Now().Add(-time.Hour)
				}
				return s, nil
			}), Membership: memberFunc(func(ctx context.Context, s auth.Subject, role string) (Principal, error) {
				resolved++
				if s.UserID != testUser {
					t.Fatal("spoofed user")
				}
				if role == "role_offline" {
					return Principal{}, ErrUnavailable
				}
				if role != "role_reader" {
					return Principal{}, ErrDenied
				}
				return Principal{UserID: testUser, Role: role}, nil
			})}
			h := a.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				p, ok := FromContext(r.Context())
				if !ok || p != (Principal{UserID: testUser, Role: "role_reader"}) {
					t.Fatalf("bad principal %+v", p)
				}
				w.WriteHeader(204)
			}))
			req := httptest.NewRequest("POST", "/", nil)
			if tc.token != "" {
				req.Header.Set("Authorization", tc.token)
			}
			req.Header.Set("X-Hasura-Role", tc.role)
			req.Header.Set("X-Hasura-User-Id", "forged")
			req.Header.Set("X-Hasura-Workspace-Id", "forged")
			req.Header.Set("X-Hasura-Allowed-Roles", "role_admin")
			if tc.name == "duplicate-token" {
				req.Header.Add("Authorization", tc.token)
			}
			if tc.name == "duplicate-role" {
				req.Header.Add("X-Hasura-Role", tc.role)
			}
			if tc.name == "cancelled" {
				ctx, cancel := context.WithCancel(req.Context())
				cancel()
				req = req.WithContext(ctx)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if w.Code != tc.status || called != (tc.status == 204) {
				t.Fatalf("status %d handler %v body %s", w.Code, called, w.Body.String())
			}
			if tc.status != 204 {
				var body struct {
					Error struct{ Code, Message string }
				}
				if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				if body.Error.Code != http.StatusText(tc.status) || body.Error.Message != http.StatusText(tc.status) {
					t.Fatalf("unsafe error %s", w.Body.String())
				}
			}
			if (tc.name == "missing" || tc.name == "malformed" || tc.name == "cancelled") && verified != 0 {
				t.Fatal("unnecessary verification")
			}
			if (tc.name == "expired" || tc.name == "invalid") && resolved != 0 {
				t.Fatal("resolved invalid identity")
			}
		})
	}
}
func TestRAGMissingDependencies(t *testing.T) {
	for _, a := range []Authenticator{{}, {Verifier: verifyFunc(func(context.Context, string) (auth.Subject, error) {
		t.Fatal("membership missing")
		return auth.Subject{}, nil
	})}} {
		w := httptest.NewRecorder()
		a.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("handler invoked") })).ServeHTTP(w, httptest.NewRequest("POST", "/", nil))
		if w.Code != 503 {
			t.Fatalf("got %d", w.Code)
		}
	}
	if _, ok := FromContext(context.Background()); ok {
		t.Fatal("unexpected principal")
	}
}
