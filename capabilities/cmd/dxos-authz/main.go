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

// Command dxos-authz is the platform's authorization service (ADR 0007): the
// Hasura authentication webhook and the /api/auth/me endpoint. Keycloak
// authenticates; this service decides which roles a caller holds, from
// core.user_roles.
package main

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/lib/pq"
	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
	"github.com/opendx-os/opendx-os/capabilities/internal/authz"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	port := envOr("PORT", "8090")
	ttl, err := time.ParseDuration(envOr("ROLE_CACHE_TTL", "5s"))
	if err != nil || ttl < 0 || ttl > time.Minute {
		log.Fatal("ROLE_CACHE_TTL must be a duration between 0 and 1m")
	}
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Fatal("DATABASE_URL is required")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer func() { _ = db.Close() }()

	cfg := auth.VerifierConfig{IssuerURL: os.Getenv("OIDC_ISSUER_URL"), JWKSURL: os.Getenv("OIDC_JWKS_URL"), Audience: os.Getenv("OIDC_AUDIENCE")}
	authn, err := auth.WaitForAuthenticator(ctx, cfg, time.Minute, log.Printf)
	if err != nil {
		log.Fatal(err)
	}

	store := authz.PostgresStore{DB: db}
	resolver := authz.NewResolver(authn, store, ttl)
	// Operators manage role assignments here. BOOTSTRAP_OPERATOR_EMAIL names
	// the account that becomes the first operator of a new installation.
	resolver.Administer(store, os.Getenv("BOOTSTRAP_OPERATOR_EMAIL"))
	// With the read-only Keycloak client dxos-directory
	// (platform/keycloak/directory-client.sh), the operator's account list also
	// shows people who have not signed in yet.
	if secret := os.Getenv("KEYCLOAK_DIRECTORY_SECRET"); secret != "" {
		accounts, err := authz.NewKeycloakAccounts(envOr("KEYCLOAK_URL", "http://keycloak:8085"), envOr("KEYCLOAK_REALM", "dxos"), secret)
		if err != nil {
			log.Fatalf("account directory: %v", err)
		}
		resolver.MirrorAccounts(accounts)
	}
	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           authz.Handler(resolver),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	log.Printf("dxos-authz listening on :%s (role cache %s)", port, ttl)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
