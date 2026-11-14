#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors
set -euo pipefail
mode="${1:-}"
case "$mode" in source|metadata|personal|package|models) ;; *) echo 'usage: test-rag.sh source|metadata|personal|package|models' >&2; exit 2;; esac
required=(TEST_DATABASE_URL)
if [[ "$mode" == source || "$mode" == metadata ]]; then
 required+=(RAG_TEST_HASURA_URL RAG_TEST_HASURA_ADMIN_SECRET RAG_TEST_FILE_API_URL RAG_TEST_S3_ENDPOINT RAG_TEST_S3_BUCKET RAG_TEST_OIDC_TOKEN_A RAG_TEST_OIDC_TOKEN_B RAG_TEST_OIDC_TOKEN_ADMIN)
fi
if [[ "$mode" != source ]]; then required+=(RAG_TEST_AI_API_URL); fi
missing=0
for name in "${required[@]}"; do
 if [[ -z "${!name:-}" ]]; then echo "NOT_RUN: missing $name" >&2; missing=1; fi
done
[[ "$missing" == 0 ]] || exit 2
if [[ "$mode" == metadata ]]; then exec python3 "$(dirname "$0")/integration/rag_metadata.py"; fi
if [[ "$mode" != source ]]; then echo 'NOT_RUN: runner belongs to a later plan' >&2; exit 2; fi
exec python3 "$(dirname "$0")/integration/rag_sources.py"
