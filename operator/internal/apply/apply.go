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
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

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

// Run executes a compiled Plan safely, preserving the prior active snapshot on failure.
func (r *Runner) Run(ctx context.Context, workspaceID string, plan planner.Plan) error {
	if strings.TrimSpace(workspaceID) == "" {
		return errors.New("workspaceID is required")
	}

	attemptIDs := make(map[string]string, len(plan.Apps))
	if r.db != nil {
		for _, app := range plan.Apps {
			var attemptID string
			err := r.db.QueryRowContext(
				ctx,
				`INSERT INTO core.app_deployments (workspace_id, app_id, version, status)
				 VALUES ($1, $2, $3, 'Applying') RETURNING id::text`,
				workspaceID, app.Metadata.Name, app.Metadata.Version,
			).Scan(&attemptID)
			if err != nil {
				return fmt.Errorf("record deployment attempt for %s: %w", app.Metadata.Name, err)
			}
			attemptIDs[app.Metadata.Name] = attemptID
		}
	}

	markFailed := func(cause error) error {
		if r.db != nil {
			for _, id := range attemptIDs {
				_, _ = r.db.ExecContext(
					ctx,
					`UPDATE core.app_deployments SET status = 'Failed', last_error = $1, completed_at = now() WHERE id = $2`,
					cause.Error(), id,
				)
			}
		}
		return cause
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

	// 2. Execute DDL for each application in plan order
	if r.db != nil {
		for _, app := range plan.Apps {
			ddl := plan.DDLs[app.Metadata.Name]
			if _, err := r.db.ExecContext(ctx, ddl); err != nil {
				return markFailed(fmt.Errorf("execute DDL for %s: %w", app.Metadata.Name, err))
			}
		}
	}

	// 3. Apply Hasura DropOps and HasuraOps (restore snapshot on error)
	if r.cfg.HasuraURL != "" {
		for _, dropOp := range plan.DropOps {
			_ = r.callHasuraIdempotent(ctx, dropOp, true)
		}
		for _, op := range plan.HasuraOps {
			if err := r.callHasuraIdempotent(ctx, op, false); err != nil {
				_ = r.restoreHasuraMetadata(ctx, snapshot)
				return markFailed(fmt.Errorf("apply Hasura metadata op %s: %w", op.Type, err))
			}
		}
	}

	// 4. Atomically publish Ready active registry rows and mark deployment attempts Ready
	if r.db != nil {
		tx, err := r.db.BeginTx(ctx, nil)
		if err != nil {
			if r.cfg.HasuraURL != "" {
				_ = r.restoreHasuraMetadata(ctx, snapshot)
			}
			return markFailed(err)
		}
		defer tx.Rollback()

		if err := publishRegistrySnapshot(ctx, tx, workspaceID, plan.Apps, plan.RemovedApps); err != nil {
			if r.cfg.HasuraURL != "" {
				_ = r.restoreHasuraMetadata(ctx, snapshot)
			}
			return markFailed(err)
		}

		for _, id := range attemptIDs {
			if _, err := tx.ExecContext(ctx, `UPDATE core.app_deployments SET status = 'Ready', completed_at = now() WHERE id = $1`, id); err != nil {
				if r.cfg.HasuraURL != "" {
					_ = r.restoreHasuraMetadata(ctx, snapshot)
				}
				return markFailed(err)
			}
		}

		if err := tx.Commit(); err != nil {
			if r.cfg.HasuraURL != "" {
				_ = r.restoreHasuraMetadata(ctx, snapshot)
			}
			return markFailed(err)
		}
	}

	return nil
}

func (r *Runner) callHasuraIdempotent(ctx context.Context, op planner.HasuraMetadataPayload, ignoreNotFound bool) error {
	// If creating a permission or relationship that may already exist on reapply, drop first ignoring not-found
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
	case "pg_create_object_relationship", "pg_create_array_relationship":
		return planner.HasuraMetadataPayload{
			Type: "pg_drop_relationship",
			Args: map[string]any{"source": args["source"], "table": args["table"], "relationship": args["name"]},
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
	return fmt.Errorf("Hasura %s failed (HTTP %d): %s", op.Type, status, text)
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
	defer resp.Body.Close()
	respBytes, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return respBytes, resp.StatusCode, err
}
