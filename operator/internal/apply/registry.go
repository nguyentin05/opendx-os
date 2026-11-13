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
	"strings"

	"github.com/lib/pq"
	"github.com/opendx-os/opendx-os/operator/internal/manifest"
	"github.com/opendx-os/opendx-os/operator/internal/planner"
)

// publicationLockKey serializes operator publications, because one apply may
// change the grants and registry rows of several applications.
const publicationLockKey = "dxos.operator.publication"

// ReadManagedState loads the definitions of the installed applications
// (core.apps rows with status Ready).
func (r *Runner) ReadManagedState(ctx context.Context) (planner.ManagedState, error) {
	if r.db == nil {
		return planner.ManagedState{}, nil
	}
	rows, err := r.db.QueryContext(
		ctx,
		`SELECT app_id, display_name, version, spec FROM core.apps WHERE status = 'Ready' ORDER BY app_id`,
	)
	if err != nil {
		return planner.ManagedState{}, fmt.Errorf("query installed applications: %w", err)
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

// AppRow is one row of core.apps.
type AppRow struct {
	AppID     string
	Version   string
	Status    string
	LastError string
	Spec      json.RawMessage
}

// ReadApps returns every application row, including failed and removed ones.
func (r *Runner) ReadApps(ctx context.Context) ([]AppRow, error) {
	if r.db == nil {
		return nil, nil
	}
	rows, err := r.db.QueryContext(ctx, `SELECT app_id, version, status, coalesce(last_error, ''), spec FROM core.apps ORDER BY app_id`)
	if err != nil {
		return nil, fmt.Errorf("query applications: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []AppRow
	for rows.Next() {
		var row AppRow
		var spec []byte
		if err := rows.Scan(&row.AppID, &row.Version, &row.Status, &row.LastError, &spec); err != nil {
			return nil, err
		}
		row.Spec = json.RawMessage(spec)
		out = append(out, row)
	}
	return out, rows.Err()
}

// RegistryError reports a conflict with the Tier 1 registry that only a
// changed manifest or platform configuration resolves: a role owned by another
// application, capability or the platform, or a capability that is not enabled.
type RegistryError struct {
	App      string
	Problems []string
}

func (e *RegistryError) Error() string {
	return fmt.Sprintf("application %s conflicts with the registry: %s", e.App, strings.Join(e.Problems, "; "))
}

// checkRegistry refuses an application before anything is written when one of
// its roles belongs to someone else or a capability it declares is not enabled.
func checkRegistry(ctx context.Context, db *sql.DB, app *manifest.DXApplication) error {
	var problems []string

	rows, err := db.QueryContext(ctx, `
		SELECT name, CASE WHEN app_id IS NOT NULL THEN 'application ' || app_id
		                  WHEN cap_id IS NOT NULL THEN 'capability ' || cap_id
		                  ELSE 'the platform' END
		  FROM core.roles
		 WHERE name = ANY($1) AND app_id IS DISTINCT FROM $2
		 ORDER BY name`, pq.Array(app.RoleNames()), app.Metadata.Name)
	if err != nil {
		return fmt.Errorf("check role owners for %s: %w", app.Metadata.Name, err)
	}
	for rows.Next() {
		var role, owner string
		if err := rows.Scan(&role, &owner); err != nil {
			_ = rows.Close()
			return err
		}
		problems = append(problems, fmt.Sprintf("role %s belongs to %s", role, owner))
	}
	if err := rows.Close(); err != nil {
		return err
	}

	enabled := map[string]bool{}
	rows, err = db.QueryContext(ctx, `SELECT cap_id FROM core.capabilities WHERE enabled AND cap_id = ANY($1)`, pq.Array(app.Spec.Capabilities))
	if err != nil {
		return fmt.Errorf("check capabilities for %s: %w", app.Metadata.Name, err)
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return err
		}
		enabled[id] = true
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, c := range app.Spec.Capabilities {
		if !enabled[c] {
			problems = append(problems, fmt.Sprintf("capability %s is not enabled", c))
		}
	}

	if len(problems) > 0 {
		return &RegistryError{App: app.Metadata.Name, Problems: problems}
	}
	return nil
}

// publishSnapshot records the desired definitions in the registry inside tx:
// core.apps (status Ready), the roles each application owns and the
// capabilities it uses. Removed applications keep their core.apps row with
// status Removed and their roles, so files, audit, data and role assignments
// still resolve and a reinstall restores access.
func publishSnapshot(ctx context.Context, tx *sql.Tx, plan planner.Plan) error {
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1::text))`, publicationLockKey); err != nil {
		return err
	}

	apps := append([]manifest.DXApplication(nil), plan.Apps...)
	sort.Slice(apps, func(i, j int) bool { return apps[i].Metadata.Name < apps[j].Metadata.Name })
	for _, app := range apps {
		if err := publishApp(ctx, tx, app); err != nil {
			return err
		}
	}

	for _, name := range plan.RemovedApps {
		if _, err := tx.ExecContext(ctx, `UPDATE core.apps SET status = 'Removed', last_error = NULL, updated_at = now() WHERE app_id = $1`, name); err != nil {
			return fmt.Errorf("mark %s removed: %w", name, err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM core.app_capabilities WHERE app_id = $1`, name); err != nil {
			return fmt.Errorf("release capabilities of %s: %w", name, err)
		}
	}
	return nil
}

func publishApp(ctx context.Context, tx *sql.Tx, app manifest.DXApplication) error {
	name := app.Metadata.Name
	specBytes, err := json.Marshal(app.Spec)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(
		ctx,
		`INSERT INTO core.apps (app_id, display_name, version, spec, status, last_error, updated_at)
		 VALUES ($1, $2, $3, $4::jsonb, 'Ready', NULL, now())
		 ON CONFLICT (app_id) DO UPDATE SET
			display_name = EXCLUDED.display_name,
			version = EXCLUDED.version,
			spec = EXCLUDED.spec,
			status = 'Ready',
			last_error = NULL,
			updated_at = now()`,
		name, app.Metadata.DisplayName, app.Metadata.Version, string(specBytes),
	); err != nil {
		return fmt.Errorf("record definition for %s: %w", name, err)
	}

	// Capabilities: exactly the declared set. The insert trigger refuses a
	// capability that is not enabled.
	if _, err := tx.ExecContext(ctx, `DELETE FROM core.app_capabilities WHERE app_id = $1 AND cap_id <> ALL($2)`, name, pq.Array(app.Spec.Capabilities)); err != nil {
		return fmt.Errorf("release capabilities of %s: %w", name, err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO core.app_capabilities (app_id, cap_id) SELECT $1, unnest($2::text[]) ON CONFLICT DO NOTHING`,
		name, pq.Array(app.Spec.Capabilities)); err != nil {
		return fmt.Errorf("record capabilities of %s: %w", name, err)
	}

	// Roles: register each declared role for this application. A role owned by
	// anyone else is refused; roles dropped from the manifest are deleted
	// together with their assignments.
	for _, role := range app.Spec.Roles {
		var got string
		err := tx.QueryRowContext(ctx,
			`INSERT INTO core.roles (name, app_id, description) VALUES ($1, $2, $3)
			 ON CONFLICT (name) DO UPDATE SET description = EXCLUDED.description
			 WHERE core.roles.app_id IS NOT DISTINCT FROM EXCLUDED.app_id
			 RETURNING name`,
			role.Name, name, role.Description,
		).Scan(&got)
		if err == sql.ErrNoRows {
			return &RegistryError{App: name, Problems: []string{fmt.Sprintf("role %s belongs to another owner", role.Name)}}
		}
		if err != nil {
			return fmt.Errorf("register role %s for %s: %w", role.Name, name, err)
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM core.roles WHERE app_id = $1 AND name <> ALL($2)`, name, pq.Array(app.RoleNames())); err != nil {
		return fmt.Errorf("remove dropped roles of %s: %w", name, err)
	}
	return nil
}
