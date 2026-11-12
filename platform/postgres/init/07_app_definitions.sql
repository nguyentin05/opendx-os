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

-- Global application definitions (ADR 0004). Domain tables, triggers and Hasura
-- metadata are shared by every workspace, so the operator plans them from this
-- table. core.app_registry remains the per-workspace publication.
CREATE TABLE IF NOT EXISTS core.app_definitions (
    app_id TEXT PRIMARY KEY CHECK (length(trim(app_id)) > 0),
    display_name TEXT NOT NULL CHECK (length(trim(display_name)) > 0),
    version TEXT NOT NULL CHECK (length(trim(version)) > 0),
    spec JSONB NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Adopt definitions already published by an earlier operator version, taking the
-- most recently published Ready snapshot of each application.
INSERT INTO core.app_definitions (app_id, display_name, version, spec, updated_at)
SELECT DISTINCT ON (app_id) app_id, display_name, version, manifest_spec, updated_at
  FROM core.app_registry
 WHERE status = 'Ready'
 ORDER BY app_id, updated_at DESC
ON CONFLICT (app_id) DO NOTHING;
