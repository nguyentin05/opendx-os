// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package models

import (
	"context"
	"errors"
	"testing"
)

func TestRegistryResolveUnavailableWithoutDB(t *testing.T) {
	for _, r := range []*Registry{nil, {}} {
		for _, c := range []Capability{CapabilityLLM, CapabilityEmbedding, CapabilityVLM, CapabilityReranking, "unknown"} {
			got, err := r.Resolve(context.Background(), c)
			if !errors.Is(err, ErrUnavailable) || got != (ActiveBinding{}) {
				t.Fatal(got, err)
			}
		}
	}
}
