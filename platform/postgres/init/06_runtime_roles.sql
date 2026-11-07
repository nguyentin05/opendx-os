-- SPDX-License-Identifier: Apache-2.0
-- Copyright 2026 DX-OS Open-Core Contributors
-- Connection identities are server authority, never business role membership.
DO $$
DECLARE name text;
BEGIN
  FOREACH name IN ARRAY ARRAY['dxos_process','dxos_hasura','dxos_reader','dxos_caller','dxos_module_test'] LOOP
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname=name) THEN
      EXECUTE format('CREATE ROLE %I LOGIN NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE NOINHERIT',name);
    END IF;
    EXECUTE format('ALTER ROLE %I NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE NOINHERIT',name);
  END LOOP;
END $$;
ALTER ROLE dxos_module_test CREATEDB;
REVOKE CREATE ON SCHEMA public FROM PUBLIC;
GRANT USAGE ON SCHEMA core, domain TO dxos_process, dxos_hasura, dxos_reader, dxos_caller;
GRANT SELECT ON ALL TABLES IN SCHEMA core TO dxos_process, dxos_reader;
GRANT INSERT, UPDATE ON core.work_items, core.outbox_events TO dxos_process;
DO $$
DECLARE name text;
BEGIN
  FOREACH name IN ARRAY ARRAY['core.command_receipts','core.processed_workflow_steps'] LOOP
    IF to_regclass(name) IS NOT NULL THEN
      EXECUTE format('GRANT INSERT, UPDATE ON %s TO dxos_process',name);
    END IF;
  END LOOP;
END $$;
GRANT INSERT ON core.file_objects TO dxos_process;
GRANT UPDATE(scan_status,scan_attempt_count,scan_next_attempt_at,scanned_at) ON core.file_objects TO dxos_process;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA core TO dxos_process;
-- Hasura metadata is private to its connection, not exposed to business roles.
CREATE SCHEMA IF NOT EXISTS hdb_catalog AUTHORIZATION dxos_hasura;
CREATE SCHEMA IF NOT EXISTS hdb_views AUTHORIZATION dxos_hasura;
-- Task 4 adds reviewed domain/source grants, not blanket future-table grants.

CREATE OR REPLACE FUNCTION core.record_domain_update_audit()
RETURNS TRIGGER LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, core, public AS $$
DECLARE
    v_actor TEXT;
    v_action TEXT;
    v_hasura_user TEXT;
BEGIN
    IF session_user NOT IN ('dxos_process', 'dxos_hasura') THEN
        RAISE EXCEPTION 'untrusted database connection for domain audit';
    END IF;

    IF session_user = 'dxos_process' THEN
        v_actor := nullif(trim( coalesce(current_setting('dxos.actor_id', true), '') ), '');
        v_action := nullif(trim( coalesce(current_setting('dxos.action', true), '') ), '');
    END IF;

    IF session_user = 'dxos_hasura' THEN
        v_hasura_user := nullif(trim( coalesce(current_setting('hasura.user', true), '') ), '');
        IF v_hasura_user IS NOT NULL THEN
            v_actor := nullif(trim( coalesce(v_hasura_user::jsonb ->> 'x-hasura-user-id', '') ), '');
            v_action := coalesce(v_action, 'UPDATE');
        END IF;
    END IF;

    IF v_actor IS NULL OR v_action IS NULL THEN
        RAISE EXCEPTION 'missing authenticated actor context for domain update';
    END IF;

    INSERT INTO core.audit_logs (
        workspace_id,
        actor_id,
        action,
        entity_type,
        entity_id,
        entity_version,
        payload
    ) VALUES (
        NEW.workspace_id,
        v_actor,
        v_action,
        TG_TABLE_SCHEMA || '.' || TG_TABLE_NAME,
        NEW.id,
        NEW.version,
        to_jsonb(NEW)
    );

    RETURN NEW;
END;
$$;

REVOKE ALL ON FUNCTION core.record_domain_update_audit() FROM PUBLIC;
