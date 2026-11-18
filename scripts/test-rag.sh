#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors
set -euo pipefail
mode="${1:-}"
case "$mode" in source|metadata|personal|package|models) ;; *) echo 'usage: test-rag.sh source|metadata|personal|package|models' >&2; exit 2;; esac
required=(TEST_DATABASE_URL)
if [[ "$mode" == source || "$mode" == metadata || "$mode" == personal || "$mode" == package || "$mode" == models ]]; then
 required+=(RAG_TEST_HASURA_URL RAG_TEST_HASURA_ADMIN_SECRET RAG_TEST_FILE_API_URL RAG_TEST_OIDC_TOKEN_A RAG_TEST_OIDC_TOKEN_B RAG_TEST_OIDC_TOKEN_ADMIN)
fi
if [[ "$mode" != models ]]; then required+=(RAG_TEST_S3_ENDPOINT RAG_TEST_S3_BUCKET); fi
if [[ "$mode" != source ]]; then required+=(RAG_TEST_AI_API_URL); fi
if [[ "$mode" == personal ]]; then required+=(RAG_TEST_UI_URL RAG_TEST_UI_STORAGE_A RAG_TEST_UI_STORAGE_B); fi
if [[ "$mode" == package ]]; then required+=(RAG_TEST_UI_URL RAG_TEST_UI_STORAGE_A RAG_TEST_UI_STORAGE_B RAG_TEST_UI_STORAGE_ADMIN RAG_TEST_COMPOSE_FILE RAG_TEST_OPERATOR RAG_TEST_DISPATCHER RAG_TEST_INTERNAL_AI_URL RAG_DISPATCHER_BEARER_TOKEN AI_DISPATCHER_DATABASE_URL RAG_PACKAGE_ARTIFACT_ROOT CLAMAV_ADDRESS RAG_INTERNAL_AI_URL RAG_SOURCE_QUALIFIED RAG_METADATA_QUALIFIED RAG_PARSER_QUALIFIED); fi
if [[ "$mode" == models ]]; then required+=(RAG_TEST_MODEL_FAKE_URL RAG_TEST_MODEL_WORKER_FAKE_URL RAG_TEST_EMBED_PROFILE RAG_TEST_GENERATE_PROFILE RAG_TEST_UI_URL RAG_TEST_UI_STORAGE_A RAG_TEST_OPERATOR RAG_TEST_DISPATCHER RAG_DISPATCHER_BEARER_TOKEN AI_DISPATCHER_DATABASE_URL RAG_PACKAGE_ARTIFACT_ROOT CLAMAV_ADDRESS RAG_INTERNAL_AI_URL RAG_SOURCE_QUALIFIED RAG_METADATA_QUALIFIED RAG_PARSER_QUALIFIED); fi
missing=0
for name in "${required[@]}"; do
 if [[ -z "${!name:-}" ]]; then echo "NOT_RUN: missing $name" >&2; missing=1; fi
done
[[ "$missing" == 0 ]] || exit 2
if [[ "$mode" == metadata ]]; then exec python3 "$(dirname "$0")/integration/rag_metadata.py"; fi
if [[ "$mode" == personal ]]; then exec python3 "$(dirname "$0")/integration/rag_personal.py"; fi
if [[ "$mode" == package ]]; then exec python3 "$(dirname "$0")/integration/rag_package.py"; fi
if [[ "$mode" == models ]]; then exec python3 "$(dirname "$0")/integration/rag_models.py"; fi
if [[ "$mode" != source ]]; then echo 'NOT_RUN: runner belongs to a later plan' >&2; exit 2; fi
exec python3 "$(dirname "$0")/integration/rag_sources.py"
