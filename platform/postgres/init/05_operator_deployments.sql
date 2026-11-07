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

-- Tier 1 Operator Deployment Attempt History (preserves active Ready snapshot in core.app_registry on failure)
CREATE TABLE IF NOT EXISTS core.app_deployments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES core.workspaces(id),
    app_id TEXT NOT NULL CHECK (length(trim(app_id)) > 0),
    version TEXT NOT NULL CHECK (length(trim(version)) > 0),
    status TEXT NOT NULL CHECK (status IN ('Applying', 'Ready', 'Failed')),
    last_error TEXT,
    started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_app_deployments_workspace_app
    ON core.app_deployments (workspace_id, app_id, started_at DESC);
