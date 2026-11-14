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

-- Tier 1 application registry and access control.

-- 1. Applications -------------------------------------------------------------
-- One row per Tier 3 application, holding its only copy of the definition.
-- Rows are never deleted: an uninstalled application keeps its row with status
-- 'Removed', so files, events, audit and retained domain data still name it.
CREATE TABLE IF NOT EXISTS core.apps (
    app_id TEXT PRIMARY KEY CHECK (app_id ~ '^[a-z][a-z0-9_]*$'),
    display_name TEXT NOT NULL CHECK (length(trim(display_name)) > 0),
    version TEXT NOT NULL CHECK (length(trim(version)) > 0),
    spec JSONB NOT NULL,
    status TEXT NOT NULL DEFAULT 'Pending' CHECK (status IN ('Pending', 'Applying', 'Ready', 'Failed', 'Removed')),
    last_error TEXT,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE OR REPLACE FUNCTION core.prevent_app_delete()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'applications are uninstalled by setting status to Removed, not deleted';
END;
$$;

DROP TRIGGER IF EXISTS trg_apps_no_delete ON core.apps;
CREATE TRIGGER trg_apps_no_delete
BEFORE DELETE ON core.apps
FOR EACH ROW EXECUTE FUNCTION core.prevent_app_delete();

-- Data created in 01_core_schema.sql is labelled with an application.
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'fk_files_app') THEN
        ALTER TABLE core.files ADD CONSTRAINT fk_files_app
            FOREIGN KEY (app_id) REFERENCES core.apps(app_id);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'fk_outbox_events_app') THEN
        ALTER TABLE core.outbox_events ADD CONSTRAINT fk_outbox_events_app
            FOREIGN KEY (app_id) REFERENCES core.apps(app_id);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'fk_audit_logs_app') THEN
        ALTER TABLE core.audit_logs ADD CONSTRAINT fk_audit_logs_app
            FOREIGN KEY (app_id) REFERENCES core.apps(app_id);
    END IF;
END $$;

-- 2. Roles ----------------------------------------------------------------------
-- Every role is owned by the platform, one application or one capability. The
-- operator registers application roles from manifests; a capability's SQL
-- file registers its own roles.
-- Names start with role_ so no role can collide with names Hasura treats as
-- privileged (admin).
CREATE TABLE IF NOT EXISTS core.roles (
    name TEXT PRIMARY KEY CHECK (name ~ '^role_[a-z0-9_]+$'),
    app_id TEXT REFERENCES core.apps(app_id),
    cap_id TEXT REFERENCES core.capabilities(cap_id),
    description TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT ck_roles_single_owner CHECK (num_nonnulls(app_id, cap_id) <= 1)
);

INSERT INTO core.roles (name, description) VALUES
    ('role_operator', 'Platform operations'),
    ('role_auditor', 'Read-only audit'),
    ('role_workflow', 'Workflow service account')
ON CONFLICT (name) DO NOTHING;

-- 3. Role assignments -------------------------------------------------------
-- Which user holds which role. Where the role applies (own records, own
-- department, whole organization) is declared per role by the application;
-- departments come from cap_hr.
CREATE TABLE IF NOT EXISTS core.user_roles (
    user_id UUID NOT NULL REFERENCES core.users(id) ON DELETE CASCADE,
    role_name TEXT NOT NULL REFERENCES core.roles(name) ON DELETE CASCADE,
    granted_by UUID REFERENCES core.users(id) ON DELETE SET NULL,
    granted_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, role_name)
);

CREATE INDEX IF NOT EXISTS idx_user_roles_role ON core.user_roles (role_name);
