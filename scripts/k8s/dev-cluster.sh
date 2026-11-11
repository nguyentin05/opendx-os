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

# Disposable local Kubernetes environment for dxos-operator (ADR 0004).
#
#   scripts/k8s/dev-cluster.sh up     create a kind cluster, PostgreSQL, Hasura and the operator
#   scripts/k8s/dev-cluster.sh demo   apply an app as a DXApplication, wait for Ready, then
#                                     remove a Hasura permission by hand and wait for repair
#   scripts/k8s/dev-cluster.sh down   delete the kind cluster
#
# Requires docker and kubectl. kind and kustomize are taken from PATH or operator/bin
# (`make -C operator kustomize`; `GOBIN=$PWD/operator/bin go install sigs.k8s.io/kind@v0.33.0`).
# The cluster holds generated credentials and disposable data only.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
CLUSTER="${DXOS_KIND_CLUSTER:-dxos-dev}"
CONTEXT="kind-${CLUSTER}"
PLATFORM_NS="dxos-platform"
OPERATOR_NS="dxos-operator-system"
IMAGE="dxos-operator:dev"
DEMO_APP="${DXOS_DEMO_APP:-procurement}"
DEMO_WORKSPACE="${DXOS_DEMO_WORKSPACE:-11111111-1111-4111-8111-111111111111}"

log() { echo "[dev-cluster] $*"; }
die() { echo "[dev-cluster] ERROR: $*" >&2; exit 1; }

tool() {
    if command -v "$1" >/dev/null 2>&1; then
        command -v "$1"
    elif [ -x "$ROOT/operator/bin/$1" ]; then
        echo "$ROOT/operator/bin/$1"
    else
        die "$1 not found in PATH or operator/bin"
    fi
}

KIND=""
KUSTOMIZE=""
kc() { kubectl --context "$CONTEXT" "$@"; }

require() {
    command -v docker >/dev/null || die "docker is required"
    command -v kubectl >/dev/null || die "kubectl is required"
    KIND="$(tool kind)"
    KUSTOMIZE="$(tool kustomize)"
}

random_hex() { head -c 24 /dev/urandom | od -An -tx1 | tr -d ' \n'; }

secret_value() {
    kc -n "$1" get secret "$2" -o "jsonpath={.data.$3}" | base64 -d
}

create_cluster() {
    if "$KIND" get clusters 2>/dev/null | grep -qx "$CLUSTER"; then
        log "cluster $CLUSTER exists"
    else
        log "creating kind cluster $CLUSTER"
        "$KIND" create cluster --name "$CLUSTER" --wait 180s
    fi
}

load_images() {
    log "building $IMAGE"
    docker build -q -t "$IMAGE" "$ROOT/operator" >/dev/null
    "$KIND" load docker-image "$IMAGE" --name "$CLUSTER"
    if docker image inspect postgres:17-alpine >/dev/null 2>&1; then
        "$KIND" load docker-image postgres:17-alpine --name "$CLUSTER"
    fi
}

create_credentials() {
    kc create namespace "$PLATFORM_NS" --dry-run=client -o yaml | kc apply -f - >/dev/null
    kc create namespace "$OPERATOR_NS" --dry-run=client -o yaml | kc apply -f - >/dev/null
    # Credentials are generated once; regenerating them would no longer match the database.
    if ! kc -n "$PLATFORM_NS" get secret dxos-platform-credentials >/dev/null 2>&1; then
        log "generating platform credentials"
        local pg hasura_db admin
        pg="$(random_hex)"; hasura_db="$(random_hex)"; admin="$(random_hex)"
        kc -n "$PLATFORM_NS" create secret generic dxos-platform-credentials \
            --from-literal=postgres-password="$pg" \
            --from-literal=hasura-db-password="$hasura_db" \
            --from-literal=hasura-admin-secret="$admin" \
            --from-literal=hasura-database-url="postgres://dxos_hasura:${hasura_db}@postgres:5432/dxos_db?sslmode=disable" >/dev/null
    fi
    if ! kc -n "$OPERATOR_NS" get secret dxos-operator-config >/dev/null 2>&1; then
        local pg admin
        pg="$(secret_value "$PLATFORM_NS" dxos-platform-credentials postgres-password)"
        admin="$(secret_value "$PLATFORM_NS" dxos-platform-credentials hasura-admin-secret)"
        kc -n "$OPERATOR_NS" create secret generic dxos-operator-config \
            --from-literal=DATABASE_URL="postgres://dxos_admin:${pg}@postgres.${PLATFORM_NS}:5432/dxos_db?sslmode=disable" \
            --from-literal=HASURA_URL="http://hasura.${PLATFORM_NS}:8080" \
            --from-literal=HASURA_ADMIN_SECRET="$admin" >/dev/null
    fi
}

create_init_configmaps() {
    local args=()
    for f in "$ROOT"/platform/postgres/init/* "$ROOT"/platform/k8s/dev/initdb/*; do
        args+=(--from-file="$(basename "$f")=$f")
    done
    kc -n "$PLATFORM_NS" create configmap postgres-init "${args[@]}" --dry-run=client -o yaml | kc apply -f - >/dev/null
    args=()
    for f in "$ROOT"/platform/postgres/capabilities/*.sql; do
        args+=(--from-file="$(basename "$f")=$f")
    done
    kc -n "$PLATFORM_NS" create configmap postgres-capabilities "${args[@]}" --dry-run=client -o yaml | kc apply -f - >/dev/null
}

up() {
    require
    create_cluster
    load_images
    create_credentials
    create_init_configmaps
    log "deploying PostgreSQL and Hasura"
    "$KUSTOMIZE" build "$ROOT/platform/k8s/dev" | kc apply -f - >/dev/null
    kc -n "$PLATFORM_NS" rollout status deployment/postgres --timeout=300s
    kc -n "$PLATFORM_NS" rollout status deployment/hasura --timeout=300s
    log "deploying dxos-operator (CRD, RBAC, controller)"
    "$KUSTOMIZE" build "$ROOT/operator/config/dev" | kc apply -f - >/dev/null
    kc -n "$OPERATOR_NS" rollout restart deployment/dxos-operator-controller-manager >/dev/null
    kc -n "$OPERATOR_NS" rollout status deployment/dxos-operator-controller-manager --timeout=180s
    log "ready: kubectl --context $CONTEXT get dxapplications"
}

psql_query() {
    kc -n "$PLATFORM_NS" exec deploy/postgres -- psql -U dxos_admin -d dxos_db -At -c "$1"
}

hasura_metadata() {
    local admin="$1" body="$2"
    curl -fsS -X POST "http://127.0.0.1:${HASURA_LOCAL_PORT}/v1/metadata" \
        -H "Content-Type: application/json" -H "X-Hasura-Admin-Secret: ${admin}" -d "$body"
}

demo() {
    require
    command -v curl >/dev/null || die "curl is required"
    local name="${DEMO_APP//_/-}"
    log "seeding workspace $DEMO_WORKSPACE"
    psql_query "INSERT INTO core.workspaces (id, code, name) VALUES ('${DEMO_WORKSPACE}', 'demo', 'Demo') ON CONFLICT DO NOTHING" >/dev/null

    log "applying apps/${DEMO_APP} as a DXApplication"
    (cd "$ROOT/operator" && go run ./cmd/dxos-operator export-crd -workspace "$DEMO_WORKSPACE" "../apps/${DEMO_APP}") | kc apply -f -
    kc wait "dxapplication/${name}" --for=condition=Ready --timeout=180s
    kc get dxapplication "$name" -o wide
    log "registry: $(psql_query "SELECT app_id || ' ' || version || ' ' || status FROM core.app_registry WHERE workspace_id = '${DEMO_WORKSPACE}'")"

    local admin pf_pid
    admin="$(secret_value "$PLATFORM_NS" dxos-platform-credentials hasura-admin-secret)"
    HASURA_LOCAL_PORT="${HASURA_LOCAL_PORT:-18080}"
    kc -n "$PLATFORM_NS" port-forward svc/hasura "${HASURA_LOCAL_PORT}:8080" >/dev/null 2>&1 &
    pf_pid=$!
    trap 'kill $pf_pid 2>/dev/null || true' RETURN
    for _ in $(seq 1 30); do
        curl -fsS "http://127.0.0.1:${HASURA_LOCAL_PORT}/healthz" >/dev/null 2>&1 && break
        sleep 1
    done

    local table role
    table="$(kc get dxapplication "$name" -o jsonpath='{.spec.domainTable.name}')"
    role="$(kc get dxapplication "$name" -o jsonpath='{.spec.domainAccess.insert[0].role}')"
    log "simulating drift: dropping insert permission of ${role} on ${table}"
    hasura_metadata "$admin" "{\"type\":\"pg_drop_insert_permission\",\"args\":{\"source\":\"default\",\"table\":{\"schema\":\"${table%%.*}\",\"name\":\"${table#*.}\"},\"role\":\"${role}\"}}" >/dev/null
    local deadline=$((SECONDS + 120))
    until hasura_metadata "$admin" '{"type":"export_metadata","args":{}}' | grep -q "\"insert_permissions\":\[{\"role\":\"${role}\""; do
        [ "$SECONDS" -lt "$deadline" ] || die "permission was not repaired within 120s"
        sleep 2
    done
    log "drift repaired by the operator: insert permission of ${role} restored"
}

down() {
    KIND="$(tool kind)"
    "$KIND" delete cluster --name "$CLUSTER"
}

case "${1:-}" in
    up) up ;;
    demo) demo ;;
    down) down ;;
    *) echo "usage: $0 up|demo|down" >&2; exit 2 ;;
esac
