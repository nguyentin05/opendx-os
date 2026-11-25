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

# Start the base stack with the knowledge overlay, without manual token setup.
# Secrets live only in .env.rag and are never printed.
#
# This is the FTS-only path: model calls stay switched off and the model/pgvector
# schema is not provisioned. `scripts/ai-up.sh` is the coordinated model
# deployment; it reuses the same bootstrap and environment files.
set -euo pipefail

# shellcheck source=scripts/lib/stack.sh
. "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/stack.sh"
cd "$STACK_ROOT"

STACK_LABEL=rag-up
export STACK_LABEL

ENV_FILE=".env.rag"
while [ "$#" -gt 0 ]; do
    case "$1" in
        --env-file)
            [ "$#" -ge 2 ] || stack_fail "--env-file requires a file"
            ENV_FILE="$2"
            shift 2
            ;;
        -h|--help)
            printf 'Usage: scripts/rag-up.sh [--env-file FILE]\n\nStart the knowledge stack with model calls off (FTS only).\n'
            exit 0
            ;;
        *)
            stack_fail "unknown argument: $1"
            ;;
    esac
done
STACK_VALUE_FILES=("$ENV_FILE" .env)
stack_env_file_path "$ENV_FILE"

stack_ensure_knowledge_env "$ENV_FILE"
# Model calls stay off, and there is no runtime catalog or model qualification
# flag to set: the reviewed deployment declaration and its reconciled selection
# are the only model source.
stack_env_ensure "$ENV_FILE" RAG_MODELS_ENABLED false
stack_env_ensure "$ENV_FILE" RAG_GLOBAL_MODEL_REGISTRY_SCHEMA_ENABLED false
stack_env_ensure "$ENV_FILE" RAG_MODELS_SCHEMA_ENABLED false
stack_env_ensure "$ENV_FILE" RAG_AGENT_MODELS_SCHEMA_ENABLED false
stack_env_ensure "$ENV_FILE" RAG_AGENT_ENABLED false
stack_env_ensure "$ENV_FILE" AI_AGENT_PROCESSING_APP_ID ""

# A caller's exported list takes precedence over every env file; use the list
# this installation was created with so cap-ai is not silently absent.
ENABLED_CAPABILITIES="$(grep -m1 '^ENABLED_CAPABILITIES=' "$ENV_FILE" | cut -d= -f2-)"
export ENABLED_CAPABILITIES

STACK_COMPOSE_FILES=(compose.yaml compose.rag.yaml)
if [ -n "${COMPOSE_FILE:-}" ]; then
    IFS=':' read -r -a STACK_COMPOSE_FILES <<<"$COMPOSE_FILE"
fi
stack_compose_init .env "$ENV_FILE"
trap stack_cleanup EXIT

stack_say "starting the stack with the knowledge overlay"
stack_compose up -d --build keycloak
stack_configure_service_clients
stack_compose up -d --build

stack_wait_healthy postgres 300
stack_wait_postgres 300
stack_require_cap_ai
stack_wait_app personal_library 300

stack_env_set "$ENV_FILE" AI_AGENT_PROCESSING_APP_ID personal_library
stack_env_set "$ENV_FILE" RAG_AGENT_ENABLED true
stack_compose up -d

cat <<EOF
[rag-up] UI: http://localhost:5173
[rag-up] Assistant and personal library are on; models stay off.
EOF
