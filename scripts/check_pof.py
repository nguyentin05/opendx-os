#!/usr/bin/env python3
# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors

"""Repository hygiene, SPDX license header, and 3-tier boundary checker."""

from __future__ import annotations

import pathlib
import sys

import yaml

ROOT = pathlib.Path(__file__).resolve().parent.parent

REQUIRED_FILES = [
    "LICENSE",
    "LICENSES/LICENSE",
    "NOTICE",
    "COPYRIGHT.txt",
    "README.md",
    "ROADMAP.md",
    "CHANGELOG.md",
    "CONTRIBUTING.md",
    "CODE_OF_CONDUCT.md",
    "SECURITY.md",
    ".editorconfig",
    ".gitattributes",
    ".gitignore",
    ".env.example",
    "Makefile",
    "platform/postgres/init/01_core_schema.sql",
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

BANNED_DIRS = {"node_modules", "vendor", ".venv", "venv", "__pycache__", ".git"}
RUNTIME_NON_DOMAIN_PATHS = ("platform", "operator/internal", "capabilities", "workspace-ui/src")
DOMAIN_APP_NAMES = ("maintenance", "procurement", "timesheet")
SYSTEM_MANAGED_COLUMNS = {"id", "created_by", "org_unit_id", "status", "version", "created_at", "updated_at"}


def iter_repo_files(root: pathlib.Path):
    for path in sorted(root.rglob("*")):
        if not path.is_file():
            continue
        if any(part in BANNED_DIRS for part in path.relative_to(root).parts):
            continue
        yield path


def check_required_files(root: pathlib.Path, errors: list[str]) -> None:
    for rel in REQUIRED_FILES:
        target = root / rel
        if not target.is_file() or target.stat().st_size == 0:
            errors.append(f"Missing or empty required file: {rel}")


def check_headers_and_lf(root: pathlib.Path, errors: list[str]) -> None:
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


def check_tier_boundaries(root: pathlib.Path, errors: list[str]) -> None:
    for path in iter_repo_files(root):
        rel = path.relative_to(root).as_posix()
        if not rel.startswith(RUNTIME_NON_DOMAIN_PATHS):
            continue
        if path.name.endswith(("_test.go", ".test.ts", ".test.tsx")) or path.name.startswith("test_"):
            continue
        text = path.read_text(encoding="utf-8", errors="ignore").lower()
        for app_name in DOMAIN_APP_NAMES:
            if f"domain.{app_name}" in text or f'"{app_name}"' in text or f"'{app_name}'" in text:
                errors.append(f"Domain literal '{app_name}' leaked into non-Tier-3 file: {rel}")


def check_app_manifests(root: pathlib.Path, errors: list[str]) -> None:
    apps_dir = root / "apps"
    if not apps_dir.is_dir():
        return
    for manifest_path in sorted(apps_dir.glob("*/manifest.yaml")):
        rel = manifest_path.relative_to(root).as_posix()
        data = yaml.safe_load(manifest_path.read_text(encoding="utf-8"))
        if not isinstance(data, dict) or data.get("kind") != "DXApplication":
            errors.append(f"{rel}: kind must be DXApplication")
            continue
        spec = data.get("spec", {})
        required_spec_keys = {
            "capabilities",
            "roles",
            "domainTable",
            "domainAccess",
            "stateMachine",
            "uiSchema",
        }
        missing = required_spec_keys - set(spec.keys())
        if missing:
            errors.append(f"{rel}: missing required spec keys: {sorted(missing)}")
            continue

        domain_access = spec.get("domainAccess", {})
        for op in ("insert", "update"):
            for rule in domain_access.get(op, []):
                cols = set(rule.get("columns", []))
                forbidden = cols & SYSTEM_MANAGED_COLUMNS
                if forbidden:
                    errors.append(
                        f"{rel}: domainAccess.{op} for role {rule.get('role')} exposes system-managed columns {sorted(forbidden)}"
                    )

        declared_roles = {role.get("name") for role in spec.get("roles", [])}
        for op in ("select", "insert", "update"):
            for rule in domain_access.get(op, []):
                if rule.get("role") not in declared_roles:
                    errors.append(f"{rel}: domainAccess.{op} uses undeclared role {rule.get('role')}")

        for wf in spec.get("workflows", []):
            wf_file = manifest_path.parent / wf.get("file", "")
            if not wf_file.is_file():
                errors.append(f"{rel}: referenced workflow file does not exist: {wf.get('file')}")


def main() -> int:
    errors: list[str] = []
    check_required_files(ROOT, errors)
    check_headers_and_lf(ROOT, errors)
    check_tier_boundaries(ROOT, errors)
    check_app_manifests(ROOT, errors)

    if errors:
        for err in errors:
            print(f"[check_pof] FAIL: {err}", file=sys.stderr)
        return 1

    print("[check_pof] PASS: repository hygiene, SPDX headers, tier boundaries, and manifests verified.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
