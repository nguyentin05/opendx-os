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

set -e

CAP_DIR="/docker-entrypoint-initdb.d/capabilities"
if [ ! -d "$CAP_DIR" ]; then
    CAP_DIR="$(cd "$(dirname "$0")/../capabilities" && pwd)"
fi

ENABLED="${ENABLED_CAPABILITIES:-all}"

echo "[dxos-init] Applying pluggable capabilities from: $CAP_DIR (Filter: $ENABLED)"
for sql_file in "$CAP_DIR"/*.sql; do
    if [ -f "$sql_file" ]; then
        filename=$(basename "$sql_file" .sql)
        cap_id=$(echo "$filename" | tr '_' '-')

        if [ "$ENABLED" = "all" ] || echo ",$ENABLED," | grep -q ",$cap_id,"; then
            echo "[dxos-init] Enabling capability: $cap_id ($sql_file)"
            psql -v ON_ERROR_STOP=1 --username "${POSTGRES_USER:-dxos_admin}" --dbname "${POSTGRES_DB:-dxos_db}" -f "$sql_file"
        else
            echo "[dxos-init] Skipping capability (disabled): $cap_id"
        fi
    fi
done
