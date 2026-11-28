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
# Generated secrets live in AI_ENV_FILE (default .env) and are never printed.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

ENV_FILE="${AI_ENV_FILE:-.env}"

fail() {
    printf '[ai-up] FAIL: %s\n' "$*" >&2
    exit 1
}

random_secret() {
    python3 -c 'import secrets; print(secrets.token_urlsafe(48))'
}

# Persist a generated value only when the operator has not already provided it.
# A non-empty exported value or a non-empty .env value wins; an empty
# placeholder in the overlay file is filled instead of duplicated.
ensure_env() {
    local key="$1" value="$2"
    if [ -n "${!key:-}" ]; then
        return 0
    fi
    if [ "$ENV_FILE" != ".env" ] && [ -f .env ] && grep -q "^${key}=." .env; then
        if grep -q "^${key}=" "$ENV_FILE" 2>/dev/null; then
            grep -v "^${key}=" "$ENV_FILE" > "${ENV_FILE}.tmp" || true
            mv "${ENV_FILE}.tmp" "$ENV_FILE"
            chmod 600 "$ENV_FILE"
            printf '[ai-up] using %s from .env\n' "$key"
        fi
        return 0
    fi
    if grep -q "^${key}=." "$ENV_FILE" 2>/dev/null; then
        return 0
    fi
    if grep -q "^${key}=" "$ENV_FILE" 2>/dev/null; then
        ENV_FILE="$ENV_FILE" KEY="$key" VALUE="$value" python3 - <<'PY'
import os
path, key, value = os.environ["ENV_FILE"], os.environ["KEY"], os.environ["VALUE"]
lines = open(path).read().splitlines()
with open(path, "w") as file:
    for line in lines:
        file.write(f"{key}={value}" if line.startswith(key + "=") else line)
        file.write("\n")
PY
        chmod 600 "$ENV_FILE"
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
ensure_env AI_BROKER_DATABASE_PASSWORD "$(random_secret)"
ensure_env AI_MODEL_MASTER_KEY "$(python3 -c 'import secrets; print(secrets.token_urlsafe(32) + "=")')"
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
ensure_env RAG_PRIVATE_UPLOAD_ENABLED true

env_args=()
if [ -f .env ] && [ "$ENV_FILE" != ".env" ]; then
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
    if [ -f .env ] && [ "$ENV_FILE" != ".env" ]; then cat .env >>"$merged"; fi
    cat "$ENV_FILE" >>"$merged"
    env_args=(--env-file "$merged")
fi

compose() {
    docker compose -p "${COMPOSE_PROJECT_NAME:-opendx-os-ai}" "${env_args[@]}" -f compose.yaml -f compose.rag.yaml "$@"
}

psql_value() {
    compose exec -T postgres sh -c 'psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -tAc "$1"' sh "$1" 2>/dev/null || true
}

# Exported value first, then .env, then the overlay env file, then the default.
env_value() {
    local key="$1" default="$2" value
    value="${!key:-}"
    for file in .env "$ENV_FILE"; do
        [ -n "$value" ] && break
        [ -f "$file" ] && value="$(grep -m1 "^${key}=." "$file" | cut -d= -f2- || true)"
    done
    printf '%s' "${value:-$default}"
}

# Use the list this installation was created with so cap-ai is not silently
# absent when the caller's shell exports a different set. .env wins over the
# generated overlay file.
ENABLED_CAPABILITIES="$(
    value="$(grep -m1 '^ENABLED_CAPABILITIES=.' .env 2>/dev/null | cut -d= -f2- || true)"
    [ -n "$value" ] || value="$(grep -m1 '^ENABLED_CAPABILITIES=.' "$ENV_FILE" 2>/dev/null | cut -d= -f2- || true)"
    printf '%s' "${value:-cap-process,cap-hr,cap-work,cap-data,cap-ai}"
)"
export ENABLED_CAPABILITIES

# The realm ships the knowledge service clients disabled with a generated
# secret; enable them here with the secrets from the overlay env file.
configure_service_clients() {
    AI_ENV_FILE="$ENV_FILE" \
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
            sys.exit("[ai-up] FAIL: Keycloak or its dxos service clients are unavailable")
        time.sleep(5)

for client_id, env in (("dxos-rag-worker", "RAG_WORKER_CLIENT_SECRET"),
                       ("dxos-file-service", "RAG_FILE_CLIENT_SECRET"),
                       ("dxos-rag-dispatcher", "RAG_DISPATCHER_CLIENT_SECRET")):
    secret = os.environ[env]
    if len(secret) < 32:
        sys.exit("[ai-up] FAIL: " + env + " in " + os.environ.get("AI_ENV_FILE", "the env file") + " is missing or too short")
    # A partial representation recreates the service account; send it whole.
    client = dict(clients[client_id], enabled=True, secret=secret)
    call("/admin/realms/dxos/clients/" + client["id"], client, token=admin, method="PUT")
print("[ai-up] knowledge service clients enabled")
PY
}

printf '[ai-up] starting the stack with the knowledge overlay\n'
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
    printf '[ai-up] Choose a new Compose project and fresh database for the Python AI deployment.\n' >&2
    exit 1
fi

printf '[ai-up] waiting for personal_library\n'
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

cat <<EOF
[ai-up] UI: http://localhost:5173
[ai-up] Personal Library is available. Configure the Assistant's LLM from Workspace AI Management.
EOF
