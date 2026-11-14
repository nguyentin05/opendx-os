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

-- Tier 1 kernel. One installation serves one organization. Tier 1 holds only
-- what every application needs (identity, files, audit, events) and never
-- references Tier 2 capabilities or Tier 3 applications. Data is labelled by
-- application (app_id); departments belong to the HR capability (cap_hr).

CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE SCHEMA IF NOT EXISTS core;
CREATE SCHEMA IF NOT EXISTS domain;

-- 1. Session actor helpers -------------------------------------------------
-- The acting user and role come only from the trusted runtime connections:
-- cap-process (dxos_process) sets dxos.* settings, Hasura (dxos_hasura) sets
-- hasura.user from the verified JWT. Any other connection has no actor.

CREATE OR REPLACE FUNCTION core.session_actor_id()
RETURNS UUID LANGUAGE plpgsql STABLE AS $$
DECLARE
    v TEXT;
BEGIN
    IF session_user = 'dxos_process' THEN
        v := current_setting('dxos.actor_id', true);
    ELSIF session_user = 'dxos_hasura' THEN
        v := nullif(trim(coalesce(current_setting('hasura.user', true), '')), '')::jsonb ->> 'x-hasura-user-id';
    END IF;
    RETURN nullif(trim(coalesce(v, '')), '')::uuid;
END;
$$;

CREATE OR REPLACE FUNCTION core.session_acting_role()
RETURNS TEXT LANGUAGE plpgsql STABLE AS $$
DECLARE
    v TEXT;
BEGIN
    IF session_user = 'dxos_process' THEN
        v := current_setting('dxos.role', true);
    ELSIF session_user = 'dxos_hasura' THEN
        v := nullif(trim(coalesce(current_setting('hasura.user', true), '')), '')::jsonb ->> 'x-hasura-role';
    END IF;
    RETURN nullif(trim(coalesce(v, '')), '');
END;
$$;

CREATE OR REPLACE FUNCTION core.touch_updated_at()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    NEW.updated_at := now();
    RETURN NEW;
END;
$$;

-- 2. Users ------------------------------------------------------------------
-- Pure identity mirrored from Keycloak. id is the OIDC subject and is never
-- generated here. Department and position belong to cap_hr; permissions to
-- core.user_roles.
CREATE TABLE IF NOT EXISTS core.users (
    id UUID PRIMARY KEY,
    email TEXT NOT NULL CHECK (length(trim(email)) > 0),
    display_name TEXT NOT NULL CHECK (length(trim(display_name)) > 0),
    status TEXT NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE', 'DISABLED')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_users_email ON core.users (lower(email));

DROP TRIGGER IF EXISTS trg_users_touch ON core.users;
CREATE TRIGGER trg_users_touch
BEFORE UPDATE ON core.users
FOR EACH ROW EXECUTE FUNCTION core.touch_updated_at();

-- 3. Files ------------------------------------------------------------------
-- A file is uploaded through one application; that application's rules decide
-- who may read it. The foreign key to core.apps is added in 03_app_registry.sql.
CREATE TABLE IF NOT EXISTS core.files (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    app_id TEXT NOT NULL CHECK (length(trim(app_id)) > 0),
    bucket TEXT NOT NULL CHECK (length(trim(bucket)) > 0),
    object_key TEXT NOT NULL CHECK (length(trim(object_key)) > 0),
    file_name TEXT NOT NULL CHECK (length(trim(file_name)) > 0),
    scan_status TEXT NOT NULL DEFAULT 'QUARANTINED' CHECK (scan_status IN ('QUARANTINED', 'CLEAN', 'INFECTED')),
    sha256_hash TEXT NOT NULL CHECK (length(trim(sha256_hash)) = 64),
    uploaded_by UUID NOT NULL REFERENCES core.users(id),
    mime_type TEXT NOT NULL CHECK (mime_type IN ('application/pdf', 'image/png', 'image/jpeg')),
    size_bytes BIGINT NOT NULL CHECK (size_bytes BETWEEN 1 AND 20971520),
    scan_attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (scan_attempt_count >= 0),
    scan_next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    scanned_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (bucket, object_key)
);

CREATE INDEX IF NOT EXISTS idx_files_quarantined_due
    ON core.files (bucket, scan_next_attempt_at, created_at, id)
    WHERE scan_status = 'QUARANTINED';

CREATE OR REPLACE FUNCTION core.enforce_file_immutability()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.id IS DISTINCT FROM OLD.id
       OR NEW.app_id IS DISTINCT FROM OLD.app_id
       OR NEW.bucket IS DISTINCT FROM OLD.bucket
       OR NEW.object_key IS DISTINCT FROM OLD.object_key
       OR NEW.file_name IS DISTINCT FROM OLD.file_name
       OR NEW.sha256_hash IS DISTINCT FROM OLD.sha256_hash
       OR NEW.uploaded_by IS DISTINCT FROM OLD.uploaded_by
       OR NEW.mime_type IS DISTINCT FROM OLD.mime_type
       OR NEW.size_bytes IS DISTINCT FROM OLD.size_bytes
       OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'core.files identity and content metadata are immutable';
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_files_immutable ON core.files;
CREATE TRIGGER trg_files_immutable
BEFORE UPDATE ON core.files
FOR EACH ROW EXECUTE FUNCTION core.enforce_file_immutability();

-- 4. Audit log ----------------------------------------------------------------
-- Append-only and hash-chained. Each application has its own chain; changes
-- made outside any application (for example HR master data) use the 'core'
-- chain. The foreign key to core.apps is added in 03_app_registry.sql.
CREATE TABLE IF NOT EXISTS core.audit_logs (
    id BIGSERIAL PRIMARY KEY,
    chain TEXT NOT NULL,
    seq BIGINT NOT NULL,
    app_id TEXT,
    actor_id UUID NOT NULL REFERENCES core.users(id),
    acting_role TEXT CHECK (acting_role IS NULL OR length(trim(acting_role)) > 0),
    action TEXT NOT NULL CHECK (length(trim(action)) > 0),
    entity_type TEXT NOT NULL CHECK (length(trim(entity_type)) > 0),
    entity_id UUID NOT NULL,
    entity_version BIGINT CHECK (entity_version IS NULL OR entity_version > 0),
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    prev_hash TEXT NOT NULL,
    row_hash TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (chain, seq)
);

CREATE INDEX IF NOT EXISTS idx_audit_logs_entity ON core.audit_logs (entity_type, entity_id);

CREATE OR REPLACE FUNCTION core.compute_audit_hash_chain()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE
    v_last_seq BIGINT;
    v_last_hash TEXT;
BEGIN
    NEW.chain := coalesce(NEW.app_id, 'core');
    -- Serialize inserts per chain within the transaction.
    PERFORM pg_advisory_xact_lock(hashtext('dxos.audit:' || NEW.chain));

    SELECT seq, row_hash
      INTO v_last_seq, v_last_hash
      FROM core.audit_logs
     WHERE chain = NEW.chain
     ORDER BY seq DESC
     LIMIT 1;

    IF v_last_seq IS NULL THEN
        NEW.seq := 1;
        NEW.prev_hash := repeat('0', 64);
    ELSE
        NEW.seq := v_last_seq + 1;
        NEW.prev_hash := v_last_hash;
    END IF;

    IF NEW.created_at IS NULL THEN
        NEW.created_at := now();
    END IF;

    -- A JSON array keeps field boundaries unambiguous, unlike a delimited string.
    NEW.row_hash := encode(
        digest(
            jsonb_build_array(
                NEW.chain,
                NEW.seq,
                NEW.app_id,
                NEW.actor_id,
                NEW.acting_role,
                NEW.action,
                NEW.entity_type,
                NEW.entity_id,
                NEW.entity_version,
                NEW.payload,
                NEW.prev_hash
            )::text,
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

-- 5. Durable event outbox (S03) ---------------------------------------------
-- The foreign key to core.apps is added in 03_app_registry.sql.
CREATE TABLE IF NOT EXISTS core.outbox_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    app_id TEXT NOT NULL CHECK (length(trim(app_id)) > 0),
    event_type TEXT NOT NULL CHECK (length(trim(event_type)) > 0),
    entity_type TEXT NOT NULL CHECK (length(trim(entity_type)) > 0),
    entity_id UUID NOT NULL,
    entity_version BIGINT NOT NULL CHECK (entity_version > 0),
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    delivery_status TEXT NOT NULL DEFAULT 'pending' CHECK (delivery_status IN ('pending', 'in_flight', 'delivered')),
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    lease_token UUID NULL,
    lease_until TIMESTAMPTZ NULL,
    delivered_at TIMESTAMPTZ NULL,
    last_error TEXT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (app_id, entity_type, entity_id, entity_version, event_type),
    CHECK (
        (delivery_status = 'pending' AND lease_token IS NULL AND lease_until IS NULL AND delivered_at IS NULL)
        OR (delivery_status = 'in_flight' AND lease_token IS NOT NULL AND lease_until IS NOT NULL AND delivered_at IS NULL)
        OR (delivery_status = 'delivered' AND lease_token IS NULL AND lease_until IS NULL AND delivered_at IS NOT NULL)
    )
);

CREATE INDEX IF NOT EXISTS idx_outbox_events_due
    ON core.outbox_events (next_attempt_at, created_at)
    WHERE delivery_status IN ('pending', 'in_flight');

-- 6. Record triggers shared by Tier 2 and Tier 3 tables ---------------------

-- Fills created_by from the session actor when the caller does not set it.
CREATE OR REPLACE FUNCTION core.stamp_record_owner()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.created_by IS NULL THEN
        NEW.created_by := core.session_actor_id();
    END IF;
    IF NEW.created_by IS NULL THEN
        RAISE EXCEPTION 'missing authenticated actor context for %.% insert', TG_TABLE_SCHEMA, TG_TABLE_NAME;
    END IF;
    RETURN NEW;
END;
$$;

-- Server-side optimistic concurrency (S04). A record's identity, creator and
-- owning department (when the table has one) never change.
CREATE OR REPLACE FUNCTION core.bump_domain_record_version()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE
    v_new JSONB := to_jsonb(NEW);
    v_old JSONB := to_jsonb(OLD);
BEGIN
    IF NEW.id IS DISTINCT FROM OLD.id
       OR v_new -> 'created_by' IS DISTINCT FROM v_old -> 'created_by'
       OR v_new -> 'org_unit_id' IS DISTINCT FROM v_old -> 'org_unit_id' THEN
        RAISE EXCEPTION 'record identity (id, created_by, org_unit_id) is immutable';
    END IF;
    NEW.version := OLD.version + 1;
    NEW.updated_at := now();
    RETURN NEW;
END;
$$;

-- Writes one audit row per INSERT or UPDATE of a domain record. The operator
-- passes the application id as the trigger argument:
--   EXECUTE FUNCTION core.record_domain_audit('<app_id>')
-- Only the two runtime connections may write domain records; cap-process must
-- also name the command in dxos.action.
CREATE OR REPLACE FUNCTION core.record_domain_audit()
RETURNS TRIGGER LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, core, public AS $$
DECLARE
    v_actor UUID;
    v_action TEXT;
BEGIN
    IF session_user NOT IN ('dxos_process', 'dxos_hasura') THEN
        RAISE EXCEPTION 'untrusted database connection for domain audit';
    END IF;

    v_actor := core.session_actor_id();
    IF session_user = 'dxos_process' THEN
        v_action := nullif(trim(coalesce(current_setting('dxos.action', true), '')), '');
    ELSE
        v_action := TG_OP;
    END IF;

    IF v_actor IS NULL OR v_action IS NULL THEN
        RAISE EXCEPTION 'missing authenticated actor context for domain %', lower(TG_OP);
    END IF;

    INSERT INTO core.audit_logs (
        app_id, actor_id, acting_role, action, entity_type, entity_id, entity_version, payload
    ) VALUES (
        nullif(TG_ARGV[0], ''),
        v_actor,
        core.session_acting_role(),
        v_action,
        TG_TABLE_SCHEMA || '.' || TG_TABLE_NAME,
        NEW.id,
        NEW.version,
        to_jsonb(NEW)
    );

    RETURN NEW;
END;
$$;

REVOKE ALL ON FUNCTION core.record_domain_audit() FROM PUBLIC;

-- An attachment column may point only at a file of the same application that
-- the acting user uploaded, so nobody can attach (and then read, or submit as
-- their own) a file someone else uploaded. Generated domain tables call it as
--   EXECUTE FUNCTION core.enforce_attachments('<app_id>', '<column>', ...)
-- An unchanged attachment is not re-checked on UPDATE.
CREATE OR REPLACE FUNCTION core.enforce_attachments()
RETURNS TRIGGER LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, core, public AS $$
DECLARE
    v_actor UUID := core.session_actor_id();
    v_new JSONB := to_jsonb(NEW);
    v_old JSONB;
    v_column TEXT;
    v_file TEXT;
BEGIN
    IF TG_OP = 'UPDATE' THEN
        v_old := to_jsonb(OLD);
    END IF;
    FOR i IN 1 .. TG_NARGS - 1 LOOP
        v_column := TG_ARGV[i];
        v_file := v_new ->> v_column;
        IF v_file IS NULL OR (v_old IS NOT NULL AND (v_old ->> v_column) IS NOT DISTINCT FROM v_file) THEN
            CONTINUE;
        END IF;
        IF v_actor IS NULL OR NOT EXISTS (
            SELECT 1 FROM core.files f
             WHERE f.id = v_file::uuid AND f.app_id = TG_ARGV[0] AND f.uploaded_by = v_actor
        ) THEN
            RAISE EXCEPTION 'attachment %: only a file you uploaded to this application can be attached', v_column
                USING ERRCODE = 'insufficient_privilege';
        END IF;
    END LOOP;
    RETURN NEW;
END;
$$;

REVOKE ALL ON FUNCTION core.enforce_attachments() FROM PUBLIC;
