#!/usr/bin/env python3
# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors

"""Tier 1 and Tier 2 runtime code must not reference Tier 3 applications."""

from __future__ import annotations

import pathlib
import sys

ROOT = pathlib.Path(__file__).resolve().parent.parent

RUNTIME_NON_DOMAIN_PATHS = ("platform", "operator/internal", "capabilities", "workspace-ui/src")
SKIPPED_DIRS = {"node_modules", "vendor", "__pycache__"}


def domain_app_names(root: pathlib.Path) -> tuple[str, ...]:
    """Return the Tier 3 application names from the package directories under apps/."""
    apps = root / "apps"
    if not apps.is_dir():
        return ()
    return tuple(sorted(path.name.replace("-", "_") for path in apps.iterdir() if path.is_dir()))


def iter_runtime_files(root: pathlib.Path):
    """Yield files in the shared runtime paths, skipping vendored and generated directories."""
    for base in RUNTIME_NON_DOMAIN_PATHS:
        for path in sorted((root / base).rglob("*")):
            if not path.is_file():
                continue
            if any(part in SKIPPED_DIRS for part in path.parts):
                continue
            yield path


def check_tier_boundaries(root: pathlib.Path, errors: list[str]) -> None:
    """Reject domain application names in Tier 1 and Tier 2 runtime paths."""
    app_names = domain_app_names(root)
    for path in iter_runtime_files(root):
        rel = path.relative_to(root).as_posix()
        if path.name.endswith(("_test.go", ".test.ts", ".test.tsx")) or path.name.startswith("test_"):
            continue
        text = path.read_text(encoding="utf-8", errors="ignore").lower()
        for app_name in app_names:
            if f"domain.{app_name}" in text or f'"{app_name}"' in text or f"'{app_name}'" in text:
                errors.append(f"Domain literal '{app_name}' leaked into non-Tier-3 file: {rel}")


def main() -> int:
    """Report Tier 3 application names used by Tier 1 or Tier 2 code."""
    errors: list[str] = []
    check_tier_boundaries(ROOT, errors)

    if errors:
        for err in errors:
            print(f"[check-architecture] FAIL: {err}", file=sys.stderr)
        return 1

    print("[check-architecture] PASS: Tier 1 and Tier 2 runtime code stays domain-agnostic.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
