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

-- History of operator deployment attempts. A failed attempt never replaces the
-- running definition in core.apps.
CREATE TABLE IF NOT EXISTS core.app_deployments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    app_id TEXT NOT NULL REFERENCES core.apps(app_id),
    version TEXT NOT NULL CHECK (length(trim(version)) > 0),
    status TEXT NOT NULL CHECK (status IN ('Applying', 'Ready', 'Failed')),
    last_error TEXT,
    started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_app_deployments_app
    ON core.app_deployments (app_id, started_at DESC);

-- Versions of the Tier 1 schema applied to this database, so a running
-- installation can be upgraded in place. Written by the migration runner; the
-- checksum detects a migration edited after it was applied.
CREATE TABLE IF NOT EXISTS core.schema_migrations (
    version TEXT PRIMARY KEY CHECK (length(trim(version)) > 0),
    checksum TEXT NOT NULL CHECK (length(trim(checksum)) = 64),
    applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
