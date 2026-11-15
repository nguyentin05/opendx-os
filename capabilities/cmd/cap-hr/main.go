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

// Command cap-hr serves the organization: departments and staff.
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
	"github.com/opendx-os/opendx-os/capabilities/internal/hr"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	port := os.Getenv("PORT")
	if port == "" {
		port = "8083"
	}
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Fatal("DATABASE_URL is required")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		log.Fatalf("failed to connect to database: %v", err)
	}
	defer func() { _ = db.Close() }()

	ttl := 5 * time.Second
	if value := os.Getenv("ROLE_CACHE_TTL"); value != "" {
		parsed, e := time.ParseDuration(value)
		if e != nil || parsed < 0 || parsed > time.Minute {
			log.Fatal("invalid ROLE_CACHE_TTL")
		}
		ttl = parsed
	}
	// Keycloak authenticates; the roles a caller holds are read from
	// core.user_roles (ADR 0007). dxos-authz mirrors users into core.users;
	// this connection only reads them.
	cfg := auth.VerifierConfig{IssuerURL: os.Getenv("OIDC_ISSUER_URL"), JWKSURL: os.Getenv("OIDC_JWKS_URL"), Audience: os.Getenv("OIDC_AUDIENCE")}
	authn, err := auth.WaitForAuthenticator(ctx, cfg, time.Minute, log.Printf)
	if err != nil {
		log.Fatal(err)
	}
	sessions := authz.NewResolver(authn, authz.PostgresStore{DB: db, ReadOnly: true}, ttl)

	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           hr.Handler(sessions, hr.Store{DB: db}),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	log.Printf("cap-hr listening on :%s", port)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
