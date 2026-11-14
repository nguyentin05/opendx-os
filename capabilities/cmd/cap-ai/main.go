// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

// Command cap-ai serves the authenticated AI capability boundary.
package main

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	_ "github.com/lib/pq"
	"github.com/opendx-os/opendx-os/capabilities/internal/ai/access"
	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
	"github.com/opendx-os/opendx-os/capabilities/internal/knowledge"
)

type config struct{ DatabaseURL, IssuerURL, JWKSURL, Audience, Port string }

func loadConfig(getenv func(string) string) (config, error) {
	c := config{DatabaseURL: getenv("DATABASE_URL"), IssuerURL: getenv("OIDC_ISSUER_URL"), JWKSURL: getenv("OIDC_JWKS_URL"), Audience: getenv("OIDC_AUDIENCE"), Port: getenv("PORT")}
	for _, v := range []string{c.DatabaseURL, c.IssuerURL, c.JWKSURL, c.Audience} {
		if strings.TrimSpace(v) == "" {
			return config{}, errors.New("DATABASE_URL and OIDC identity configuration are required")
		}
	}
	if c.Port == "" {
		c.Port = "8083"
	}
	port, err := strconv.Atoi(c.Port)
	if err != nil || port < 1 || port > 65535 {
		return config{}, errors.New("invalid PORT")
	}
	return c, nil
}
func main() {
	c, err := loadConfig(os.Getenv)
	if err != nil {
		log.Fatal(err)
	}
	db, err := sql.Open("postgres", c.DatabaseURL)
	if err != nil {
		log.Fatal("invalid database configuration")
	}
	defer func() { _ = db.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	err = db.PingContext(ctx)
	cancel()
	if err != nil {
		log.Fatal("database unavailable")
	}
	verifier, err := auth.NewStrictAuthenticator(context.Background(), auth.VerifierConfig{IssuerURL: c.IssuerURL, JWKSURL: c.JWKSURL, Audience: c.Audience})
	if err != nil {
		log.Fatal("identity verification unavailable or invalid configuration")
	}
	if address := os.Getenv("RAG_INTERNAL_LISTEN"); address != "" {
		if os.Getenv("RAG_SOURCE_QUALIFIED") != "true" || os.Getenv("AI_WORKER_DATABASE_URL") == "" {
			log.Fatal("internal knowledge service requires qualified sources and a worker database connection")
		}
		workerDB, err := sql.Open("postgres", os.Getenv("AI_WORKER_DATABASE_URL"))
		if err != nil {
			log.Fatal("invalid worker database configuration")
		}
		defer workerDB.Close()
		serviceVerifier, err := auth.NewSubjectVerifier(context.Background(), auth.VerifierConfig{IssuerURL: c.IssuerURL, JWKSURL: c.JWKSURL, Audience: c.Audience})
		if err != nil {
			log.Fatal("service verification unavailable")
		}
		internalCtx, cancel := context.WithCancel(context.Background())
		defer cancel()
		_, err = knowledge.StartInternalListener(internalCtx, address, os.Getenv("RAG_WORKER_SERVICE_SUBJECT"), os.Getenv("RAG_FILE_SERVICE_SUBJECT"), knowledge.NewStore(workerDB).InternalHandler(serviceVerifier, os.Getenv("RAG_WORKER_SERVICE_SUBJECT"), os.Getenv("RAG_FILE_SERVICE_SUBJECT")))
		if err != nil {
			log.Fatal("invalid internal knowledge service configuration")
		}
	}
	store := knowledge.NewStore(db)
	personal := os.Getenv("RAG_PERSONAL_ENABLED") == "true" && os.Getenv("RAG_SOURCE_QUALIFIED") == "true" && os.Getenv("RAG_METADATA_QUALIFIED") == "true" && os.Getenv("RAG_PARSER_QUALIFIED") == "true"
	if err = store.ConfigurePersonal(personal, os.Getenv("RAG_FILE_API_URL"), os.Getenv("KNOWLEDGE_ARTIFACT_ROOT")); err != nil {
		log.Fatal("invalid personal-source configuration")
	}
	server := &http.Server{Addr: ":" + c.Port, Handler: newHandlerWithKnowledge(access.Authenticator{Authority: knowledge.NewAuthority(verifier, db)}, store, os.Getenv("RAG_METADATA_ENABLED") == "true" && os.Getenv("RAG_SOURCE_QUALIFIED") == "true"), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}
	log.Printf("cap-ai listening on port %s", c.Port)
	if err = server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal("cap-ai listener failed")
	}
}
