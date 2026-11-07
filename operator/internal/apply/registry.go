// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package apply executes compiled DDL and Hasura metadata plans with safe rollback.
package apply

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/opendx-os/opendx-os/operator/internal/manifest"
	"github.com/opendx-os/opendx-os/operator/internal/planner"
)

// ReadManagedState loads all currently active Ready applications for a workspace from core.app_registry.
func (r *Runner) ReadManagedState(ctx context.Context, workspaceID string) (planner.ManagedState, error) {
	if r.db == nil {
		return planner.ManagedState{}, nil
	}
	rows, err := r.db.QueryContext(
		ctx,
		`SELECT app_id, display_name, version, manifest_spec FROM core.app_registry WHERE workspace_id = $1 AND status = 'Ready' ORDER BY app_id`,
		workspaceID,
	)
	if err != nil {
		return planner.ManagedState{}, fmt.Errorf("query managed state: %w", err)
	}
	defer rows.Close()

	var state planner.ManagedState
	for rows.Next() {
		var appID, displayName, version string
		var specBytes []byte
		if err := rows.Scan(&appID, &displayName, &version, &specBytes); err != nil {
			return planner.ManagedState{}, err
		}
		var spec manifest.AppSpec
		if err := json.Unmarshal(specBytes, &spec); err != nil {
			return planner.ManagedState{}, fmt.Errorf("decode active spec for %s: %w", appID, err)
		}
		state.ActiveApps = append(state.ActiveApps, manifest.DXApplication{
			APIVersion: "dxos.io/v1alpha1",
			Kind:       "DXApplication",
			Metadata: manifest.AppMetadata{
				Name:        appID,
				DisplayName: displayName,
				Version:     version,
			},
			Spec: spec,
		})
	}
	return state, rows.Err()
}

func publishRegistrySnapshot(ctx context.Context, tx *sql.Tx, workspaceID string, apps []manifest.DXApplication, removed []string) error {
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1::text))`, workspaceID); err != nil {
		return err
	}

	for _, rem := range removed {
		if _, err := tx.ExecContext(ctx, `DELETE FROM core.app_registry WHERE workspace_id = $1 AND app_id = $2`, workspaceID, rem); err != nil {
			return fmt.Errorf("remove uninstalled app %s: %w", rem, err)
		}
	}

	for _, app := range apps {
		specBytes, err := json.Marshal(app.Spec)
		if err != nil {
			return err
		}
		uiBytes, err := json.Marshal(app.Spec.UISchema)
		if err != nil {
			return err
		}
		smBytes, err := json.Marshal(app.Spec.StateMachine)
		if err != nil {
			return err
		}
		capArray := "{" + strings.Join(app.Spec.Capabilities, ",") + "}"

		_, err = tx.ExecContext(
			ctx,
			`INSERT INTO core.app_registry (
				workspace_id, app_id, display_name, version, role_name, domain_table,
				manifest_spec, ui_schema, state_machine, dependent_capabilities, status, last_error, updated_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb, $8::jsonb, $9::jsonb, $10::text[], 'Ready', NULL, now())
			ON CONFLICT (workspace_id, app_id) DO UPDATE SET
				display_name = EXCLUDED.display_name,
				version = EXCLUDED.version,
				role_name = EXCLUDED.role_name,
				domain_table = EXCLUDED.domain_table,
				manifest_spec = EXCLUDED.manifest_spec,
				ui_schema = EXCLUDED.ui_schema,
				state_machine = EXCLUDED.state_machine,
				dependent_capabilities = EXCLUDED.dependent_capabilities,
				status = 'Ready',
				last_error = NULL,
				updated_at = now()`,
			workspaceID,
			app.Metadata.Name,
			app.Metadata.DisplayName,
			app.Metadata.Version,
			app.Spec.Role,
			app.Spec.DomainTable.Name,
			string(specBytes),
			string(uiBytes),
			string(smBytes),
			capArray,
		)
		if err != nil {
			return fmt.Errorf("publish Ready snapshot for %s: %w", app.Metadata.Name, err)
		}
	}
	return nil
}
