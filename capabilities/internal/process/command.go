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

package process

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
)

var validTableNameRegex = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*\.[a-zA-Z_][a-zA-Z0-9_]*$`)

// Engine executes state transitions and command reconciliation.
type Engine struct {
	db *sql.DB
}

// NewEngine creates a new process command engine.
func NewEngine(db *sql.DB) *Engine {
	return &Engine{db: db}
}

// Custom errors
var (
	ErrNotFound            = errors.New("record not found in workspace")
	ErrVersionConflict     = errors.New("version conflict: record has been modified")
	ErrQuarantinedFile     = errors.New("file is quarantined or infected (scan_status != 'CLEAN')")
	ErrInvalidTargetTable  = errors.New("invalid target table name format")
	ErrUnauthorizedRole    = errors.New("unauthorized role for this command")
)

// Execute handles state mutation with strict idempotency, quarantine, and version checking.
func (e *Engine) Execute(ctx context.Context, iden *auth.Identity, req *CommandRequest) (*CommandResponse, int, error) {
	if !validTableNameRegex.MatchString(req.TargetTable) {
		return nil, 400, ErrInvalidTargetTable
	}

	// 1. Check idempotency receipt
	var cachedResp string
	err := e.db.QueryRowContext(ctx,
		`SELECT response_payload::text FROM core.command_receipts WHERE workspace_id = $1 AND command_id = $2`,
		iden.WorkspaceID, req.CommandID,
	).Scan(&cachedResp)

	if err == nil {
		var resp CommandResponse
		if jsonErr := json.Unmarshal([]byte(cachedResp), &resp); jsonErr == nil {
			resp.IdempotentRe = true
			return &resp, 200, nil
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, 500, fmt.Errorf("failed to check idempotency receipt: %w", err)
	}

	// 2. Quarantine check on attachments
	if req.AttachmentID != "" {
		var scanStatus string
		err := e.db.QueryRowContext(ctx,
			`SELECT scan_status FROM core.file_objects WHERE workspace_id = $1 AND id = $2`,
			iden.WorkspaceID, req.AttachmentID,
		).Scan(&scanStatus)

		if errors.Is(err, sql.ErrNoRows) {
			return nil, 404, errors.New("attached file object not found in workspace")
		} else if err != nil {
			return nil, 500, fmt.Errorf("failed to check file scan status: %w", err)
		}

		if scanStatus != "CLEAN" {
			return nil, 422, ErrQuarantinedFile
		}
	}

	// 3. Begin database transaction
	tx, err := e.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return nil, 500, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// 4. Lock and load target domain record
	var currentStatus string
	var currentVersion int
	query := fmt.Sprintf(
		`SELECT status, version FROM %s WHERE workspace_id = $1 AND id = $2 FOR UPDATE`,
		req.TargetTable,
	)
	err = tx.QueryRowContext(ctx, query, iden.WorkspaceID, req.TargetID).Scan(&currentStatus, &currentVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, 404, ErrNotFound
	} else if err != nil {
		return nil, 500, fmt.Errorf("failed to query target record: %w", err)
	}

	// 5. Version check (Optimistic concurrency control)
	if currentVersion != req.ExpectedVersion {
		return nil, 409, fmt.Errorf("%w: expected %d, got %d", ErrVersionConflict, req.ExpectedVersion, currentVersion)
	}

	// 6. Update target domain record status (trigger will increment version)
	updateQuery := fmt.Sprintf(
		`UPDATE %s SET status = $1 WHERE workspace_id = $2 AND id = $3 RETURNING version`,
		req.TargetTable,
	)
	var newVersion int
	err = tx.QueryRowContext(ctx, updateQuery, req.TargetStatus, iden.WorkspaceID, req.TargetID).Scan(&newVersion)
	if err != nil {
		return nil, 500, fmt.Errorf("failed to update record: %w", err)
	}

	// 7. Serialize workspace audit lock to maintain SHA-256 hash-chain integrity
	_, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1::text))`, iden.WorkspaceID)
	if err != nil {
		return nil, 500, fmt.Errorf("failed to acquire audit lock: %w", err)
	}

	// 8. Insert into core.audit_logs
	payloadJSON, _ := json.Marshal(req)
	_, err = tx.ExecContext(ctx,
		`INSERT INTO core.audit_logs (workspace_id, actor_id, action, entity_type, entity_id, entity_version, payload)
		 VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb)`,
		iden.WorkspaceID, iden.UserID, req.Action, req.TargetTable, req.TargetID, newVersion, payloadJSON,
	)
	if err != nil {
		return nil, 500, fmt.Errorf("failed to write audit log: %w", err)
	}

	// 9. Insert durable outbox event for async workflows
	eventType := fmt.Sprintf("%s.%s", strings.TrimPrefix(req.TargetTable, "domain."), strings.ToLower(req.Action))
	_, err = tx.ExecContext(ctx,
		`INSERT INTO core.outbox_events (workspace_id, event_type, aggregate_type, aggregate_id, aggregate_version, payload)
		 VALUES ($1, $2, $3, $4, $5, $6::jsonb)`,
		iden.WorkspaceID, eventType, req.TargetTable, req.TargetID, newVersion, payloadJSON,
	)
	if err != nil {
		return nil, 500, fmt.Errorf("failed to emit outbox event: %w", err)
	}

	resp := CommandResponse{
		CommandID:    req.CommandID,
		TargetID:     req.TargetID,
		Status:       req.TargetStatus,
		Version:      newVersion,
		ExecutedAt:   time.Now().UTC(),
		IdempotentRe: false,
	}

	respBytes, _ := json.Marshal(resp)

	// 10. Record command receipt
	_, err = tx.ExecContext(ctx,
		`INSERT INTO core.command_receipts (workspace_id, command_id, target_table, target_id, action, status, version, response_payload)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8::jsonb)`,
		iden.WorkspaceID, req.CommandID, req.TargetTable, req.TargetID, req.Action, req.TargetStatus, newVersion, respBytes,
	)
	if err != nil {
		return nil, 500, fmt.Errorf("failed to record command receipt: %w", err)
	}

	if err = tx.Commit(); err != nil {
		return nil, 500, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return &resp, 200, nil
}
