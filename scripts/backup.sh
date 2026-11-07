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
    echo "Usage: $0 <backup_dir>" >&2
    exit 1
fi

if [ -z "${DATABASE_URL:-}" ] || [ -z "${OBJECTS_DIR:-}" ]; then
    echo "ERROR: DATABASE_URL and OBJECTS_DIR are required for coordinated backup" >&2
    exit 1
fi

BACKUP_DIR="$1"
mkdir -p "${BACKUP_DIR}/objects"

pg_dump --clean --if-exists --no-owner --no-privileges "$DATABASE_URL" > "${BACKUP_DIR}/database.sql"
cp -R "${OBJECTS_DIR}/." "${BACKUP_DIR}/objects/"
(cd "${BACKUP_DIR}" && find objects -type f -exec sha256sum {} + | sort > "${BACKUP_DIR}/objects.sha256")
sha256sum "${BACKUP_DIR}/database.sql" > "${BACKUP_DIR}/database.sql.sha256"

echo "[backup] Coordinated database and object store backup saved to ${BACKUP_DIR}"
