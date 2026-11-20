#!/usr/bin/env python3
# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors

"""Open-source distribution metadata, SPDX headers, and text encoding."""

from __future__ import annotations

import pathlib
import sys

ROOT = pathlib.Path(__file__).resolve().parent.parent

REQUIRED_FILES = [
    "LICENSE",
    "NOTICE",
    "COPYRIGHT.txt",
    "README.md",
    "CHANGELOG.md",
    "CONTRIBUTING.md",
    "CODE_OF_CONDUCT.md",
    "SECURITY.md",
]
HEADER_EXTENSIONS = {
    ".go",
    ".py",
    ".ts",
    ".tsx",
    ".sql",
    ".sh",
    ".yaml",
    ".yml",
}
HEADER_FILENAMES = {"Makefile", "Dockerfile"}
BANNED_DIRS = {
    "node_modules",
    "vendor",
    ".venv",
    ".venv-docs",
    "venv",
    "__pycache__",
    ".git",
    ".scratch",
    ".vale",
}
BANNED_FILES = set()


def iter_repo_files(root: pathlib.Path):
    """Yield every repository file except vendored and generated directories."""
    for path in sorted(root.rglob("*")):
        if not path.is_file() or path.name in BANNED_FILES:
            continue
        if any(part in BANNED_DIRS for part in path.relative_to(root).parts):
            continue
        yield path


def check_required_files(root: pathlib.Path, errors: list[str]) -> None:
    """Require the standard open-source metadata files to exist and be non-empty."""
    for rel in REQUIRED_FILES:
        target = root / rel
        if not target.is_file() or target.stat().st_size == 0:
            errors.append(f"Missing or empty required file: {rel}")


def check_headers_and_lf(root: pathlib.Path, errors: list[str]) -> None:
    """Reject CRLF and non-UTF-8 text, and require the SPDX header on source files."""
    for path in iter_repo_files(root):
        rel = path.relative_to(root).as_posix()
        raw = path.read_bytes()
        if b"\x00" in raw:
            continue
        if b"\r\n" in raw:
            errors.append(f"CRLF line endings found in: {rel}")
        try:
            text = raw.decode("utf-8")
        except UnicodeDecodeError:
            errors.append(f"Non-UTF-8 text file: {rel}")
            continue

        needs_header = (
            path.suffix in HEADER_EXTENSIONS or path.name in HEADER_FILENAMES
        )
        if needs_header:
            head_lines = "\n".join(text.splitlines()[:6])
            if (
                "SPDX-License-Identifier: Apache-2.0" not in head_lines
                or "Copyright 2026 DX-OS Open-Core Contributors" not in head_lines
            ):
                errors.append(f"Missing SPDX Apache-2.0 header or copyright in: {rel}")


def main() -> int:
    """Run every check, report each failure, and return the process exit code."""
    errors: list[str] = []
    check_required_files(ROOT, errors)
    check_headers_and_lf(ROOT, errors)

    if errors:
        for err in errors:
            print(f"[check-hygiene] FAIL: {err}", file=sys.stderr)
        return 1

    print("[check-hygiene] PASS: open-source metadata files, SPDX headers, and text encoding verified.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
