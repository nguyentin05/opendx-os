#!/bin/bash
# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors
#
# Creates or updates the Keycloak client dxos-provisioner, which dxos-authz uses
# to create accounts (POST /api/auth/users). Its service account holds only
# manage-users and view-users of the realm, never anything in master. Without
# KEYCLOAK_PROVISIONER_SECRET the client is disabled (or never created), because
# a confidential client with an empty secret would accept an empty one.
# Runs in the Keycloak image, after Keycloak is up; safe to run again.
set -euo pipefail

realm="${KEYCLOAK_REALM:-dxos}"
secret="${KEYCLOAK_PROVISIONER_SECRET:-}"
kc() { /opt/keycloak/bin/kcadm.sh "$@" --config /tmp/kcadm.config; }

for attempt in $(seq 60); do
    if kc config credentials --server "$KEYCLOAK_URL" --realm master \
        --user "$KEYCLOAK_ADMIN" --password "$KEYCLOAK_ADMIN_PASSWORD" >/dev/null 2>&1; then
        break
    fi
    [ "$attempt" -lt 60 ] || { echo "[provisioner] FAIL: Keycloak at $KEYCLOAK_URL is unreachable" >&2; exit 1; }
    sleep 5
done

id="$(kc get clients -r "$realm" -q clientId=dxos-provisioner --fields id --format csv --noquotes)"
if [ -z "$secret" ]; then
    [ -z "$id" ] || kc update "clients/$id" -r "$realm" -s enabled=false
    echo "[provisioner] no KEYCLOAK_PROVISIONER_SECRET: account creation through dxos-authz is off"
    exit 0
fi
if ! [[ "$secret" =~ ^[A-Za-z0-9]{16,}$ ]]; then
    echo "[provisioner] FAIL: KEYCLOAK_PROVISIONER_SECRET must be at least 16 letters or digits" >&2
    exit 1
fi

# The secret goes through stdin, not the command line.
client='{"clientId":"dxos-provisioner","enabled":true,"publicClient":false,"serviceAccountsEnabled":true,
"standardFlowEnabled":false,"implicitFlowEnabled":false,"directAccessGrantsEnabled":false,
"clientAuthenticatorType":"client-secret","secret":"'"$secret"'"}'
if [ -z "$id" ]; then
    printf '%s' "$client" | kc create clients -r "$realm" -f - >/dev/null
else
    printf '%s' "$client" | kc update "clients/$id" -r "$realm" -f -
fi
kc add-roles -r "$realm" --uusername service-account-dxos-provisioner \
    --cclientid realm-management --rolename manage-users --rolename view-users
echo "[provisioner] account creation through dxos-authz is on (client dxos-provisioner in realm $realm)"
