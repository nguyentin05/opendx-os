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

if [ $# -lt 1 ] || [ -z "${1:-}" ]; then
    echo "Usage: $0 <target_dir>" >&2
    exit 1
fi

TARGET_DIR="$1"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

if ! command -v go >/dev/null 2>&1; then
    echo "ERROR: prerequisite 'go' is not installed or not in PATH" >&2
    exit 1
fi

for req in platform apps contracts operator capabilities workspace-ui compose.yaml .env.example LICENSE NOTICE COPYRIGHT.txt; do
    if [ ! -e "${REPO_ROOT}/${req}" ]; then
        echo "ERROR: missing required source component: ${req}" >&2
        exit 1
    fi
done

echo "==> Installing OpenDX-OS (DX-OS Open-Core) into: ${TARGET_DIR}"

mkdir -p "${TARGET_DIR}/bin"

for item in platform apps contracts operator capabilities workspace-ui scripts compose.yaml .env.example LICENSE NOTICE COPYRIGHT.txt Makefile; do
    if [ -e "${REPO_ROOT}/${item}" ]; then
        cp -R "${REPO_ROOT}/${item}" "${TARGET_DIR}/"
    fi
done

echo "--> Building dxos-operator binary..."
(cd "${REPO_ROOT}/operator" && go build -o "${TARGET_DIR}/bin/dxos-operator" ./cmd/dxos-operator)
chmod +x "${TARGET_DIR}/bin/dxos-operator"

echo "--> Building cap-process binary..."
(cd "${REPO_ROOT}/capabilities" && go build -o "${TARGET_DIR}/bin/cap-process" ./cmd/cap-process)
chmod +x "${TARGET_DIR}/bin/cap-process"

echo "==> OpenDX-OS installation completed successfully at: ${TARGET_DIR}"
