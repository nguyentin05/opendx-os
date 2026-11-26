#!/usr/bin/env python3
# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors
"""Transactional, checksum-verified additive migrations. DSNs remain env-only."""
import argparse
import hashlib
import os
from pathlib import Path
import re
import subprocess
import sys
from urllib.parse import urlsplit, unquote, parse_qs

p = argparse.ArgumentParser()
p.add_argument('--database-url-env', required=True)
p.add_argument('--through', required=True, help='inclusive Core migration version')
a = p.parse_args()
if not re.fullmatch(r'[A-Za-z_][A-Za-z0-9_]*', a.database_url_env) or not os.environ.get(a.database_url_env):
    sys.exit('NOT_RUN: database URL environment variable required')
if not re.fullmatch(r'[0-9]{4}', a.through):
    sys.exit('invalid migration version (expected four digits)')
root = Path(__file__).resolve().parent.parent
folder = root / 'platform/postgres/migrations/core'
files = sorted(folder.glob('[0-9][0-9][0-9][0-9]_*.sql'))
versions = [f.name[:4] for f in files]
if len(set(versions)) != len(versions) or a.through not in versions:
    sys.exit('unknown or duplicate migration version')
selected = [f for f in files if f.name[:4] <= a.through]
sql = ["BEGIN; SELECT pg_advisory_xact_lock(782634901);", (root / "platform/postgres/migrations/ledger.sql").read_text()]
# Verify every recorded Core checksum before applying any migration.
for f in files:
    v = f.name[:4]
    checksum = hashlib.sha256(f.read_bytes()).hexdigest()
    sql.append(f"DO $$ BEGIN IF EXISTS (SELECT 1 FROM core.schema_migrations WHERE scope='core' AND version='{v}' AND checksum<>'{checksum}') THEN RAISE EXCEPTION 'migration checksum conflict: scope=core version={v}'; END IF; END $$;")
for f in selected:
    v = f.name[:4]
    checksum = hashlib.sha256(f.read_bytes()).hexdigest()
    sql += [f"SELECT NOT EXISTS (SELECT 1 FROM core.schema_migrations WHERE scope='core' AND version='{v}') AS apply_migration \\gset", '\\if :apply_migration', f.read_text(), f"INSERT INTO core.schema_migrations(scope,version,checksum) VALUES('core','{v}','{checksum}');", '\\endif']
sql.append('COMMIT;')
u = urlsplit(os.environ[a.database_url_env])
if u.scheme not in ('postgres', 'postgresql') or not u.hostname or not u.path.strip('/'):
    sys.exit('invalid PostgreSQL URL')
env = dict(os.environ, PGHOST=u.hostname, PGPORT=str(u.port or 5432),
           PGDATABASE=unquote(u.path[1:]), PGUSER=unquote(u.username or ''),
           PGPASSWORD=unquote(u.password or ''), PGCONNECT_TIMEOUT='5')
options = parse_qs(u.query)
if 'sslmode' in options:
    env['PGSSLMODE'] = options['sslmode'][0]
try:
    result = subprocess.run(['psql', '-X', '-q', '-v', 'ON_ERROR_STOP=1'], input='\n'.join(sql), text=True, capture_output=True, env=env)
except FileNotFoundError:
    sys.exit('NOT_RUN: psql required')
if result.returncode:
    # Connection errors can contain credentials: forward only our own conflict line.
    for line in result.stderr.splitlines():
        marker = line.find('migration checksum conflict')
        if marker >= 0:
            print(line[marker:].strip(), file=sys.stderr)
    sys.exit('FAIL: migration failed (connection, SQL or checksum conflict); transaction rolled back')
print('PASS: migrations applied or unchanged')
