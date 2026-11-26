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
