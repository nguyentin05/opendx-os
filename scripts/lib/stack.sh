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

# Shared Compose bootstrap for the knowledge deployment scripts. It is sourced by
# `rag-up.sh` (FTS-only stack) and `ai-up.sh` (coordinated model deployment), so
# both use the same project, environment files, Keycloak client setup and
# readiness checks. It never prints a secret value.
#
# The caller sets STACK_COMPOSE_FILES (in precedence order) and the env files that
# carry this installation's secrets, then calls stack_compose_init.

STACK_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
STACK_ENV_ARGS=()
STACK_COMPOSE_ARGS=()
STACK_COMPOSE_FILES=()
# STACK_VALUE_FILES is the lookup order for stack_env_value: the installation's
# own file first, then the shared .env.
STACK_VALUE_FILES=()
STACK_CLEANUP_FILES=()

stack_cleanup() {
    local file
    for file in "${STACK_CLEANUP_FILES[@]}"; do
        [ -n "$file" ] && rm -f "$file"
    done
    return 0
}

stack_fail() {
    printf '[%s] FAIL: %s\n' "${STACK_LABEL:-stack}" "$*" >&2
    exit 1
}

stack_say() {
    printf '[%s] %s\n' "${STACK_LABEL:-stack}" "$*"
}

stack_random_secret() {
    python3 -c 'import secrets; print(secrets.token_urlsafe(48))'
}

# Keep an existing value, even an empty one; create missing values only.
stack_env_ensure() {
    local file="$1" key="$2" value="${3:-}"
    grep -q "^${key}=" "$file" || printf '%s=%s\n' "$key" "$value" >>"$file"
}

stack_env_set() {
    local file="$1" key="$2" value="$3"
    if grep -q "^${key}=" "$file"; then
        if [ "$(grep -m1 "^${key}=" "$file" | cut -d= -f2-)" != "$value" ]; then
            sed -i "s|^${key}=.*|${key}=${value}|" "$file"
        fi
    else
        printf '%s=%s\n' "$key" "$value" >>"$file"
    fi
}

# stack_env_file_path FILE creates an owner-only environment file.
stack_env_file_path() {
    umask 077
    touch "$1"
    chmod 600 "$1"
}

# stack_env_value KEY DEFAULT resolves a setting without printing it. An exported
# value wins, then STACK_VALUE_FILES in order, then the default.
stack_env_value() {
    local key="$1" default="${2:-}" value file
    value="${!key:-}"
    for file in "${STACK_VALUE_FILES[@]}"; do
        [ -n "$value" ] && break
        [ -f "$file" ] && value="$(grep -m1 "^${key}=" "$file" | cut -d= -f2- || true)"
    done
    printf '%s' "${value:-$default}"
}

# stack_compose_init FILE... builds the `docker compose` argument list once.
# Compose 2.24+ and 3+ accept several --env-file flags in precedence order; older
# releases accept one, so the same files are concatenated privately.
stack_compose_init() {
    local files=("$@") file merged version minor multiple_env_files=0
    STACK_ENV_ARGS=()
    for file in "${files[@]}"; do
        [ -f "$file" ] || continue
        STACK_ENV_ARGS+=(--env-file "$file")
    done
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
    if [ "$multiple_env_files" -eq 0 ] && [ "${#STACK_ENV_ARGS[@]}" -gt 1 ]; then
        merged="$(mktemp "${TMPDIR:-/tmp}/dxos-stack-env.XXXXXX")"
        chmod 600 "$merged"
        STACK_CLEANUP_FILES+=("$merged")
        cat "${files[@]}" >"$merged" 2>/dev/null || true
        STACK_ENV_ARGS=(--env-file "$merged")
    fi
    STACK_COMPOSE_ARGS=()
    for file in "${STACK_COMPOSE_FILES[@]}"; do
        STACK_COMPOSE_ARGS+=(-f "$file")
    done
}

stack_compose() {
    docker compose "${STACK_ENV_ARGS[@]}" "${STACK_COMPOSE_ARGS[@]}" "$@"
}

stack_psql_value() {
    stack_compose exec -T postgres sh -c 'psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -tAc "$1"' sh "$1" 2>/dev/null || true
}

# stack_set_role_password ROLE PASSWORD gives one restricted login its password.
# It runs as the bootstrap (migration) authority inside the database container;
# the password reaches psql as a variable, never as SQL text on the command line.
stack_set_role_password() {
    local role="$1" password="$2"
    if [ -z "$password" ]; then
        stack_fail "a password is required for the $role login"
    fi
    stack_compose exec -T postgres \
        sh -c 'psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB" -X -q -v role="$1" -v pw="$2"' \
        sh "$role" "$password" <<'SQL'
ALTER ROLE :"role" PASSWORD :'pw';
SQL
}

stack_role_exists() {
    [ "$(stack_psql_value "SELECT count(*) FROM pg_roles WHERE rolname='$1'")" = "1" ]
}

# stack_ensure_knowledge_env creates the knowledge installation's shared settings
# once, keeping every value an installation already holds. Model switches are the
# caller's decision, because the FTS-only and model deployments differ there.
stack_ensure_knowledge_env() {
    local file="$1"
    stack_env_ensure "$file" AI_DATABASE_PASSWORD "$(stack_random_secret)"
    stack_env_ensure "$file" AI_WORKER_DATABASE_PASSWORD "$(stack_random_secret)"
    stack_env_ensure "$file" AI_DISPATCHER_DATABASE_PASSWORD "$(stack_random_secret)"
    stack_env_ensure "$file" AI_GATEWAY_DATABASE_PASSWORD "$(stack_random_secret)"
    stack_env_ensure "$file" AI_MODEL_CATALOG_DATABASE_PASSWORD "$(stack_random_secret)"
    stack_env_ensure "$file" PARSER_SERVICE_TOKEN "$(stack_random_secret)"
    stack_env_ensure "$file" RAG_WORKER_CLIENT_SECRET "$(stack_random_secret)"
    stack_env_ensure "$file" RAG_FILE_CLIENT_SECRET "$(stack_random_secret)"
    stack_env_ensure "$file" RAG_DISPATCHER_CLIENT_SECRET "$(stack_random_secret)"
    stack_env_ensure "$file" RAG_WORKER_SERVICE_SUBJECT "33333333-3333-4333-8333-000000000001"
    stack_env_ensure "$file" RAG_FILE_SERVICE_SUBJECT "33333333-3333-4333-8333-000000000002"
    stack_env_ensure "$file" RAG_DISPATCHER_SERVICE_SUBJECT "33333333-3333-4333-8333-000000000003"
    stack_env_ensure "$file" RAG_WORKER_CLIENT_ID "dxos-rag-worker"
    stack_env_ensure "$file" RAG_FILE_CLIENT_ID "dxos-file-service"
    stack_env_ensure "$file" RAG_DISPATCHER_CLIENT_ID "dxos-rag-dispatcher"
    stack_env_ensure "$file" ENABLED_CAPABILITIES "cap-process,cap-hr,cap-work,cap-data,cap-ai"
    stack_env_ensure "$file" RAG_SOURCE_QUALIFIED true
    stack_env_ensure "$file" RAG_METADATA_QUALIFIED true
    stack_env_ensure "$file" RAG_PARSER_QUALIFIED true
    stack_env_ensure "$file" RAG_METADATA_ENABLED true
    stack_env_ensure "$file" RAG_PACKAGE_ENABLED true
    stack_env_ensure "$file" RAG_PERSONAL_ENABLED true
    stack_env_ensure "$file" RAG_PRIVATE_UPLOAD_ENABLED true
}

# stack_sql SQL runs one statement as the bootstrap login inside the database
# container. Callers pass local SQL; no secret is interpolated into argv.
stack_sql() {
    stack_compose exec -T postgres sh -c 'psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB" -X -q' <<<"$1"
}

# stack_wait_healthy SERVICE LIMIT waits for the Compose healthcheck. PostgreSQL
# health is a TCP probe precisely so it passes only after the init scripts have
# finished; a psql-over-socket check would pass during initialization.
stack_wait_healthy() {
    local service="$1" limit="${2:-240}" container status
    local deadline=$((SECONDS + limit))
    while :; do
        container="$(stack_compose ps -q "$service" 2>/dev/null || true)"
        if [ -n "$container" ]; then
            status="$(docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' "$container" 2>/dev/null || true)"
            case "$status" in
                healthy|running) return 0 ;;
            esac
        fi
        if [ "$SECONDS" -ge "$deadline" ]; then
            stack_compose logs --tail 20 "$service" >&2 || true
            stack_fail "$service did not become ready"
        fi
        sleep 3
    done
}

stack_wait_postgres() {
    local limit="${1:-300}"
    local deadline=$((SECONDS + limit))
    until [ "$(stack_psql_value 'SELECT 1')" = "1" ]; do
        if [ "$SECONDS" -ge "$deadline" ]; then
            stack_fail "PostgreSQL did not become ready"
        fi
        sleep 5
    done
}

# stack_require_cap_ai refuses a data volume that was initialized without cap-ai.
# It never deletes data or resets migration checksums.
stack_require_cap_ai() {
    local capability schema
    capability="$(stack_psql_value "SELECT count(*) FROM core.capabilities WHERE cap_id='cap-ai' AND enabled")"
    schema="$(stack_psql_value "SELECT count(*) FROM pg_namespace WHERE nspname='cap_ai'")"
    if [ "$capability" != "1" ] || [ "$schema" != "1" ]; then
        cat >&2 <<EOF
[${STACK_LABEL:-stack}] FAIL: the PostgreSQL volume was initialized without cap-ai.
Recreate this stack's data so the overlay's capability list is applied:
  docker compose ${STACK_ENV_ARGS[*]} ${STACK_COMPOSE_ARGS[*]} down -v
Then run the deployment again. This script does not delete data.
EOF
        exit 1
    fi
}

# The realm ships the knowledge service clients disabled with a generated secret;
# enable them here with the secrets from the installation's environment file.
stack_configure_service_clients() {
    KC_URL="http://localhost:$(stack_env_value KEYCLOAK_PORT 8085)" \
    KC_ADMIN="$(stack_env_value KEYCLOAK_ADMIN admin)" \
    KC_ADMIN_PASSWORD="$(stack_env_value KEYCLOAK_ADMIN_PASSWORD admin)" \
    RAG_WORKER_CLIENT_SECRET="$(stack_env_value RAG_WORKER_CLIENT_SECRET '')" \
    RAG_FILE_CLIENT_SECRET="$(stack_env_value RAG_FILE_CLIENT_SECRET '')" \
    RAG_DISPATCHER_CLIENT_SECRET="$(stack_env_value RAG_DISPATCHER_CLIENT_SECRET '')" \
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
            sys.exit("[%s] FAIL: Keycloak or its dxos service clients are unavailable" % os.environ.get("STACK_LABEL", "stack"))
        time.sleep(5)

for client_id, env in (("dxos-rag-worker", "RAG_WORKER_CLIENT_SECRET"),
                       ("dxos-file-service", "RAG_FILE_CLIENT_SECRET"),
                       ("dxos-rag-dispatcher", "RAG_DISPATCHER_CLIENT_SECRET")):
    secret = os.environ[env]
    if len(secret) < 32:
        sys.exit("[%s] FAIL: %s in the installation environment file is missing or too short"
                 % (os.environ.get("STACK_LABEL", "stack"), env))
    # A partial representation recreates the service account; send it whole.
    client = dict(clients[client_id], enabled=True, secret=secret)
    call("/admin/realms/dxos/clients/" + client["id"], client, token=admin, method="PUT")
print("[%s] knowledge service clients enabled" % os.environ.get("STACK_LABEL", "stack"))
PY
}

stack_wait_app() {
    local app="$1" limit="${2:-300}" status deadline=$((SECONDS + ${2:-300}))
    stack_say "waiting for $app"
    while :; do
        status="$(stack_psql_value "SELECT status FROM core.apps WHERE app_id='$app'")"
        [ "$status" = "Ready" ] && break
        if [ "$SECONDS" -ge "$deadline" ]; then
            stack_compose logs operator 2>&1 | tail -20 >&2 || true
            stack_fail "$app did not become Ready"
        fi
        sleep 5
    done
}
