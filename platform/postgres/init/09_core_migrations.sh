#!/bin/sh
# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors
set -eu

migration_dir="$(dirname "$0")/../migrations"
if [ -d /docker-entrypoint-initdb.d/migrations ]; then
    migration_dir=/docker-entrypoint-initdb.d/migrations
fi
umask 077
migration_script=$(mktemp)
trap 'rm -f "$migration_script"' EXIT HUP INT TERM
cat > "$migration_script" <<'SQL'
BEGIN;
SELECT pg_advisory_xact_lock(782634901);
\i :migration_dir/ledger.sql
SQL

for file in "$migration_dir"/core/[0-9][0-9][0-9][0-9]_*.sql; do
    [ -f "$file" ] || continue
    filename=$(basename "$file")
    version=${filename%%_*}
    checksum=$(sha256sum "$file" | cut -d ' ' -f1)
    cat >> "$migration_script" <<SQL
SELECT EXISTS(SELECT FROM core.schema_migrations WHERE scope='core' AND version='$version' AND checksum<>'$checksum') AS conflict \\gset
\\if :conflict
DO \$\$ BEGIN RAISE EXCEPTION 'Core migration checksum conflict at $version'; END \$\$;
\\endif
SQL
done

for file in "$migration_dir"/core/[0-9][0-9][0-9][0-9]_*.sql; do
    [ -f "$file" ] || continue
    filename=$(basename "$file")
    version=${filename%%_*}
    checksum=$(sha256sum "$file" | cut -d ' ' -f1)
    cat >> "$migration_script" <<SQL
SELECT NOT EXISTS(SELECT FROM core.schema_migrations WHERE scope='core' AND version='$version') AS apply_migration \\gset
\\if :apply_migration
\\i :migration_dir/core/$filename
INSERT INTO core.schema_migrations(scope,version,checksum) VALUES('core','$version','$checksum');
\\endif
SQL
done
printf 'COMMIT;\n' >> "$migration_script"
psql -X -q -v ON_ERROR_STOP=1 --username "${POSTGRES_USER:-dxos_admin}" \
    --dbname "${POSTGRES_DB:-dxos_db}" -v migration_dir="$migration_dir" -f "$migration_script"
