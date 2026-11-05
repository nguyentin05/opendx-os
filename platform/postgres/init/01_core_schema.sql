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

CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE SCHEMA IF NOT EXISTS core;
CREATE SCHEMA IF NOT EXISTS domain;

-- 1. Tenant Workspaces
CREATE TABLE IF NOT EXISTS core.workspaces (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    code TEXT UNIQUE NOT NULL CHECK (length(trim(code)) > 0),
    name TEXT NOT NULL CHECK (length(trim(name)) > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 2. Shared Personnel Master Entity (Universal Identity)
CREATE TABLE IF NOT EXISTS core.employees (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES core.workspaces(id),
    full_name TEXT NOT NULL CHECK (length(trim(full_name)) > 0),
    email TEXT NOT NULL CHECK (length(trim(email)) > 0),
    department TEXT NOT NULL CHECK (length(trim(department)) > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, id),
    UNIQUE (workspace_id, email)
);

-- 3. Shared Work Items (Core Task Routing)
CREATE TABLE IF NOT EXISTS core.work_items (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES core.workspaces(id),
    title TEXT NOT NULL CHECK (length(trim(title)) > 0),
    description TEXT NOT NULL DEFAULT '',
    priority TEXT NOT NULL DEFAULT 'MEDIUM' CHECK (priority IN ('LOW', 'MEDIUM', 'HIGH', 'CRITICAL')),
    assigned_employee_id UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, id),
    FOREIGN KEY (workspace_id, assigned_employee_id) REFERENCES core.employees(workspace_id, id)
);

-- 4. Quarantine-First File Objects (S06)
CREATE TABLE IF NOT EXISTS core.file_objects (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES core.workspaces(id),
    bucket TEXT NOT NULL CHECK (length(trim(bucket)) > 0),
    object_key TEXT NOT NULL CHECK (length(trim(object_key)) > 0),
    file_name TEXT NOT NULL CHECK (length(trim(file_name)) > 0),
    scan_status TEXT NOT NULL DEFAULT 'QUARANTINED' CHECK (scan_status IN ('QUARANTINED', 'CLEAN', 'INFECTED')),
    sha256_hash TEXT NOT NULL CHECK (length(trim(sha256_hash)) = 64),
    uploaded_by UUID,
    scanned_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, id),
    UNIQUE (bucket, object_key),
    FOREIGN KEY (workspace_id, uploaded_by) REFERENCES core.employees(workspace_id, id)
);

-- 5. Serialized Per-Workspace Audit Hash-Chain
CREATE TABLE IF NOT EXISTS core.audit_logs (
    id BIGSERIAL PRIMARY KEY,
    workspace_id UUID NOT NULL REFERENCES core.workspaces(id),
    workspace_seq BIGINT NOT NULL,
    actor_id TEXT NOT NULL CHECK (length(trim(actor_id)) > 0),
    action TEXT NOT NULL CHECK (length(trim(action)) > 0),
    entity_type TEXT NOT NULL CHECK (length(trim(entity_type)) > 0),
    entity_id UUID NOT NULL,
    entity_version INTEGER NOT NULL CHECK (entity_version > 0),
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    prev_hash TEXT NOT NULL,
    row_hash TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, workspace_seq)
);

CREATE OR REPLACE FUNCTION core.compute_audit_hash_chain()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE
    v_last_seq BIGINT;
    v_last_hash TEXT;
BEGIN
    -- Serialize audit log inserts per workspace within the transaction
    PERFORM pg_advisory_xact_lock(hashtext(NEW.workspace_id::text));

    SELECT workspace_seq, row_hash
      INTO v_last_seq, v_last_hash
      FROM core.audit_logs
     WHERE workspace_id = NEW.workspace_id
     ORDER BY workspace_seq DESC
     LIMIT 1;

    IF v_last_seq IS NULL THEN
        NEW.workspace_seq := 1;
        NEW.prev_hash := repeat('0', 64);
    ELSE
        NEW.workspace_seq := v_last_seq + 1;
        NEW.prev_hash := v_last_hash;
    END IF;

    IF NEW.created_at IS NULL THEN
        NEW.created_at := now();
    END IF;

    NEW.row_hash := encode(
        digest(
            concat_ws(
                '|',
                NEW.workspace_id::text,
                NEW.workspace_seq::text,
                NEW.actor_id,
                NEW.action,
                NEW.entity_type,
                NEW.entity_id::text,
                NEW.entity_version::text,
                NEW.payload::text,
                NEW.prev_hash
            ),
            'sha256'
        ),
        'hex'
    );
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_audit_hash_chain ON core.audit_logs;
CREATE TRIGGER trg_audit_hash_chain
BEFORE INSERT ON core.audit_logs
FOR EACH ROW EXECUTE FUNCTION core.compute_audit_hash_chain();

CREATE OR REPLACE FUNCTION core.prevent_audit_mutation()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'core.audit_logs is append-only and immutable';
END;
$$;

DROP TRIGGER IF EXISTS trg_audit_immutable ON core.audit_logs;
CREATE TRIGGER trg_audit_immutable
BEFORE UPDATE OR DELETE ON core.audit_logs
FOR EACH ROW EXECUTE FUNCTION core.prevent_audit_mutation();

-- 6. Durable Event Outbox for Asynchronous Messaging & Event-Driven Architecture (S03)
CREATE TABLE IF NOT EXISTS core.outbox_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES core.workspaces(id),
    app_id TEXT NOT NULL CHECK (length(trim(app_id)) > 0),
    event_type TEXT NOT NULL CHECK (length(trim(event_type)) > 0),
    entity_type TEXT NOT NULL CHECK (length(trim(entity_type)) > 0),
    entity_id UUID NOT NULL,
    entity_version INTEGER NOT NULL CHECK (entity_version > 0),
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, id),
    UNIQUE (workspace_id, app_id, entity_type, entity_id, entity_version, event_type)
);

-- 7. Shared Server-Side Version Bump Function for Domain Records (S04)
CREATE OR REPLACE FUNCTION core.bump_domain_record_version()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.id IS DISTINCT FROM OLD.id OR NEW.workspace_id IS DISTINCT FROM OLD.workspace_id THEN
        RAISE EXCEPTION 'record identity (id, workspace_id) is immutable';
    END IF;
    NEW.version := OLD.version + 1;
    NEW.updated_at := now();
    RETURN NEW;
END;
$$;
