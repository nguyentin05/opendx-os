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
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

type Verifier interface {
	Verify(context.Context, string) (*Identity, error)
}
type VerifierConfig struct{ IssuerURL, Audience, JWKSURL string }
type jwtVerifier struct {
	cfg         VerifierConfig
	client      *http.Client
	mu          sync.Mutex
	keys        map[string]*rsa.PublicKey
	lastRefresh time.Time
	lastAttempt time.Time
}

var uuid = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
var roleName = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

func NewVerifier(ctx context.Context, cfg VerifierConfig) (Verifier, error) {
	for _, s := range []string{cfg.IssuerURL, cfg.JWKSURL} {
		u, e := url.Parse(s)
		if e != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
			return nil, errors.New("invalid identity URL")
		}
	}
	if cfg.Audience == "" {
		return nil, errors.New("audience required")
	}
	v := &jwtVerifier{cfg: cfg, client: &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	if e := v.refresh(ctx); e != nil {
		return nil, e
	}
	return v, nil
}

// Signing-key snapshots are trusted for at most five minutes. Expired snapshots
// fail closed if refresh fails; no stale key grace period is permitted.
const jwksCacheLifetime = 5 * time.Minute
const jwksRefreshBackoff = time.Second

func (v *jwtVerifier) refresh(ctx context.Context) error {
	req, e := http.NewRequestWithContext(ctx, "GET", v.cfg.JWKSURL, nil)
	if e != nil {
		return e
	}
	res, e := v.client.Do(req)
	if e != nil {
		return e
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != 200 {
		return fmt.Errorf("JWKS status %d", res.StatusCode)
	}
	var doc struct {
		Keys []struct{ Kty, Kid, N, E, Alg, Use string }
	}
	body, e := io.ReadAll(io.LimitReader(res.Body, (1<<20)+1))
	if e != nil {
		return e
	}
	if len(body) > 1<<20 {
		return errors.New("oversized JWKS")
	}
	if e = json.Unmarshal(body, &doc); e != nil {
		return e
	}
	keys := map[string]*rsa.PublicKey{}
	for _, k := range doc.Keys {
		if k.Kty != "RSA" || k.Kid == "" || (k.Alg != "" && k.Alg != "RS256") || (k.Use != "" && k.Use != "sig") {
			continue
		}
		n, e := base64.RawURLEncoding.DecodeString(k.N)
		if e != nil {
			return e
		}
		b, e := base64.RawURLEncoding.DecodeString(k.E)
		if e != nil || len(b) > 4 {
			return errors.New("invalid exponent")
		}
		exp := new(big.Int).SetBytes(b).Int64()
		mod := new(big.Int).SetBytes(n)
		if exp < 3 || exp%2 == 0 || mod.BitLen() < 2048 {
			return errors.New("unsafe RSA key")
		}
		if keys[k.Kid] != nil {
			return errors.New("duplicate key ID")
		}
		keys[k.Kid] = &rsa.PublicKey{N: mod, E: int(exp)}
	}
	if len(keys) == 0 {
		return errors.New("no signing keys")
	}
	v.keys = keys
	v.lastRefresh = time.Now()
	return nil
}
func (v *jwtVerifier) verifiedPayload(ctx context.Context, token string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(token) > 32768 {
		return nil, errors.New("oversized token")
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errors.New("invalid token")
	}
	h, e := base64.RawURLEncoding.DecodeString(parts[0])
	if e != nil {
		return nil, e
	}
	var header struct {
		Alg, Kid string
		Crit     []string
	}
	if json.Unmarshal(h, &header) != nil || header.Alg != "RS256" || header.Kid == "" || len(header.Crit) > 0 {
		return nil, errors.New("unsupported JWT header")
	}
	v.mu.Lock()
	key := v.keys[header.Kid]
	expired := time.Since(v.lastRefresh) >= jwksCacheLifetime
	if key == nil || expired {
		// A miss or expired snapshot may trigger one serialized fetch per second.
		// Backoff never authorizes an expired key, including after fetch failure.
		if time.Since(v.lastAttempt) < jwksRefreshBackoff {
			v.mu.Unlock()
			return nil, errors.New("signing keys unavailable during refresh backoff")
		}
		v.lastAttempt = time.Now()
		e = v.refresh(ctx)
		key = v.keys[header.Kid]
	}
	v.mu.Unlock()
	if e != nil {
		return nil, e
	}
	if key == nil {
		return nil, errors.New("unknown signing key")
	}
	sig, e := base64.RawURLEncoding.DecodeString(parts[2])
	if e != nil {
		return nil, e
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if e = rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], sig); e != nil {
		return nil, e
	}
	payload, e := base64.RawURLEncoding.DecodeString(parts[1])
	if e != nil {
		return nil, e
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return payload, nil
}

func (v *jwtVerifier) Verify(ctx context.Context, token string) (*Identity, error) {
	payload, e := v.verifiedPayload(ctx, token)
	if e != nil {
		return nil, e
	}
	var c struct {
		Iss    string          `json:"iss"`
		Aud    json.RawMessage `json:"aud"`
		Sub    string          `json:"sub"`
		Exp    int64           `json:"exp"`
		Nbf    int64           `json:"nbf"`
		Claims struct {
			User      string   `json:"x-hasura-user-id"`
			Workspace string   `json:"x-hasura-workspace-id"`
			Default   string   `json:"x-hasura-default-role"`
			Allowed   []string `json:"x-hasura-allowed-roles"`
		} `json:"https://hasura.io/jwt/claims"`
	}
	if e = json.Unmarshal(payload, &c); e != nil {
		return nil, e
	}
	now := time.Now().Unix()
	if c.Iss != v.cfg.IssuerURL || c.Exp <= now || c.Nbf > now {
		return nil, errors.New("invalid issuer or validity")
	}
	var audience string
	var audiences []string
	match := false
	if json.Unmarshal(c.Aud, &audience) == nil {
		match = audience == v.cfg.Audience
	} else if json.Unmarshal(c.Aud, &audiences) == nil {
		for _, a := range audiences {
			match = match || a == v.cfg.Audience
		}
	}
	if !match {
		return nil, errors.New("invalid audience")
	}
	if !uuid.MatchString(c.Sub) || !uuid.MatchString(c.Claims.User) || !uuid.MatchString(c.Claims.Workspace) || c.Sub != c.Claims.User {
		return nil, errors.New("invalid signed identity")
	}
	found := false
	seen := map[string]bool{}
	for _, r := range c.Claims.Allowed {
		if !roleName.MatchString(r) || seen[r] {
			return nil, errors.New("invalid roles")
		}
		seen[r] = true
		found = found || r == c.Claims.Default
	}
	if !found {
		return nil, errors.New("default role not granted")
	}
	return &Identity{UserID: strings.ToLower(c.Sub), WorkspaceID: strings.ToLower(c.Claims.Workspace), Role: c.Claims.Default, AllowedRoles: c.Claims.Allowed}, nil
}
