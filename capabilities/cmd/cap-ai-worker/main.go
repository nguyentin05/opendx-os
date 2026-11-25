// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
package main

import (
	"context"
	"database/sql"
	_ "github.com/lib/pq"
	"github.com/opendx-os/opendx-os/capabilities/internal/ai/knowledge"
	"github.com/opendx-os/opendx-os/capabilities/internal/ai/knowledge/docir"
	"github.com/opendx-os/opendx-os/capabilities/internal/ai/models"
	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
	"log"
	"os"
	"os/signal"
	"syscall"
)

// modelAvailabilityLog is the one sanitized start-up line the worker logs when
// models are switched on but no embedding route is callable: an absent, disabled or
// unreconciled declaration. It distinguishes the documented degraded boot from a
// start-up failure, and from the plain models-off baseline, which logs nothing. The
// message names no profile, endpoint, credential or document.
func modelAvailabilityLog(gateway *models.Gateway, getenv func(string) string) (string, bool) {
	if gateway != nil || getenv(models.ModelsEnabledEnv) != "true" {
		return "", false
	}
	return "embedding model unavailable: no callable deployment profile; claiming no model work", true
}

func main() {
	if os.Getenv("RAG_SOURCE_QUALIFIED") != "true" || os.Getenv("RAG_METADATA_QUALIFIED") != "true" || os.Getenv("RAG_PARSER_QUALIFIED") != "true" {
		log.Fatal("worker requires source, metadata and parser qualification")
	}
	for _, key := range []string{"AI_WORKER_DATABASE_URL", "RAG_WORKER_SERVICE_SUBJECT", "RAG_INTERNAL_FILE_URL", "PARSER_SOCKET", "PARSER_SERVICE_TOKEN"} {
		if os.Getenv(key) == "" {
			log.Fatal("missing worker configuration: " + key)
		}
	}
	serviceToken, err := auth.TokenGetterFrom(os.Getenv("RAG_WORKER_BEARER_TOKEN"), os.Getenv("OIDC_TOKEN_URL"), os.Getenv("RAG_WORKER_CLIENT_ID"), os.Getenv("RAG_WORKER_CLIENT_SECRET"))
	if err != nil {
		log.Fatal("invalid worker token configuration")
	}
	db, err := sql.Open("postgres", os.Getenv("AI_WORKER_DATABASE_URL"))
	if err != nil {
		log.Fatal("worker database unavailable")
	}
	defer func() { _ = db.Close() }()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	worker := knowledge.Worker{Store: knowledge.NewStore(db), Parser: docir.NewUnixClient(os.Getenv("PARSER_SOCKET"), os.Getenv("PARSER_SERVICE_TOKEN")), FileEndpoint: os.Getenv("RAG_INTERNAL_FILE_URL"), ServiceToken: serviceToken, WorkerID: os.Getenv("RAG_WORKER_SERVICE_SUBJECT"), PackageEndpoint: os.Getenv("RAG_INTERNAL_AI_URL")}
	gateway, embed, generate, closeGateway, modelErr := models.RuntimeFromEnvironment(ctx, os.Getenv)
	if modelErr != nil {
		log.Fatal("invalid model gateway configuration")
	}
	defer closeGateway()
	if message, degraded := modelAvailabilityLog(gateway, os.Getenv); degraded {
		// A declared but unreconciled deployment is not a worker startup failure:
		// the worker still parses and indexes, and claims no embedding job because
		// the database reports no current EMBED profile.
		log.Print(message)
	}
	worker.Store.ConfigureModels(gateway, embed, generate)
	if err = worker.Run(ctx); err != nil && ctx.Err() == nil {
		log.Fatal("worker failed")
	}
}
