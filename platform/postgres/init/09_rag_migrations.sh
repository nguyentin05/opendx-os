#!/bin/sh
# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors
set -eu
migration_dir="$(dirname "$0")/../migrations"
if [ -d /docker-entrypoint-initdb.d/migrations ]; then migration_dir=/docker-entrypoint-initdb.d/migrations; fi
# Runs after 07_app_definitions.sql (app_capabilities is a source policy dependency).
core_hash=$(sha256sum "$migration_dir/core/0001_private_file_policy.sql" | cut -d ' ' -f1)
ai_hash=$(sha256sum "$migration_dir/cap_ai/0001_metadata_jobs.sql" | cut -d ' ' -f1)
psql -X -v ON_ERROR_STOP=1 --username "${POSTGRES_USER:-dxos_admin}" --dbname "${POSTGRES_DB:-dxos_db}" -v core_hash="$core_hash" -v ai_hash="$ai_hash" -v migration_dir="$migration_dir" <<'SQL'
BEGIN;
SELECT pg_advisory_xact_lock(782634901);
CREATE TABLE IF NOT EXISTS core.schema_migrations(scope text NOT NULL,version text NOT NULL,checksum text NOT NULL,applied_at timestamptz NOT NULL DEFAULT now(),PRIMARY KEY(scope,version));
SELECT EXISTS(SELECT FROM core.schema_migrations WHERE (scope='core' AND version='0001' AND checksum<>:'core_hash') OR(scope='cap-ai' AND version='0001' AND checksum<>:'ai_hash')) AS conflict \gset
\if :conflict
\quit 3
\endif
SELECT NOT EXISTS(SELECT FROM core.schema_migrations WHERE scope='core' AND version='0001') AS core_new \gset
\if :core_new
\i :migration_dir/core/0001_private_file_policy.sql
INSERT INTO core.schema_migrations(scope,version,checksum) VALUES('core','0001',:'core_hash');
\endif
SELECT EXISTS(SELECT FROM core.capabilities WHERE cap_id='cap-ai' AND enabled) AND NOT EXISTS(SELECT FROM core.schema_migrations WHERE scope='cap-ai' AND version='0001') AS ai_new \gset
\if :ai_new
\i :migration_dir/cap_ai/0001_metadata_jobs.sql
INSERT INTO core.schema_migrations(scope,version,checksum) VALUES('cap-ai','0001',:'ai_hash');
\endif
COMMIT;
SQL
