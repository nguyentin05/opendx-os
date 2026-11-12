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

-- Tier 2 capability: cap-work ([H])
-- Work items (Kanban cards) that link the records of different applications to
-- one piece of work. Created by an application, by a workflow, or by another
-- capability such as an equipment alert (source_type/source_id).

CREATE SCHEMA IF NOT EXISTS cap_work;

CREATE TABLE IF NOT EXISTS cap_work.work_items (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    app_id TEXT REFERENCES core.apps(app_id),
    title TEXT NOT NULL CHECK (length(trim(title)) > 0),
    description TEXT NOT NULL DEFAULT '',
    priority TEXT NOT NULL DEFAULT 'MEDIUM' CHECK (priority IN ('LOW', 'MEDIUM', 'HIGH', 'CRITICAL')),
    board_column TEXT NOT NULL DEFAULT 'TODO' CHECK (board_column IN ('TODO', 'IN_PROGRESS', 'DONE')),
    assignee_id UUID REFERENCES core.users(id),
    source_type TEXT CHECK (source_type IS NULL OR length(trim(source_type)) > 0),
    source_id UUID,
    created_by UUID NOT NULL REFERENCES core.users(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT ck_work_items_source CHECK ((source_type IS NULL) = (source_id IS NULL)),
    CONSTRAINT ck_work_items_origin CHECK (app_id IS NOT NULL OR source_type IS NOT NULL)
);

CREATE INDEX IF NOT EXISTS idx_work_items_assignee ON cap_work.work_items (assignee_id);
CREATE INDEX IF NOT EXISTS idx_work_items_app ON cap_work.work_items (app_id);
CREATE INDEX IF NOT EXISTS idx_work_items_source ON cap_work.work_items (source_type, source_id);

DROP TRIGGER IF EXISTS trg_work_items_owner ON cap_work.work_items;
CREATE TRIGGER trg_work_items_owner
BEFORE INSERT ON cap_work.work_items
FOR EACH ROW EXECUTE FUNCTION core.stamp_record_owner();

DROP TRIGGER IF EXISTS trg_work_items_touch ON cap_work.work_items;
CREATE TRIGGER trg_work_items_touch
BEFORE UPDATE ON cap_work.work_items
FOR EACH ROW EXECUTE FUNCTION core.touch_updated_at();

INSERT INTO core.capabilities (cap_id, display_name, category_hpdi, base_route, enabled)
VALUES ('cap-work', 'Work Items & Kanban', 'H', '/api/cap/work', true)
ON CONFLICT (cap_id) DO UPDATE SET enabled = true, updated_at = now();
