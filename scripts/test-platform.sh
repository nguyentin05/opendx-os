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

if [ -z "${TEST_DATABASE_URL:-}" ]; then
    echo "[test-platform] NOT_RUN: TEST_DATABASE_URL is required" >&2
    exit 1
fi

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"

psql -v ON_ERROR_STOP=1 "$TEST_DATABASE_URL" -f "$ROOT_DIR/platform/postgres/init/01_core_schema.sql"
psql -v ON_ERROR_STOP=1 "$TEST_DATABASE_URL" -f "$ROOT_DIR/platform/postgres/init/02_capability_registry.sql"
psql -v ON_ERROR_STOP=1 "$TEST_DATABASE_URL" -f "$ROOT_DIR/platform/postgres/init/03_app_registry.sql"
psql -v ON_ERROR_STOP=1 "$TEST_DATABASE_URL" -f "$ROOT_DIR/platform/postgres/init/05_operator_deployments.sql"
psql -v ON_ERROR_STOP=1 "$TEST_DATABASE_URL" -f "$ROOT_DIR/platform/postgres/init/07_app_definitions.sql"
psql -v ON_ERROR_STOP=1 "$TEST_DATABASE_URL" -f "$ROOT_DIR/platform/postgres/capabilities/cap_process.sql"
psql -v ON_ERROR_STOP=1 "$TEST_DATABASE_URL" -f "$ROOT_DIR/platform/postgres/tests/core_contract.sql"

echo "[test-platform] PASS: core schema and behavioral SQL assertions verified."
