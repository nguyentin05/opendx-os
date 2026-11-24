// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package models

import (
	"bytes"
	"errors"
	"os"
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
  model: openrouter/qwen/qwen3-8b
  execution: hosted
  api_key_env: OPENROUTER_API_KEY
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
				if d.Embedding.Limits != defaultDeploymentLimits {
					t.Fatalf("embedding limits = %+v, want the safe defaults %+v", d.Embedding.Limits, defaultDeploymentLimits)
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
			doc:     edit(t, deploymentLLMOnly, "  api_key_env: OPENROUTER_API_KEY", "  api_key: sk-live-abc123"),
			wantErr: "unknown key",
		},
		{
			name:    "secret value as credential reference",
			doc:     edit(t, deploymentLLMOnly, "api_key_env: OPENROUTER_API_KEY", "api_key_env: 'sk-proj-abc123'"),
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
			doc:     edit(t, deploymentLLMOnly, "  api_key_env: OPENROUTER_API_KEY", "  api_key_env: OPENROUTER_API_KEY\n  api_base: https://127.0.0.1:8443"),
			wantErr: "private address",
		},
		{
			name:    "plain http hosted endpoint",
			doc:     edit(t, deploymentLLMOnly, "  api_key_env: OPENROUTER_API_KEY", "  api_key_env: OPENROUTER_API_KEY\n  api_base: http://api.example.com"),
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
			doc:     edit(t, deploymentLLMOnly, "  api_key_env: OPENROUTER_API_KEY\n", ""),
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
			doc:     edit(t, deploymentLLMOnly, "model: openrouter/qwen/qwen3-8b", "model: openrouter/*"),
			wantErr: "model",
		},
		{
			name:    "mutable router alias",
			doc:     edit(t, deploymentLLMOnly, "model: openrouter/qwen/qwen3-8b", "model: openrouter/auto"),
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
			doc:     edit(t, deploymentLLMOnly, "model: openrouter/qwen/qwen3-8b", "model: 'openrouter/qwen/qwen3-8b:free'"),
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
		{name: "provider change", doc: edit(t, deploymentBoth, "openrouter/qwen/qwen3-8b", "anthropic/claude-sonnet-4")},
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
		DropParams            *bool `yaml:"drop_params"`
		TurnOffMessageLogging *bool `yaml:"turn_off_message_logging"`
		Cache                 *bool `yaml:"cache"`
	} `yaml:"litellm_settings"`
	GeneralSettings struct {
		DisableSpendLogs *bool `yaml:"disable_spend_logs"`
		DatabaseURL      any   `yaml:"database_url"`
		StoreModelInDB   any   `yaml:"store_model_in_db"`
		MasterKey        any   `yaml:"master_key"`
	} `yaml:"general_settings"`
	RouterSettings struct {
		NumRetries *int `yaml:"num_retries"`
		Fallbacks  any  `yaml:"fallbacks"`
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

// TestRenderProxyConfig pins the generated proxy configuration: exactly the
// declared routes, fixed digest aliases, secret references only, and every
// automatic dispatch path disabled.
func TestRenderProxyConfig(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "sk-live-do-not-render")
	d := loadDeployment(t, deploymentBoth)
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
	if llm.LiteLLMParams.Model != "openrouter/qwen/qwen3-8b" {
		t.Fatalf("llm deployment = %+v, want the declared model", llm.LiteLLMParams)
	}
	if llm.LiteLLMParams.APIKey != "os.environ/OPENROUTER_API_KEY" {
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

	// Nothing that could widen dispatch, persist content or reach a database.
	for _, forbidden := range []string{
		"sk-live-do-not-render", "database_url", "postgres", "master_key",
		"store_model_in_db", "fallback", "cache", "callbacks", "success_callback",
		"telemetry", "drop_params: true", "num_retries: 2", "*",
	} {
		if strings.Contains(strings.ToLower(string(raw)), strings.ToLower(forbidden)) {
			t.Fatalf("generated configuration contains %q\n%s", forbidden, raw)
		}
	}
	if doc.RouterSettings.Fallbacks != nil {
		t.Fatalf("router_settings.fallbacks = %v, want none", doc.RouterSettings.Fallbacks)
	}

	t.Run("deterministic", func(t *testing.T) {
		again, _ := renderProxy(t, loadDeployment(t, deploymentBoth))
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
		raw, doc := renderProxy(t, loadDeployment(t, edit(t, deploymentBoth, "enabled: true", "enabled: false")))
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

	t.Run("unreviewed deployment rejected", func(t *testing.T) {
		if _, err := RenderProxyConfig(Deployment{Embedding: &ModelSpec{Model: "ollama/x"}}); !errors.Is(err, ErrDeployment) {
			t.Fatalf("RenderProxyConfig() = %v, want ErrDeployment", err)
		}
	})
}
