#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors
#
# Runs scripts/seed-demo.py against a Kubernetes deployment (deploy/k8s):
#
#   scripts/k8s/seed-demo.sh <env file> seed|scopes|flow|admin|org|lines
#
# The public hosts do not serve Keycloak's admin API, so Keycloak and the
# gateway are reached through port-forwards, and PostgreSQL through kubectl
# exec. The env file is the one given to deploy/k8s/apply.sh; only
# KEYCLOAK_ADMIN_PASSWORD (and DEMO_PASSWORD, if set) are read from it.
# Demo data only: never run it against a deployment holding real data.
set -euo pipefail

[ $# -ge 2 ] || { echo "usage: $0 <env file> seed|scopes|flow|admin|org|lines" >&2; exit 2; }
env_file="$1"
shift
[ -f "$env_file" ] || { echo "[seed-demo] $env_file not found" >&2; exit 1; }
KUBECTL="${KUBECTL:-kubectl}"
NS="${DXOS_NAMESPACE:-dxos}"
KC_PORT="${KEYCLOAK_LOCAL_PORT:-18085}"
GW_PORT="${GATEWAY_LOCAL_PORT:-19080}"
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"

# The file is data, never executed.
read_key() { sed -n "s/^$1=//p" "$env_file" | tail -n1; }
KEYCLOAK_ADMIN_PASSWORD="$(read_key KEYCLOAK_ADMIN_PASSWORD)"
[ -n "$KEYCLOAK_ADMIN_PASSWORD" ] || { echo "[seed-demo] KEYCLOAK_ADMIN_PASSWORD missing in $env_file" >&2; exit 1; }
export KEYCLOAK_ADMIN_PASSWORD
demo_password="$(read_key DEMO_PASSWORD)"
[ -z "$demo_password" ] || export DEMO_PASSWORD="$demo_password"

"$KUBECTL" -n "$NS" port-forward svc/keycloak "$KC_PORT:8085" >/dev/null 2>&1 &
keycloak_forward=$!
"$KUBECTL" -n "$NS" port-forward svc/apisix "$GW_PORT:9080" >/dev/null 2>&1 &
gateway_forward=$!
trap 'kill "$keycloak_forward" "$gateway_forward" 2>/dev/null || true' EXIT
for port in "$KC_PORT" "$GW_PORT"; do
    for _ in $(seq 30); do
        (exec 3<>"/dev/tcp/127.0.0.1/$port") 2>/dev/null && break
        sleep 1
    done
done

export KEYCLOAK_PORT="$KC_PORT" APISIX_GATEWAY_PORT="$GW_PORT"
export DXOS_PSQL="$KUBECTL -n $NS exec -i postgres-0 -c postgres --"
# Earlier generations of dxos-public stay until pruned; they hold the same host.
app_host="$("$KUBECTL" -n "$NS" get configmap -o jsonpath='{range .items[?(@.data.APP_HOST)]}{.data.APP_HOST}{"\n"}{end}' | head -n1)"
[ -z "$app_host" ] || export DXOS_APP_URL="https://$app_host"
# The personal library role is seeded only where its application is installed.
if "$KUBECTL" get dxapplication personal-library >/dev/null 2>&1; then
    export ENABLED_CAPABILITIES="${ENABLED_CAPABILITIES:-cap-process,cap-hr,cap-ai}"
else
    export ENABLED_CAPABILITIES="${ENABLED_CAPABILITIES:-cap-process,cap-hr}"
fi

python3 "$ROOT/scripts/seed-demo.py" "$@"
