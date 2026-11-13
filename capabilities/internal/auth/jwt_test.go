// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package auth

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

const user = "22222222-2222-4222-8222-222222222222"
const workspace = "11111111-1111-4111-8111-111111111111"

func signed(t *testing.T, k *rsa.PrivateKey, kid string, c map[string]any) string {
	t.Helper()
	h, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": kid})
	p, _ := json.Marshal(c)
	s := base64.RawURLEncoding.EncodeToString(h) + "." + base64.RawURLEncoding.EncodeToString(p)
	d := sha256.Sum256([]byte(s))
	sig, e := rsa.SignPKCS1v15(rand.Reader, k, crypto.SHA256, d[:])
	if e != nil {
		t.Fatal(e)
	}
	return s + "." + base64.RawURLEncoding.EncodeToString(sig)
}
func claims() map[string]any {
	return map[string]any{"iss": "https://issuer.test", "aud": "runtime", "sub": user, "exp": time.Now().Add(time.Hour).Unix(), "https://hasura.io/jwt/claims": map[string]any{"x-hasura-user-id": user, "x-hasura-workspace-id": workspace, "x-hasura-default-role": "role_user", "x-hasura-allowed-roles": []string{"role_user", "role_manager"}}}
}
func TestVerifiedJWTAndMiddleware(t *testing.T) {
	k, _ := rsa.GenerateKey(rand.Reader, 2048)
	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	kid := "one"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]string{"kty": "RSA", "alg": "RS256", "use": "sig", "kid": kid, "n": base64.RawURLEncoding.EncodeToString(k.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(k.E)).Bytes())}}}) // Best-effort response; the client may have disconnected.
	}))
	defer srv.Close()
	v, e := NewVerifier(context.Background(), VerifierConfig{IssuerURL: "https://issuer.test", Audience: "runtime", JWKSURL: srv.URL})
	if e != nil {
		t.Fatal(e)
	}
	for _, tc := range []struct {
		name    string
		change  func(map[string]any)
		key     *rsa.PrivateKey
		missing bool
	}{
		{name: "valid", key: k}, {name: "missing", missing: true, key: k}, {name: "expired", key: k, change: func(c map[string]any) { c["exp"] = time.Now().Add(-time.Minute).Unix() }}, {name: "issuer", key: k, change: func(c map[string]any) { c["iss"] = "other" }}, {name: "audience", key: k, change: func(c map[string]any) { c["aud"] = "other" }}, {name: "signature", key: other}, {name: "subject", key: k, change: func(c map[string]any) { c["sub"] = "other" }}, {name: "workspace", key: k, change: func(c map[string]any) {
			c["https://hasura.io/jwt/claims"].(map[string]any)["x-hasura-workspace-id"] = "invalid"
		}}, {name: "default", key: k, change: func(c map[string]any) {
			c["https://hasura.io/jwt/claims"].(map[string]any)["x-hasura-default-role"] = "admin"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := claims()
			if tc.change != nil {
				tc.change(c)
			}
			token := signed(t, tc.key, "one", c)
			if tc.missing {
				token = ""
			}
			id, e := v.Verify(context.Background(), token)
			if tc.name == "valid" {
				if e != nil || id.UserID != user || id.WorkspaceID != workspace || id.Role != "role_user" {
					t.Fatalf("%+v %v", id, e)
				}
			} else if e == nil {
				t.Fatal("invalid token accepted")
			}
		})
	}
	kid = "rotated"
	if _, e := v.Verify(context.Background(), signed(t, k, kid, claims())); e != nil {
		t.Fatalf("rotation: %v", e)
	}
	for _, tc := range []struct {
		token, role string
		status      int
	}{{"", "role_user", 401}, {signed(t, k, kid, claims()), "admin", 403}, {signed(t, k, kid, claims()), "role_manager", 204}} {
		r := httptest.NewRequest("GET", "/", nil)
		r.Header.Set("Authorization", "Bearer "+tc.token)
		r.Header.Set("X-Hasura-Role", tc.role)
		r.Header.Set("X-Hasura-Workspace-Id", "spoof")
		r.Header.Set("X-Hasura-User-Id", "spoof")
		w := httptest.NewRecorder()
		Middleware(v, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id, e := ExtractIdentity(r)
			if e != nil || id.UserID != user || id.WorkspaceID != workspace || id.Role != "role_manager" {
				t.Errorf("identity %+v %v", id, e)
			}
			w.WriteHeader(204)
		})).ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("got %d want %d", w.Code, tc.status)
		}
	}
}

func TestRejectOversizedJWKS(t *testing.T) {
	k, _ := rsa.GenerateKey(rand.Reader, 2048)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]string{"kty": "RSA", "kid": "one", "n": base64.RawURLEncoding.EncodeToString(k.N.Bytes()), "e": "AQAB"}}}) // Best-effort response; the client may have disconnected.
		_, _ = w.Write(make([]byte, 1<<20))
	}))
	defer srv.Close()
	if _, err := NewVerifier(context.Background(), VerifierConfig{IssuerURL: "https://issuer.test", Audience: "runtime", JWKSURL: srv.URL}); err == nil {
		t.Fatal("oversized JWKS accepted")
	}
}

func TestKnownSigningKeysExpireAndRefresh(t *testing.T) {
	old, _ := rsa.GenerateKey(rand.Reader, 2048)
	replacement, _ := rsa.GenerateKey(rand.Reader, 2048)
	for _, mode := range []string{"removed", "same_kid_replacement", "unavailable"} {
		t.Run(mode, func(t *testing.T) {
			active := old
			status := http.StatusOK
			removed := false
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if status != http.StatusOK {
					w.WriteHeader(status)
					return
				}
				keys := []any{}
				if !removed {
					keys = append(keys, map[string]string{"kty": "RSA", "kid": "one", "alg": "RS256", "n": base64.RawURLEncoding.EncodeToString(active.N.Bytes()), "e": "AQAB"})
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"keys": keys}) // Best-effort response; the client may have disconnected.
			}))
			defer srv.Close()
			verifier, err := NewVerifier(context.Background(), VerifierConfig{IssuerURL: "https://issuer.test", Audience: "runtime", JWKSURL: srv.URL})
			if err != nil {
				t.Fatal(err)
			}
			token := signed(t, old, "one", claims())
			if _, err = verifier.Verify(context.Background(), token); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "removed":
				removed = true
			case "same_kid_replacement":
				active = replacement
			case "unavailable":
				status = 503
			}
			v := verifier.(*jwtVerifier)
			v.mu.Lock()
			v.lastRefresh = time.Now().Add(-6 * time.Minute)
			v.mu.Unlock()
			if _, err = verifier.Verify(context.Background(), token); err == nil {
				t.Fatal("stale signing key accepted")
			}
			if mode == "same_kid_replacement" {
				if _, err = verifier.Verify(context.Background(), signed(t, replacement, "one", claims())); err != nil {
					t.Fatalf("replacement rejected: %v", err)
				}
			}
			if mode == "unavailable" {
				if _, err = verifier.Verify(context.Background(), token); err == nil {
					t.Fatal("stale key accepted during refresh backoff")
				}
			}
		})
	}
}

// A Keycloak token after ADR 0007 carries identity only: no Hasura claims.
func TestAuthenticateReturnsIdentityWithoutRoles(t *testing.T) {
	k, _ := rsa.GenerateKey(rand.Reader, 2048)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]string{"kty": "RSA", "alg": "RS256", "use": "sig", "kid": "one", "n": base64.RawURLEncoding.EncodeToString(k.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(k.E)).Bytes())}}})
	}))
	defer srv.Close()
	a, e := NewAuthenticator(context.Background(), VerifierConfig{IssuerURL: "https://issuer.test", Audience: "runtime", JWKSURL: srv.URL})
	if e != nil {
		t.Fatal(e)
	}
	identity := func() map[string]any {
		return map[string]any{"iss": "https://issuer.test", "aud": []string{"runtime", "account"}, "sub": "22222222-2222-4222-8222-22222222222A",
			"exp": time.Now().Add(time.Hour).Unix(), "email": "alice@example.test", "name": "Alice Nguyen", "preferred_username": "alice"}
	}

	p, e := a.Authenticate(context.Background(), signed(t, k, "one", identity()))
	if e != nil {
		t.Fatal(e)
	}
	if p.Subject != "22222222-2222-4222-8222-22222222222a" || p.Email != "alice@example.test" || p.Name != "Alice Nguyen" {
		t.Fatalf("principal = %+v", p)
	}

	c := identity()
	delete(c, "name")
	if p, e = a.Authenticate(context.Background(), signed(t, k, "one", c)); e != nil || p.Name != "alice" {
		t.Fatalf("name must fall back to preferred_username: %+v, %v", p, e)
	}
	for name, change := range map[string]func(map[string]any){
		"no email": func(c map[string]any) { delete(c, "email") },
		"audience": func(c map[string]any) { c["aud"] = "other" },
		"expired":  func(c map[string]any) { c["exp"] = time.Now().Add(-time.Minute).Unix() },
		"subject":  func(c map[string]any) { c["sub"] = "alice" },
	} {
		c := identity()
		change(c)
		if _, e := a.Authenticate(context.Background(), signed(t, k, "one", c)); e == nil {
			t.Fatalf("%s: token must be rejected", name)
		}
	}
}
