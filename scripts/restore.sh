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
    echo "ERROR: DATABASE_URL and OBJECTS_DIR are required for coordinated restore" >&2
    exit 1
fi

BACKUP_DIR="$1"
if [ ! -f "${BACKUP_DIR}/database.sql" ] || [ ! -d "${BACKUP_DIR}/objects" ] || [ ! -f "${BACKUP_DIR}/objects.sha256" ]; then
    echo "ERROR: incomplete backup archive in ${BACKUP_DIR}" >&2
    exit 1
fi

(cd "${BACKUP_DIR}" && sha256sum -c objects.sha256 >/dev/null)

mkdir -p "${OBJECTS_DIR}"
rm -rf "${OBJECTS_DIR:?}"/*
cp -R "${BACKUP_DIR}/objects/." "${OBJECTS_DIR}/"

psql -v ON_ERROR_STOP=1 "$DATABASE_URL" -f "${BACKUP_DIR}/database.sql" >/dev/null

# Verify every core.file_objects row has matching object bytes and SHA-256 digest on disk
psql -v ON_ERROR_STOP=1 -At "$DATABASE_URL" -c "SELECT object_key || '|' || sha256_hash FROM core.file_objects" | while IFS='|' read -r obj_key expected_hash; do
    [ -z "$obj_key" ] && continue
    target_file="${OBJECTS_DIR}/${obj_key}"
    if [ ! -f "$target_file" ]; then
        echo "ERROR: restored database references missing object: ${obj_key}" >&2
        exit 1
    fi
    actual_hash="$(sha256sum "$target_file" | awk '{print $1}')"
    if [ "$actual_hash" != "$expected_hash" ]; then
        echo "ERROR: digest mismatch for ${obj_key}: expected ${expected_hash}, got ${actual_hash}" >&2
        exit 1
    fi
done

echo "[restore] Coordinated database and object store restore verified."
