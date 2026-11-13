// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func subjectFixture(t *testing.T) (SubjectVerifier, func(map[string]any) string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]string{"kty": "RSA", "kid": "subject", "alg": "RS256", "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())}}})
	}))
	t.Cleanup(server.Close)
	verifier, err := NewSubjectVerifier(context.Background(), VerifierConfig{IssuerURL: "https://issuer.test", Audience: "runtime", JWKSURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	return verifier, func(c map[string]any) string { return signed(t, key, "subject", c) }
}
func subjectClaims() map[string]any {
	return map[string]any{"iss": "https://issuer.test", "aud": "runtime", "sub": user, "exp": time.Now().Add(time.Hour).Unix()}
}
func TestSubjectOnlyToken(t *testing.T) {
	v, sign := subjectFixture(t)
	c := subjectClaims()
	got, err := v.VerifySubject(context.Background(), sign(c))
	if err != nil || got.UserID != user || got.ExpiresAt.Unix() != c["exp"].(int64) {
		t.Fatalf("%+v %v", got, err)
	}
	c["sub"] = strings.ToUpper(user)
	c["aud"] = []string{"other", "runtime"}
	got, err = v.VerifySubject(context.Background(), sign(c))
	if err != nil || got.UserID != user {
		t.Fatalf("%+v %v", got, err)
	}
}
func TestSubjectInvalidTokens(t *testing.T) {
	v, sign := subjectFixture(t)
	for _, tc := range []struct {
		name   string
		change func(map[string]any)
	}{
		{"issuer", func(c map[string]any) { c["iss"] = "other" }}, {"audience", func(c map[string]any) { c["aud"] = "other" }},
		{"expired", func(c map[string]any) { c["exp"] = time.Now().Add(-time.Second).Unix() }}, {"missing-exp", func(c map[string]any) { delete(c, "exp") }},
		{"future", func(c map[string]any) { c["nbf"] = time.Now().Add(time.Hour).Unix() }}, {"subject", func(c map[string]any) { c["sub"] = "invalid" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := subjectClaims()
			tc.change(c)
			got, err := v.VerifySubject(context.Background(), sign(c))
			if err == nil || got != (Subject{}) {
				t.Fatalf("accepted: %+v %v", got, err)
			}
		})
	}
	good := sign(subjectClaims())
	parts := strings.Split(good, ".")
	parts[2] = base64.RawURLEncoding.EncodeToString([]byte("wrong-signature"))
	for _, token := range []string{"", "malformed", strings.Repeat("x", 32769), strings.Join(parts, ".")} {
		got, err := v.VerifySubject(context.Background(), token)
		if err == nil || got != (Subject{}) {
			t.Fatalf("accepted invalid token: %+v %v", got, err)
		}
	}
}
func TestSubjectIgnoresBusinessClaims(t *testing.T) {
	v, sign := subjectFixture(t)
	c := subjectClaims()
	c["https://hasura.io/jwt/claims"] = map[string]any{"x-hasura-user-id": "another-user", "x-hasura-workspace-id": "invalid", "x-hasura-allowed-roles": []string{"role_operator"}}
	got, err := v.VerifySubject(context.Background(), sign(c))
	if err != nil || got.UserID != user {
		t.Fatalf("%+v %v", got, err)
	}
}
func TestSubjectCancelled(t *testing.T) {
	v, sign := subjectFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got, err := v.VerifySubject(ctx, sign(subjectClaims()))
	if err == nil || got != (Subject{}) {
		t.Fatalf("cancelled request accepted: %+v %v", got, err)
	}
}
func TestSubjectUnavailableKeys(t *testing.T) {
	v, sign := subjectFixture(t)
	token := sign(subjectClaims())
	raw := v.(*jwtVerifier)
	raw.mu.Lock()
	raw.lastRefresh = time.Now().Add(-6 * time.Minute)
	raw.cfg.JWKSURL = "http://127.0.0.1:1"
	raw.mu.Unlock()
	got, err := v.VerifySubject(context.Background(), token)
	if err == nil || got != (Subject{}) {
		t.Fatalf("expired keys accepted: %+v %v", got, err)
	}
}
func TestSubjectUnknownKey(t *testing.T) {
	v, sign := subjectFixture(t)
	token := sign(subjectClaims())
	raw := v.(*jwtVerifier)
	raw.mu.Lock()
	raw.keys = map[string]*rsa.PublicKey{}
	raw.lastAttempt = time.Now()
	raw.mu.Unlock()
	got, err := v.VerifySubject(context.Background(), token)
	if err == nil || got != (Subject{}) {
		t.Fatalf("unknown key accepted: %+v %v", got, err)
	}
}
