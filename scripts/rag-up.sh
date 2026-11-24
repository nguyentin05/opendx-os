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
ensure_env AI_AGENT_PROCESSING_APP_ID ""

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

# Exported value first, then .env.rag, then .env, then the default.
env_value() {
    local key="$1" default="$2" value
    value="${!key:-}"
    for file in "$ENV_FILE" .env; do
        [ -n "$value" ] && break
        [ -f "$file" ] && value="$(grep -m1 "^${key}=" "$file" | cut -d= -f2- || true)"
    done
    printf '%s' "${value:-$default}"
}

# The realm ships the knowledge service clients disabled with a generated
# secret; enable them here with the secrets from .env.rag.
configure_service_clients() {
    KC_URL="http://localhost:$(env_value KEYCLOAK_PORT 8085)" \
    KC_ADMIN="$(env_value KEYCLOAK_ADMIN admin)" \
    KC_ADMIN_PASSWORD="$(env_value KEYCLOAK_ADMIN_PASSWORD admin)" \
    RAG_WORKER_CLIENT_SECRET="$(env_value RAG_WORKER_CLIENT_SECRET '')" \
    RAG_FILE_CLIENT_SECRET="$(env_value RAG_FILE_CLIENT_SECRET '')" \
    RAG_DISPATCHER_CLIENT_SECRET="$(env_value RAG_DISPATCHER_CLIENT_SECRET '')" \
    python3 - <<'PY'
import json, os, sys, time, urllib.error, urllib.parse, urllib.request

base = os.environ["KC_URL"]

def call(path, data=None, token=None, form=False, method=None):
    headers = {"Accept": "application/json"}
    if token:
        headers["Authorization"] = "Bearer " + token
    body = None
    if data is not None:
        body = urllib.parse.urlencode(data).encode() if form else json.dumps(data).encode()
        headers["Content-Type"] = "application/x-www-form-urlencoded" if form else "application/json"
    with urllib.request.urlopen(urllib.request.Request(base + path, body, headers, method=method), timeout=10) as r:
        raw = r.read()
        return json.loads(raw) if raw else None

deadline = time.time() + 300
while True:
    try:
        admin = call("/realms/master/protocol/openid-connect/token",
                     {"grant_type": "password", "client_id": "admin-cli",
                      "username": os.environ["KC_ADMIN"], "password": os.environ["KC_ADMIN_PASSWORD"]},
                     form=True)["access_token"]
        clients = {}
        for client_id in ("dxos-rag-worker", "dxos-file-service", "dxos-rag-dispatcher"):
            found = call("/admin/realms/dxos/clients?clientId=" + client_id, token=admin)
            if len(found) != 1:
                raise LookupError(client_id)
            clients[client_id] = found[0]
        break
    except (urllib.error.URLError, OSError, LookupError, KeyError):
        if time.time() > deadline:
            sys.exit("[rag-up] FAIL: Keycloak or its dxos service clients are unavailable")
        time.sleep(5)

for client_id, env in (("dxos-rag-worker", "RAG_WORKER_CLIENT_SECRET"),
                       ("dxos-file-service", "RAG_FILE_CLIENT_SECRET"),
                       ("dxos-rag-dispatcher", "RAG_DISPATCHER_CLIENT_SECRET")):
    secret = os.environ[env]
    if len(secret) < 32:
        sys.exit("[rag-up] FAIL: " + env + " in .env.rag is missing or too short")
    # A partial representation recreates the service account; send it whole.
    client = dict(clients[client_id], enabled=True, secret=secret)
    call("/admin/realms/dxos/clients/" + client["id"], client, token=admin, method="PUT")
print("[rag-up] knowledge service clients enabled")
PY
}

printf '[rag-up] starting the stack with the knowledge overlay\n'
compose up -d --build keycloak
configure_service_clients
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
  docker compose --env-file .env.rag -f compose.yaml -f compose.rag.yaml down -v
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

set_env AI_AGENT_PROCESSING_APP_ID personal_library
set_env RAG_AGENT_ENABLED true
compose up -d

cat <<EOF
[rag-up] UI: http://localhost:5173
[rag-up] Assistant and personal library are on; models stay off.
EOF
