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

# Prepare a disposable PostgreSQL 17 database for the Go integration suites.
# The harness creates one database per test from TEST_DATABASE_URL, so the
# platform schemas and the cluster-global runtime roles must already exist.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

if [ -z "${TEST_DATABASE_URL:-}" ]; then
    echo "[provision-test-db] FAIL: TEST_DATABASE_URL is required" >&2
    exit 1
fi

for file in \
    platform/postgres/init/01_core_schema.sql \
    platform/postgres/init/02_capability_registry.sql \
    platform/postgres/init/03_app_registry.sql \
    platform/postgres/init/05_operator_deployments.sql \
    platform/postgres/init/07_app_definitions.sql \
    platform/postgres/capabilities/cap_hr.sql \
    platform/postgres/capabilities/cap_work.sql \
    platform/postgres/migrations/core/0007_work_item_source_version.sql \
    platform/postgres/capabilities/cap_process.sql \
    platform/postgres/capabilities/cap_data.sql \
    platform/postgres/init/06_runtime_roles.sql \
    platform/postgres/migrations/core/0008_account_provisioning.sql
do
    psql -q -v ON_ERROR_STOP=1 -f "${ROOT_DIR}/${file}" "${TEST_DATABASE_URL}"
done

echo "[provision-test-db] PASS: platform schemas and runtime roles provisioned"
