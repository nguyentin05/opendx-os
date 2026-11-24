//go:build integration

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package knowledge

import (
	"database/sql"
	"testing"

	"github.com/opendx-os/opendx-os/capabilities/internal/ai/models"
	"github.com/opendx-os/opendx-os/capabilities/internal/testutil"
)

const capAIMigrations = "../../../../platform/postgres/migrations/cap_ai"

// applyDeploymentAuthority applies the deployment-authority chain on top of the
// 0003/0004/0005 base every model fixture already applies, in ledger order.
func applyDeploymentAuthority(t *testing.T, db *sql.DB) {
	t.Helper()
	testutil.ApplyDeploymentAuthority(t, db, capAIMigrations)
}

// deploymentLogin opens the restricted deployment login that owns live model
// authority. It is never a superuser and holds no table privilege.
func deploymentLogin(t *testing.T, db *sql.DB) *sql.DB {
	t.Helper()
	return testutil.DeploymentLogin(t, db)
}

// selectDeployment records the given profiles as the complete live selection
// through the real reconciliation path. A purpose that is absent is withdrawn, so
// a test can prove the partial-availability contract.
func selectDeployment(t *testing.T, db *sql.DB, profiles ...models.Profile) {
	t.Helper()
	testutil.SelectDeployment(t, db, deploymentProfiles(profiles...)...)
}

// deploymentProfiles converts the runtime profiles a fixture configured into the
// plain declaration a deployment reconciles.
func deploymentProfiles(profiles ...models.Profile) []testutil.DeploymentProfile {
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
	return declared
}

// residentProfile makes one immutable profile revision the live selection for its
// purpose through the same path the deployment uses.
func residentProfile(t *testing.T, db *sql.DB, profile models.Profile) models.Profile {
	t.Helper()
	selectDeployment(t, db, profile)
	return profile
}
