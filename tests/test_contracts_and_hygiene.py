# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors

"""Automated contract and hygiene tests for OpenDX-OS."""

from __future__ import annotations

import pathlib
import subprocess
import sys
import unittest

import yaml

ROOT = pathlib.Path(__file__).resolve().parent.parent

CAPABILITIES = ROOT / "capabilities"
GO_MODULE_PREFIX = "github.com/opendx-os/opendx-os/capabilities"

# Intelligence packages live under a single internal/ai/ root. The Agent and
# knowledge runtimes, the model gateway, the provisioning CLI support code and
# the read-scope contracts are siblings; DocIR parsing stays nested inside the
# knowledge package it belongs to.
AI_ROOT = CAPABILITIES / "internal" / "ai"
AI_PACKAGES = {
    "access": "access",
    "agent": "agent",
    "knowledge": "knowledge",
    "knowledge/docir": "docir",
    "models": "models",
    "modelprovisioning": "modelprovisioning",
    "contracts": "contracts",
}

# Pre-consolidation locations. Nothing may import them and they must not exist,
# because a leftover copy silently forks the runtime authority.
STALE_INTERNAL_PACKAGES = (
    "internal/agent",
    "internal/knowledge",
)
# Half-moved packages: these belong next to knowledge, not inside it.
STALE_NESTED_PACKAGES = (
    "internal/knowledge/models",
    "internal/knowledge/modelprovisioning",
    "internal/knowledge/contracts",
)

# Executable tooling that runs the moved packages. A stale path here breaks a
# build or a CI job rather than a document, so it is checked too.
TOOLING_GLOBS = (
    ".github/workflows/*.yml",
    ".github/workflows/*.yaml",
    "scripts/**/*.py",
    "scripts/**/*.sh",
    "Makefile",
)


def _go_source_files(root: pathlib.Path):
    for path in sorted(root.rglob("*.go")):
        if "vendor" in path.parts:
            continue
        yield path


class TestContractsAndHygiene(unittest.TestCase):
    def test_repository_hygiene_script_passes(self) -> None:
        target_script = ROOT / "scripts" / "check_hygiene.py"
        result = subprocess.run(
            [sys.executable, str(target_script)],
            cwd=ROOT,
            capture_output=True,
            text=True,
            check=False,
        )
        self.assertEqual(
            result.returncode,
            0,
            f"{target_script.name} failed:\n{result.stderr}\n{result.stdout}",
        )

    def test_ai_package_layout(self) -> None:
        # The consolidated target layout exists and keeps its package names.
        for rel, package in AI_PACKAGES.items():
            directory = AI_ROOT / rel
            with self.subTest(package=rel, check="directory"):
                self.assertTrue(directory.is_dir(), f"Missing intelligence package: internal/ai/{rel}")
            sources = sorted(directory.glob("*.go")) if directory.is_dir() else []
            with self.subTest(package=rel, check="go sources"):
                self.assertTrue(sources, f"internal/ai/{rel} contains no Go sources")
            declarations = {
                line.split()[1]
                for line in "\n".join(
                    path.read_text(encoding="utf-8") for path in sources[:3]
                ).splitlines()
                if line.startswith("package ")
            }
            with self.subTest(package=rel, check="package clause"):
                self.assertEqual(
                    declarations,
                    {package},
                    f"internal/ai/{rel} must declare `package {package}`",
                )

        # No pre-consolidation or half-moved package may survive.
        for rel in STALE_INTERNAL_PACKAGES + STALE_NESTED_PACKAGES:
            with self.subTest(stale=rel):
                self.assertFalse(
                    (CAPABILITIES / rel).exists(),
                    f"Stale package still exists: capabilities/{rel}",
                )

        # No Go source may import a pre-consolidation path.
        stale_imports = tuple(f'"{GO_MODULE_PREFIX}/{rel}' for rel in STALE_INTERNAL_PACKAGES)
        offenders = [
            f"{path.relative_to(ROOT).as_posix()}"
            for path in _go_source_files(CAPABILITIES)
            if any(stale in path.read_text(encoding="utf-8") for stale in stale_imports)
        ]
        self.assertEqual(offenders, [], f"Stale internal/agent or internal/knowledge imports: {offenders}")

        # Executable tooling must reference the consolidated paths too.
        tooling_offenders = []
        for pattern in TOOLING_GLOBS:
            for path in sorted(ROOT.glob(pattern)):
                if not path.is_file():
                    continue
                text = path.read_text(encoding="utf-8", errors="ignore")
                for rel in STALE_INTERNAL_PACKAGES:
                    if f"/{rel}" in text or f'"{rel}' in text or f"'{rel}" in text:
                        tooling_offenders.append(f"{path.relative_to(ROOT).as_posix()} -> {rel}")
        self.assertEqual(tooling_offenders, [], f"Stale package path in tooling: {tooling_offenders}")

    def test_core_schema_invariants(self) -> None:
        init_dir = ROOT / "platform" / "postgres" / "init"
        cap_dir = ROOT / "platform" / "postgres" / "capabilities"
        kernel_sql = (init_dir / "01_core_schema.sql").read_text(encoding="utf-8")
        cap_reg_sql = (init_dir / "02_capability_registry.sql").read_text(encoding="utf-8")
        app_reg_sql = (init_dir / "03_app_registry.sql").read_text(encoding="utf-8")
        cap_sql = {
            path.stem: path.read_text(encoding="utf-8") for path in sorted(cap_dir.glob("cap_*.sql"))
        }
        init_sql = {path.name: path.read_text(encoding="utf-8") for path in sorted(init_dir.glob("*.sql"))}

        # Tier 1 kernel: identity, files, audit and events only.
        for table in ("core.users", "core.files", "core.audit_logs", "core.outbox_events"):
            self.assertIn(
                f"CREATE TABLE IF NOT EXISTS {table}",
                kernel_sql,
                f"Missing {table} in 01_core_schema.sql",
            )

        # Registries live in their own files, not in the kernel.
        self.assertIn("CREATE TABLE IF NOT EXISTS core.capabilities", cap_reg_sql)
        for table in ("core.apps", "core.roles", "core.user_roles"):
            self.assertIn(f"CREATE TABLE IF NOT EXISTS {table}", app_reg_sql)
            self.assertNotIn(f"CREATE TABLE IF NOT EXISTS {table}", kernel_sql)
        self.assertNotIn("CREATE TABLE IF NOT EXISTS core.capabilities", kernel_sql)

        # One installation is one organization: no workspace concept remains.
        for name, sql in {**init_sql, **{f"{k}.sql": v for k, v in cap_sql.items()}}.items():
            self.assertNotIn("workspace", sql.lower(), f"{name} still refers to workspaces")

        # Tier 1 never depends on Tier 2: no table or key in init/ points at a capability schema.
        for name, sql in init_sql.items():
            self.assertNotRegex(sql, r"REFERENCES\s+cap_", f"{name} references a Tier 2 table")
            self.assertNotRegex(
                sql, r"CREATE TABLE IF NOT EXISTS\s+(cap_|domain\.)", f"{name} creates a Tier 2/3 table"
            )

        # Each Tier 2 capability owns its schema and registers itself.
        expected_tables = {
            "cap_hr": ("cap_hr.org_units", "cap_hr.staff"),
            "cap_work": ("cap_work.work_items",),
            "cap_process": ("cap_process.command_receipts", "cap_process.processed_workflow_steps"),
            "cap_data": (
                "cap_data.devices",
                "cap_data.ot_events",
                "cap_data.export_batches",
                "cap_data.data_lineage",
            ),
            "cap_ai": (),
        }
        self.assertEqual(sorted(cap_sql), sorted(expected_tables))
        for cap, tables in expected_tables.items():
            sql = cap_sql[cap]
            self.assertIn("INSERT INTO core.capabilities", sql, f"{cap}.sql does not register itself")
            self.assertIn(cap.replace("_", "-"), sql)
            for table in tables:
                self.assertIn(f"CREATE TABLE IF NOT EXISTS {table}", sql, f"Missing {table} in {cap}.sql")
                self.assertNotIn(f"CREATE TABLE IF NOT EXISTS {table}", kernel_sql)

        # Audit chains are serialized per application; versions are bumped server-side.
        self.assertIn("pg_advisory_xact_lock(hashtext('dxos.audit:' || NEW.chain))", kernel_sql)
        self.assertIn("core.bump_domain_record_version()", kernel_sql)

        # Pluggable capability loader script must exist.
        self.assertTrue((init_dir / "04_capabilities.sh").exists(), "Missing 04_capabilities.sh loader")

    def test_contextual_employee_isolation_in_manifests(self) -> None:
        # Users are pure identity; department data belongs to cap_hr and
        # application-specific attributes to each application's domain table.
        for path in (
            ROOT / "platform" / "postgres" / "init" / "01_core_schema.sql",
            ROOT / "platform" / "postgres" / "capabilities" / "cap_hr.sql",
        ):
            sql = path.read_text(encoding="utf-8")
            self.assertNotIn("skill_level", sql)
            self.assertNotIn("work_shift", sql)

        for app in ("maintenance", "timesheet"):
            manifest = yaml.safe_load(
                (ROOT / "apps" / app / "manifest.yaml").read_text(encoding="utf-8")
            )
            users = next(
                item for item in manifest["spec"]["sharedAccess"] if item["table"] == "core.users"
            )
            # Applications read identity columns only.
            self.assertEqual(users["columns"], ["id", "display_name"], app)
            # Department scoping comes from cap-hr, never from a column the app owns.
            self.assertIn("cap-hr", manifest["spec"]["capabilities"], app)
            columns = {c["name"] for t in manifest["spec"]["tables"] for c in t["columns"]}
            self.assertNotIn("org_unit_id", columns, app)
            self.assertNotIn("department", columns, app)


if __name__ == "__main__":
    unittest.main()
