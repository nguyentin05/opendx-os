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

// Command cap-process serves work item state command endpoints.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/opendx-os/opendx-os/capabilities/internal/events"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/lib/pq"
	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
	"github.com/opendx-os/opendx-os/capabilities/internal/process"
	"github.com/opendx-os/opendx-os/capabilities/internal/registry"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	port := os.Getenv("PORT")
	if port == "" {
		port = "8081"
	}

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Fatal("DATABASE_URL is required")
	}

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		log.Fatalf("failed to connect to database: %v", err)
	}
	defer db.Close()

	verifier, err := auth.NewVerifier(context.Background(), auth.VerifierConfig{IssuerURL: os.Getenv("OIDC_ISSUER_URL"), JWKSURL: os.Getenv("OIDC_JWKS_URL"), Audience: os.Getenv("OIDC_AUDIENCE")})
	if err != nil {
		log.Fatal(err)
	}
	engine := process.NewEngine(db, registry.NewStore(db))
	if err := engine.ConfigureWorkflowService(os.Getenv("WORKFLOW_SERVICE_USER_ID"), os.Getenv("WORKFLOW_SERVICE_ROLE")); err != nil {
		log.Fatal("valid WORKFLOW_SERVICE_USER_ID and WORKFLOW_SERVICE_ROLE are required")
	}
	endpoint := os.Getenv("KESTRA_URL")
	if events.ValidateEndpoint(endpoint) != nil || os.Getenv("KESTRA_API_TOKEN") == "" {
		log.Fatal("valid KESTRA_URL webhook endpoint and KESTRA_API_TOKEN are required")
	}
	client := &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}
	publisher := events.NewPublisher(db, client, endpoint)
	publisher.SetBearerToken(os.Getenv("KESTRA_API_TOKEN"))
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := publisher.PublishPending(ctx, 50); err != nil && ctx.Err() == nil {
					log.Print("outbox delivery cycle failed")
				}
			}
		}
	}()
	defer func() { stop(); <-workerDone }()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok", "service": "cap-process"})
	})

	mux.Handle("POST /api/cap/process/commands/{app_id}/{command}", engine.AuthenticatedHandler(verifier))
	mux.Handle("POST /api/cap/process/effects/work-item", engine.AuthenticatedEffectHandler(verifier))

	log.Printf("[cap-process] Listening on port %s", port)
	server := &http.Server{Addr: ":" + port, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		server.Shutdown(shutdown)
	}()
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Print("HTTP server stopped unexpectedly")
	}
}
