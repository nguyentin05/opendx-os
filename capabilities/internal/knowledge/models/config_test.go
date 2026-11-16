// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
package models

import (
	"context"
	"testing"
)

func TestModelConfigurationFailsClosed(t *testing.T) {
	for _, env := range []map[string]string{{"RAG_MODELS_ENABLED": "true"}, {"RAG_MODELS_ENABLED": "true", "RAG_MODELS_QUALIFIED": "true"}} {
		if _, _, _, close, e := FromEnvironment(context.Background(), func(k string) string { return env[k] }); e == nil {
			if close != nil {
				close()
			}
			t.Fatal("incomplete config enabled models")
		}
	}
	if g, _, _, _, e := FromEnvironment(context.Background(), func(string) string { return "" }); e != nil || g != nil {
		t.Fatal("default must be disabled", e)
	}
}

func TestRegistryFlagOffRuntimePreservesDisabledDefault(t *testing.T) {
	for _, flag := range []string{"", "false", "true"} {
		gateway, embed, generate, resolver, closeDB, err := RuntimeFromEnvironment(context.Background(), func(key string) string {
			if key == "RAG_GLOBAL_MODEL_REGISTRY_ENABLED" {
				return flag
			}
			return ""
		})
		closeDB()
		if err != nil || gateway != nil || resolver != nil || embed.ID != "" || generate.ID != "" {
			t.Fatal(flag, err)
		}
	}
}
