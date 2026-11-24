// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package models

import (
	"errors"
	"strings"
	"testing"
)

// A selection field means exactly one purpose. The database receives the purpose
// in the payload and would reconcile the profile it was told about, so a
// GENERATE profile in the Embedding field (or the reverse) must be refused here
// instead of silently selecting the other purpose.
func TestDeploymentDeclarationRequiresTheFieldPurpose(t *testing.T) {
	limits := Limits{MaxInputTokens: 10, MaxOutputTokens: 10, MaxBytes: 10, TimeoutMS: 10}
	embed := Profile{ID: "embed", Revision: 1, Purpose: "EMBED", Execution: "LOCAL",
		ProviderID: "local", ModelID: "embed-model", Dimension: 4, Limits: limits}
	generate := Profile{ID: "generate", Revision: 1, Purpose: "GENERATE", Execution: "LOCAL",
		ProviderID: "local", ModelID: "gen-model", Limits: limits}

	for _, test := range []struct {
		name     string
		profile  Profile
		field    string
		purpose  string
		wantErr  string
		accepted bool
	}{
		{name: "generation profile in the embedding field", profile: generate, field: "Embedding", purpose: "EMBED", wantErr: "Embedding requires purpose"},
		{name: "embedding profile in the generation field", profile: embed, field: "Generation", purpose: "GENERATE", wantErr: "Generation requires purpose"},
		{name: "embedding profile in the embedding field", profile: embed, field: "Embedding", purpose: "EMBED", accepted: true},
		{name: "generation profile in the generation field", profile: generate, field: "Generation", purpose: "GENERATE", accepted: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			declaration, err := deploymentDeclaration(test.profile, test.field, test.purpose)
			if test.accepted {
				if err != nil || declaration["purpose"] != test.purpose {
					t.Fatalf("deploymentDeclaration() = %v, %v", declaration, err)
				}
				return
			}
			if !errors.Is(err, ErrPolicy) {
				t.Fatalf("deploymentDeclaration() = %v, want ErrPolicy", err)
			}
			if !strings.Contains(err.Error(), test.wantErr) ||
				!strings.Contains(err.Error(), `got purpose "`+test.profile.Purpose+`"`) {
				t.Fatalf("deploymentDeclaration() = %v, want it to name the field and the actual purpose", err)
			}
		})
	}
}
