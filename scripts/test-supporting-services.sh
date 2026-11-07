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

if [ -z "${S3_ENDPOINT:-}" ] || [ -z "${CLAMAV_ADDRESS:-}" ] || [ -z "${KESTRA_URL:-}" ]; then
    echo "[test-supporting-services] NOT_RUN: S3_ENDPOINT, CLAMAV_ADDRESS, and KESTRA_URL are required" >&2
    exit 1
fi

# 1. Verify unauthenticated S3 request is rejected
S3_CODE="$(curl -s -o /dev/null -w "%{http_code}" "${S3_ENDPOINT}/")"
if [ "$S3_CODE" = "200" ]; then
    echo "[test-supporting-services] FAIL: unauthenticated S3 root returned 200" >&2
    exit 1
fi

# 2. Verify untrusted Kestra invocation is rejected
KESTRA_CODE="$(curl -s -o /dev/null -w "%{http_code}" -X POST "${KESTRA_URL}" -H "Content-Type: application/json" -d '{}')"
if [ "$KESTRA_CODE" = "200" ]; then
    echo "[test-supporting-services] FAIL: unauthenticated Kestra webhook returned 200" >&2
    exit 1
fi

echo "[test-supporting-services] PASS: supporting service probes verified."
