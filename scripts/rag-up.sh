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

# Start the base stack with the knowledge overlay, without manual token setup.
# Secrets live only in .env.rag and are never printed.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

export COMPOSE_PROJECT_NAME="${COMPOSE_PROJECT_NAME:-dxos-rag}"
export POSTGRES_PORT="${POSTGRES_PORT:-15432}"
ENV_FILE=".env.rag"

fail() {
    printf '[rag-up] FAIL: %s\n' "$*" >&2
    exit 1
}

random_secret() {
    python3 -c 'import secrets; print(secrets.token_urlsafe(48))'
}

# Keep an existing value, even an empty one; create missing values only.
ensure_env() {
    local key="$1" value="$2"
    grep -q "^${key}=" "$ENV_FILE" || printf '%s=%s\n' "$key" "$value" >>"$ENV_FILE"
}

set_env() {
    local key="$1" value="$2"
    if grep -q "^${key}=" "$ENV_FILE"; then
        if [ "$(grep -m1 "^${key}=" "$ENV_FILE" | cut -d= -f2-)" != "$value" ]; then
            sed -i "s|^${key}=.*|${key}=${value}|" "$ENV_FILE"
        fi
    else
        printf '%s=%s\n' "$key" "$value" >>"$ENV_FILE"
    fi
}

umask 077
touch "$ENV_FILE"
chmod 600 "$ENV_FILE"

ensure_env AI_DATABASE_PASSWORD "$(random_secret)"
ensure_env AI_WORKER_DATABASE_PASSWORD "$(random_secret)"
ensure_env AI_DISPATCHER_DATABASE_PASSWORD "$(random_secret)"
ensure_env AI_GATEWAY_DATABASE_PASSWORD "$(random_secret)"
ensure_env PARSER_SERVICE_TOKEN "$(random_secret)"
ensure_env RAG_WORKER_CLIENT_SECRET "$(random_secret)"
ensure_env RAG_FILE_CLIENT_SECRET "$(random_secret)"
ensure_env RAG_DISPATCHER_CLIENT_SECRET "$(random_secret)"
ensure_env RAG_WORKER_SERVICE_SUBJECT "33333333-3333-4333-8333-000000000001"
ensure_env RAG_FILE_SERVICE_SUBJECT "33333333-3333-4333-8333-000000000002"
ensure_env RAG_DISPATCHER_SERVICE_SUBJECT "33333333-3333-4333-8333-000000000003"
ensure_env RAG_WORKER_CLIENT_ID "dxos-rag-worker"
ensure_env RAG_FILE_CLIENT_ID "dxos-file-service"
ensure_env RAG_DISPATCHER_CLIENT_ID "dxos-rag-dispatcher"
ensure_env ENABLED_CAPABILITIES "cap-process,cap-hr,cap-work,cap-data,cap-ai"
ensure_env RAG_SOURCE_QUALIFIED true
ensure_env RAG_METADATA_QUALIFIED true
ensure_env RAG_PARSER_QUALIFIED true
ensure_env RAG_METADATA_ENABLED true
ensure_env RAG_PACKAGE_ENABLED true
ensure_env RAG_PERSONAL_ENABLED true
ensure_env RAG_PRIVATE_UPLOAD_ENABLED true
ensure_env RAG_MODELS_QUALIFIED false
ensure_env RAG_MODELS_ENABLED false
ensure_env RAG_GLOBAL_MODEL_REGISTRY_ENABLED false
ensure_env RAG_GLOBAL_MODEL_REGISTRY_SCHEMA_ENABLED false
ensure_env RAG_MODELS_SCHEMA_ENABLED false
ensure_env RAG_AGENT_MODELS_SCHEMA_ENABLED false
ensure_env RAG_AGENT_ENABLED false
ensure_env AI_AGENT_BILLING_APP_ID ""

# A caller's exported list takes precedence over every env file; use the list
# this installation was created with so cap-ai is not silently absent.
ENABLED_CAPABILITIES="$(grep -m1 '^ENABLED_CAPABILITIES=' "$ENV_FILE" | cut -d= -f2-)"
export ENABLED_CAPABILITIES

env_args=()
if [ -f .env ]; then
    env_args+=(--env-file .env)
fi
env_args+=(--env-file "$ENV_FILE")

# Older Compose releases accept only one --env-file. Merge in that case.
multiple_env_files=0
version="$(docker compose version --short 2>/dev/null | sed 's/^v//' || true)"
case "$version" in
    2.*)
        minor="${version#2.}"
        minor="${minor%%.*}"
        case "$minor" in
            ''|*[!0-9]*) ;;
            *) [ "$minor" -ge 24 ] && multiple_env_files=1 ;;
        esac
        ;;
    3.*|4.*|5.*|6.*|7.*|8.*|9.*)
        multiple_env_files=1
        ;;
esac
if [ "$multiple_env_files" -eq 0 ]; then
    merged="$(mktemp "${TMPDIR:-/tmp}/dxos-rag-env.XXXXXX")"
    chmod 600 "$merged"
    trap 'rm -f "$merged"' EXIT
    if [ -f .env ]; then cat .env >>"$merged"; fi
    cat "$ENV_FILE" >>"$merged"
    env_args=(--env-file "$merged")
fi

compose() {
    docker compose "${env_args[@]}" -f compose.yaml -f compose.rag.yaml "$@"
}

psql_value() {
    compose exec -T postgres sh -c 'psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -tAc "$1"' sh "$1" 2>/dev/null || true
}

printf '[rag-up] starting %s on port %s\n' "$COMPOSE_PROJECT_NAME" "$POSTGRES_PORT"
compose up -d --build

deadline=$((SECONDS + 300))
until [ "$(psql_value 'SELECT 1')" = "1" ]; do
    if [ "$SECONDS" -ge "$deadline" ]; then
        fail "PostgreSQL did not become ready"
    fi
    sleep 5
done

capability="$(psql_value "SELECT count(*) FROM core.capabilities WHERE cap_id='cap-ai' AND enabled")"
schema="$(psql_value "SELECT count(*) FROM pg_namespace WHERE nspname='cap_ai'")"
if [ "$capability" != "1" ] || [ "$schema" != "1" ]; then
    cat >&2 <<EOF
[rag-up] FAIL: the PostgreSQL volume was initialized without cap-ai.
Recreate this stack's data so the overlay's capability list is applied:
  docker compose -p $COMPOSE_PROJECT_NAME down -v
Then run make rag-up again. This script does not delete data.
EOF
    exit 1
fi

printf '[rag-up] waiting for personal_library\n'
deadline=$((SECONDS + 300))
while :; do
    status="$(psql_value "SELECT status FROM core.apps WHERE app_id='personal_library'")"
    [ "$status" = "Ready" ] && break
    if [ "$SECONDS" -ge "$deadline" ]; then
        compose logs operator 2>&1 | tail -20 >&2 || true
        fail "personal_library did not become Ready"
    fi
    sleep 5
done

set_env AI_AGENT_BILLING_APP_ID personal_library
set_env RAG_AGENT_ENABLED true
compose up -d

cat <<EOF
[rag-up] UI: http://localhost:5173
[rag-up] Assistant and personal library are on; models stay off.
EOF
