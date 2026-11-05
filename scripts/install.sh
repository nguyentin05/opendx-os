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

set -euo pipefail

TARGET_DIR="${1:-/opt/opendx-os}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

echo "==> Installing OpenDX-OS (DX-OS Open-Core) into: ${TARGET_DIR}"

mkdir -p "${TARGET_DIR}/bin"
mkdir -p "${TARGET_DIR}/platform"
mkdir -p "${TARGET_DIR}/apps"
mkdir -p "${TARGET_DIR}/config"

echo "--> Copying platform schemas and compose configurations..."
cp -r "${REPO_ROOT}/platform" "${TARGET_DIR}/"
cp -r "${REPO_ROOT}/apps" "${TARGET_DIR}/"
cp "${REPO_ROOT}/compose.yaml" "${TARGET_DIR}/compose.yaml"
if [ -f "${REPO_ROOT}/.env.example" ]; then
    cp "${REPO_ROOT}/.env.example" "${TARGET_DIR}/.env.example"
fi

echo "--> Building and installing dxos-operator binary..."
if command -v go >/dev/null 2>&1; then
    (cd "${REPO_ROOT}/operator" && go build -o "${TARGET_DIR}/bin/dxos-operator" ./cmd/dxos-operator)
    chmod +x "${TARGET_DIR}/bin/dxos-operator"
    echo "--> dxos-operator installed to ${TARGET_DIR}/bin/dxos-operator"
else
    echo "--> Warning: Go not found, skipping operator compilation"
fi

echo "==> OpenDX-OS installation completed successfully at: ${TARGET_DIR}"
