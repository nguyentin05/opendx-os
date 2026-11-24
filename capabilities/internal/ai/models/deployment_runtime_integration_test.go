//go:build integration

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package models

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
	"github.com/opendx-os/opendx-os/capabilities/internal/testutil"
)

// These cases replace the retired slot-resolution fixtures. The security questions
// they asked are the same ones the deployment selection must answer: is the exact
// configured profile the current selection, is a stale or withdrawn revision
// refused, and can no runtime login reach the retired lookup at all?

const runtimeIntegrationDeclaration = `enabled: true
embedding:
  model: 'ollama/embed-model'
  execution: local
  api_base: http://ollama:11434
  revision: 'embed-revision'
  dimension: 8
llm:
  model: 'ollama/llm-model'
  execution: local
  api_base: http://ollama:11434
  revision: 'llm-revision'
`

func runtimeFixture(t *testing.T) (*sql.DB, Profile, Profile) {
	t.Helper()
	db := modelDB(t, true)
	applyRegistry(t, db)
	applyModelAuthority(t, db)
	deployment, err := LoadDeployment(strings.NewReader(runtimeIntegrationDeclaration))
	if err != nil {
		t.Fatal(err)
	}
	embed, err := deployment.Embedding.Profile("EMBED")
	if err != nil {
		t.Fatal(err)
	}
	generate, err := deployment.LLM.Profile("GENERATE")
	if err != nil {
		t.Fatal(err)
	}
	applySelection(t, db, DeploymentSelection{Embedding: &embed, Generation: &generate})
	return db, embed, generate
}

// runtimeEnvironment writes the declaration and returns the runtime environment for
// this database. The gateway DSN names the restricted gateway login itself, because
// the binding re-checks the session role: a privileged test login is refused.
func integrationRuntimeEnvironment(t *testing.T, db *sql.DB) map[string]string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "models.yaml")
	if err := os.WriteFile(path, []byte(runtimeIntegrationDeclaration), 0o600); err != nil {
		t.Fatal(err)
	}
	var name string
	if err := db.QueryRow(`SELECT current_database()`).Scan(&name); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	gateway := *u
	gateway.User = url.User("dxos_ai_gateway")
	return map[string]string{
		"RAG_MODELS_ENABLED":            "true",
		"RAG_SOURCE_QUALIFIED":          "true",
		"RAG_METADATA_QUALIFIED":        "true",
		"RAG_PARSER_QUALIFIED":          "true",
		"RAG_LOCAL_MODELS_QUALIFIED":    "true",
		"RAG_MODEL_DEPLOYMENT_FILE":     path,
		"RAG_MODEL_LITELLM_ENDPOINT":    "http://litellm:4000",
		"LITELLM_MASTER_KEY":            "runtime-integration-credential",
		"AI_GATEWAY_DATABASE_URL":       gateway.String(),
		"RAG_MODEL_LITELLM_CONFIG_FILE": "",
	}
}

// TestDeploymentRuntimeBindsExactSelection is the runtime half of the replaced
// slot cases: startup binds exactly the reconciled profiles, and a state the
// database does not hold leaves the runtime without a model route instead of
// serving another one — the process still boots and keeps lexical search.
func TestDeploymentRuntimeBindsExactSelection(t *testing.T) {
	db, embed, generate := runtimeFixture(t)
	env := integrationRuntimeEnvironment(t, db)
	getenv := func(key string) string { return env[key] }
	gateway, gotEmbed, gotGenerate, closeRuntime, err := RuntimeFromEnvironment(context.Background(), getenv)
	if err != nil || gateway == nil || gotEmbed != embed || gotGenerate != generate {
		t.Fatalf("runtime binding: %v %q %q", err, gotEmbed.ID, gotGenerate.ID)
	}
	if len(gateway.Routes) != 2 || gateway.Routes[embed.ID] != embed || gateway.Routes[generate.ID] != generate {
		t.Fatal("the runtime bound a route the deployment did not declare")
	}
	closeRuntime()

	// A withdrawn purpose is an unreconciled deployment: the declaration still
	// names it while the database no longer serves it, so the runtime serves no
	// model route at all and degrades to lexical search rather than crash-looping.
	applySelection(t, db, DeploymentSelection{Embedding: &embed})
	if g, _, _, closeRuntime, err := RuntimeFromEnvironment(context.Background(), getenv); err != nil || g != nil {
		closeRuntime()
		t.Fatalf("the runtime started on a declaration the deployment does not select: %v %v", g, err)
	}

	// Restoring the selection restores the binding.
	applySelection(t, db, DeploymentSelection{Embedding: &embed, Generation: &generate})
	gateway, _, _, closeRuntime, err = RuntimeFromEnvironment(context.Background(), getenv)
	if err != nil || gateway == nil {
		t.Fatalf("restored binding: %v", err)
	}
	closeRuntime()

	// A stale proxy configuration serves no route, before any call.
	stale := filepath.Join(t.TempDir(), "litellm.yaml")
	if err := os.WriteFile(stale, []byte("# edited\nmodel_list: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env["RAG_MODEL_LITELLM_CONFIG_FILE"] = stale
	if g, _, _, closeRuntime, err := RuntimeFromEnvironment(context.Background(), getenv); err != nil || g != nil {
		closeRuntime()
		t.Fatalf("a stale generated proxy configuration was admitted: %v %v", g, err)
	}
}

// TestDeploymentRuntimePolicyRejectsUnselectedProfile proves the durable authority
// answers for the exact configured profile only, and never for a historical row.
func TestDeploymentRuntimePolicyRejectsUnselectedProfile(t *testing.T) {
	db, embed, generate := runtimeFixture(t)
	ctx := context.Background()
	gatewayDB := testutil.As(t, integrationDSN(t, db), "dxos_ai_gateway")
	var raw []byte
	if err := gatewayDB.QueryRow(`SELECT cap_ai.model_profile($1,$2)`, generate.ID, generate.Revision).Scan(&raw); err != nil {
		t.Fatalf("the live profile is unreadable: %v", err)
	}
	var stored Profile
	if err := json.Unmarshal(raw, &stored); err != nil || !deploymentProfileMatches(generate, stored) {
		t.Fatalf("the live profile does not match the configured route: %v", err)
	}
	// An earlier revision of the same purpose is not current once the selection
	// moves on, and a withdrawn profile is not readable at all: the function answers
	// SQL NULL for a profile the deployment does not currently select.
	applySelection(t, db, DeploymentSelection{Embedding: &embed})
	raw = nil
	if err := gatewayDB.QueryRow(`SELECT cap_ai.model_profile($1,$2)`, generate.ID, generate.Revision).Scan(&raw); err != nil || raw != nil {
		t.Fatalf("a withdrawn profile stayed readable: %s err=%v", raw, err)
	}
	// The retired slot lookup is unreachable for every runtime login.
	for _, role := range []string{"dxos_ai_gateway", "dxos_ai_worker", "dxos_ai_api"} {
		var allowed bool
		if err := db.QueryRow(`SELECT has_function_privilege($1,'cap_ai.registry_resolve_slot(text)','EXECUTE')`, role).Scan(&allowed); err != nil || allowed {
			t.Fatalf("%s can still resolve a slot: %v", role, err)
		}
	}
	_ = ctx
}

// TestDeploymentRuntimePolicyDeniesUnconfiguredProfile proves a model call for a
// profile the runtime did not configure is refused before any egress.
func TestDeploymentRuntimePolicyDeniesUnconfiguredProfile(t *testing.T) {
	db, embed, generate := runtimeFixture(t)
	provider := &runtimeIntegrationProvider{}
	authority := &runtimeIntegrationAuthority{profile: embed}
	gateway := &Gateway{Policy: &Policy{Authority: authority, Enabled: true}, Operations: &OperationStore{DB: testutil.As(t, integrationDSN(t, db), "dxos_ai_gateway")},
		Providers: map[string]Provider{embed.ProviderID: provider}, Routes: map[string]Profile{generate.ID: generate}}
	op := Operation{Key: "runtime-mismatch", AppID: "app", ScopeKind: "APP", Purpose: "EMBED", ProfileID: embed.ID, ProfileRevision: embed.Revision}
	if _, err := gateway.Call(context.Background(), &auth.Identity{UserID: "actor"}, op, Payload{Texts: []string{"x"}}); err != ErrPolicy {
		t.Fatalf("an unconfigured profile was dispatched: %v", err)
	}
	if provider.calls != 0 {
		t.Fatal("the provider was invoked for an unconfigured profile")
	}
}

// integrationDSN returns the fixture database's DSN so a test can open one
// restricted login on it.
func integrationDSN(t *testing.T, db *sql.DB) string {
	t.Helper()
	var name string
	if err := db.QueryRow(`SELECT current_database()`).Scan(&name); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	return u.String()
}

type runtimeIntegrationProvider struct{ calls int }

func (p *runtimeIntegrationProvider) Invoke(context.Context, Profile, Payload) (Result, error) {
	p.calls++
	return Result{}, nil
}

type runtimeIntegrationAuthority struct{ profile Profile }

func (a *runtimeIntegrationAuthority) Current(context.Context, *auth.Identity, Operation) (Decision, error) {
	return Decision{Allowed: true, Profile: a.profile, QueryClassification: "NON_PERSONAL"}, nil
}

// TestRegistrySlotUniqueness keeps the legacy table's own invariants asserted while
// the table still exists; no runtime path reads it any more.
func TestRegistrySlotUniqueness(t *testing.T) {
	db := modelDB(t, true)
	applyRegistry(t, db)
	for _, q := range []string{
		`INSERT INTO cap_ai.global_model_slots(capability) VALUES('LLM')`,
		`INSERT INTO cap_ai.global_model_slots(capability) VALUES('OTHER')`,
		`UPDATE cap_ai.global_model_slots SET slot_revision=-1 WHERE capability='LLM'`,
		`UPDATE cap_ai.global_model_slots SET profile_id='missing' WHERE capability='LLM'`,
	} {
		if _, e := db.Exec(q); e == nil {
			t.Fatal(q)
		}
	}
}
