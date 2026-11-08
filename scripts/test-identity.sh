#!/bin/sh
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

set -eu

if [ -z "${OIDC_ISSUER_URL:-}" ] || [ -z "${APISIX_URL:-}" ]; then
    echo "[test-identity] NOT_RUN: OIDC_ISSUER_URL and APISIX_URL are required" >&2
    exit 1
fi

# 1. Verify OIDC discovery endpoint is reachable
curl -fsS "${OIDC_ISSUER_URL}/.well-known/openid-configuration" >/dev/null

# 2. Verify unauthenticated or spoofed-header request cannot read protected Hasura data
RESP="$(curl -sS -X POST "${APISIX_URL}/v1/graphql" \
    -H "Content-Type: application/json" \
    -H "X-Hasura-User-Id: 22222222-2222-4222-8222-222222222222" \
    -H "X-Hasura-Workspace-Id: 11111111-1111-4111-8111-111111111111" \
    -H "X-Hasura-Role: role_operator" \
    -d '{"query":"{ core_workspaces { id } }"}')"

echo "$RESP" | grep -q '"errors"' || {
    echo "[test-identity] FAIL: spoofed headers returned protected data: $RESP" >&2
    exit 1
}

echo "[test-identity] PASS: OIDC discovery and header-spoofing rejection verified."
