//go:build integration

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package agent

import (
	"database/sql"
	"testing"

	"github.com/opendx-os/opendx-os/capabilities/internal/ai/models"
	"github.com/opendx-os/opendx-os/capabilities/internal/testutil"
)

// capAIMigrations is the path of the cap_ai migrations from this package.
const capAIMigrations = "../../../../platform/postgres/migrations/cap_ai"

// applyDeploymentAuthority applies the deployment-authority chain on top of the
// 0004/0005 base the agent fixtures already apply, in ledger order.
func applyDeploymentAuthority(t *testing.T, db *sql.DB) {
	t.Helper()
	testutil.ApplyDeploymentAuthority(t, db, capAIMigrations)
}

// selectDeployment records one complete live selection through the real
// reconciliation path, so a fixture cannot bind a route the runtime would refuse.
func selectDeployment(t *testing.T, db *sql.DB, profiles ...models.Profile) {
	t.Helper()
	declared := make([]testutil.DeploymentProfile, 0, len(profiles))
	for _, profile := range profiles {
		declared = append(declared, testutil.DeploymentProfile{
			ID: profile.ID, Revision: profile.Revision, Purpose: profile.Purpose, Execution: profile.Execution,
			ProviderID: profile.ProviderID, ModelID: profile.ModelID, CredentialRef: profile.CredentialRef,
			Dimension: profile.Dimension,
			Limits: map[string]int{
				"max_input_tokens": profile.Limits.MaxInputTokens, "max_output_tokens": profile.Limits.MaxOutputTokens,
				"max_bytes": profile.Limits.MaxBytes, "timeout_ms": profile.Limits.TimeoutMS,
			},
		})
	}
	testutil.SelectDeployment(t, db, declared...)
}
