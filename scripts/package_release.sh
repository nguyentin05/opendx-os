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

VERSION="${1:-0.1.0-demo}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
DIST_DIR="${REPO_ROOT}/dist"
ARCHIVE_NAME="dxos-open-core-${VERSION}"
ARCHIVE_FILE="${DIST_DIR}/${ARCHIVE_NAME}.tar.gz"
STAGE_DIR="$(mktemp -d)"
trap 'rm -rf "${STAGE_DIR}"' EXIT

mkdir -p "${DIST_DIR}" "${STAGE_DIR}/${ARCHIVE_NAME}"

tar -C "${REPO_ROOT}" \
    --exclude=".git" \
    --exclude=".agents" \
    --exclude="AGENTS.md" \
    --exclude="CLAUDE.md" \
    --exclude="docs/superpowers" \
    --exclude="dist" \
    --exclude="build" \
    --exclude="bin" \
    --exclude="coverage" \
    --exclude="node_modules" \
    --exclude="vendor" \
    --exclude=".venv" \
    --exclude="__pycache__" \
    --exclude=".env" \
    -cf - . | tar -C "${STAGE_DIR}/${ARCHIVE_NAME}" -xf -

tar -C "${STAGE_DIR}" -czf "${ARCHIVE_FILE}" "${ARCHIVE_NAME}"
(cd "${DIST_DIR}" && sha256sum "${ARCHIVE_NAME}.tar.gz" > "${ARCHIVE_NAME}.tar.gz.sha256")

echo "[package-release] Created ${ARCHIVE_FILE} and ${ARCHIVE_FILE}.sha256"
