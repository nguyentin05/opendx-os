#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors
set -euo pipefail
if [ "${1:-}" != source ]; then
    echo 'Use scripts/test-rag.sh source for the Core file gate or make test-ai for Python AI.' >&2
    exit 2
fi
exec python3 "$(dirname "$0")/integration/rag_sources.py"
