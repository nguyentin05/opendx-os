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

# Fail-closed check of a packaged source archive: it must exist, carry the
# components the installer requires, and contain no local-only material.

set -euo pipefail

archive="${1:-}"
if [ -z "${archive}" ]; then
    echo "usage: $0 <archive.tar.gz>" >&2
    exit 2
fi

fail() {
    echo "[verify-archive] FAIL: $*" >&2
    exit 1
}

[ -f "${archive}" ] || fail "archive not found: ${archive}"
[ -s "${archive}" ] || fail "archive is empty: ${archive}"

listing="$(tar -tzf "${archive}")" || fail "cannot read archive: ${archive}"
[ -n "${listing}" ] || fail "archive has no entries: ${archive}"

forbidden=(
    "/.agents/"
    "/AGENTS.md"
    "/AGENTS.local.md"
    "/CLAUDE.md"
    "/.claude/"
    "/.scratch/"
    "/.superpowers/"
    "/docs/_internal/"
    "/docs/superpowers/"
)
for pattern in "${forbidden[@]}"; do
    if grep -Fq -- "${pattern}" <<<"${listing}"; then
        fail "archive contains ${pattern}"
    fi
done
# Local environment files hold generated secrets; only .env.example ships.
if grep -Eq -- '/\.env(\.[^/]*)?$' <<<"$(grep -Fv -- '/.env.example' <<<"${listing}")"; then
    fail "archive contains a local .env file"
fi

required=(
    "LICENSE"
    "NOTICE"
    "COPYRIGHT.txt"
    "THIRD_PARTY_LICENSES.txt"
    "README.md"
    "compose.yaml"
    ".env.example"
    "platform"
    "apps"
    "contracts"
    "operator"
    "capabilities"
    "workspace-ui"
    "scripts/install.sh"
    "scripts/package_release.sh"
)

has_path() {
    local want="$1"
    local entry rel
    while IFS= read -r entry; do
        rel="${entry#*/}"
        rel="${rel%/}"
        if [ "${rel}" = "${want}" ]; then
            return 0
        fi
    done <<<"${listing}"
    return 1
}

for path in "${required[@]}"; do
    has_path "${path}" || fail "archive is missing ${path}"
done

echo "[verify-archive] PASS: ${archive}"
