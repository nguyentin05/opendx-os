//go:build integration

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

// Deployment-authority fixtures shared by the knowledge and agent integration
// suites. The exact deployment selection replaced the legacy slot as model
// authority, so a suite that needs a usable model binds one the same way a
// deployment does: through cap_ai.reconcile_deployment_models, as the restricted
// deployment login. A fixture writing enabled=true or a slot binding would be
// testing an authority the runtime no longer consults.
//
// This package deliberately does not import internal/ai/models: the models
// integration tests import this package, and a test-only import cycle would break
// them. The declaration payload is therefore built here from plain values, which
// is also what a deployment does — the SQL function is the authority.

package testutil

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/url"
	"os"
	"testing"

	_ "github.com/lib/pq"
)

// DeploymentProfile is one declared route as a fixture states it: the reviewed
// non-secret identity a deployment reconciles.
type DeploymentProfile struct {
	ID            string
	Revision      int64
	Purpose       string
	Execution     string
	ProviderID    string
	ModelID       string
	CredentialRef string
	Dimension     int
	Limits        map[string]int
}

// ApplyDeploymentAuthority applies the deployment-authority chain to one fixture
// database. migrationsPath is the absolute path of the cap_ai migration directory.
func ApplyDeploymentAuthority(t *testing.T, db *sql.DB, migrationsPath string) {
	t.Helper()
	for _, migration := range []string{
		"0008_personal_model_processing.sql", "0007_agent_scopes.sql", "0009_personal_hybrid.sql",
		"0010_agent_model_authority.sql", "0011_model_administration.sql",
		"0012_personal_admin_authority_locks.sql", "0013_deployment_models.sql",
		"0014_model_deployment_index_status.sql",
	} {
		raw, err := os.ReadFile(migrationsPath + "/" + migration)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(string(raw)); err != nil {
			t.Fatalf("%s: %v", migration, err)
		}
	}
}

// DeploymentLogin opens the restricted deployment login on the fixture database.
func DeploymentLogin(t *testing.T, db *sql.DB) *sql.DB {
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
	return As(t, u.String(), "dxos_ai_model_catalog")
}

// SelectDeployment records one complete selection through the real reconciliation
// path. A purpose that is absent is withdrawn.
func SelectDeployment(t *testing.T, db *sql.DB, profiles ...DeploymentProfile) []byte {
	t.Helper()
	declared := make([]map[string]any, 0, len(profiles))
	for _, profile := range profiles {
		entry := map[string]any{
			"id": profile.ID, "revision": profile.Revision, "purpose": profile.Purpose,
			"execution": profile.Execution, "provider_id": profile.ProviderID,
			"model_id": profile.ModelID, "limits": profile.Limits,
		}
		if profile.Purpose == "EMBED" {
			entry["dimension"] = profile.Dimension
		}
		if profile.CredentialRef != "" {
			entry["credential_ref"] = profile.CredentialRef
		}
		declared = append(declared, entry)
	}
	payload, err := json.Marshal(map[string]any{"profiles": declared})
	if err != nil {
		t.Fatal(err)
	}
	login := DeploymentLogin(t, db)
	var raw []byte
	if err := login.QueryRowContext(context.Background(), `SELECT cap_ai.reconcile_deployment_models($1::jsonb)`, string(payload)).Scan(&raw); err != nil {
		t.Fatalf("deployment selection: %v", err)
	}
	return raw
}
