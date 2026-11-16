// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
package main

import (
	"context"
	"database/sql"
	_ "github.com/lib/pq"
	"github.com/opendx-os/opendx-os/capabilities/internal/knowledge"
	"github.com/opendx-os/opendx-os/capabilities/internal/knowledge/docir"
	"github.com/opendx-os/opendx-os/capabilities/internal/knowledge/models"
	"log"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	if os.Getenv("RAG_SOURCE_QUALIFIED") != "true" || os.Getenv("RAG_METADATA_QUALIFIED") != "true" || os.Getenv("RAG_PARSER_QUALIFIED") != "true" {
		log.Fatal("worker requires source, metadata and parser qualification")
	}
	for _, key := range []string{"AI_WORKER_DATABASE_URL", "RAG_WORKER_SERVICE_SUBJECT", "RAG_WORKER_BEARER_TOKEN", "RAG_INTERNAL_FILE_URL", "PARSER_SOCKET", "PARSER_SERVICE_TOKEN"} {
		if os.Getenv(key) == "" {
			log.Fatal("missing worker configuration: " + key)
		}
	}
	db, err := sql.Open("postgres", os.Getenv("AI_WORKER_DATABASE_URL"))
	if err != nil {
		log.Fatal("worker database unavailable")
	}
	defer db.Close()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	worker := knowledge.Worker{Store: knowledge.NewStore(db), Parser: docir.NewUnixClient(os.Getenv("PARSER_SOCKET"), os.Getenv("PARSER_SERVICE_TOKEN")), FileEndpoint: os.Getenv("RAG_INTERNAL_FILE_URL"), ServiceToken: os.Getenv("RAG_WORKER_BEARER_TOKEN"), WorkerID: os.Getenv("RAG_WORKER_SERVICE_SUBJECT"), PackageEndpoint: os.Getenv("RAG_INTERNAL_AI_URL")}
	gateway, embed, generate, resolver, closeGateway, modelErr := models.RuntimeFromEnvironment(ctx, os.Getenv)
	if modelErr != nil {
		log.Fatal("invalid model gateway configuration")
	}
	defer closeGateway()
	worker.Store.ConfigureModels(gateway, embed, generate)
	if resolver != nil {
		worker.Store.ConfigureModelResolver(resolver)
	}
	if err = worker.Run(ctx); err != nil && ctx.Err() == nil {
		log.Fatal("worker failed")
	}
}
