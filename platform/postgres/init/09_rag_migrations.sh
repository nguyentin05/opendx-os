#!/bin/sh
# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors
set -eu
migration_dir="$(dirname "$0")/../migrations"
if [ -d /docker-entrypoint-initdb.d/migrations ]; then migration_dir=/docker-entrypoint-initdb.d/migrations; fi
umask 077
migration_script=$(mktemp)
trap 'rm -f "$migration_script"' EXIT HUP INT TERM
cat > "$migration_script" <<'SQL'
BEGIN;
SELECT pg_advisory_xact_lock(782634901);
CREATE TABLE IF NOT EXISTS core.schema_migrations(scope text NOT NULL,version text NOT NULL,checksum text NOT NULL,applied_at timestamptz NOT NULL DEFAULT now(),PRIMARY KEY(scope,version));
SELECT EXISTS(SELECT FROM core.capabilities WHERE cap_id='cap-ai' AND enabled) AS ai_enabled \gset
SQL
# Verify every checksum before any domain mutation. No connection secrets enter argv/output.
for phase in check apply; do
 for folder in core cap_ai; do
  scope=$(echo "$folder" | tr _ -)
  if [ "$folder" = cap_ai ]; then printf '\\if :ai_enabled\n' >> "$migration_script"; fi
  for file in "$migration_dir/$folder"/[0-9][0-9][0-9][0-9]_*.sql; do
   [ -f "$file" ] || continue
   filename=$(basename "$file"); version=$(printf '%s' "$filename" | cut -c1-4)
   checksum=$(sha256sum "$file" | cut -d ' ' -f1)
   if [ "$phase" = check ]; then
    cat >> "$migration_script" <<SQL
SELECT EXISTS(SELECT FROM core.schema_migrations WHERE scope='$scope' AND version='$version' AND checksum<>'$checksum') AS conflict \\gset
\\if :conflict
\\quit 3
\\endif
SQL
   else
    cat >> "$migration_script" <<SQL
SELECT NOT EXISTS(SELECT FROM core.schema_migrations WHERE scope='$scope' AND version='$version') AS apply_migration \\gset
\\if :apply_migration
\\i :migration_dir/$folder/$filename
INSERT INTO core.schema_migrations(scope,version,checksum) VALUES('$scope','$version','$checksum');
\\endif
SQL
   fi
  done
  if [ "$folder" = cap_ai ]; then printf '\\endif\n' >> "$migration_script"; fi
 done
done
printf 'COMMIT;\n' >> "$migration_script"
psql -X -q -v ON_ERROR_STOP=1 --username "${POSTGRES_USER:-dxos_admin}" --dbname "${POSTGRES_DB:-dxos_db}" -v migration_dir="$migration_dir" -f "$migration_script"
