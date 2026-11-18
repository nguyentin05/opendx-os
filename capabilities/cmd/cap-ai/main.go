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
	"github.com/opendx-os/opendx-os/capabilities/internal/agent"
	"github.com/opendx-os/opendx-os/capabilities/internal/ai/access"
	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
	"github.com/opendx-os/opendx-os/capabilities/internal/knowledge"
	"github.com/opendx-os/opendx-os/capabilities/internal/knowledge/models"
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

func chatEnabled(getenv func(string) string) bool {
	packages, personal := effectiveSourceKinds(getenv)
	return getenv("RAG_AGENT_ENABLED") == "true" && getenv("RAG_METADATA_ENABLED") == "true" && (packages || personal)
}

func effectiveSourceKinds(getenv func(string) string) (packages, personal bool) {
	qualified := getenv("RAG_SOURCE_QUALIFIED") == "true" && getenv("RAG_METADATA_QUALIFIED") == "true" &&
		getenv("RAG_PARSER_QUALIFIED") == "true"
	return qualified && getenv("RAG_PACKAGE_ENABLED") == "true", qualified && getenv("RAG_PERSONAL_ENABLED") == "true"
}

func validateChatBillingApp(ctx context.Context, db *sql.DB, appID string) error {
	var ready bool
	if strings.TrimSpace(appID) == "" || db == nil ||
		db.QueryRowContext(ctx, `SELECT EXISTS (SELECT FROM core.apps WHERE app_id = $1 AND status = 'Ready')`, appID).Scan(&ready) != nil || !ready {
		return errors.New("chat requires a Ready AI_AGENT_BILLING_APP_ID")
	}
	return nil
}

func newRuntimeChatHandler(authenticator agent.ChatAuthenticator, sources *knowledge.Store, modelsGateway *models.Gateway,
	runtimeResolver *models.RuntimeResolver, billingAppID string, packages, personal bool,
) *agent.Handler {
	db := authenticator.DB
	store := &agent.PostgresConversationStore{DB: db}
	scopes := agent.SourceKindGate{
		ScopeResolver: &agent.PostgresScopeResolver{DB: db}, AppEnabled: packages, PersonalEnabled: personal,
	}
	reader := knowledge.NewPolicy(db)
	var resolver knowledge.ModelResolver
	var policy knowledge.ModelPolicy
	if runtimeResolver != nil {
		resolver = runtimeResolver
	}
	if modelsGateway != nil && modelsGateway.Policy != nil {
		policy = modelsGateway.Policy
	}
	// The chat path selects models only from installation slots. The native
	// routes may still use their configured legacy profiles.
	search := *sources
	search.ConfigureModels(modelsGateway, models.Profile{}, models.Profile{})
	search.ConfigureModelResolver(resolver)
	retrieval := &agent.RetrievalCoordinator{Scopes: scopes, Search: &search, Reader: reader}
	checker := agent.NewSourcePolicyChecker(reader, resolver, policy, billingAppID)
	memory := agent.NewWorkingMemory(scopes.FilterHistory(checker), 0)
	reply := agent.NewOrchestrator(store, memory, retrieval, reader, resolver, modelsGateway, billingAppID)
	return agent.NewHandler(authenticator, store, reply, scopes, sources)
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
	identityConfig := auth.VerifierConfig{IssuerURL: c.IssuerURL, JWKSURL: c.JWKSURL, Audience: c.Audience}
	verifier, err := auth.NewStrictAuthenticator(context.Background(), identityConfig)
	if err != nil {
		log.Fatal("identity verification unavailable or invalid configuration")
	}
	packageEnabled, personal := effectiveSourceKinds(os.Getenv)
	if os.Getenv("RAG_PACKAGE_ENABLED") == "true" && !packageEnabled {
		log.Fatal("package processing requires qualified predecessors")
	}
	var chatVerifier auth.SubjectVerifier
	var billingAppID string
	if chatEnabled(os.Getenv) {
		billingAppID = os.Getenv("AI_AGENT_BILLING_APP_ID")
		billingCtx, billingCancel := context.WithTimeout(context.Background(), 3*time.Second)
		billingErr := validateChatBillingApp(billingCtx, db, billingAppID)
		billingCancel()
		if billingErr != nil {
			log.Fatal(billingErr)
		}
		chatVerifier, err = auth.NewSubjectVerifier(context.Background(), identityConfig)
		if err != nil {
			log.Fatal("chat identity verification unavailable or invalid configuration")
		}
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
		internalStore := knowledge.NewStore(workerDB)
		if err = internalStore.ConfigurePackages(packageEnabled, os.Getenv("RAG_PACKAGE_ARTIFACT_ROOT")); err != nil {
			log.Fatal("invalid package artifact configuration")
		}
		if packageEnabled {
			dispatcherDB, e := sql.Open("postgres", os.Getenv("AI_DISPATCHER_DATABASE_URL"))
			if e != nil {
				log.Fatal("dispatcher database unavailable")
			}
			defer dispatcherDB.Close()
			dispatcherStore := knowledge.NewStore(dispatcherDB)
			if e = dispatcherStore.ConfigurePackages(true, os.Getenv("RAG_PACKAGE_ARTIFACT_ROOT")); e != nil {
				log.Fatal("invalid package reconcile configuration")
			}
			if e = internalStore.ConfigureDispatcher(dispatcherStore, os.Getenv("RAG_DISPATCHER_SERVICE_SUBJECT"), os.Getenv("RAG_WORKER_SERVICE_SUBJECT"), os.Getenv("RAG_FILE_SERVICE_SUBJECT")); e != nil {
				log.Fatal("distinct dispatcher identity required")
			}
		}
		_, err = knowledge.StartInternalListener(internalCtx, address, os.Getenv("RAG_WORKER_SERVICE_SUBJECT"), os.Getenv("RAG_FILE_SERVICE_SUBJECT"), internalStore.InternalHandler(serviceVerifier, os.Getenv("RAG_WORKER_SERVICE_SUBJECT"), os.Getenv("RAG_FILE_SERVICE_SUBJECT")))
		if err != nil {
			log.Fatal("invalid internal knowledge service configuration")
		}
	}
	store := knowledge.NewStore(db)
	if err = store.ConfigurePackages(packageEnabled, os.Getenv("RAG_PACKAGE_ARTIFACT_ROOT")); err != nil {
		log.Fatal("invalid package source configuration")
	}
	modelCtx, modelCancel := context.WithTimeout(context.Background(), 5*time.Second)
	gateway, embed, generate, resolver, closeGateway, modelErr := models.RuntimeFromEnvironment(modelCtx, os.Getenv)
	modelCancel()
	if modelErr != nil {
		log.Fatal("invalid model gateway configuration")
	}
	defer closeGateway()
	store.ConfigureModels(gateway, embed, generate)
	if resolver != nil {
		store.ConfigureModelResolver(resolver)
	}
	if err = store.ConfigurePersonal(personal, os.Getenv("RAG_FILE_API_URL"), os.Getenv("KNOWLEDGE_ARTIFACT_ROOT")); err != nil {
		log.Fatal("invalid personal-source configuration")
	}
	var chat http.Handler
	if chatVerifier != nil {
		chat = newRuntimeChatHandler(agent.ChatAuthenticator{Verifier: chatVerifier, DB: db}, store, gateway, resolver, billingAppID, packageEnabled, personal)
	}
	nativeAuthority := access.Authenticator{Authority: knowledge.NewAuthority(verifier, db)}
	nativeEnabled := os.Getenv("RAG_METADATA_ENABLED") == "true" && os.Getenv("RAG_SOURCE_QUALIFIED") == "true"
	server := &http.Server{
		Addr: ":" + c.Port, Handler: newHandlerWithKnowledge(nativeAuthority, store, nativeEnabled, chat),
		ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second,
	}
	log.Printf("cap-ai listening on port %s", c.Port)
	if err = server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal("cap-ai listener failed")
	}
}
