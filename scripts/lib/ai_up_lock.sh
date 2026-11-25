# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors
#
# Shared deployment-lock location for `scripts/ai-up.sh`.
#
# A Compose project owns one deployment at a time regardless of which checkout
# or state directory invokes `ai-up`. The lock therefore cannot live under the
# per-checkout state directory: two checkouts deploying the same project would
# each lock their own file and cut over concurrently. It lives in a stable
# per-user location keyed by the normalized Compose project name.
#
# `AI_UP_LOCK_DIR` overrides the location so tests can keep their locks private.
# The caller creates the directory owner-only and opens the lock owner-only.

# ai_up_lock_dir prints the directory that holds every project lock for this user.
ai_up_lock_dir() {
    if [ -n "${AI_UP_LOCK_DIR:-}" ]; then
        printf '%s\n' "$AI_UP_LOCK_DIR"
        return 0
    fi
    if [ -n "${XDG_RUNTIME_DIR:-}" ]; then
        printf '%s\n' "$XDG_RUNTIME_DIR/dxos/ai-up"
        return 0
    fi
    printf '%s\n' "${HOME:-/tmp}/.cache/dxos/ai-up"
}

# ai_up_lock_project normalizes a Compose project name (or falls back to the
# checkout directory) to the name used in the lock file.
ai_up_lock_project() {
    local project="${1:-}" root="${2:-}"
    if [ -z "$project" ]; then
        project="$(basename "$root")"
    fi
    printf '%s' "$project" | tr '[:upper:]' '[:lower:]' | tr -cd 'a-z0-9_-'
}

# ai_up_lock_path prints the one lock file for a Compose project.
ai_up_lock_path() {
    local project
    project="$(ai_up_lock_project "${1:-}" "${2:-}")"
    printf '%s/ai-up.%s.lock\n' "$(ai_up_lock_dir)" "${project:-default}"
}
