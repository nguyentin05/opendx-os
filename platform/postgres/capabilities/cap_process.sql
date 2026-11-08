-- SPDX-License-Identifier: Apache-2.0
-- Copyright 2026 DX-OS Open-Core Contributors
--
-- Licensed under the Apache License, Version 2.0 (the "License");
-- you may not use this file except in compliance with the License.
-- You may obtain a copy of the License at
--
--     http://www.apache.org/licenses/LICENSE-2.0
--
-- Unless required by applicable law or agreed to in writing, software
-- distributed under the License is distributed on an "AS IS" BASIS,
-- WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
-- See the License for the specific language governing permissions and
-- limitations under the License.

-- Tier 2 Pluggable Capability Schema: cap-process ([P])
-- Manages: OCC State Machine Transitions, Command Gatekeeping, Quarantine Scan Pipeline

-- 1. Command Idempotency Receipts (S03)
CREATE TABLE IF NOT EXISTS core.command_receipts (
    workspace_id UUID NOT NULL REFERENCES core.workspaces(id),
    actor_id TEXT NOT NULL CHECK (length(trim(actor_id)) > 0),
    app_id TEXT NOT NULL CHECK (length(trim(app_id)) > 0),
    entity_id UUID NOT NULL,
    command TEXT NOT NULL CHECK (length(trim(command)) > 0),
    idempotency_key TEXT NOT NULL CHECK (length(trim(idempotency_key)) > 0),
    payload_hash TEXT NOT NULL CHECK (length(trim(payload_hash)) = 64),
    response_status INTEGER NOT NULL,
    response_body JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, actor_id, app_id, entity_id, command, idempotency_key)
);

-- 2. Workflow Step Idempotency Tracking (S03)
CREATE TABLE IF NOT EXISTS core.processed_workflow_steps (
    event_id UUID NOT NULL,
    step_name TEXT NOT NULL CHECK (length(trim(step_name)) > 0),
    workspace_id UUID NOT NULL,
    work_item_id UUID NOT NULL,
    result JSONB NOT NULL,
    outcome TEXT NOT NULL CHECK (length(trim(outcome)) > 0),
    processed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (event_id, step_name),
    FOREIGN KEY (workspace_id, event_id) REFERENCES core.outbox_events(workspace_id, id),
    FOREIGN KEY (workspace_id, work_item_id) REFERENCES core.work_items(workspace_id, id)
);

-- 3. Register cap-process in capability registry
INSERT INTO core.capability_registry (cap_id, display_name, category_hpdi, base_route, enabled)
VALUES ('cap-process', 'Process Orchestration & OCC Transitions', 'P', '/api/cap/process', true)
ON CONFLICT (cap_id) DO UPDATE SET enabled = true, updated_at = now();
