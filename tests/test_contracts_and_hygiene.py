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


class TestContractsAndHygiene(unittest.TestCase):
    def test_repository_hygiene_script_passes(self) -> None:
        target_script = ROOT / "scripts" / "check_hygiene.py"
        if not target_script.exists():
            target_script = ROOT / "scripts" / "check_pof.py"
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

    def test_core_schema_invariants(self) -> None:
        kernel_sql = (ROOT / "platform" / "postgres" / "init" / "01_core_schema.sql").read_text(encoding="utf-8")
        cap_reg_sql = (ROOT / "platform" / "postgres" / "init" / "02_capability_registry.sql").read_text(encoding="utf-8")
        app_reg_sql = (ROOT / "platform" / "postgres" / "init" / "03_app_registry.sql").read_text(encoding="utf-8")
        cap_data_sql = (ROOT / "platform" / "postgres" / "capabilities" / "cap_data.sql").read_text(encoding="utf-8")
        cap_process_sql = (ROOT / "platform" / "postgres" / "capabilities" / "cap_process.sql").read_text(encoding="utf-8")
        cap_ai_sql = (ROOT / "platform" / "postgres" / "capabilities" / "cap_ai.sql").read_text(encoding="utf-8")

        kernel_tables = [
            "core.workspaces",
            "core.employees",
            "core.work_items",
            "core.file_objects",
            "core.audit_logs",
            "core.outbox_events",
        ]
        for table in kernel_tables:
            self.assertIn(
                f"CREATE TABLE IF NOT EXISTS {table}",
                kernel_sql,
                f"Missing {table} in 01_core_schema.sql",
            )

        # Registries are cleanly separated in init/ (02_capability_registry.sql & 03_app_registry.sql)
        self.assertIn("CREATE TABLE IF NOT EXISTS core.capability_registry", cap_reg_sql)
        self.assertIn("CREATE TABLE IF NOT EXISTS core.app_registry", app_reg_sql)
        self.assertNotIn("CREATE TABLE IF NOT EXISTS core.capability_registry", kernel_sql)
        self.assertNotIn("CREATE TABLE IF NOT EXISTS core.app_registry", kernel_sql)

        # Tier 2 cap-process module owns command receipts and workflow step idempotency
        cap_process_tables = [
            "core.command_receipts",
            "core.processed_workflow_steps",
        ]
        for table in cap_process_tables:
            self.assertIn(
                f"CREATE TABLE IF NOT EXISTS {table}",
                cap_process_sql,
                f"Missing {table} in cap_process.sql",
            )
            self.assertNotIn(
                f"CREATE TABLE IF NOT EXISTS {table}",
                kernel_sql,
                f"{table} leaked into 01_core_schema.sql",
            )
        self.assertNotIn("CREATE TABLE IF NOT EXISTS core.outbox_events", cap_process_sql)

        # Pure Core Kernel must delegate device, telemetry, and link tables to cap-data (Tier 2)
        self.assertNotIn("CREATE TABLE IF NOT EXISTS core.devices", kernel_sql)
        self.assertNotIn("CREATE TABLE IF NOT EXISTS core.ot_events", kernel_sql)
        self.assertNotIn("source_device_id", kernel_sql)
        self.assertNotIn("CREATE TABLE IF NOT EXISTS domain.", kernel_sql)

        # Tier 2 capabilities are pluggable modules in platform/postgres/capabilities/
        cap_data_tables = [
            "core.devices",
            "core.device_work_items",
            "core.ot_events",
            "core.export_batches",
            "core.data_lineage",
        ]
        for table in cap_data_tables:
            self.assertIn(
                f"CREATE TABLE IF NOT EXISTS {table}",
                cap_data_sql,
                f"Missing {table} in platform/postgres/capabilities/cap_data.sql",
            )
        self.assertIn("cap-data", cap_data_sql)
        self.assertIn("cap-process", cap_process_sql)
        self.assertIn("cap-ai", cap_ai_sql)

        # Serialized per-workspace audit lock & server-side version trigger function must exist
        self.assertIn("pg_advisory_xact_lock(hashtext(NEW.workspace_id::text))", kernel_sql)
        self.assertIn("core.bump_domain_record_version()", kernel_sql)

        # Pluggable capability loader script must exist and be executable
        cap_loader = ROOT / "platform" / "postgres" / "init" / "04_capabilities.sh"
        self.assertTrue(cap_loader.exists(), "Missing 04_capabilities.sh loader")

    def test_contextual_employee_isolation_in_manifests(self) -> None:
        sql = (ROOT / "platform" / "postgres" / "init" / "01_core_schema.sql").read_text(encoding="utf-8")
        # Core employee entity must remain pure without domain-specific attribute pollution
        self.assertNotIn("skill_level", sql)
        self.assertNotIn("work_shift", sql)

        maint = yaml.safe_load(
            (ROOT / "apps" / "maintenance" / "manifest.yaml").read_text(encoding="utf-8")
        )
        ts = yaml.safe_load(
            (ROOT / "apps" / "timesheet" / "manifest.yaml").read_text(encoding="utf-8")
        )

        maint_emp = next(
            item for item in maint["spec"]["sharedAccess"] if item["table"] == "core.employees"
        )
        ts_emp = next(
            item for item in ts["spec"]["sharedAccess"] if item["table"] == "core.employees"
        )

        # Both apps access pure core identity columns
        self.assertEqual(
            maint_emp["columns"],
            ["id", "workspace_id", "full_name", "department"],
        )
        self.assertEqual(
            ts_emp["columns"],
            ["id", "workspace_id", "full_name", "department"],
        )

        # Contextual isolation is enforced via relationship traversal:
        # Maintenance traverses employee -> maintenance_records only
        self.assertEqual(
            [r["targetTable"] for r in maint_emp["relationships"]],
            ["domain.maintenance_records"],
        )

        # Timesheet traverses employee -> timesheet_entries only
        self.assertEqual(
            [r["targetTable"] for r in ts_emp["relationships"]],
            ["domain.timesheet_entries"],
        )


if __name__ == "__main__":
    unittest.main()
