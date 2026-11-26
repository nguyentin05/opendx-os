#!/bin/sh
# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors
set -eu

psql -X -v ON_ERROR_STOP=1 --username "${POSTGRES_USER:-dxos_admin}" \
  --dbname "${POSTGRES_DB:-dxos_db}" -v broker_pw="${AI_BROKER_DATABASE_PASSWORD:?}" <<'SQL'
BEGIN;
CREATE EXTENSION vector VERSION '0.8.7';
CREATE EXTENSION pgmq VERSION '1.13.2';
CREATE ROLE dxos_ai_owner NOLOGIN NOSUPERUSER NOBYPASSRLS;
GRANT USAGE ON SCHEMA core TO dxos_ai_owner;
GRANT SELECT (id, status), UPDATE (display_name) ON core.users TO dxos_ai_owner;
GRANT SELECT (user_id, role_name), UPDATE (granted_at) ON core.user_roles TO dxos_ai_owner;
GRANT SELECT (name, app_id, cap_id) ON core.roles TO dxos_ai_owner;
GRANT SELECT (app_id, status, spec, publication_generation) ON core.apps TO dxos_ai_owner;
GRANT SELECT (cap_id, enabled) ON core.capabilities TO dxos_ai_owner;
GRANT SELECT (app_id, cap_id) ON core.app_capabilities TO dxos_ai_owner;
GRANT SELECT (id, app_id, file_name, sha256_hash, size_bytes, mime_type, scan_status,
    uploaded_by, read_policy) ON core.files TO dxos_ai_owner;
GRANT REFERENCES (id) ON core.users, core.files TO dxos_ai_owner;
GRANT REFERENCES (app_id) ON core.apps TO dxos_ai_owner;
CREATE ROLE dxos_ai_broker LOGIN NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE NOINHERIT;
ALTER ROLE dxos_ai_broker PASSWORD :'broker_pw';
ALTER ROLE dxos_ai_broker SET statement_timeout = '5s';
SELECT pgmq.create('dxos_ai_ingest');
REVOKE ALL ON SCHEMA pgmq FROM PUBLIC;
GRANT USAGE ON SCHEMA pgmq TO dxos_ai_broker;
GRANT SELECT ON pgmq.meta TO dxos_ai_broker;
GRANT SELECT, INSERT, UPDATE, DELETE ON pgmq.q_dxos_ai_ingest TO dxos_ai_broker;
GRANT USAGE ON SEQUENCE pgmq.q_dxos_ai_ingest_msg_id_seq TO dxos_ai_broker;
COMMIT;
SQL
