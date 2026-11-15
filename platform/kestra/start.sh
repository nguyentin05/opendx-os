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
#
# Starts Kestra with the three secrets application flows may read. Kestra
# takes secrets from SECRET_<NAME> variables holding base64, so the plain
# values of the platform's environment are encoded here:
#
#   DXOS_WEBHOOK_KEY     key of the webhook trigger the event publisher calls
#   DXOS_PROCESS_URL     where flows reach cap-process inside the network
#   DXOS_WORKFLOW_TOKEN  workflow service credential for the effect endpoints
set -euo pipefail

for name in DXOS_WEBHOOK_KEY DXOS_PROCESS_URL DXOS_WORKFLOW_TOKEN; do
    value="${!name:-}"
    if [ -z "$value" ]; then
        echo "[kestra] $name is required" >&2
        exit 1
    fi
    export "SECRET_${name}=$(printf '%s' "$value" | base64 -w0)"
    unset "$name"
done

exec /app/kestra server standalone --config /app/config/config.yaml
