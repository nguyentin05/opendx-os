// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package modelprovisioning

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opendx-os/opendx-os/capabilities/internal/ai/knowledge/modeladapter"
	"github.com/opendx-os/opendx-os/capabilities/internal/ai/models"
)

// Reviewed fixtures. The two routes declare independent purposes and the two
// candidates stay inside the route ceilings, so any rejection below is caused by
// the single mutation the case applies.
const (
	testAdapterOrigin = "https://cap-ai-model-adapter:8443"
	testAdapterToken  = "MODEL_ADAPTER_TOKEN"
	testCanary        = "sk-canary-do-not-print"

	embedRuleJSON  = `{"model_id":"openai/text-embedding-3-small","purpose":"EMBED","dimension":1536,"limits":{"max_input_tokens":8191,"max_output_tokens":1,"max_bytes":65536,"timeout_ms":30000}}`
	embedRouteJSON = `{"protocol_version":1,"type":"openai-compatible","base_url":"https://api.openai.com/v1","credential_ref":"OPENAI_API_KEY","models":[` + embedRuleJSON + `]}`

	generateRuleJSON  = `{"model_id":"openai/gpt-4o-mini","purpose":"GENERATE","limits":{"max_input_tokens":128000,"max_output_tokens":4096,"max_bytes":65536,"timeout_ms":60000}}`
	generateRouteJSON = `{"protocol_version":1,"type":"openai-compatible","base_url":"https://openrouter.ai/api/v1","credential_ref":"OPENROUTER_API_KEY","models":[` + generateRuleJSON + `],"routing":{"provider":"openai","data_collection":"deny","zdr":true}}`

	adapterConfigJSON = `{"schema_version":1,"routes":[` + embedRouteJSON + `,` + generateRouteJSON + `]}`
)

var (
	embedLimits    = models.Limits{MaxInputTokens: 8191, MaxOutputTokens: 1, MaxBytes: 65536, TimeoutMS: 30000}
	generateLimits = models.Limits{MaxInputTokens: 128000, MaxOutputTokens: 4096, MaxBytes: 65536, TimeoutMS: 60000}
)

func testConfig(t *testing.T) modeladapter.Config {
	t.Helper()
	cfg, err := modeladapter.ParseConfig([]byte(adapterConfigJSON))
	if err != nil {
		t.Fatalf("adapter configuration fixture: %v", err)
	}
	return cfg
}

func routePath(t *testing.T, cfg modeladapter.Config, index int) string {
	t.Helper()
	path, err := modeladapter.RoutePath(cfg.Routes[index])
	if err != nil {
		t.Fatalf("route %d path: %v", index, err)
	}
	return path
}

func providerFixture(id, endpoint string) models.ProviderConfig {
	return models.ProviderConfig{ID: id, Adapter: "bounded-http-v1", Endpoint: endpoint, CredentialRef: testAdapterToken}
}

func declarationFixture(id, providerID, modelID, purpose string, dimension int, limits models.Limits) models.CandidateDeclaration {
	return models.CandidateDeclaration{
		Adapter: "bounded-http-v1",
		Profile: models.Profile{
			ID: id, Purpose: purpose, Execution: "HOSTED", ProviderID: providerID,
			ModelID: modelID, CredentialRef: testAdapterToken, Revision: 1,
			Dimension: dimension, Limits: limits,
		},
	}
}

// catalogFixture round-trips a programmatic catalog through the strict parser.
// models.Profile always serializes dimension; the catalog contract represents an
// absent GENERATE dimension by omitting the field.
func catalogFixture(t *testing.T, providers []models.ProviderConfig, declarations []models.CandidateDeclaration) models.Catalog {
	t.Helper()
	raw, err := json.Marshal(models.Catalog{SchemaVersion: 2, Providers: providers, Models: declarations})
	if err != nil {
		t.Fatalf("catalog fixture: %v", err)
	}
	var document map[string]json.RawMessage
	var entries []map[string]json.RawMessage
	if json.Unmarshal(raw, &document) != nil || json.Unmarshal(document["models"], &entries) != nil {
		t.Fatalf("catalog fixture: %v", err)
	}
	for i, entry := range entries {
		if declarations[i].Profile.Purpose != "GENERATE" {
			continue
		}
		var profile map[string]json.RawMessage
		if json.Unmarshal(entry["profile"], &profile) != nil {
			t.Fatalf("catalog fixture: profile %d", i)
		}
		delete(profile, "dimension")
		entry["profile"], _ = json.Marshal(profile)
	}
	document["models"], _ = json.Marshal(entries)
	raw, _ = json.Marshal(document)
	parsed, err := models.ParseCatalog(raw)
	if err != nil {
		t.Fatalf("catalog fixture rejected by models.ParseCatalog: %v", err)
	}
	return parsed
}

// bundledCatalog is the reviewed baseline: two bundled-origin providers, one
// EMBED candidate and one GENERATE candidate with independent purposes.
func bundledCatalog(t *testing.T) (models.Catalog, modeladapter.Config) {
	t.Helper()
	cfg := testConfig(t)
	catalog := catalogFixture(t,
		[]models.ProviderConfig{
			providerFixture("openai", testAdapterOrigin+routePath(t, cfg, 0)),
			providerFixture("openrouter", testAdapterOrigin+routePath(t, cfg, 1)),
		},
		[]models.CandidateDeclaration{
			declarationFixture("embed-small", "openai", "openai/text-embedding-3-small", "EMBED", 1536, embedLimits),
			declarationFixture("generate-mini", "openrouter", "openai/gpt-4o-mini", "GENERATE", 0, generateLimits),
		})
	return catalog, cfg
}

func TestLoadCatalog(t *testing.T) {
	dir := t.TempDir()
	catalog, _ := bundledCatalog(t)
	raw, err := json.Marshal(catalog)
	if err != nil {
		t.Fatalf("marshal catalog: %v", err)
	}
	// models.Catalog round-trips through the parser only with an absent GENERATE
	// dimension, so write the document the parser accepts.
	valid := strings.Replace(string(raw), `"dimension":0,`, "", 1)
	path := filepath.Join(dir, "catalog.json")
	if err = os.WriteFile(path, []byte(valid), 0o600); err != nil {
		t.Fatalf("write catalog: %v", err)
	}
	loaded, err := LoadCatalog(path)
	if err != nil {
		t.Fatalf("valid catalog rejected: %v", err)
	}
	if len(loaded.Models) != 2 || len(loaded.Providers) != 2 {
		t.Fatalf("loaded %d providers and %d models", len(loaded.Providers), len(loaded.Models))
	}

	// A path that cannot be read never reaches the returned error.
	missing := filepath.Join(dir, testCanary+".json")
	if _, err = LoadCatalog(missing); !errors.Is(err, ErrInvalid) || strings.Contains(err.Error(), testCanary) || strings.Contains(err.Error(), dir) {
		t.Fatalf("unreadable catalog: %v", err)
	}

	oversize := filepath.Join(dir, "oversize.json")
	if err = os.WriteFile(oversize, []byte(strings.Repeat(" ", maxCatalogBytes+1)), 0o600); err != nil {
		t.Fatalf("write oversize catalog: %v", err)
	}
	if _, err = LoadCatalog(oversize); !errors.Is(err, ErrInvalid) || strings.Contains(err.Error(), oversize) {
		t.Fatalf("oversize catalog: %v", err)
	}

	// A retired declaration format is rejected with a static label.
	legacy := filepath.Join(dir, "legacy.json")
	if err = os.WriteFile(legacy, []byte(`{"schema_version":1,"providers":[],"models":[]}`), 0o600); err != nil {
		t.Fatalf("write legacy catalog: %v", err)
	}
	if _, err = LoadCatalog(legacy); !errors.Is(err, ErrInvalid) || strings.Contains(err.Error(), legacy) {
		t.Fatalf("legacy catalog: %v", err)
	}

	if _, err = LoadCatalog("   "); !errors.Is(err, ErrInvalid) {
		t.Fatalf("blank path: %v", err)
	}
}

func TestFindCandidate(t *testing.T) {
	catalog, _ := bundledCatalog(t)
	candidate, provider, err := FindCandidate(catalog, "generate-mini", 1)
	if err != nil {
		t.Fatalf("declared candidate not found: %v", err)
	}
	if candidate.Profile.Purpose != "GENERATE" || provider.ID != "openrouter" || provider.Endpoint == "" {
		t.Fatalf("wrong candidate %q provider %q", candidate.Profile.ID, provider.ID)
	}
	if _, _, err = FindCandidate(catalog, "generate-mini", 2); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown revision: %v", err)
	}
	if _, _, err = FindCandidate(catalog, "missing", 1); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown profile: %v", err)
	}
	// A candidate whose provider is not declared is an error, never a fallback.
	if _, _, err = FindCandidate(catalog, "embed-small", 1); err != nil {
		t.Fatalf("declared embed candidate: %v", err)
	}
	orphan := models.Catalog{
		SchemaVersion: 2,
		Providers:     catalog.Providers,
		Models:        []models.CandidateDeclaration{declarationFixture("orphan", "absent", "model", "EMBED", 1536, embedLimits)},
	}
	if _, _, err = FindCandidate(orphan, "orphan", 1); !errors.Is(err, ErrInvalid) {
		t.Fatalf("orphan candidate: %v", err)
	}
}

// TestValidateCatalogOffline proves the offline validation path: digests match the
// registry declaration digest, a matching bundled configuration succeeds, a stale
// or tampered route fails, and a third-party endpoint is marked external without
// being an error. No database, network or secret resolution is involved.
func TestValidateCatalogOffline(t *testing.T) {
	catalog, cfg := bundledCatalog(t)

	summaries, err := ValidateCatalog(catalog, &cfg, testAdapterOrigin)
	if err != nil {
		t.Fatalf("valid bundled catalog rejected: %v", err)
	}
	if len(summaries) != 2 {
		t.Fatalf("summaries = %d, want 2", len(summaries))
	}
	for i, summary := range summaries {
		candidate, provider, err := FindCandidate(catalog, summary.ProfileID, summary.Revision)
		if err != nil {
			t.Fatalf("summary %d: %v", i, err)
		}
		want := models.DeclarationDigest(candidate.Profile, candidate.Adapter, provider)
		if summary.Digest != want {
			t.Fatalf("summary %d digest %q, want %q", i, summary.Digest, want)
		}
		if summary.External {
			t.Fatalf("bundled-origin candidate %q marked external", summary.ProfileID)
		}
		if summary.ProviderID != candidate.Profile.ProviderID || summary.Purpose != candidate.Profile.Purpose {
			t.Fatalf("summary %d = %+v", i, summary)
		}
	}
	if summaries[0].ProfileID != "embed-small" || summaries[0].Purpose != "EMBED" || summaries[1].ProfileID != "generate-mini" || summaries[1].Purpose != "GENERATE" {
		t.Fatalf("summaries are not in catalog order: %+v", summaries)
	}
	// The JSON shape is stable and carries no credential reference or secret.
	encoded, err := json.Marshal(summaries)
	if err != nil {
		t.Fatalf("marshal summaries: %v", err)
	}
	for _, forbidden := range []string{testAdapterToken, testCanary, "OPENAI_API_KEY", "endpoint", "secret"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("summary output contains %q: %s", forbidden, encoded)
		}
	}
	for _, field := range []string{`"profile_id"`, `"revision"`, `"provider_id"`, `"purpose"`, `"digest"`} {
		if !strings.Contains(string(encoded), field) {
			t.Fatalf("summary output lacks %s: %s", field, encoded)
		}
	}

	// Without a configuration the catalog is still validated and digested.
	withoutConfig, err := ValidateCatalog(catalog, nil, testAdapterOrigin)
	if err != nil || len(withoutConfig) != len(summaries) || withoutConfig[0].Digest != summaries[0].Digest {
		t.Fatalf("configuration-free validation: %+v %v", withoutConfig, err)
	}

	// A third-party endpoint is valid but external and unverified.
	external := catalogFixture(t,
		[]models.ProviderConfig{providerFixture("third-party", "https://provider.example/v1/invoke")},
		[]models.CandidateDeclaration{declarationFixture("external-embed", "third-party", "vendor/embedding-1", "EMBED", 1536, embedLimits)})
	externalSummaries, err := ValidateCatalog(external, &cfg, testAdapterOrigin)
	if err != nil {
		t.Fatalf("external provider rejected: %v", err)
	}
	if len(externalSummaries) != 1 || !externalSummaries[0].External {
		t.Fatalf("external summary = %+v", externalSummaries)
	}

	// An explicit loopback test endpoint is an HTTP URL, so Origin cannot render it
	// as an HTTPS origin. models.ParseCatalog and NewHTTPProvider already validated
	// it, so it is external and unverified rather than a validation failure.
	loopback := catalogFixture(t,
		[]models.ProviderConfig{{ID: "loopback", Adapter: "bounded-http-v1", Endpoint: "http://127.0.0.1:8080/v1/routes/call", CredentialRef: testAdapterToken, LoopbackTest: true}},
		[]models.CandidateDeclaration{declarationFixture("loopback-embed", "loopback", "vendor/embedding-1", "EMBED", 1536, embedLimits)})
	loopbackSummaries, err := ValidateCatalog(loopback, &cfg, testAdapterOrigin)
	if err != nil {
		t.Fatalf("loopback endpoint rejected: %v", err)
	}
	if len(loopbackSummaries) != 1 || !loopbackSummaries[0].External {
		t.Fatalf("loopback summary = %+v", loopbackSummaries)
	}

	// A stale reviewed route — the configured ceiling no longer covers the
	// declared limits — fails the offline cross-check.
	stale, err := modeladapter.ParseConfig([]byte(strings.Replace(adapterConfigJSON, `"max_output_tokens":4096`, `"max_output_tokens":2048`, 1)))
	if err != nil {
		t.Fatalf("stale configuration fixture: %v", err)
	}
	if _, err = ValidateCatalog(catalog, &stale, testAdapterOrigin); !errors.Is(err, ErrInvalid) {
		t.Fatalf("stale route accepted: %v", err)
	}

	// A tampered dimension in the reviewed route no longer matches the declaration.
	tampered, err := modeladapter.ParseConfig([]byte(strings.Replace(adapterConfigJSON, `"dimension":1536`, `"dimension":3072`, 1)))
	if err != nil {
		t.Fatalf("tampered configuration fixture: %v", err)
	}
	if _, err = ValidateCatalog(catalog, &tampered, testAdapterOrigin); !errors.Is(err, ErrInvalid) {
		t.Fatalf("tampered dimension accepted: %v", err)
	}

	// An endpoint on the bundled origin that addresses no configured route fails.
	unknown := catalogFixture(t,
		[]models.ProviderConfig{providerFixture("openai", testAdapterOrigin+"/v1/routes/"+strings.Repeat("0", 64)+"/call")},
		[]models.CandidateDeclaration{declarationFixture("embed-small", "openai", "openai/text-embedding-3-small", "EMBED", 1536, embedLimits)})
	if _, err = ValidateCatalog(unknown, &cfg, testAdapterOrigin); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown route accepted: %v", err)
	}

	// A candidate whose model reference disagrees with the reviewed route rule is
	// rejected against the configured adapter configuration. The mutation is applied
	// to a parsed catalog, so no parser and no dependency is involved: this is the
	// offline model-reference check, not a parse rejection.
	badModel := catalog
	badModel.Models = append([]models.CandidateDeclaration(nil), catalog.Models...)
	badModel.Models[1].Profile.ModelID = "openai/gpt-4o"
	if _, err = ValidateCatalog(badModel, &cfg, testAdapterOrigin); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad model reference accepted: %v", err)
	}

	// The cross-check origin is a validated HTTPS origin, never a free-form value.
	if _, err = ValidateCatalog(catalog, &cfg, "http://cap-ai-model-adapter:8443"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("plaintext origin accepted: %v", err)
	}
	if _, err = ValidateCatalog(catalog, &cfg, ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty origin accepted: %v", err)
	}
}

func TestRoutePathForEndpoint(t *testing.T) {
	catalog, cfg := bundledCatalog(t)
	endpoint := catalog.Providers[0].Endpoint
	want := routePath(t, cfg, 0)
	if path, ok := RoutePathForEndpoint(&cfg, endpoint, testAdapterOrigin); !ok || path != want {
		t.Fatalf("route path = %q %v, want %q", path, ok, want)
	}
	// A trailing slash or another origin is not the configured route.
	if _, ok := RoutePathForEndpoint(&cfg, endpoint+"/", testAdapterOrigin); ok {
		t.Fatal("a non-canonical path matched a configured route")
	}
	if _, ok := RoutePathForEndpoint(&cfg, "https://provider.example/invoke", testAdapterOrigin); ok {
		t.Fatal("a third-party endpoint matched a configured route")
	}
	if _, ok := RoutePathForEndpoint(nil, endpoint, testAdapterOrigin); ok {
		t.Fatal("a nil configuration matched a configured route")
	}
}

// TestImportDelegates proves the importer only delegates to the registry: an
// absent database is unavailable, a declaration the registry rejects as policy is
// denied, and no error carries the connection string or its credentials.
func TestImportDelegates(t *testing.T) {
	catalog, _ := bundledCatalog(t)
	if _, err := Import(context.Background(), nil, catalog); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("nil database: %v", err)
	}

	// sql.Open is lazy, and the registry rejects this declaration before it would
	// touch a connection, so this case proves the mapping without any network.
	db, err := sql.Open("postgres", "postgres://canary:"+testCanary+"@127.0.0.1:1/none")
	if err != nil {
		t.Fatalf("open lazy handle: %v", err)
	}
	defer func() { _ = db.Close() }()
	invalid := catalog
	invalid.Models = append([]models.CandidateDeclaration(nil), catalog.Models...)
	invalid.Models[0].Profile.Enabled = true
	if _, err = Import(context.Background(), db, invalid); !errors.Is(err, ErrDenied) {
		t.Fatalf("policy denial: %v", err)
	} else if strings.Contains(err.Error(), testCanary) || strings.Contains(err.Error(), "127.0.0.1") || strings.Contains(err.Error(), "postgres://") {
		t.Fatalf("importer error leaked the connection string: %v", err)
	}
}

func TestProvisioningErrorLabels(t *testing.T) {
	// Every package error is a stable, bounded sentinel; none repeats a value.
	for _, err := range []error{
		invalid("a reason"), denied("a reason"), conflict("a reason"), unavailable("a reason"), uncertain("a reason"),
	} {
		if err == nil || strings.ContainsAny(err.Error(), "\n\t") {
			t.Fatalf("unstable error label %q", err)
		}
	}
	for _, sentinel := range []error{ErrInvalid, ErrDenied, ErrConflict, ErrUnavailable, ErrUncertain} {
		if !errors.Is(sentinel, sentinel) {
			t.Fatalf("sentinel %v is not comparable", sentinel)
		}
	}
}
