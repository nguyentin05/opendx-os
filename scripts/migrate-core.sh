#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors
set -euo pipefail
exec python3 "$(dirname "$0")/migrate_core.py" "$@"
