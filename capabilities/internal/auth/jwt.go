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

// VerifierConfig names the token issuer, the audience tokens must carry, and
// where the issuer publishes its signing keys.
type VerifierConfig struct{ IssuerURL, Audience, JWKSURL string }
type jwtVerifier struct {
	cfg         VerifierConfig
	client      *http.Client
	mu          sync.Mutex
	keys        map[string]*rsa.PublicKey
	lastRefresh time.Time
	lastAttempt time.Time
	refreshing  bool
}

var uuid = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// NewAuthenticator fetches the issuer's signing keys and returns an
// Authenticator for its tokens.
func NewAuthenticator(ctx context.Context, cfg VerifierConfig) (Authenticator, error) {
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

// WaitForAuthenticator is NewAuthenticator retried every two seconds for up to
// wait, for services that start before the identity provider is ready.
func WaitForAuthenticator(ctx context.Context, cfg VerifierConfig, wait time.Duration, logf func(string, ...any)) (Authenticator, error) {
	deadline := time.Now().Add(wait)
	for {
		a, err := NewAuthenticator(ctx, cfg)
		if err == nil || time.Now().After(deadline) || ctx.Err() != nil {
			return a, err
		}
		if logf != nil {
			logf("waiting for identity provider: %v", err)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

// Signing keys are refreshed after five minutes, or at once for an unknown key
// ID, at most once per second and without holding the lock during the fetch.
// An answer from the issuer is authoritative: a key it no longer lists is
// dropped. If the issuer cannot be reached, the last keys stay in use for up to
// an hour, so a short identity-provider outage does not reject every valid
// token; after that verification fails closed.
const jwksCacheLifetime = 5 * time.Minute
const jwksRefreshBackoff = time.Second
const jwksMaxStaleness = time.Hour

// refresh loads the keys at start-up, when there must be at least one.
func (v *jwtVerifier) refresh(ctx context.Context) error {
	keys, e := v.fetch(ctx)
	if e != nil {
		return e
	}
	if len(keys) == 0 {
		return errors.New("no signing keys")
	}
	v.keys = keys
	v.lastRefresh = time.Now()
	return nil
}

// signingKey returns the key for kid, refreshing the set when it is old or
// lacks kid.
func (v *jwtVerifier) signingKey(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	v.mu.Lock()
	key := v.keys[kid]
	fresh := time.Since(v.lastRefresh) < jwksCacheLifetime
	if (key == nil || !fresh) && !v.refreshing && time.Since(v.lastAttempt) >= jwksRefreshBackoff {
		v.refreshing = true
		v.lastAttempt = time.Now()
		v.mu.Unlock()
		keys, err := v.fetch(ctx)
		v.mu.Lock()
		v.refreshing = false
		if err == nil {
			v.keys = keys
			v.lastRefresh = time.Now()
		}
		key = v.keys[kid]
	}
	age := time.Since(v.lastRefresh)
	v.mu.Unlock()
	if key == nil {
		return nil, errors.New("unknown signing key")
	}
	if age >= jwksMaxStaleness {
		return nil, errors.New("signing keys are stale and the issuer is unreachable")
	}
	return key, nil
}

// fetch downloads and validates the issuer's key set. An empty set is a valid
// answer (every key revoked).
func (v *jwtVerifier) fetch(ctx context.Context) (map[string]*rsa.PublicKey, error) {
	req, e := http.NewRequestWithContext(ctx, "GET", v.cfg.JWKSURL, nil)
	if e != nil {
		return nil, e
	}
	res, e := v.client.Do(req)
	if e != nil {
		return nil, e
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("JWKS status %d", res.StatusCode)
	}
	var doc struct {
		Keys []struct{ Kty, Kid, N, E, Alg, Use string }
	}
	body, e := io.ReadAll(io.LimitReader(res.Body, (1<<20)+1))
	if e != nil {
		return nil, e
	}
	if len(body) > 1<<20 {
		return nil, errors.New("oversized JWKS")
	}
	if e = json.Unmarshal(body, &doc); e != nil {
		return nil, e
	}
	keys := map[string]*rsa.PublicKey{}
	for _, k := range doc.Keys {
		if k.Kty != "RSA" || k.Kid == "" || (k.Alg != "" && k.Alg != "RS256") || (k.Use != "" && k.Use != "sig") {
			continue
		}
		n, e := base64.RawURLEncoding.DecodeString(k.N)
		if e != nil {
			return nil, e
		}
		b, e := base64.RawURLEncoding.DecodeString(k.E)
		if e != nil || len(b) > 4 {
			return nil, errors.New("invalid exponent")
		}
		exp := new(big.Int).SetBytes(b).Int64()
		mod := new(big.Int).SetBytes(n)
		if exp < 3 || exp%2 == 0 || mod.BitLen() < 2048 {
			return nil, errors.New("unsafe RSA key")
		}
		if keys[k.Kid] != nil {
			return nil, errors.New("duplicate key ID")
		}
		keys[k.Kid] = &rsa.PublicKey{N: mod, E: int(exp)}
	}
	return keys, nil
}

// verifiedPayload checks the token's header and RS256 signature against the
// issuer's signing keys and returns the decoded payload.
func (v *jwtVerifier) verifiedPayload(ctx context.Context, token string) ([]byte, error) {
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
	key, e := v.signingKey(ctx, header.Kid)
	if e != nil {
		return nil, e
	}
	sig, e := base64.RawURLEncoding.DecodeString(parts[2])
	if e != nil {
		return nil, e
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if e = rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], sig); e != nil {
		return nil, e
	}
	return base64.RawURLEncoding.DecodeString(parts[1])
}

// standardClaims are the registered claims every accepted token must carry.
type standardClaims struct {
	Iss string          `json:"iss"`
	Aud json.RawMessage `json:"aud"`
	Sub string          `json:"sub"`
	Exp int64           `json:"exp"`
	Nbf int64           `json:"nbf"`
}

// check accepts only this issuer and audience, an unexpired token, and a UUID
// subject.
func (c standardClaims) check(cfg VerifierConfig) error {
	now := time.Now().Unix()
	if c.Iss != cfg.IssuerURL || c.Exp <= now || c.Nbf > now {
		return errors.New("invalid issuer or validity")
	}
	var audience string
	var audiences []string
	match := false
	if json.Unmarshal(c.Aud, &audience) == nil {
		match = audience == cfg.Audience
	} else if json.Unmarshal(c.Aud, &audiences) == nil {
		for _, a := range audiences {
			match = match || a == cfg.Audience
		}
	}
	if !match {
		return errors.New("invalid audience")
	}
	if !uuid.MatchString(c.Sub) {
		return errors.New("invalid subject")
	}
	return nil
}

// Principal is who a token says the caller is. It carries no permissions:
// roles are looked up in core.user_roles (ADR 0007).
type Principal struct {
	Subject string // Keycloak user ID, the core.users primary key
	Email   string
	Name    string
}

// Authenticator verifies a Keycloak access token and returns its principal.
type Authenticator interface {
	Authenticate(ctx context.Context, token string) (*Principal, error)
}

func (v *jwtVerifier) Authenticate(ctx context.Context, token string) (*Principal, error) {
	payload, e := v.verifiedPayload(ctx, token)
	if e != nil {
		return nil, e
	}
	var c struct {
		standardClaims
		Email    string `json:"email"`
		Name     string `json:"name"`
		Username string `json:"preferred_username"`
	}
	if e = json.Unmarshal(payload, &c); e != nil {
		return nil, e
	}
	if e = c.check(v.cfg); e != nil {
		return nil, e
	}
	p := &Principal{Subject: strings.ToLower(c.Sub), Email: strings.TrimSpace(c.Email), Name: strings.TrimSpace(c.Name)}
	if p.Name == "" {
		p.Name = strings.TrimSpace(c.Username)
	}
	if p.Name == "" {
		p.Name = p.Email
	}
	if p.Email == "" || len(p.Email) > 320 || len(p.Name) > 200 {
		return nil, errors.New("token lacks a usable email")
	}
	return p, nil
}
