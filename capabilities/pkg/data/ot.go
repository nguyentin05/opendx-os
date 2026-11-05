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
	"time"

	"github.com/lib/pq"
)

// OTIngestionRequest represents OT sensor telemetry.
type OTIngestionRequest struct {
	WorkspaceID string  `json:"workspace_id"`
	DeviceID    string  `json:"device_id"`
	EventKey    string  `json:"event_dedup_key"`
	MetricName  string  `json:"metric_name"`
	MetricValue float64 `json:"metric_value"`
}

// OTIngestionResponse represents ingestion result.
type OTIngestionResponse struct {
	EventID      string    `json:"event_id,omitempty"`
	Deduplicated bool      `json:"deduplicated"`
	WorkItemID   string    `json:"work_item_id,omitempty"`
	IngestedAt   time.Time `json:"ingested_at"`
}

// OTEngine handles deduplication and work item generation for OT events.
type OTEngine struct {
	db *sql.DB
}

// NewOTEngine creates a new OT telemetry engine.
func NewOTEngine(db *sql.DB) *OTEngine {
	return &OTEngine{db: db}
}

// IngestTelemetry deduplicates and stores OT events.
func (e *OTEngine) IngestTelemetry(ctx context.Context, req *OTIngestionRequest) (*OTIngestionResponse, error) {
	tx, err := e.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	var eventID string
	err = tx.QueryRowContext(ctx,
		`INSERT INTO core.ot_events (workspace_id, device_id, event_dedup_key, metric_name, metric_value)
		 VALUES ($1, $2, $3, $4, $5)
		 RETURNING id`,
		req.WorkspaceID, req.DeviceID, req.EventKey, req.MetricName, req.MetricValue,
	).Scan(&eventID)

	if err != nil {
		// Check for unique constraint violation (dedup key collision)
		if pqErr, ok := err.(*pq.Error); ok && pqErr.Code == "23505" {
			// Duplicate event detected: idempotent return
			return &OTIngestionResponse{
				Deduplicated: true,
				IngestedAt:   time.Now().UTC(),
			}, nil
		}
		return nil, fmt.Errorf("failed to insert ot event: %w", err)
	}

	// Update device last_reading
	_, _ = tx.ExecContext(ctx,
		`UPDATE core.devices SET last_reading = $1 WHERE workspace_id = $2 AND id = $3`,
		fmt.Sprintf("%s=%.2f", req.MetricName, req.MetricValue),
		req.WorkspaceID, req.DeviceID,
	)

	// If threshold breached (e.g., temperature > 80.0), create a work item and associate it with device
	var workItemID string
	if req.MetricName == "temperature" && req.MetricValue > 80.0 {
		err = tx.QueryRowContext(ctx,
			`INSERT INTO core.work_items (workspace_id, title, description, priority)
			 VALUES ($1, $2, $3, 'HIGH')
			 RETURNING id`,
			req.WorkspaceID,
			fmt.Sprintf("OT Alert: Overheating on device %s", req.DeviceID),
			fmt.Sprintf("Telemetry metric %s reached %.2f", req.MetricName, req.MetricValue),
		).Scan(&workItemID)
		if err == nil && workItemID != "" {
			_, _ = tx.ExecContext(ctx,
				`INSERT INTO core.device_work_items (workspace_id, work_item_id, device_id)
				 VALUES ($1, $2, $3)
				 ON CONFLICT DO NOTHING`,
				req.WorkspaceID, workItemID, req.DeviceID,
			)
		}
	}

	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit ot event: %w", err)
	}

	return &OTIngestionResponse{
		EventID:      eventID,
		Deduplicated: false,
		WorkItemID:   workItemID,
		IngestedAt:   time.Now().UTC(),
	}, nil
}
