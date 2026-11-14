#!/usr/bin/env bash
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

# Writes a .env from .env.example with a fresh random value for every secret:
# database and runtime role passwords, the Hasura admin secret, Keycloak's
# admin password and Kestra's credentials. Refuses to overwrite an existing
# file unless --force is given.
#
#   scripts/init-env.sh [--force] [path]      (default path: .env)
#
# Passwords are applied when the PostgreSQL volume is first initialised; for an
# existing stack run `docker compose down -v` afterwards (this deletes its data).

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
force=0
if [ "${1:-}" = "--force" ]; then
    force=1
    shift
fi
target="${1:-$ROOT/.env}"

if [ -e "$target" ] && [ "$force" -ne 1 ]; then
    echo "[init-env] $target exists; keep it, or rerun with --force to replace it" >&2
    exit 1
fi

SECRETS=(
    POSTGRES_PASSWORD
    HASURA_ADMIN_SECRET
    KEYCLOAK_ADMIN_PASSWORD
    KESTRA_PASSWORD
    KESTRA_API_TOKEN
    KESTRA_DATABASE_PASSWORD
    HASURA_DATABASE_PASSWORD
    PROCESS_DATABASE_PASSWORD
    AUTHZ_DATABASE_PASSWORD
)

random_secret() {
    # 32 characters from [A-Za-z0-9]: safe in URLs, YAML and shell. A bounded
    # read avoids SIGPIPE under pipefail.
    local chars
    chars="$(head -c 1024 /dev/urandom | LC_ALL=C tr -dc 'A-Za-z0-9')"
    printf '%s' "${chars:0:32}"
}

tmp="$(mktemp "${target}.XXXXXX")"
trap 'rm -f "$tmp"' EXIT
cp "$ROOT/.env.example" "$tmp"
for key in "${SECRETS[@]}"; do
    if ! grep -q "^${key}=" "$tmp"; then
        echo "[init-env] .env.example has no ${key}" >&2
        exit 1
    fi
    value="$(random_secret)"
    sed -i "s|^${key}=.*|${key}=${value}|" "$tmp"
done
chmod 600 "$tmp"
mv "$tmp" "$target"
trap - EXIT
echo "[init-env] wrote $target with ${#SECRETS[@]} random secrets (mode 600)"
