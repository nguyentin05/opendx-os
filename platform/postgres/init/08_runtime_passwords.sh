#!/bin/sh
# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# Gives the runtime connection roles created in 06_runtime_roles.sql their login
# passwords, and creates the Kestra role and database. Passwords come from the
# environment (.env under Docker Compose, a Secret under Kubernetes). A role
# whose variable is unset keeps no password and cannot log in over TCP.
set -e

psql_admin() {
    psql -v ON_ERROR_STOP=1 --username "${POSTGRES_USER:-dxos_admin}" "$@"
}

set_password() {
    role="$1"
    password="$2"
    if [ -z "$password" ]; then
        echo "[dxos-init] No password for $role; it cannot log in over TCP"
        return 0
    fi
    psql_admin --dbname "${POSTGRES_DB:-dxos_db}" -v role="$role" -v pw="$password" <<'SQL'
ALTER ROLE :"role" PASSWORD :'pw';
SQL
    echo "[dxos-init] Password set for $role"
}

set_password dxos_hasura "${HASURA_DATABASE_PASSWORD:-}"
set_password dxos_process "${PROCESS_DATABASE_PASSWORD:-}"
set_password dxos_authz "${AUTHZ_DATABASE_PASSWORD:-}"

# Kestra keeps its repository and queue in a database of its own, owned by a
# role with no rights in the platform database.
if [ -n "${KESTRA_DATABASE_PASSWORD:-}" ]; then
    psql_admin --dbname postgres -v pw="$KESTRA_DATABASE_PASSWORD" <<'SQL'
SELECT 'CREATE ROLE dxos_kestra LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS'
WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'dxos_kestra')
\gexec
ALTER ROLE dxos_kestra PASSWORD :'pw';
SELECT 'CREATE DATABASE dxos_kestra OWNER dxos_kestra'
WHERE NOT EXISTS (SELECT 1 FROM pg_database WHERE datname = 'dxos_kestra')
\gexec
REVOKE CONNECT ON DATABASE dxos_kestra FROM PUBLIC;
SQL
    echo "[dxos-init] Kestra role and database ready"
else
    echo "[dxos-init] KESTRA_DATABASE_PASSWORD unset; Kestra database not created"
fi
