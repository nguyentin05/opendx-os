#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors
#
# Deploy the platform to the current kubectl context.
#
#   deploy/k8s/apply.sh <overlay directory> <env file>
#   deploy/k8s/apply.sh deploy/k8s/overlays/eks ~/dxos-k8s.env   (any overlay; eks is the example)
#
# The env file holds the secrets, as written by `scripts/init-env.sh <file>`
# plus S3_ACCESS_KEY_ID and S3_SECRET_ACCESS_KEY. Keep it outside the
# repository. Every secret must be at least 16 letters or digits: the development
# defaults of compose.yaml are refused.
#
# The script creates only the Secrets, which are never committed, then applies
# the overlay with server-side apply (the postgres-seed ConfigMap is larger than
# the client-side apply annotation allows). Everything else is in the manifests;
# the ConfigMaps under base/tier1-platform/configmaps are generated from
# platform/ by `make k8s-config`. It is safe to run again. PostgreSQL applies the database passwords only when it
# initializes an empty volume.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
KUBECTL="${KUBECTL:-kubectl}"
NS=dxos
OPERATOR_NS=dxos-operator-system
# Matches KESTRA_USER in deploy/k8s/base/kustomization.yaml.
KESTRA_USER=admin@dxos.local

SECRETS=(
    POSTGRES_PASSWORD
    HASURA_ADMIN_SECRET
    KEYCLOAK_ADMIN_PASSWORD
    KESTRA_PASSWORD
    KESTRA_WEBHOOK_KEY
    WORKFLOW_SERVICE_TOKEN
    KESTRA_DATABASE_PASSWORD
    HASURA_DATABASE_PASSWORD
    PROCESS_DATABASE_PASSWORD
    AUTHZ_DATABASE_PASSWORD
    HR_DATABASE_PASSWORD
    S3_ACCESS_KEY_ID
    S3_SECRET_ACCESS_KEY
)

die() { echo "[deploy] ERROR: $*" >&2; exit 1; }

[ $# -eq 2 ] || die "usage: deploy/k8s/apply.sh <overlay directory> <env file>"
overlay="$1"
env_file="$2"
[ -f "$overlay/kustomization.yaml" ] || die "$overlay has no kustomization.yaml"
[ -f "$env_file" ] || die "$env_file not found"

# Read only the secret keys; the file is data, never executed.
declare -A value
while IFS= read -r line || [ -n "$line" ]; do
    for key in "${SECRETS[@]}"; do
        if [[ "$line" == "$key="* ]]; then
            value[$key]="${line#*=}"
        fi
    done
done <"$env_file"

for key in "${SECRETS[@]}"; do
    # Letters and digits only: the values are embedded in connection URLs.
    [[ "${value[$key]:-}" =~ ^[A-Za-z0-9]{16,}$ ]] ||
        die "$key in $env_file must be at least 16 letters or digits (development defaults are refused)"
done

command -v "$KUBECTL" >/dev/null || die "kubectl is required"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
chmod 700 "$tmp"

apply() { "$KUBECTL" apply --server-side --force-conflicts -f - >/dev/null; }

for ns in "$NS" "$OPERATOR_NS"; do
    "$KUBECTL" create namespace "$ns" --dry-run=client -o yaml | apply
done

for key in "${SECRETS[@]}"; do
    printf '%s=%s\n' "$key" "${value[$key]}"
done >"$tmp/secrets.env"
"$KUBECTL" -n "$NS" create secret generic dxos-secrets --from-env-file="$tmp/secrets.env" \
    --dry-run=client -o yaml | apply

# SeaweedFS reads its S3 identity from a file; the shared one holds the
# development keys, so the deployment's keys replace them.
sed -e "s/dxos_s3_access_key/${value[S3_ACCESS_KEY_ID]}/" \
    -e "s/dxos_s3_secret_key/${value[S3_SECRET_ACCESS_KEY]}/" \
    "$ROOT/platform/seaweedfs/s3.json" >"$tmp/s3.json"
"$KUBECTL" -n "$NS" create secret generic seaweedfs-s3 --from-file=s3.json="$tmp/s3.json" \
    --dry-run=client -o yaml | apply

# dxos-controller installs applications with the database owner and the
# Hasura and Kestra admin credentials, like the Compose operator.
cat >"$tmp/operator.env" <<EOF
DATABASE_URL=postgres://dxos_admin:${value[POSTGRES_PASSWORD]}@postgres.${NS}.svc:5432/dxos_db?sslmode=disable
HASURA_URL=http://hasura.${NS}.svc:8080
HASURA_ADMIN_SECRET=${value[HASURA_ADMIN_SECRET]}
KESTRA_URL=http://kestra.${NS}.svc:8080
KESTRA_USER=${KESTRA_USER}
KESTRA_PASSWORD=${value[KESTRA_PASSWORD]}
EOF
"$KUBECTL" -n "$OPERATOR_NS" create secret generic dxos-operator-config --from-env-file="$tmp/operator.env" \
    --dry-run=client -o yaml | apply

"$KUBECTL" kustomize "$overlay" | apply
echo "[deploy] applied $overlay; watch: $KUBECTL -n $NS get pods -w"
