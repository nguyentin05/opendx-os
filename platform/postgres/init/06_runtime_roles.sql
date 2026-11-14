-- SPDX-License-Identifier: Apache-2.0
-- Copyright 2026 DX-OS Open-Core Contributors
-- Connection identities are server authority, never business role membership.
DO $$
DECLARE name text;
BEGIN
  FOREACH name IN ARRAY ARRAY['dxos_process','dxos_hasura','dxos_authz','dxos_reader','dxos_caller','dxos_module_test'] LOOP
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
GRANT INSERT, UPDATE ON core.outbox_events TO dxos_process;
GRANT INSERT ON core.files TO dxos_process;
GRANT UPDATE(scan_status,scan_attempt_count,scan_next_attempt_at,scanned_at) ON core.files TO dxos_process;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA core TO dxos_process;
-- dxos-authz (ADR 0007) reads the registry and role grants, and mirrors
-- Keycloak identities: it writes only the identity columns of core.users.
GRANT USAGE ON SCHEMA core TO dxos_authz;
GRANT SELECT ON core.roles, core.user_roles, core.apps, core.capabilities TO dxos_authz;
GRANT SELECT, INSERT ON core.users TO dxos_authz;
GRANT UPDATE (email, display_name) ON core.users TO dxos_authz;
-- Capability schemas exist only when the capability is enabled (04_capabilities.sh).
-- cap-process writes its own records and the work items its workflows create;
-- HR and device master data are managed by administrators.
DO $$
DECLARE
    s TEXT;
BEGIN
    FOREACH s IN ARRAY ARRAY['cap_hr', 'cap_work', 'cap_process', 'cap_data'] LOOP
        IF to_regnamespace(s) IS NOT NULL THEN
            EXECUTE format('GRANT USAGE ON SCHEMA %I TO dxos_process, dxos_reader', s);
            EXECUTE format('GRANT SELECT ON ALL TABLES IN SCHEMA %I TO dxos_process, dxos_reader', s);
        END IF;
    END LOOP;
    FOREACH s IN ARRAY ARRAY['cap_work', 'cap_process'] LOOP
        IF to_regnamespace(s) IS NOT NULL THEN
            EXECUTE format('GRANT INSERT, UPDATE ON ALL TABLES IN SCHEMA %I TO dxos_process', s);
        END IF;
    END LOOP;
END $$;
-- Hasura metadata is private to its connection, not exposed to business roles.
CREATE SCHEMA IF NOT EXISTS hdb_catalog AUTHORIZATION dxos_hasura;
CREATE SCHEMA IF NOT EXISTS hdb_views AUTHORIZATION dxos_hasura;
-- Task 4 adds reviewed domain/source grants, not blanket future-table grants.
-- The domain audit trigger function is defined once, in 01_core_schema.sql.
