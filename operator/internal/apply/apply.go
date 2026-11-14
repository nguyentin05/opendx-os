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

package apply

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/opendx-os/opendx-os/operator/internal/manifest"
	"github.com/opendx-os/opendx-os/operator/internal/planner"
)

// Config holds endpoints and credentials for Operator apply operations.
type Config struct {
	HasuraURL         string
	HasuraAdminSecret string
}

// Runner executes compiled plans against PostgreSQL and Hasura.
type Runner struct {
	db     *sql.DB
	cfg    Config
	client *http.Client
}

// NewRunner creates a new apply Runner.
func NewRunner(db *sql.DB, cfg Config, client *http.Client) *Runner {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &Runner{
		db:     db,
		cfg:    cfg,
		client: client,
	}
}

// Run executes a compiled Plan safely, preserving the prior active definitions on failure.
//
// targets names the applications this run installs or upgrades; each gets a
// deployment attempt in core.app_deployments and, when it is new, a core.apps
// row in status Applying. Everything the plan covers is applied: DDL, Hasura
// metadata and, in one transaction at the end, the registry (core.apps, roles
// and capabilities). A failure before that transaction leaves the registry on
// the previous definitions.
func (r *Runner) Run(ctx context.Context, plan planner.Plan, targets []string) error {
	byName := make(map[string]*manifest.DXApplication, len(plan.Apps))
	for i := range plan.Apps {
		byName[plan.Apps[i].Metadata.Name] = &plan.Apps[i]
	}
	for _, name := range targets {
		if byName[name] == nil {
			return fmt.Errorf("cannot install %s: not in the desired definition set", name)
		}
	}

	var attemptIDs []string
	markFailed := func(cause error) error {
		if r.db != nil {
			for _, id := range attemptIDs {
				_, _ = r.db.ExecContext(ctx,
					`UPDATE core.app_deployments SET status = 'Failed', last_error = $1, completed_at = now() WHERE id = $2`,
					cause.Error(), id)
			}
			for _, name := range targets {
				// A new application is marked Failed; an installed one keeps
				// serving its previous version and only records the error.
				_, _ = r.db.ExecContext(ctx,
					`UPDATE core.apps SET last_error = $2, updated_at = now(),
					        status = CASE WHEN status IN ('Applying', 'Failed') THEN 'Failed' ELSE status END
					  WHERE app_id = $1`,
					name, cause.Error())
			}
		}
		return cause
	}

	if r.db != nil {
		for _, name := range targets {
			app := byName[name]
			specBytes, err := json.Marshal(app.Spec)
			if err != nil {
				return err
			}
			if _, err := r.db.ExecContext(ctx,
				`INSERT INTO core.apps (app_id, display_name, version, spec, status)
				 VALUES ($1, $2, $3, $4::jsonb, 'Applying') ON CONFLICT (app_id) DO NOTHING`,
				name, app.Metadata.DisplayName, app.Metadata.Version, string(specBytes)); err != nil {
				return fmt.Errorf("register %s: %w", name, err)
			}
			var attemptID string
			if err := r.db.QueryRowContext(ctx,
				`INSERT INTO core.app_deployments (app_id, version, status) VALUES ($1, $2, 'Applying') RETURNING id::text`,
				name, app.Metadata.Version,
			).Scan(&attemptID); err != nil {
				return fmt.Errorf("record deployment attempt for %s: %w", name, err)
			}
			attemptIDs = append(attemptIDs, attemptID)
		}
	}

	// 0. Refuse registry conflicts and schema changes that need a data
	// migration before touching anything.
	alterDDL := make(map[string]string, len(plan.Apps))
	if r.db != nil {
		for i := range plan.Apps {
			app := &plan.Apps[i]
			if err := checkRegistry(ctx, r.db, app); err != nil {
				return markFailed(err)
			}
			live, err := r.liveColumns(ctx, app.Spec.DomainTable.Name)
			if err != nil {
				return markFailed(err)
			}
			var states []string
			if hasColumn(live, "status") {
				if states, err = r.statesInUse(ctx, app.Spec.DomainTable.Name); err != nil {
					return markFailed(err)
				}
			}
			diff, err := planner.DiffSchema(live, states, app)
			if err != nil {
				return markFailed(err)
			}
			alterDDL[app.Metadata.Name] = diff.DDL(app.Spec.DomainTable.Name)
		}
	}

	// 1. Snapshot Hasura metadata before applying changes so we can roll back on failure
	var snapshot json.RawMessage
	if r.cfg.HasuraURL != "" {
		snap, err := r.exportHasuraMetadata(ctx)
		if err != nil {
			return markFailed(fmt.Errorf("export Hasura metadata snapshot: %w", err))
		}
		snapshot = snap
	}

	// 2. Execute DDL for each application in plan order. Missing columns are
	// added before the generated DDL, whose constraints and indexes use them.
	if r.db != nil {
		for _, app := range plan.Apps {
			if alter := alterDDL[app.Metadata.Name]; alter != "" {
				if _, err := r.db.ExecContext(ctx, alter); err != nil {
					return markFailed(fmt.Errorf("add columns for %s: %w", app.Metadata.Name, err))
				}
			}
			if _, err := r.db.ExecContext(ctx, plan.DDLs[app.Metadata.Name]); err != nil {
				return markFailed(fmt.Errorf("execute DDL for %s: %w", app.Metadata.Name, err))
			}
		}
	}

	// 3. Apply Hasura DropOps and HasuraOps (restore snapshot on error)
	restore := func() {
		if r.cfg.HasuraURL != "" {
			_ = r.restoreHasuraMetadata(ctx, snapshot)
		}
	}
	if r.cfg.HasuraURL != "" {
		for _, dropOp := range plan.DropOps {
			_ = r.callHasuraIdempotent(ctx, dropOp, true)
		}
		relationships := relationshipIndex(snapshot)
		for _, op := range plan.HasuraOps {
			if err := r.applyOperation(ctx, op, relationships); err != nil {
				restore()
				return markFailed(fmt.Errorf("apply Hasura metadata op %s: %w", op.Type, err))
			}
		}
	}

	// 4. Atomically record the registry and mark deployment attempts Ready
	if r.db != nil {
		tx, err := r.db.BeginTx(ctx, nil)
		if err != nil {
			restore()
			return markFailed(err)
		}
		defer func() { _ = tx.Rollback() }() // Best-effort cleanup; a committed transaction is already closed.

		if err := publishSnapshot(ctx, tx, plan); err != nil {
			restore()
			return markFailed(err)
		}
		for _, id := range attemptIDs {
			if _, err := tx.ExecContext(ctx, `UPDATE core.app_deployments SET status = 'Ready', completed_at = now() WHERE id = $1`, id); err != nil {
				restore()
				return markFailed(err)
			}
		}
		if err := tx.Commit(); err != nil {
			restore()
			return markFailed(err)
		}
	}

	return nil
}

// applyOperation applies one desired metadata operation. A relationship that
// Hasura already has with the same definition is left alone; permission
// filters depend on it, and Hasura refuses to drop it from under them. One
// whose definition changed is dropped with its dependent permissions, which the
// plan recreates afterwards (permissions follow relationships in a plan).
func (r *Runner) applyOperation(ctx context.Context, op planner.HasuraMetadataPayload, relationships map[string]string) error {
	args, _ := op.Args.(map[string]any)
	if op.Type != "pg_create_object_relationship" && op.Type != "pg_create_array_relationship" || args == nil {
		return r.callHasuraIdempotent(ctx, op, false)
	}
	table, _ := args["table"].(planner.TableRef)
	name, _ := args["name"].(string)
	key := relationshipKey(op.Type, table, name)
	want := relationshipDef(args["using"])
	if have, exists := relationships[key]; exists {
		if have == want {
			return nil
		}
		drop := planner.HasuraMetadataPayload{
			Type: "pg_drop_relationship",
			Args: map[string]any{"source": args["source"], "table": table, "relationship": name, "cascade": true},
		}
		if err := r.postMetadata(ctx, drop, true); err != nil {
			return err
		}
	}
	if err := r.postMetadata(ctx, op, false); err != nil {
		return err
	}
	relationships[key] = want
	return nil
}

func (r *Runner) callHasuraIdempotent(ctx context.Context, op planner.HasuraMetadataPayload, ignoreNotFound bool) error {
	// A permission that may already exist on reapply is dropped first, ignoring not-found.
	if dropOp, ok := correspondingDropOp(op); ok {
		_ = r.postMetadata(ctx, dropOp, true)
	}
	return r.postMetadata(ctx, op, ignoreNotFound)
}

func correspondingDropOp(op planner.HasuraMetadataPayload) (planner.HasuraMetadataPayload, bool) {
	args, ok := op.Args.(map[string]any)
	if !ok {
		return planner.HasuraMetadataPayload{}, false
	}
	switch op.Type {
	case "pg_create_select_permission":
		return planner.HasuraMetadataPayload{
			Type: "pg_drop_select_permission",
			Args: map[string]any{"source": args["source"], "table": args["table"], "role": args["role"]},
		}, true
	case "pg_create_insert_permission":
		return planner.HasuraMetadataPayload{
			Type: "pg_drop_insert_permission",
			Args: map[string]any{"source": args["source"], "table": args["table"], "role": args["role"]},
		}, true
	case "pg_create_update_permission":
		return planner.HasuraMetadataPayload{
			Type: "pg_drop_update_permission",
			Args: map[string]any{"source": args["source"], "table": args["table"], "role": args["role"]},
		}, true
	default:
		return planner.HasuraMetadataPayload{}, false
	}
}

func (r *Runner) exportHasuraMetadata(ctx context.Context) (json.RawMessage, error) {
	body, status, err := r.rawHasuraRequest(ctx, planner.HasuraMetadataPayload{
		Type: "export_metadata",
		Args: map[string]any{},
	})
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("export_metadata HTTP %d: %s", status, string(body))
	}
	return json.RawMessage(body), nil
}

func (r *Runner) restoreHasuraMetadata(ctx context.Context, snapshot json.RawMessage) error {
	if len(snapshot) == 0 {
		return nil
	}
	_, status, err := r.rawHasuraRequest(ctx, planner.HasuraMetadataPayload{
		Type: "replace_metadata",
		Args: snapshot,
	})
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("replace_metadata HTTP %d", status)
	}
	return nil
}

func (r *Runner) postMetadata(ctx context.Context, op planner.HasuraMetadataPayload, ignoreBenign bool) error {
	body, status, err := r.rawHasuraRequest(ctx, op)
	if err != nil {
		return err
	}
	if status >= 200 && status < 300 {
		return nil
	}
	text := string(body)
	if op.Type == "pg_track_table" && strings.Contains(text, "already-tracked") {
		return nil
	}
	if ignoreBenign && (strings.Contains(text, "not-exists") || strings.Contains(text, "does not exist") || strings.Contains(text, "not_found")) {
		return nil
	}
	return fmt.Errorf("hasura %s failed (HTTP %d): %s", op.Type, status, text)
}

func (r *Runner) rawHasuraRequest(ctx context.Context, payload any) ([]byte, int, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, 0, err
	}
	endpoint := strings.TrimRight(r.cfg.HasuraURL, "/") + "/v1/metadata"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(b))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	if r.cfg.HasuraAdminSecret != "" {
		req.Header.Set("X-Hasura-Admin-Secret", r.cfg.HasuraAdminSecret)
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	respBytes, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return respBytes, resp.StatusCode, err
}
