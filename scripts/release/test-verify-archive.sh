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

# Negative tests for verify-archive.sh: a gate nobody has seen fail is a gate
# nobody knows works.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
VERIFY="${SCRIPT_DIR}/verify-archive.sh"
WORK="$(mktemp -d)"
trap 'rm -rf "${WORK}"' EXIT

expect_pass() {
    if ! "${VERIFY}" "$1" >/dev/null 2>&1; then
        echo "[test-verify-archive] FAIL: expected acceptance for $1" >&2
        "${VERIFY}" "$1" || true
        exit 1
    fi
}

expect_fail() {
    if "${VERIFY}" "$1" >/dev/null 2>&1; then
        echo "[test-verify-archive] FAIL: expected rejection for $1" >&2
        exit 1
    fi
}

new_tree() {
    local root="$1"
    mkdir -p "${root}/package/scripts"
    for dir in platform apps contracts operator capabilities workspace-ui; do
        mkdir -p "${root}/package/${dir}"
        echo content > "${root}/package/${dir}/.keep"
    done
    for file in LICENSE NOTICE COPYRIGHT.txt README.md compose.yaml .env.example; do
        echo content > "${root}/package/${file}"
    done
    echo content > "${root}/package/scripts/install.sh"
    echo content > "${root}/package/scripts/package_release.sh"
}

new_tree "${WORK}/clean"
tar -C "${WORK}/clean" -czf "${WORK}/clean.tar.gz" package
expect_pass "${WORK}/clean.tar.gz"

new_tree "${WORK}/forbidden"
mkdir -p "${WORK}/forbidden/package/.scratch"
echo content > "${WORK}/forbidden/package/.scratch/notes.md"
tar -C "${WORK}/forbidden" -czf "${WORK}/forbidden.tar.gz" package
expect_fail "${WORK}/forbidden.tar.gz"

new_tree "${WORK}/secrets"
echo "AI_DATABASE_PASSWORD=generated" > "${WORK}/secrets/package/.env.rag"
tar -C "${WORK}/secrets" -czf "${WORK}/secrets.tar.gz" package
expect_fail "${WORK}/secrets.tar.gz"

new_tree "${WORK}/missing"
rm "${WORK}/missing/package/LICENSE"
tar -C "${WORK}/missing" -czf "${WORK}/missing.tar.gz" package
expect_fail "${WORK}/missing.tar.gz"

tar -C "${WORK}" -czf "${WORK}/empty.tar.gz" --files-from /dev/null
expect_fail "${WORK}/empty.tar.gz"

expect_fail "${WORK}/absent.tar.gz"

echo "[test-verify-archive] PASS: the gate accepts a complete archive and rejects forbidden, secret-bearing, incomplete, empty, and missing archives"
