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

-- Tier 1 Kernel Extension Registry: Pluggable Domain Applications
CREATE TABLE IF NOT EXISTS core.app_registry (
    workspace_id UUID NOT NULL REFERENCES core.workspaces(id),
    app_id TEXT NOT NULL CHECK (length(trim(app_id)) > 0),
    display_name TEXT NOT NULL CHECK (length(trim(display_name)) > 0),
    version TEXT NOT NULL CHECK (length(trim(version)) > 0),
    role_name TEXT NOT NULL CHECK (length(trim(role_name)) > 0),
    domain_table TEXT NOT NULL CHECK (length(trim(domain_table)) > 0),
    manifest_spec JSONB NOT NULL,
    ui_schema JSONB NOT NULL,
    state_machine JSONB NOT NULL,
    dependent_capabilities TEXT[] NOT NULL DEFAULT '{}',
    status TEXT NOT NULL DEFAULT 'Pending' CHECK (status IN ('Pending', 'Applying', 'Ready', 'Failed')),
    last_error TEXT,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, app_id)
);
