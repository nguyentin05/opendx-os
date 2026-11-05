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

package data

import (
	"context"
	"database/sql"
	"fmt"
)

// LakehouseRecord represents an item exported to lakehouse.
type LakehouseRecord struct {
	EntityType    string `json:"entity_type"`
	EntityID      string `json:"entity_id"`
	EntityVersion int    `json:"entity_version"`
}

// ExportBatchRequest specifies export parameters.
type ExportBatchRequest struct {
	WorkspaceID string            `json:"workspace_id"`
	BatchKey    string            `json:"batch_key"`
	StorageURI  string            `json:"storage_uri"`
	Records     []LakehouseRecord `json:"records"`
}

// LakehouseEngine manages reconciled exports and lineage traceback.
type LakehouseEngine struct {
	db *sql.DB
}

// NewLakehouseEngine creates a new Lakehouse engine.
func NewLakehouseEngine(db *sql.DB) *LakehouseEngine {
	return &LakehouseEngine{db: db}
}

// ReconcileExportBatch idempotently registers an export batch and its source lineage.
func (e *LakehouseEngine) ReconcileExportBatch(ctx context.Context, req *ExportBatchRequest) (string, error) {
	tx, err := e.db.BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("failed to begin tx: %w", err)
	}
	defer tx.Rollback()

	// 1. Check or upsert export batch
	var batchID string
	err = tx.QueryRowContext(ctx,
		`INSERT INTO core.export_batches (workspace_id, batch_name, format, storage_uri, row_count, status)
		 VALUES ($1, $2, 'PARQUET', $3, $4, 'COMPLETED')
		 ON CONFLICT (workspace_id, batch_name) DO UPDATE
		 SET row_count = EXCLUDED.row_count, storage_uri = EXCLUDED.storage_uri, status = 'COMPLETED'
		 RETURNING id`,
		req.WorkspaceID, req.BatchKey, req.StorageURI, len(req.Records),
	).Scan(&batchID)

	if err != nil {
		return "", fmt.Errorf("failed to upsert export batch: %w", err)
	}

	// 2. Clear old lineage for this batch to ensure retry idempotency
	_, err = tx.ExecContext(ctx, `DELETE FROM core.data_lineage WHERE batch_id = $1`, batchID)
	if err != nil {
		return "", fmt.Errorf("failed to clear existing lineage for retry: %w", err)
	}

	// 3. Insert lineage items with full 4-part source key
	for _, rec := range req.Records {
		_, err = tx.ExecContext(ctx,
			`INSERT INTO core.data_lineage (workspace_id, batch_id, entity_type, entity_id, entity_version)
			 VALUES ($1, $2, $3, $4, $5)`,
			req.WorkspaceID, batchID, rec.EntityType, rec.EntityID, rec.EntityVersion,
		)
		if err != nil {
			return "", fmt.Errorf("failed to insert data lineage: %w", err)
		}
	}

	if err = tx.Commit(); err != nil {
		return "", fmt.Errorf("failed to commit export batch: %w", err)
	}

	return batchID, nil
}

// LineageTraceback returns the exact source entity records for a given batch.
func (e *LakehouseEngine) LineageTraceback(ctx context.Context, workspaceID, batchID string) ([]LakehouseRecord, error) {
	rows, err := e.db.QueryContext(ctx,
		`SELECT entity_type, entity_id, entity_version
		 FROM core.data_lineage
		 WHERE workspace_id = $1 AND batch_id = $2
		 ORDER BY created_at ASC`,
		workspaceID, batchID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query lineage traceback: %w", err)
	}
	defer rows.Close()

	var records []LakehouseRecord
	for rows.Next() {
		var r LakehouseRecord
		if err := rows.Scan(&r.EntityType, &r.EntityID, &r.EntityVersion); err != nil {
			return nil, err
		}
		records = append(records, r)
	}

	return records, nil
}
