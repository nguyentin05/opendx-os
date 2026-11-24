// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package models

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// deploymentBoth is the accepted reference declaration the reject cases mutate.
// It is illustrative only: no model, route or revision below is qualified.
const deploymentBoth = `enabled: true
embedding:
  model: ollama/nomic-embed-text
  execution: local
  api_base: http://ollama:11434
  revision: '2026-11-01'
  dimension: 768
  indexing_parameters: {input_type: passage}
  query_parameters: {input_type: query}
llm:
  model: openrouter/qwen/qwen3-8b
  execution: hosted
  api_key_env: OPENROUTER_API_KEY
  revision: 'route-2026-11-01'
  routing: {provider: deepinfra, data_collection: deny, zdr: true}
  limits: {max_input_tokens: 12000, max_output_tokens: 1500, max_bytes: 262144, timeout_ms: 60000}
`

const deploymentEmbeddingOnly = `enabled: true
embedding:
  model: ollama/nomic-embed-text
  execution: local
  api_base: http://ollama:11434
  revision: '2026-11-01'
  dimension: 768
  indexing_parameters: {input_type: passage}
  query_parameters: {input_type: query}
`

const deploymentLLMOnly = `enabled: true
llm:
  model: anthropic/claude-sonnet-4
  execution: hosted
  api_key_env: ANTHROPIC_API_KEY
  revision: 'route-2026-11-01'
`

// deploymentOpenRouter declares a hosted openrouter.ai route with no reviewed
// provider routing, which the reviewed baseline rejects.
const deploymentOpenRouter = `enabled: true
llm:
  model: openrouter/qwen/qwen3-8b
  execution: hosted
  api_key_env: OPENROUTER_API_KEY
  revision: 'route-2026-11-01'
`

// deploymentRenderable declares both purposes without reviewed provider routing.
// Rendering fails closed for a route whose routing the pinned configuration
// cannot express, so the render contract is pinned on a hosted route that needs
// none.
const deploymentRenderable = `enabled: true
embedding:
  model: ollama/nomic-embed-text
  execution: local
  api_base: http://ollama:11434
  revision: '2026-11-01'
  dimension: 768
  indexing_parameters: {input_type: passage}
  query_parameters: {input_type: query}
llm:
  model: anthropic/claude-sonnet-4
  execution: hosted
  api_key_env: ANTHROPIC_API_KEY
  revision: 'route-2026-11-01'
`

// edit replaces one literal fragment of a declaration, so a reject case states
// exactly the one thing it changes.
func edit(t *testing.T, doc, old, new string) string {
	t.Helper()
	if !strings.Contains(doc, old) {
		t.Fatalf("test setup: %q is not in the declaration", old)
	}
	return strings.Replace(doc, old, new, 1)
}

func loadDeployment(t *testing.T, doc string) Deployment {
	t.Helper()
	d, err := LoadDeployment(strings.NewReader(doc))
	if err != nil {
		t.Fatalf("LoadDeployment() = %v, want a valid deployment", err)
	}
	return d
}

// TestLoadDeployment accepts exactly the declared shapes and rejects every
// ambiguous, unsafe or unsupported one before any value is interpreted.
func TestLoadDeployment(t *testing.T) {
	tests := []struct {
		name    string
		doc     string
		wantErr string
		check   func(t *testing.T, d Deployment)
	}{
		{
			name: "both purposes accepted",
			doc:  deploymentBoth,
			check: func(t *testing.T, d Deployment) {
				if !d.Enabled || d.Embedding == nil || d.LLM == nil {
					t.Fatalf("LoadDeployment() = %+v, want an enabled deployment with both purposes", d)
				}
				if d.Embedding.Model != "ollama/nomic-embed-text" || d.LLM.Model != "openrouter/qwen/qwen3-8b" {
					t.Fatalf("models = %q/%q, want the declared models", d.Embedding.Model, d.LLM.Model)
				}
				if d.Embedding.Limits != (Limits{MaxInputTokens: 8192, MaxOutputTokens: 1, MaxBytes: 65536, TimeoutMS: 30000}) {
					t.Fatalf("embedding limits = %+v, want the embedding defaults (no completion tokens)", d.Embedding.Limits)
				}
				if d.LLM.Limits != (Limits{MaxInputTokens: 12000, MaxOutputTokens: 1500, MaxBytes: 262144, TimeoutMS: 60000}) {
					t.Fatalf("llm limits = %+v, want the declared limits", d.LLM.Limits)
				}
				want := Route{Provider: "deepinfra", DataCollection: "deny", ZDR: true}
				if d.LLM.Routing == nil || *d.LLM.Routing != want {
					t.Fatalf("llm routing = %+v, want %+v", d.LLM.Routing, want)
				}
			},
		},
		{
			name: "embedding only",
			doc:  deploymentEmbeddingOnly,
			check: func(t *testing.T, d Deployment) {
				if d.Embedding == nil || d.LLM != nil {
					t.Fatalf("LoadDeployment() = %+v, want embedding only", d)
				}
			},
		},
		{
			name: "llm only",
			doc:  deploymentLLMOnly,
			check: func(t *testing.T, d Deployment) {
				if d.Embedding != nil || d.LLM == nil {
					t.Fatalf("LoadDeployment() = %+v, want an LLM only", d)
				}
				// Defaults are purpose-appropriate: a generation route keeps a
				// completion bound, an embedding route (above) does not.
				if d.LLM.Limits != (Limits{MaxInputTokens: 8192, MaxOutputTokens: 2048, MaxBytes: 65536, TimeoutMS: 30000}) {
					t.Fatalf("llm limits = %+v, want the generation defaults", d.LLM.Limits)
				}
			},
		},
		{
			// The reviewed baseline requires explicit provider routing on an
			// openrouter.ai route; accepting the route without it would be a
			// declaration broader than the baseline.
			name:    "openrouter route without reviewed routing",
			doc:     deploymentOpenRouter,
			wantErr: "routing",
		},
		{
			name:    "openrouter endpoint declared on a non-openrouter route",
			doc:     edit(t, deploymentLLMOnly, "  api_key_env: ANTHROPIC_API_KEY", "  api_key_env: ANTHROPIC_API_KEY\n  api_base: https://openrouter.ai/api/v1"),
			wantErr: "routing",
		},
		{
			name:    "routing on a non-openrouter route",
			doc:     edit(t, deploymentLLMOnly, "  revision: 'route-2026-11-01'", "  revision: 'route-2026-11-01'\n  routing: {provider: deepinfra, data_collection: deny, zdr: true}"),
			wantErr: "routing",
		},
		{
			name: "openrouter route with reviewed routing accepted",
			doc:  edit(t, deploymentOpenRouter, "  revision: 'route-2026-11-01'", "  revision: 'route-2026-11-01'\n  routing: {provider: deepinfra, data_collection: allow, zdr: false}"),
			check: func(t *testing.T, d Deployment) {
				want := Route{Provider: "deepinfra", DataCollection: "allow", ZDR: false}
				if d.LLM == nil || d.LLM.Routing == nil || *d.LLM.Routing != want {
					t.Fatalf("llm routing = %+v, want %+v", d.LLM.Routing, want)
				}
			},
		},
		{
			name: "no model declared",
			doc:  "enabled: true\n",
			check: func(t *testing.T, d Deployment) {
				if !d.Enabled || d.Embedding != nil || d.LLM != nil {
					t.Fatalf("LoadDeployment() = %+v, want an enabled deployment with no route", d)
				}
			},
		},
		{
			// Deny by default: a declaration that omits the switch must never
			// be read as enabled.
			name: "absent enabled prevents inference",
			doc:  "embedding:\n  model: ollama/nomic-embed-text\n  execution: local\n  api_base: http://ollama:11434\n  revision: '2026-11-01'\n  dimension: 768\n",
			check: func(t *testing.T, d Deployment) {
				if d.Enabled {
					t.Fatal("LoadDeployment() enabled a deployment whose declaration omits enabled")
				}
			},
		},
		{
			name: "false enabled prevents inference",
			doc:  edit(t, deploymentBoth, "enabled: true", "enabled: false"),
			check: func(t *testing.T, d Deployment) {
				if d.Enabled {
					t.Fatal("LoadDeployment() enabled a deployment declared as disabled")
				}
			},
		},
		{
			name:    "unknown purpose section",
			doc:     deploymentBoth + "rerank:\n  model: cohere/rerank-3\n  revision: '1'\n",
			wantErr: "unknown key",
		},
		{
			name:    "purpose inside a model spec",
			doc:     edit(t, deploymentEmbeddingOnly, "  dimension: 768", "  dimension: 768\n  purpose: RERANK"),
			wantErr: "unknown key",
		},
		{
			name:    "unknown top level key",
			doc:     deploymentEmbeddingOnly + "models: []\n",
			wantErr: "unknown key",
		},
		{
			name:    "unknown model spec key",
			doc:     edit(t, deploymentEmbeddingOnly, "  dimension: 768", "  dimensions: 768"),
			wantErr: "unknown key",
		},
		{
			name:    "case aliased key",
			doc:     edit(t, deploymentEmbeddingOnly, "enabled: true", "Enabled: true"),
			wantErr: "unknown key",
		},
		{
			name:    "duplicate key",
			doc:     deploymentEmbeddingOnly + "enabled: true\n",
			wantErr: "duplicate key",
		},
		{
			name:    "duplicate model section",
			doc:     deploymentBoth + "embedding:\n  model: ollama/other\n  execution: local\n  api_base: http://ollama:11434\n  revision: '2'\n  dimension: 768\n",
			wantErr: "duplicate key",
		},
		{
			name:    "trailing document",
			doc:     deploymentBoth + "---\nenabled: false\n",
			wantErr: "trailing",
		},
		{
			// An empty second document is still a second document.
			name:    "trailing empty document",
			doc:     deploymentBoth + "---\n",
			wantErr: "trailing",
		},
		{
			name:    "empty document",
			doc:     "",
			wantErr: "empty",
		},
		{
			name:    "null document",
			doc:     "---\n",
			wantErr: "mapping",
		},
		{
			// `revision: null` is an absent revision spelled as a value, so it
			// must fail as text rather than decode to an empty string.
			name:    "null revision",
			doc:     edit(t, deploymentEmbeddingOnly, "revision: '2026-11-01'", "revision: null"),
			wantErr: "text",
		},
		{
			name:    "anchor and alias",
			doc:     "embedding: &spec\n  model: ollama/nomic-embed-text\n  execution: local\n  api_base: http://ollama:11434\n  revision: '2026-11-01'\n  dimension: 768\nllm: *spec\n",
			wantErr: "alias",
		},
		{
			name:    "missing embedding revision",
			doc:     edit(t, deploymentEmbeddingOnly, "  revision: '2026-11-01'\n", ""),
			wantErr: "revision",
		},
		{
			name:    "missing llm revision",
			doc:     edit(t, deploymentLLMOnly, "  revision: 'route-2026-11-01'\n", ""),
			wantErr: "revision",
		},
		{
			name:    "zero dimension",
			doc:     edit(t, deploymentEmbeddingOnly, "dimension: 768", "dimension: 0"),
			wantErr: "dimension",
		},
		{
			name:    "negative dimension",
			doc:     edit(t, deploymentEmbeddingOnly, "dimension: 768", "dimension: -8"),
			wantErr: "dimension",
		},
		{
			name:    "out of bound dimension",
			doc:     edit(t, deploymentEmbeddingOnly, "dimension: 768", "dimension: 16001"),
			wantErr: "dimension",
		},
		{
			name:    "generation dimension",
			doc:     edit(t, deploymentLLMOnly, "  revision: 'route-2026-11-01'", "  revision: 'route-2026-11-01'\n  dimension: 768"),
			wantErr: "dimension",
		},
		{
			name:    "non integer dimension",
			doc:     edit(t, deploymentEmbeddingOnly, "dimension: 768", "dimension: 768.5"),
			wantErr: "integer",
		},
		{
			name:    "inline secret",
			doc:     edit(t, deploymentLLMOnly, "  api_key_env: ANTHROPIC_API_KEY", "  api_key: sk-live-abc123"),
			wantErr: "unknown key",
		},
		{
			name:    "secret value as credential reference",
			doc:     edit(t, deploymentLLMOnly, "api_key_env: ANTHROPIC_API_KEY", "api_key_env: 'sk-proj-abc123'"),
			wantErr: "environment variable",
		},
		{
			name:    "endpoint credentials",
			doc:     edit(t, deploymentEmbeddingOnly, "api_base: http://ollama:11434", "api_base: http://user:pass@ollama:11434"),
			wantErr: "credentials",
		},
		{
			name:    "endpoint query",
			doc:     edit(t, deploymentEmbeddingOnly, "api_base: http://ollama:11434", "api_base: 'http://ollama:11434/?token=abc'"),
			wantErr: "query",
		},
		{
			name:    "endpoint fragment",
			doc:     edit(t, deploymentEmbeddingOnly, "api_base: http://ollama:11434", "api_base: 'http://ollama:11434#admin'"),
			wantErr: "fragment",
		},
		{
			name:    "endpoint redirect target",
			doc:     edit(t, deploymentEmbeddingOnly, "api_base: http://ollama:11434", "api_base: http://ollama:11434/redirect"),
			wantErr: "redirect",
		},
		{
			name:    "empty api_base",
			doc:     edit(t, deploymentEmbeddingOnly, "  api_base: http://ollama:11434", "  api_base: ''"),
			wantErr: "api_base",
		},
		{
			name:    "cloud endpoint declared local",
			doc:     edit(t, deploymentEmbeddingOnly, "api_base: http://ollama:11434", "api_base: https://ollama.com"),
			wantErr: "not a deployment-local address",
		},
		{
			name:    "public address declared local",
			doc:     edit(t, deploymentEmbeddingOnly, "api_base: http://ollama:11434", "api_base: http://8.8.8.8:11434"),
			wantErr: "not a deployment-local address",
		},
		{
			name:    "hosted provider declared local",
			doc:     edit(t, deploymentEmbeddingOnly, "model: ollama/nomic-embed-text", "model: openai/text-embedding-3-small"),
			wantErr: "hosted provider",
		},
		{
			name:    "private endpoint declared hosted",
			doc:     edit(t, deploymentLLMOnly, "  api_key_env: ANTHROPIC_API_KEY", "  api_key_env: ANTHROPIC_API_KEY\n  api_base: https://127.0.0.1:8443"),
			wantErr: "private address",
		},
		{
			name:    "plain http hosted endpoint",
			doc:     edit(t, deploymentLLMOnly, "  api_key_env: ANTHROPIC_API_KEY", "  api_key_env: ANTHROPIC_API_KEY\n  api_base: http://api.example.com"),
			wantErr: "HTTPS",
		},
		{
			name:    "unknown execution",
			doc:     edit(t, deploymentEmbeddingOnly, "execution: local", "execution: CLOUD"),
			wantErr: "execution",
		},
		{
			name:    "local route without api_base",
			doc:     edit(t, deploymentEmbeddingOnly, "  api_base: http://ollama:11434\n", ""),
			wantErr: "api_base",
		},
		{
			name:    "hosted route without credential reference",
			doc:     edit(t, deploymentLLMOnly, "  api_key_env: ANTHROPIC_API_KEY\n", ""),
			wantErr: "api_key_env",
		},
		{
			name:    "endpoint override through semantic parameters",
			doc:     edit(t, deploymentEmbeddingOnly, "indexing_parameters: {input_type: passage}", "indexing_parameters: {api_base: 'http://evil:11434'}"),
			wantErr: "not allowed",
		},
		{
			name:    "unreviewed semantic parameter",
			doc:     edit(t, deploymentEmbeddingOnly, "indexing_parameters: {input_type: passage}", "indexing_parameters: {dimensions: '3'}"),
			wantErr: "not allowed",
		},
		{
			name:    "unreviewed semantic parameter value",
			doc:     edit(t, deploymentEmbeddingOnly, "indexing_parameters: {input_type: passage}", "indexing_parameters: {input_type: everything}"),
			wantErr: "value",
		},
		{
			name:    "unpaired semantic parameters",
			doc:     edit(t, deploymentBoth, "  query_parameters: {input_type: query}\n", ""),
			wantErr: "query_parameters",
		},
		{
			name:    "semantic parameters on a generation route",
			doc:     edit(t, deploymentLLMOnly, "  revision: 'route-2026-11-01'", "  revision: 'route-2026-11-01'\n  indexing_parameters: {input_type: passage}\n  query_parameters: {input_type: query}"),
			wantErr: "embedding",
		},
		{
			name:    "incomplete limits",
			doc:     edit(t, deploymentBoth, "limits: {max_input_tokens: 12000, max_output_tokens: 1500, max_bytes: 262144, timeout_ms: 60000}", "limits: {max_input_tokens: 12000}"),
			wantErr: "limits",
		},
		{
			name:    "out of bound limits",
			doc:     edit(t, deploymentBoth, "max_bytes: 262144", "max_bytes: 2000000"),
			wantErr: "limits",
		},
		{
			name:    "zero limit",
			doc:     edit(t, deploymentBoth, "timeout_ms: 60000", "timeout_ms: 0"),
			wantErr: "limits",
		},
		{
			name:    "wildcard model",
			doc:     edit(t, deploymentLLMOnly, "model: anthropic/claude-sonnet-4", "model: anthropic/*"),
			wantErr: "model",
		},
		{
			name:    "mutable router alias",
			doc:     edit(t, deploymentLLMOnly, "model: anthropic/claude-sonnet-4", "model: anthropic/auto"),
			wantErr: "model",
		},
		{
			// A local model is pinned by tag: `revision` records the review, the
			// tag is part of the upstream identity.
			name: "tagged local model accepted",
			doc:  edit(t, deploymentEmbeddingOnly, "model: ollama/nomic-embed-text", "model: 'ollama/nomic-embed-text:v1.5'"),
			check: func(t *testing.T, d Deployment) {
				if d.Embedding.Model != "ollama/nomic-embed-text:v1.5" {
					t.Fatalf("model = %q, want the tagged model", d.Embedding.Model)
				}
			},
		},
		{
			name:    "floating local tag",
			doc:     edit(t, deploymentEmbeddingOnly, "model: ollama/nomic-embed-text", "model: 'ollama/nomic-embed-text:latest'"),
			wantErr: "model",
		},
		{
			name:    "model without provider prefix",
			doc:     edit(t, deploymentEmbeddingOnly, "model: ollama/nomic-embed-text", "model: nomic-embed-text"),
			wantErr: "model",
		},
		{
			name:    "model with a routing suffix",
			doc:     edit(t, deploymentLLMOnly, "model: anthropic/claude-sonnet-4", "model: 'anthropic/claude-sonnet-4:free'"),
			wantErr: "model",
		},
		{
			name:    "routing on a local route",
			doc:     edit(t, deploymentEmbeddingOnly, "  dimension: 768", "  dimension: 768\n  routing: {provider: deepinfra, data_collection: deny, zdr: true}"),
			wantErr: "routing",
		},
		{
			name:    "incomplete routing",
			doc:     edit(t, deploymentBoth, "routing: {provider: deepinfra, data_collection: deny, zdr: true}", "routing: {provider: deepinfra, data_collection: deny}"),
			wantErr: "routing",
		},
		{
			name:    "unreviewed routing policy",
			doc:     edit(t, deploymentBoth, "data_collection: deny", "data_collection: maybe"),
			wantErr: "routing",
		},
		{
			name:    "duplicate key inside limits",
			doc:     edit(t, deploymentBoth, "max_input_tokens: 12000", "max_input_tokens: 12000, max_input_tokens: 12000"),
			wantErr: "duplicate key",
		},
		{
			name:    "unknown key inside limits",
			doc:     edit(t, deploymentBoth, "timeout_ms: 60000}", "timeout_ms: 60000, max_retries: 3}"),
			wantErr: "unknown key",
		},
		{
			name:    "duplicate key inside routing",
			doc:     edit(t, deploymentBoth, "provider: deepinfra", "provider: deepinfra, provider: deepinfra"),
			wantErr: "duplicate key",
		},
		{
			name:    "unknown key inside routing",
			doc:     edit(t, deploymentBoth, "zdr: true}", "zdr: true, fallback: deepinfra}"),
			wantErr: "unknown key",
		},
		{
			name:    "oversized declaration",
			doc:     edit(t, deploymentEmbeddingOnly, "enabled: true", "enabled: true\n"+strings.Repeat("# "+strings.Repeat("x", 90)+"\n", 800)),
			wantErr: "size",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			d, err := LoadDeployment(strings.NewReader(test.doc))
			if test.wantErr == "" {
				if err != nil {
					t.Fatalf("LoadDeployment() = %v, want a valid deployment", err)
				}
				if test.check != nil {
					test.check(t, d)
				}
				return
			}
			if err == nil {
				t.Fatalf("LoadDeployment() accepted %+v, want a rejection containing %q", d, test.wantErr)
			}
			if !errors.Is(err, ErrDeployment) {
				t.Fatalf("LoadDeployment() = %v, want ErrDeployment", err)
			}
			if !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(test.wantErr)) {
				t.Fatalf("LoadDeployment() = %v, want a rejection mentioning %q", err, test.wantErr)
			}
		})
	}
}

// TestExampleDeploymentLoads keeps the committed example inside the schema: it
// is the only declaration-shaped documentation a deployment copies from.
func TestExampleDeploymentLoads(t *testing.T) {
	raw, err := os.ReadFile("../../../../models.example.yaml")
	if err != nil {
		t.Fatalf("ReadFile(models.example.yaml) = %v", err)
	}
	d, err := LoadDeployment(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("LoadDeployment(models.example.yaml) = %v", err)
	}
	if d.Embedding == nil || d.LLM == nil {
		t.Fatalf("models.example.yaml = %+v, want one route per purpose", d)
	}
}

func embeddingProfile(t *testing.T, d Deployment) Profile {
	t.Helper()
	p, err := d.Embedding.Profile("EMBED")
	if err != nil {
		t.Fatalf("Embedding.Profile(EMBED) = %v", err)
	}
	return p
}

func llmProfile(t *testing.T, d Deployment) Profile {
	t.Helper()
	p, err := d.LLM.Profile("GENERATE")
	if err != nil {
		t.Fatalf("LLM.Profile(GENERATE) = %v", err)
	}
	return p
}

// TestDeploymentIdentity pins the immutable identity: the same reviewed route
// always derives the same profile and alias, and every reviewed change derives a
// new one.
func TestDeploymentIdentity(t *testing.T) {
	base := loadDeployment(t, deploymentBoth)
	baseEmbed, baseLLM := embeddingProfile(t, base), llmProfile(t, base)
	baseEmbedAlias, err := base.Embedding.Alias("EMBED")
	if err != nil {
		t.Fatalf("Embedding.Alias(EMBED) = %v", err)
	}
	baseLLMAlias, err := base.LLM.Alias("GENERATE")
	if err != nil {
		t.Fatalf("LLM.Alias(GENERATE) = %v", err)
	}

	if baseEmbed.Purpose != "EMBED" || baseEmbed.Execution != "LOCAL" || baseEmbed.Dimension != 768 || baseEmbed.Revision != 1 || !baseEmbed.Enabled {
		t.Fatalf("embedding profile = %+v, want a LOCAL EMBED profile of revision 1 with the declared dimension", baseEmbed)
	}
	if baseLLM.Purpose != "GENERATE" || baseLLM.Execution != "HOSTED" || baseLLM.Dimension != 0 || baseLLM.Revision != 1 || !baseLLM.Enabled {
		t.Fatalf("llm profile = %+v, want a HOSTED GENERATE profile of revision 1 with no dimension", baseLLM)
	}
	if baseLLM.ProviderID != "openrouter" || baseLLM.ModelID != "openrouter/qwen/qwen3-8b" || baseLLM.CredentialRef != "OPENROUTER_API_KEY" {
		t.Fatalf("llm profile = %+v, want the declared provider, model and credential reference", baseLLM)
	}
	if len(baseEmbed.ID) != 64 || len(baseLLM.ID) != 64 {
		t.Fatalf("profile ids = %q/%q, want digest-derived identities", baseEmbed.ID, baseLLM.ID)
	}
	if baseEmbed.ID == baseLLM.ID {
		t.Fatalf("profile ids are equal (%q), want purpose-separated identities", baseEmbed.ID)
	}
	if !strings.Contains(baseEmbedAlias, baseEmbed.ID) || !strings.HasPrefix(baseEmbedAlias, "dxos-embed-") {
		t.Fatalf("embedding alias = %q, want a digest-derived dxos-embed- alias", baseEmbedAlias)
	}
	if !strings.Contains(baseLLMAlias, baseLLM.ID) || !strings.HasPrefix(baseLLMAlias, "dxos-llm-") {
		t.Fatalf("llm alias = %q, want a digest-derived dxos-llm- alias", baseLLMAlias)
	}

	// Golden identity. These literals are the digest of the deploymentBoth
	// embedding route as reviewed here: the one pinned value that makes an
	// unintended change to the identity struct, its JSON tags, the domain
	// separator or the normalization of a hashed field fail loudly. A deliberate
	// identity change must update them, and with them every stored profile and
	// every proxy alias of the deployments already running.
	const (
		wantEmbedID    = "9d1ef5525d1ff7c6931b0f8037ad3333e10978b97ff94bedd3de49764ac7262d"
		wantEmbedAlias = "dxos-embed-9d1ef5525d1ff7c6931b0f8037ad3333e10978b97ff94bedd3de49764ac7262d"
	)
	if baseEmbed.ID != wantEmbedID {
		t.Fatalf("embedding profile id = %q, want the pinned golden %q", baseEmbed.ID, wantEmbedID)
	}
	if baseEmbedAlias != wantEmbedAlias {
		t.Fatalf("embedding alias = %q, want the pinned golden %q", baseEmbedAlias, wantEmbedAlias)
	}

	// Field order, quoting and spacing are declaration details, not identity.
	reordered := loadDeployment(t, `llm:
  limits: {timeout_ms: 60000, max_bytes: 262144, max_output_tokens: 1500, max_input_tokens: 12000}
  routing: {zdr: true, data_collection: "deny", provider: "deepinfra"}
  revision: "route-2026-11-01"
  api_key_env: "OPENROUTER_API_KEY"
  execution: "hosted"
  model: "openrouter/qwen/qwen3-8b"
embedding:
  query_parameters: {input_type: "query"}
  indexing_parameters: {input_type: "passage"}
  dimension: 768
  revision: "2026-11-01"
  api_base: "http://ollama:11434"
  execution: "local"
  model: "ollama/nomic-embed-text"
enabled: true
`)
	if reorderedEmbed, reorderedLLM := embeddingProfile(t, reordered), llmProfile(t, reordered); reorderedEmbed != baseEmbed || reorderedLLM != baseLLM {
		t.Fatalf("reordered declaration derived %+v/%+v, want %+v/%+v", reorderedEmbed, reorderedLLM, baseEmbed, baseLLM)
	}
	if alias, err := reordered.Embedding.Alias("EMBED"); err != nil || alias != baseEmbedAlias {
		t.Fatalf("reordered alias = %q, %v, want %q", alias, err, baseEmbedAlias)
	}

	for _, change := range []struct {
		name string
		doc  string
		// wantEmbedding reports whether the embedding identity must change too.
		wantEmbedding bool
	}{
		{name: "upstream model change", doc: edit(t, deploymentBoth, "openrouter/qwen/qwen3-8b", "openrouter/qwen/qwen3-32b")},
		// A provider change leaves openrouter.ai, which no longer permits routing,
		// so the case changes the provider prefix and drops the routing block.
		{name: "provider change", doc: edit(t, edit(t, deploymentBoth, "  routing: {provider: deepinfra, data_collection: deny, zdr: true}\n", ""), "openrouter/qwen/qwen3-8b", "mistral/mistral-large")},
		{name: "route revision change", doc: edit(t, deploymentBoth, "route-2026-11-01", "route-2026-12-01")},
		{name: "approved routing change", doc: edit(t, deploymentBoth, "provider: deepinfra", "provider: together")},
		{name: "routing policy change", doc: edit(t, deploymentBoth, "zdr: true", "zdr: false")},
		{name: "credential reference change", doc: edit(t, deploymentBoth, "OPENROUTER_API_KEY", "OPENROUTER_API_KEY_ROTATED")},
		{name: "declared limits change", doc: edit(t, deploymentBoth, "max_output_tokens: 1500", "max_output_tokens: 1024")},
		{name: "embedding model change", doc: edit(t, deploymentBoth, "ollama/nomic-embed-text", "ollama/mxbai-embed-large"), wantEmbedding: true},
		{name: "embedding revision change", doc: edit(t, deploymentBoth, "'2026-11-01'", "'2026-11-02'"), wantEmbedding: true},
		{name: "embedding endpoint change", doc: edit(t, deploymentBoth, "http://ollama:11434", "http://embeddings:11434"), wantEmbedding: true},
		{name: "dimension change", doc: edit(t, deploymentBoth, "dimension: 768", "dimension: 1024"), wantEmbedding: true},
		{name: "indexing parameter change", doc: edit(t, deploymentBoth, "indexing_parameters: {input_type: passage}", "indexing_parameters: {input_type: query}"), wantEmbedding: true},
		{name: "query parameter change", doc: edit(t, deploymentBoth, "query_parameters: {input_type: query}", "query_parameters: {input_type: passage}"), wantEmbedding: true},
		{name: "index and query modes swapped", doc: edit(t, deploymentBoth, "indexing_parameters: {input_type: passage}\n  query_parameters: {input_type: query}", "indexing_parameters: {input_type: query}\n  query_parameters: {input_type: passage}"), wantEmbedding: true},
	} {
		t.Run(change.name, func(t *testing.T) {
			d := loadDeployment(t, change.doc)
			if change.wantEmbedding {
				if p := embeddingProfile(t, d); p.ID == baseEmbed.ID {
					t.Fatalf("embedding identity %q survived %s", p.ID, change.name)
				}
				return
			}
			if p := llmProfile(t, d); p.ID == baseLLM.ID {
				t.Fatalf("llm identity %q survived %s", p.ID, change.name)
			}
			// A generation-only change must never force a re-embedding.
			if p := embeddingProfile(t, d); p.ID != baseEmbed.ID {
				t.Fatalf("embedding identity changed to %q on %s", p.ID, change.name)
			}
			if alias, err := d.Embedding.Alias("EMBED"); err != nil || alias != baseEmbedAlias {
				t.Fatalf("embedding alias = %q, %v on %s, want %q", alias, err, change.name, baseEmbedAlias)
			}
		})
	}

	// The credential reference is reviewed metadata; the secret value behind it
	// is not part of identity, so a rotation reuses the same profile.
	t.Run("secret value rotation keeps identity", func(t *testing.T) {
		t.Setenv("OPENROUTER_API_KEY", "sk-rotated-value")
		rotated := loadDeployment(t, deploymentBoth)
		if p := llmProfile(t, rotated); p != baseLLM {
			t.Fatalf("profile after secret rotation = %+v, want %+v", p, baseLLM)
		}
	})

	t.Run("purpose is explicit", func(t *testing.T) {
		if _, err := base.Embedding.Profile("RERANK"); !errors.Is(err, ErrDeployment) {
			t.Fatalf("Embedding.Profile(RERANK) = %v, want ErrDeployment", err)
		}
		if _, err := base.LLM.Alias("OCR"); !errors.Is(err, ErrDeployment) {
			t.Fatalf("LLM.Alias(OCR) = %v, want ErrDeployment", err)
		}
		if _, err := base.LLM.Profile("EMBED"); err == nil {
			t.Fatal("LLM.Profile(EMBED) derived an embedding profile from a generation route")
		}
		if _, err := base.Embedding.Profile("GENERATE"); err == nil {
			t.Fatal("Embedding.Profile(GENERATE) derived a generation profile from an embedding route")
		}
	})

	t.Run("unreviewed spec cannot derive identity", func(t *testing.T) {
		// A spec built outside LoadDeployment is still validated, so a caller
		// cannot hand-derive an identity the declaration grammar rejects.
		bad := ModelSpec{Model: "openrouter/auto", Execution: "HOSTED", APIKeyEnv: "OPENROUTER_API_KEY", Revision: "r1"}
		if _, err := bad.Profile("GENERATE"); !errors.Is(err, ErrDeployment) {
			t.Fatalf("Profile() = %v, want ErrDeployment", err)
		}
		if _, err := (&ModelSpec{}).Alias("EMBED"); !errors.Is(err, ErrDeployment) {
			t.Fatalf("Alias() = %v, want ErrDeployment", err)
		}
	})
}

// proxyClaim records a configuration setting that is only acceptable in its
// inactive form: absent, explicitly false, or an empty list or mapping. The
// render contract is asserted on the parsed document instead of on substrings,
// so a stronger explicit `cache: false` or `fallbacks: []` passes while any
// value that enables or declares the setting still fails.
type proxyClaim struct {
	declared bool
	value    any
}

func (c *proxyClaim) UnmarshalYAML(node *yaml.Node) error {
	c.declared = true
	return node.Decode(&c.value)
}

// inactive reports whether the setting does nothing: absent, false, an empty
// list or an empty mapping. A string, a number, true or a populated collection
// is a declared, enabling value.
func (c proxyClaim) inactive() bool {
	if !c.declared {
		return true
	}
	return inactiveValue(c.value)
}

// proxyDocument mirrors the generated configuration for parsing in tests.
type proxyDocument struct {
	ModelList []struct {
		ModelName     string `yaml:"model_name"`
		LiteLLMParams struct {
			Model   string `yaml:"model"`
			APIBase string `yaml:"api_base"`
			APIKey  string `yaml:"api_key"`
		} `yaml:"litellm_params"`
		ModelInfo struct {
			ID   string `yaml:"id"`
			Mode string `yaml:"mode"`
		} `yaml:"model_info"`
	} `yaml:"model_list"`
	LiteLLMSettings struct {
		DropParams            *bool      `yaml:"drop_params"`
		TurnOffMessageLogging *bool      `yaml:"turn_off_message_logging"`
		Cache                 proxyClaim `yaml:"cache"`
	} `yaml:"litellm_settings"`
	GeneralSettings struct {
		DisableSpendLogs *bool      `yaml:"disable_spend_logs"`
		DatabaseURL      proxyClaim `yaml:"database_url"`
		Database         proxyClaim `yaml:"database"`
		StoreModelInDB   proxyClaim `yaml:"store_model_in_db"`
		MasterKey        proxyClaim `yaml:"master_key"`
		Telemetry        proxyClaim `yaml:"telemetry"`
	} `yaml:"general_settings"`
	RouterSettings struct {
		NumRetries             *int       `yaml:"num_retries"`
		Fallbacks              proxyClaim `yaml:"fallbacks"`
		ContextWindowFallbacks proxyClaim `yaml:"context_window_fallbacks"`
		ContentPolicyFallbacks proxyClaim `yaml:"content_policy_fallbacks"`
	} `yaml:"router_settings"`
}

func renderProxy(t *testing.T, d Deployment) ([]byte, proxyDocument) {
	t.Helper()
	raw, err := RenderProxyConfig(d)
	if err != nil {
		t.Fatalf("RenderProxyConfig() = %v", err)
	}
	var doc proxyDocument
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&doc); err != nil {
		t.Fatalf("generated configuration is not strict YAML: %v\n%s", err, raw)
	}
	return raw, doc
}

// inactiveValue reports whether a decoded YAML value does nothing if it is a
// configuration key.
func inactiveValue(node any) bool {
	switch typed := node.(type) {
	case nil:
		return true
	case bool:
		return !typed
	case []any:
		return len(typed) == 0
	case map[string]any:
		return len(typed) == 0
	}
	return false
}

// enableKeyProblem walks the parsed document and returns the path of the first
// key that switches telemetry, a database, message logging or spend logging on,
// or declares a backing store. It parses the tree instead of matching
// substrings, so a disabling switch or an empty list is acceptable while any
// enabling or declaring value is not.
func enableKeyProblem(raw []byte) (string, error) {
	var tree map[string]any
	if err := yaml.Unmarshal(raw, &tree); err != nil {
		return "", err
	}
	// The only keys allowed to carry these words are the disabling switches.
	disabling := map[string]bool{"turn_off_message_logging": true, "disable_spend_logs": true}
	var walk func(path string, node any) string
	walk = func(path string, node any) string {
		switch typed := node.(type) {
		case map[string]any:
			keys := make([]string, 0, len(typed))
			for key := range typed {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				next := path + "." + key
				lower := strings.ToLower(key)
				restricted := strings.Contains(lower, "telemetry") || strings.Contains(lower, "database") ||
					strings.Contains(lower, "message_log") || strings.Contains(lower, "logging") ||
					strings.Contains(lower, "spend_log")
				if restricted && !disabling[key] && !inactiveValue(typed[key]) {
					return next
				}
				if found := walk(next, typed[key]); found != "" {
					return found
				}
			}
		case []any:
			for i, item := range typed {
				if found := walk(fmt.Sprintf("%s[%d]", path, i), item); found != "" {
					return found
				}
			}
		}
		return ""
	}
	return walk("", tree), nil
}

// TestRenderProxyConfig pins the generated proxy configuration: exactly the
// declared routes, fixed digest aliases, secret references only, every automatic
// dispatch path disabled, and no telemetry, database or message-logging setting.
func TestRenderProxyConfig(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-live-do-not-render")
	d := loadDeployment(t, deploymentRenderable)
	raw, doc := renderProxy(t, d)

	if len(doc.ModelList) != 2 {
		t.Fatalf("model_list = %d entries, want one per configured purpose\n%s", len(doc.ModelList), raw)
	}
	wantAliases := make([]string, 0, 2)
	for _, spec := range []*ModelSpec{d.Embedding, d.LLM} {
		purpose := "EMBED"
		if spec == d.LLM {
			purpose = "GENERATE"
		}
		alias, err := spec.Alias(purpose)
		if err != nil {
			t.Fatalf("Alias(%s) = %v", purpose, err)
		}
		wantAliases = append(wantAliases, alias)
		profile, err := spec.Profile(purpose)
		if err != nil {
			t.Fatalf("Profile(%s) = %v", purpose, err)
		}
		if !strings.Contains(string(raw), profile.ID) {
			t.Fatalf("generated configuration does not carry identity %q\n%s", profile.ID, raw)
		}
	}
	for i, alias := range wantAliases {
		if doc.ModelList[i].ModelName != alias {
			t.Fatalf("model_list[%d].model_name = %q, want %q", i, doc.ModelList[i].ModelName, alias)
		}
	}
	embed, llm := doc.ModelList[0], doc.ModelList[1]
	if embed.LiteLLMParams.Model != "ollama/nomic-embed-text" || embed.LiteLLMParams.APIBase != "http://ollama:11434" {
		t.Fatalf("embedding deployment = %+v, want the declared model and endpoint", embed.LiteLLMParams)
	}
	if embed.ModelInfo.Mode != "embedding" {
		t.Fatalf("embedding model_info.mode = %q, want embedding", embed.ModelInfo.Mode)
	}
	if llm.LiteLLMParams.Model != "anthropic/claude-sonnet-4" {
		t.Fatalf("llm deployment = %+v, want the declared model", llm.LiteLLMParams)
	}
	if llm.LiteLLMParams.APIKey != "os.environ/ANTHROPIC_API_KEY" {
		t.Fatalf("llm api_key = %q, want the environment reference", llm.LiteLLMParams.APIKey)
	}
	if embed.LiteLLMParams.APIKey != "" {
		t.Fatalf("embedding api_key = %q, want no credential reference", embed.LiteLLMParams.APIKey)
	}

	if doc.LiteLLMSettings.DropParams == nil || *doc.LiteLLMSettings.DropParams {
		t.Fatalf("litellm_settings.drop_params = %v, want an explicit false", doc.LiteLLMSettings.DropParams)
	}
	if doc.LiteLLMSettings.TurnOffMessageLogging == nil || !*doc.LiteLLMSettings.TurnOffMessageLogging {
		t.Fatalf("litellm_settings.turn_off_message_logging = %v, want true", doc.LiteLLMSettings.TurnOffMessageLogging)
	}
	if doc.GeneralSettings.DisableSpendLogs == nil || !*doc.GeneralSettings.DisableSpendLogs {
		t.Fatalf("general_settings.disable_spend_logs = %v, want true", doc.GeneralSettings.DisableSpendLogs)
	}
	if doc.RouterSettings.NumRetries == nil || *doc.RouterSettings.NumRetries != 0 {
		t.Fatalf("router_settings.num_retries = %v, want 0", doc.RouterSettings.NumRetries)
	}

	// Parsed settings, not substrings: cache, the three fallback lists, a
	// database, message logging and telemetry must all be absent or explicitly
	// inactive. Emitting `cache: false` or `fallbacks: []` is a stronger
	// statement of the same control and passes; any enabling value fails.
	for _, claim := range []struct {
		name  string
		claim proxyClaim
	}{
		{"litellm_settings.cache", doc.LiteLLMSettings.Cache},
		{"router_settings.fallbacks", doc.RouterSettings.Fallbacks},
		{"router_settings.context_window_fallbacks", doc.RouterSettings.ContextWindowFallbacks},
		{"router_settings.content_policy_fallbacks", doc.RouterSettings.ContentPolicyFallbacks},
		{"general_settings.database", doc.GeneralSettings.Database},
		{"general_settings.database_url", doc.GeneralSettings.DatabaseURL},
		{"general_settings.store_model_in_db", doc.GeneralSettings.StoreModelInDB},
		{"general_settings.master_key", doc.GeneralSettings.MasterKey},
		{"general_settings.telemetry", doc.GeneralSettings.Telemetry},
	} {
		if !claim.claim.inactive() {
			t.Fatalf("%s = %v, want it absent or explicitly inactive", claim.name, claim.claim.value)
		}
	}

	// Nothing that could widen dispatch or reference a backing store at all.
	for _, forbidden := range []string{
		"sk-live-do-not-render", "postgres", "callbacks", "success_callback",
		"drop_params: true", "num_retries: 2", "*",
	} {
		if strings.Contains(strings.ToLower(string(raw)), strings.ToLower(forbidden)) {
			t.Fatalf("generated configuration contains %q\n%s", forbidden, raw)
		}
	}

	t.Run("no telemetry database or message logging enable keys", func(t *testing.T) {
		// LiteLLM reads telemetry from the proxy environment at the pinned
		// version, so the rendered configuration must carry no telemetry,
		// database or message-logging setting that could enable one. The
		// requirement itself is owned by ADR 0011 (proxy environment, T4/T7).
		problem, err := enableKeyProblem(raw)
		if err != nil {
			t.Fatalf("generated configuration is not parseable: %v", err)
		}
		if problem != "" {
			t.Fatalf("generated configuration enables %s\n%s", problem, raw)
		}
		// The check is not vacuous, and an explicit disabling value passes.
		if problem, err := enableKeyProblem([]byte("general_settings:\n  telemetry: true\n")); err != nil || problem != ".general_settings.telemetry" {
			t.Fatalf("enableKeyProblem() = %q, %v; want it to report an enabled telemetry key", problem, err)
		}
		if problem, err := enableKeyProblem([]byte("litellm_settings:\n  cache: false\nrouter_settings:\n  fallbacks: []\n")); err != nil || problem != "" {
			t.Fatalf("enableKeyProblem() = %q, %v; want an explicit disabling value to pass", problem, err)
		}
	})

	t.Run("deterministic", func(t *testing.T) {
		again, _ := renderProxy(t, loadDeployment(t, deploymentRenderable))
		if !bytes.Equal(again, raw) {
			t.Fatalf("RenderProxyConfig() is not deterministic:\n%s\n%s", again, raw)
		}
	})

	t.Run("embedding only", func(t *testing.T) {
		only, doc := renderProxy(t, loadDeployment(t, deploymentEmbeddingOnly))
		if len(doc.ModelList) != 1 || doc.ModelList[0].ModelInfo.Mode != "embedding" {
			t.Fatalf("model_list = %+v, want the embedding route only\n%s", doc.ModelList, only)
		}
	})

	t.Run("llm only", func(t *testing.T) {
		only, doc := renderProxy(t, loadDeployment(t, deploymentLLMOnly))
		if len(doc.ModelList) != 1 || doc.ModelList[0].ModelInfo.Mode != "" {
			t.Fatalf("model_list = %+v, want the generation route only\n%s", doc.ModelList, only)
		}
	})

	t.Run("disabled renders no route", func(t *testing.T) {
		// A disabled declaration must not start inference for an unused route.
		raw, doc := renderProxy(t, loadDeployment(t, edit(t, deploymentRenderable, "enabled: true", "enabled: false")))
		if len(doc.ModelList) != 0 {
			t.Fatalf("model_list = %+v, want no route\n%s", doc.ModelList, raw)
		}
		if doc.RouterSettings.NumRetries == nil || *doc.RouterSettings.NumRetries != 0 {
			t.Fatalf("router_settings.num_retries = %v, want 0", doc.RouterSettings.NumRetries)
		}
	})

	t.Run("no model renders no route", func(t *testing.T) {
		if _, doc := renderProxy(t, loadDeployment(t, "enabled: true\n")); len(doc.ModelList) != 0 {
			t.Fatalf("model_list = %+v, want no route", doc.ModelList)
		}
	})

	t.Run("route with reviewed provider routing is not rendered", func(t *testing.T) {
		// Rendering a route whose routing it cannot express would turn a reviewed
		// data-handling control into an accepted no-op, so the renderer fails
		// closed instead, enabled or not.
		for _, doc := range []string{deploymentBoth, edit(t, deploymentBoth, "enabled: true", "enabled: false")} {
			_, err := RenderProxyConfig(loadDeployment(t, doc))
			if !errors.Is(err, ErrDeployment) {
				t.Fatalf("RenderProxyConfig() = %v, want ErrDeployment for a route with routing", err)
			}
			if !strings.Contains(strings.ToLower(err.Error()), "routing") {
				t.Fatalf("RenderProxyConfig() = %v, want an error naming the routing control", err)
			}
		}
	})

	t.Run("unreviewed deployment rejected", func(t *testing.T) {
		if _, err := RenderProxyConfig(Deployment{Embedding: &ModelSpec{Model: "ollama/x"}}); !errors.Is(err, ErrDeployment) {
			t.Fatalf("RenderProxyConfig() = %v, want ErrDeployment", err)
		}
	})
}
