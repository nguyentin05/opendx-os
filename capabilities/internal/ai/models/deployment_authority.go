// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package models

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/lib/pq"
)

// Deployment authority is the only way live model authority changes. The
// database records the exact selected profile revisions, keeps model_profiles as
// immutable history, and authorizes a model call only while the exact revision is
// the current selection for its purpose. These three wrappers are the whole Go
// surface over that contract; none of them approves a processing policy.
//
// The connection must be the restricted deployment login (dxos_ai_model_catalog).
// A gateway, worker, API or administration session is refused by the database, so
// no wrapper re-checks the role: the SQL session check is the authority.

// DeploymentSelection is the complete declaration of what this installation
// serves. A nil purpose is a disabled route: it is withdrawn, not left enabled.
type DeploymentSelection struct {
	Embedding  *Profile
	Generation *Profile
}

// DeploymentPurposeState reports what the database now holds for one purpose.
type DeploymentPurposeState struct {
	Purpose           string `json:"purpose"`
	ProfileID         string `json:"profile_id"`
	Revision          int64  `json:"revision"`
	Enabled           bool   `json:"enabled"`
	SelectionRevision int64  `json:"selection_revision"`
	Changed           bool   `json:"changed"`
}

// DeploymentSelectionReport is the sanitized result of a reconciliation.
type DeploymentSelectionReport struct {
	Profiles  []DeploymentPurposeState `json:"profiles"`
	Created   int                      `json:"created"`
	Withdrawn []string                 `json:"withdrawn"`
	Changed   bool                     `json:"changed"`
}

// DeploymentSuspension is the sanitized result of an emergency withdrawal.
type DeploymentSuspension struct {
	Suspended  int      `json:"suspended"`
	Purposes   []string `json:"purposes"`
	ActorID    string   `json:"actor_id"`
	OccurredAt string   `json:"occurred_at"`
}

// DeploymentStatusProfile is the sanitized live metadata for one purpose. It
// never carries a credential reference, an endpoint or any content.
type DeploymentStatusProfile struct {
	Purpose           string `json:"purpose"`
	ProfileID         string `json:"profile_id"`
	Revision          int64  `json:"revision"`
	Execution         string `json:"execution"`
	ProviderID        string `json:"provider_id"`
	ModelID           string `json:"model_id"`
	Dimension         int    `json:"dimension"`
	Limits            Limits `json:"limits"`
	Selected          bool   `json:"selected"`
	SelectionRevision int64  `json:"selection_revision"`
	SelectedAt        string `json:"selected_at"`
	Enabled           bool   `json:"enabled"`
}

// DeploymentStatus is the read-only view the deployment and administration
// logins may hold. Enabled is the installation switch: no live profile means
// model calls are unavailable and search stays lexical.
type DeploymentStatus struct {
	Enabled      bool                      `json:"enabled"`
	LiveProfiles int                       `json:"live_profiles"`
	Profiles     []DeploymentStatusProfile `json:"profiles"`
}

// DeploymentIndexStatus is the sanitized indexing state of one installation: how
// much embedding work is queued, leased or failed, and how many embedding
// generations are ready. It carries counts only — never a document, chunk, title,
// app, profile credential or endpoint.
type DeploymentIndexStatus struct {
	EmbeddingJobsPending int `json:"embedding_jobs_pending"`
	EmbeddingJobsLeased  int `json:"embedding_jobs_leased"`
	EmbeddingJobsFailed  int `json:"embedding_jobs_failed"`
	EmbeddingsReady      int `json:"embeddings_ready"`
	EmbeddingsBuilding   int `json:"embeddings_building"`
	EmbeddingsFailed     int `json:"embeddings_failed"`
}

// deploymentDeclaration is the exact key set cap_ai.reconcile_deployment_models
// accepts. Client-side state (Enabled, an absent GENERATE dimension, a zero
// revision) can never be smuggled into the declaration.
//
// field and purpose identify the selection field the profile came from, and the
// profile must carry exactly that purpose: a GENERATE profile in the Embedding
// field (or the reverse) would otherwise be reconciled as the other purpose, so
// the caller would select something it never declared.
func deploymentDeclaration(p Profile, field, purpose string) (map[string]any, error) {
	if p.ID == "" || p.Revision < 1 || p.Purpose != purpose {
		return nil, fmt.Errorf("deployment selection: %s requires purpose %q, got purpose %q: %w", field, purpose, p.Purpose, ErrPolicy)
	}
	if p.Execution != "LOCAL" && p.Execution != "HOSTED" {
		return nil, ErrPolicy
	}
	if p.ProviderID == "" || p.ModelID == "" {
		return nil, ErrPolicy
	}
	if p.Limits.MaxInputTokens < 1 || p.Limits.MaxOutputTokens < 1 || p.Limits.MaxBytes < 1 || p.Limits.TimeoutMS < 1 {
		return nil, ErrPolicy
	}
	declaration := map[string]any{
		"id": p.ID, "revision": p.Revision, "purpose": p.Purpose,
		"execution": p.Execution, "provider_id": p.ProviderID, "model_id": p.ModelID,
		"limits": p.Limits,
	}
	if p.Purpose == "EMBED" {
		if p.Dimension < 1 {
			return nil, ErrPolicy
		}
		declaration["dimension"] = p.Dimension
	} else if p.Dimension != 0 {
		return nil, ErrPolicy
	}
	if p.CredentialRef != "" {
		declaration["credential_ref"] = p.CredentialRef
	}
	return declaration, nil
}

// ReconcileDeployment records the complete installation selection. It is
// idempotent for an identical declaration, withdraws any other live revision per
// purpose, and never touches an approval, policy, document or operation row.
func ReconcileDeployment(ctx context.Context, db *sql.DB, selection DeploymentSelection) (DeploymentSelectionReport, error) {
	report := DeploymentSelectionReport{}
	if db == nil {
		return report, ErrUnavailable
	}
	profiles := make([]map[string]any, 0, 2)
	// Each field means exactly one purpose. The pairing is checked here, not in
	// the database: the payload carries the purpose, so a mismatched field would
	// silently select the other purpose instead of being refused.
	for _, field := range []struct {
		name    string
		purpose string
		profile *Profile
	}{
		{"Embedding", "EMBED", selection.Embedding},
		{"Generation", "GENERATE", selection.Generation},
	} {
		if field.profile == nil {
			continue
		}
		declaration, err := deploymentDeclaration(*field.profile, field.name, field.purpose)
		if err != nil {
			return report, err
		}
		profiles = append(profiles, declaration)
	}
	payload, err := json.Marshal(map[string]any{"profiles": profiles})
	if err != nil {
		return report, ErrPolicy
	}
	var raw []byte
	if err := db.QueryRowContext(ctx, `SELECT cap_ai.reconcile_deployment_models($1::jsonb)`, string(payload)).Scan(&raw); err != nil {
		return report, deploymentError(err)
	}
	if err := json.Unmarshal(raw, &report); err != nil {
		return report, ErrUnavailable
	}
	return report, nil
}

// SuspendModelCalls withdraws every live profile in one transaction. The actor is
// authenticated inside the same statement, so a revoked or inactive
// administrator cannot suspend and an authorized one always leaves an audit row.
func SuspendModelCalls(ctx context.Context, db *sql.DB, actorID string) (DeploymentSuspension, error) {
	result := DeploymentSuspension{}
	if db == nil || actorID == "" {
		return result, ErrUnavailable
	}
	var raw []byte
	if err := db.QueryRowContext(ctx, `SELECT cap_ai.suspend_model_calls($1)`, actorID).Scan(&raw); err != nil {
		return result, deploymentError(err)
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return result, ErrUnavailable
	}
	return result, nil
}

// ReadDeploymentStatus reads the sanitized deployment metadata. It is read-only and
// never returns a credential reference, endpoint, token or content.
func ReadDeploymentStatus(ctx context.Context, db *sql.DB) (DeploymentStatus, error) {
	status := DeploymentStatus{}
	if db == nil {
		return status, ErrUnavailable
	}
	var raw []byte
	if err := db.QueryRowContext(ctx, `SELECT cap_ai.model_deployment_status()`).Scan(&raw); err != nil {
		return status, deploymentError(err)
	}
	if err := json.Unmarshal(raw, &status); err != nil {
		return status, ErrUnavailable
	}
	return status, nil
}

// ReadDeploymentIndexStatus reads the sanitized embedding index counts. Like the
// deployment status it is read-only and returns no content, identity or secret.
func ReadDeploymentIndexStatus(ctx context.Context, db *sql.DB) (DeploymentIndexStatus, error) {
	status := DeploymentIndexStatus{}
	if db == nil {
		return status, ErrUnavailable
	}
	var raw []byte
	if err := db.QueryRowContext(ctx, `SELECT cap_ai.model_deployment_index_status()`).Scan(&raw); err != nil {
		return status, deploymentError(err)
	}
	if err := json.Unmarshal(raw, &status); err != nil {
		return status, ErrUnavailable
	}
	return status, nil
}

// deploymentError keeps the deployment boundary sanitized: callers see only the
// package errors, never a driver message or a SQL state.
func deploymentError(err error) error {
	if err == nil {
		return nil
	}
	var pg *pq.Error
	if errors.As(err, &pg) {
		switch pg.Code {
		case "42501", "22023", "22003":
			// No deployment session, or a declaration the database refuses.
			return ErrPolicy
		case "23505", "40001":
			// An immutable revision conflict, or a concurrent selection change.
			return ErrConflict
		}
	}
	return ErrUnavailable
}
