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

# Checks a running `docker compose up` stack: every service is up (and healthy
# where it has a healthcheck), the runtime roles log in with their .env
# passwords, Kestra uses its own database, the operator has installed the
# application packages, and each HTTP entry point answers.
#
#   docker compose up -d --build
#   scripts/test-compose.sh            # waits up to WAIT_SECONDS (default 300)
#
# Honors COMPOSE_PROJECT_NAME and the *_PORT variables, like compose itself.

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
if [ -f .env ]; then
    set -a
    # shellcheck disable=SC1091
    . ./.env
    set +a
fi

WAIT_SECONDS="${WAIT_SECONDS:-300}"
SERVICES=(postgres hasura keycloak seaweedfs clamav kestra operator cap-process apisix workspace-ui)
failures=0

pass() { echo "  PASS  $*"; }
fail() { echo "  FAIL  $*"; failures=$((failures + 1)); }

service_state() {
    # "<state> <health>"; health is empty when the service has no healthcheck.
    docker compose ps -a --format '{{.State}} {{.Health}}' "$1" 2>/dev/null | head -n1
}

echo "[test-compose] Waiting up to ${WAIT_SECONDS}s for services"
deadline=$((SECONDS + WAIT_SECONDS))
while :; do
    pending=()
    for svc in "${SERVICES[@]}"; do
        read -r state health <<<"$(service_state "$svc") "
        if [ "$state" != "running" ] || { [ -n "$health" ] && [ "$health" != "healthy" ]; }; then
            pending+=("$svc")
        fi
    done
    [ ${#pending[@]} -eq 0 ] && break
    if [ $SECONDS -ge $deadline ]; then
        break
    fi
    sleep 5
done

echo "[test-compose] Services"
for svc in "${SERVICES[@]}"; do
    read -r state health <<<"$(service_state "$svc") "
    if [ "$state" = "running" ] && { [ -z "$health" ] || [ "$health" = "healthy" ]; }; then
        pass "$svc ${health:-running}"
    else
        fail "$svc state=${state:-missing} health=${health:-none}"
    fi
done

echo "[test-compose] Database"
psql_as() {
    # Log in from a separate container over the compose network, with the
    # password the postgres service was given. (Inside the postgres container
    # loopback connections are trusted, which would prove nothing.)
    local role="$1" password_var="$2" db="$3" sql="$4"
    local out
    if out="$(docker compose run --rm --no-deps -T --entrypoint sh postgres -c \
        "PGPASSWORD=\"\$$password_var\" psql -h postgres -U $role -d $db -tAc \"$sql\" 2>&1" 2>/dev/null)"; then
        echo "$out"
    else
        echo "$out" | tail -n1
        return 1
    fi
}
for pair in dxos_hasura:HASURA_DATABASE_PASSWORD dxos_process:PROCESS_DATABASE_PASSWORD; do
    role="${pair%%:*}"
    out="$(psql_as "$role" "${pair#*:}" '$POSTGRES_DB' 'SELECT current_user')" || true
    if [ "$out" = "$role" ]; then pass "$role logs in"; else fail "$role cannot log in: $out"; fi
done
out="$(psql_as dxos_kestra KESTRA_DATABASE_PASSWORD dxos_kestra \
    "SELECT count(*) FROM information_schema.tables WHERE table_schema = 'public'")" || true
if [[ "$out" =~ ^[0-9]+$ ]] && [ "$out" -gt 0 ]; then
    pass "Kestra created ${out} tables in dxos_kestra"
else
    fail "Kestra database has no tables: $out"
fi
out="$(docker compose exec -T postgres sh -c 'psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -tA' \
    <<<"SELECT string_agg(cap_id, ',' ORDER BY cap_id) FROM core.capabilities WHERE enabled" 2>&1)" || true
if [ -n "$out" ]; then pass "enabled capabilities: $out"; else fail "no capability registered"; fi

echo "[test-compose] Applications"
apps_dir="${OPERATOR_APPS_DIR:-./apps}"
want="$(find "$apps_dir" -mindepth 2 -maxdepth 2 -name manifest.yaml | wc -l)"
app_status() {
    docker compose exec -T postgres sh -c 'psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -tA' \
        <<<"SELECT app_id || ' ' || status FROM core.apps WHERE status <> 'Removed' ORDER BY 1" 2>&1
}
# The operator's first pass may still be running.
while :; do
    ready="$(app_status | grep -c ' Ready$' || true)"
    [ "$ready" -ge "$want" ] || [ $SECONDS -ge $deadline ] && break
    sleep 5
done
while read -r app status; do
    [ -z "$app" ] && continue
    if [ "$status" = "Ready" ]; then pass "$app Ready"; else fail "$app $status"; fi
done <<<"$(app_status)"
if [ "$ready" -lt "$want" ]; then
    fail "${ready} of ${want} packages in ${apps_dir} installed (see: docker compose logs operator)"
fi

echo "[test-compose] HTTP entry points"
probe() {
    local name="$1" url="$2" want="$3" code
    code="$(curl -s -o /dev/null -w '%{http_code}' -m 10 "$url" || true)"
    if [ "$code" = "$want" ]; then pass "$name $url -> $code"; else fail "$name $url -> $code (want $want)"; fi
}
probe hasura "http://localhost:${HASURA_PORT:-8080}/healthz" 200
probe cap-process "http://localhost:${CAP_PROCESS_PORT:-8091}/healthz" 200
probe keycloak "http://localhost:${KEYCLOAK_PORT:-8085}/realms/dxos" 200
probe gateway "http://localhost:${APISIX_GATEWAY_PORT:-9080}/realms/dxos" 200
probe workspace-ui "http://localhost:${WORKSPACE_UI_PORT:-5173}/" 200
# Kestra's API sits behind basic auth: 401 without credentials means it is serving.
probe kestra "http://localhost:${KESTRA_PORT:-8082}/api/v1/configs" 401

if [ "$failures" -gt 0 ]; then
    echo "[test-compose] FAIL: ${failures} check(s) failed"
    exit 1
fi
echo "[test-compose] PASS"
