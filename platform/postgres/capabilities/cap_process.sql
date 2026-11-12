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

-- Tier 2 capability: cap-process ([P])
-- Commands on domain records (state transitions with optimistic concurrency),
-- command idempotency and workflow step idempotency.

CREATE SCHEMA IF NOT EXISTS cap_process;

-- 1. Command idempotency receipts (S03)
CREATE TABLE IF NOT EXISTS cap_process.command_receipts (
    actor_id UUID NOT NULL REFERENCES core.users(id),
    app_id TEXT NOT NULL REFERENCES core.apps(app_id),
    entity_id UUID NOT NULL,
    command TEXT NOT NULL CHECK (length(trim(command)) > 0),
    idempotency_key TEXT NOT NULL CHECK (length(trim(idempotency_key)) > 0),
    payload_hash TEXT NOT NULL CHECK (length(trim(payload_hash)) = 64),
    response_status INTEGER NOT NULL,
    response_body JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (actor_id, app_id, entity_id, command, idempotency_key)
);

-- 2. Workflow step idempotency (S03). result holds what the step produced,
-- such as the id of a work item it created.
CREATE TABLE IF NOT EXISTS cap_process.processed_workflow_steps (
    event_id UUID NOT NULL REFERENCES core.outbox_events(id),
    step_name TEXT NOT NULL CHECK (length(trim(step_name)) > 0),
    app_id TEXT NOT NULL REFERENCES core.apps(app_id),
    result JSONB NOT NULL,
    outcome TEXT NOT NULL CHECK (length(trim(outcome)) > 0),
    processed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (event_id, step_name)
);

-- 3. Register cap-process
INSERT INTO core.capabilities (cap_id, display_name, category_hpdi, base_route, enabled)
VALUES ('cap-process', 'Process Orchestration & OCC Transitions', 'P', '/api/cap/process', true)
ON CONFLICT (cap_id) DO UPDATE SET enabled = true, updated_at = now();
