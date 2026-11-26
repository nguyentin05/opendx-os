#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors
set -euo pipefail
cd "$(dirname "$0")/.."

project="dxos-ai-test-$$"
models="$(mktemp -d)"
compose() {
    docker compose -p "$project" -f compose.yaml -f compose.rag.yaml "$@"
}
export POSTGRES_DB=dxos_ai_test POSTGRES_PORT=0 BIND_ADDRESS=127.0.0.1
export POSTGRES_USER=dxos_admin POSTGRES_PASSWORD=local-ai-test
export AI_DATABASE_PASSWORD=local-api-test AI_WORKER_DATABASE_PASSWORD=local-worker-test
export AI_BROKER_DATABASE_PASSWORD=local-broker-test AI_DISPATCHER_DATABASE_PASSWORD=local-dispatcher-test
export RAG_WORKER_SERVICE_SUBJECT=33333333-3333-4333-8333-000000000001
export RAG_FILE_SERVICE_SUBJECT=33333333-3333-4333-8333-000000000002
export RAG_DISPATCHER_SERVICE_SUBJECT=33333333-3333-4333-8333-000000000003
export ENABLED_CAPABILITIES=cap-process,cap-hr,cap-work,cap-data,cap-ai

cleanup() {
    cd "$repo"
    compose down --volumes >/dev/null
    rm -rf -- "$models"
}
repo="$PWD"
trap cleanup EXIT
compose up -d --build --wait postgres
db_address="$(compose port postgres 5432)"
export AI_MIGRATION_DATABASE_URL="postgres://$POSTGRES_USER:$POSTGRES_PASSWORD@$db_address/$POSTGRES_DB"
export DATABASE_URL="postgres://dxos_ai_api:$AI_DATABASE_PASSWORD@$db_address/$POSTGRES_DB"
export AI_TEST_WORKER_DATABASE_URL="postgres://dxos_ai_worker:$AI_WORKER_DATABASE_PASSWORD@$db_address/$POSTGRES_DB"
export AI_TEST_BROKER_URL="pgmq://dxos_ai_broker:$AI_BROKER_DATABASE_PASSWORD@$db_address/$POSTGRES_DB"
export AI_PARSER_ARTIFACTS="$models"
export HAYSTACK_TELEMETRY_ENABLED=false LANGSMITH_TRACING=false

cd capabilities/ai
uv sync --locked
uv run ruff check .
uv run hf download docling-project/docling-layout-heron \
    config.json preprocessor_config.json model.safetensors README.md \
    --revision 8f39ad3c0b4c58e9c2d2c84a38465abf757272d8 \
    --local-dir "$models/docling-project--docling-layout-heron"
uv run alembic upgrade head
uv run pytest -q
