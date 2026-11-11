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
	"sort"

	"github.com/lib/pq"
	"github.com/opendx-os/opendx-os/operator/internal/manifest"
	"github.com/opendx-os/opendx-os/operator/internal/planner"
)

// publicationLockKey serializes operator publications across every workspace,
// because definitions are global and one apply may republish several workspaces.
const publicationLockKey = "dxos.operator.publication"

// ReadManagedState loads the active global application definitions.
func (r *Runner) ReadManagedState(ctx context.Context) (planner.ManagedState, error) {
	if r.db == nil {
		return planner.ManagedState{}, nil
	}
	rows, err := r.db.QueryContext(
		ctx,
		`SELECT app_id, display_name, version, spec FROM core.app_definitions ORDER BY app_id`,
	)
	if err != nil {
		return planner.ManagedState{}, fmt.Errorf("query managed definitions: %w", err)
	}
	defer func() { _ = rows.Close() }()

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

// RegistryRow is one per-workspace publication from core.app_registry.
type RegistryRow struct {
	AppID       string
	WorkspaceID string
	Version     string
	Status      string
	Spec        json.RawMessage
}

// ReadRegistry returns every publication ordered by application and workspace.
func (r *Runner) ReadRegistry(ctx context.Context) ([]RegistryRow, error) {
	if r.db == nil {
		return nil, nil
	}
	rows, err := r.db.QueryContext(ctx, `SELECT app_id, workspace_id::text, version, status, manifest_spec FROM core.app_registry ORDER BY app_id, workspace_id`)
	if err != nil {
		return nil, fmt.Errorf("query registry: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []RegistryRow
	for rows.Next() {
		var row RegistryRow
		var spec []byte
		if err := rows.Scan(&row.AppID, &row.WorkspaceID, &row.Version, &row.Status, &spec); err != nil {
			return nil, err
		}
		row.Spec = json.RawMessage(spec)
		out = append(out, row)
	}
	return out, rows.Err()
}

// ReadPublications maps each application name to the workspaces where it is published.
func (r *Runner) ReadPublications(ctx context.Context) (map[string][]string, error) {
	published := map[string][]string{}
	rows, err := r.ReadRegistry(ctx)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		published[row.AppID] = append(published[row.AppID], row.WorkspaceID)
	}
	return published, nil
}

func publishSnapshot(ctx context.Context, tx *sql.Tx, plan planner.Plan, pub Publication) error {
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1::text))`, publicationLockKey); err != nil {
		return err
	}

	byName := make(map[string]manifest.DXApplication, len(plan.Apps))
	for _, app := range plan.Apps {
		byName[app.Metadata.Name] = app
		specBytes, err := json.Marshal(app.Spec)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(
			ctx,
			`INSERT INTO core.app_definitions (app_id, display_name, version, spec, updated_at)
			 VALUES ($1, $2, $3, $4::jsonb, now())
			 ON CONFLICT (app_id) DO UPDATE SET
				display_name = EXCLUDED.display_name,
				version = EXCLUDED.version,
				spec = EXCLUDED.spec,
				updated_at = now()`,
			app.Metadata.Name, app.Metadata.DisplayName, app.Metadata.Version, string(specBytes),
		); err != nil {
			return fmt.Errorf("record definition for %s: %w", app.Metadata.Name, err)
		}
	}

	// A removed definition no longer has a table contract or permissions, so it is
	// unpublished from every workspace, not only the ones named in pub.
	for _, name := range plan.RemovedApps {
		if _, err := tx.ExecContext(ctx, `DELETE FROM core.app_registry WHERE app_id = $1`, name); err != nil {
			return fmt.Errorf("unpublish removed app %s: %w", name, err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM core.app_definitions WHERE app_id = $1`, name); err != nil {
			return fmt.Errorf("remove definition %s: %w", name, err)
		}
	}

	for _, ws := range sortedKeys(pub.Remove) {
		for _, name := range pub.Remove[ws] {
			if _, err := tx.ExecContext(ctx, `DELETE FROM core.app_registry WHERE workspace_id = $1 AND app_id = $2`, ws, name); err != nil {
				return fmt.Errorf("unpublish %s from workspace %s: %w", name, ws, err)
			}
		}
	}

	for _, ws := range sortedKeys(pub.Upsert) {
		for _, name := range pub.Upsert[ws] {
			if err := publishApp(ctx, tx, ws, byName[name]); err != nil {
				return err
			}
		}
	}
	return nil
}

func publishApp(ctx context.Context, tx *sql.Tx, workspaceID string, app manifest.DXApplication) error {
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
		pq.Array(app.Spec.Capabilities),
	)
	if err != nil {
		return fmt.Errorf("publish Ready snapshot for %s in workspace %s: %w", app.Metadata.Name, workspaceID, err)
	}
	return nil
}

func sortedKeys(m map[string][]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
