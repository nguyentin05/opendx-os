// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/opendx-os/opendx-os/capabilities/internal/ai/models"
)

// TestRuntimeModelGateKeepsWorkerBooting fixes the worker's model contract: the
// embedding gateway is optional, a disabled or unreconciled declaration leaves the
// worker parsing and indexing with no model work, and only a declaration the
// runtime cannot accept stops start-up. The worker then claims nothing because the
// database reports no current EMBED profile, which keeps the claim path safe.
func TestRuntimeModelGateKeepsWorkerBooting(t *testing.T) {
	valid := "enabled: true\nembedding:\n  model: 'ollama/embed-model'\n  execution: local\n  api_base: http://ollama:11434\n  revision: 'embed-revision'\n  dimension: 8\n"
	disabled := "enabled: false\nembedding:\n  model: 'ollama/embed-model'\n  execution: local\n  api_base: http://ollama:11434\n  revision: 'embed-revision'\n  dimension: 8\n"
	base := map[string]string{
		"RAG_MODELS_ENABLED":         "true",
		"RAG_SOURCE_QUALIFIED":       "true",
		"RAG_METADATA_QUALIFIED":     "true",
		"RAG_PARSER_QUALIFIED":       "true",
		"RAG_LOCAL_MODELS_QUALIFIED": "true",
		"RAG_MODEL_LITELLM_ENDPOINT": "http://litellm:4000",
		"LITELLM_MASTER_KEY":         "runtime-proxy-credential",
	}
	write := func(t *testing.T, declaration string) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), "models.yaml")
		if err := os.WriteFile(path, []byte(declaration), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	for _, tc := range []struct {
		name        string
		declaration string
		gatewayDSN  string
	}{
		{"no model configuration", "", ""},
		{"disabled declaration", disabled, ""},
		{"enabled declaration without a binding", valid, ""},
		{"enabled declaration with an unreachable authority", valid, "postgres://gateway@127.0.0.1:1/test"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := map[string]string{}
			for key, value := range base {
				env[key] = value
			}
			env["AI_GATEWAY_DATABASE_URL"] = tc.gatewayDSN
			if tc.declaration != "" {
				env["RAG_MODEL_DEPLOYMENT_FILE"] = write(t, tc.declaration)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			gateway, embed, generate, closeRuntime, err := models.RuntimeFromEnvironment(ctx, func(key string) string { return env[key] })
			defer closeRuntime()
			if err != nil || gateway != nil || embed.ID != "" || generate.ID != "" {
				t.Fatalf("worker startup gate: %v %v %q %q", err, gateway, embed.ID, generate.ID)
			}
		})
	}
	t.Run("an invalid declaration is still a startup error", func(t *testing.T) {
		env := map[string]string{}
		for key, value := range base {
			env[key] = value
		}
		env["RAG_MODEL_DEPLOYMENT_FILE"] = write(t, "not a declaration at all")
		if _, _, _, closeRuntime, err := models.RuntimeFromEnvironment(context.Background(), func(key string) string { return env[key] }); err == nil {
			closeRuntime()
			t.Fatal("an invalid declaration booted the worker")
		}
	})
}
