# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""Operational packaging, out-of-tree installation, and backup verification tests."""

from __future__ import annotations

import os
import pathlib
import shutil
import subprocess
import tarfile
import tempfile
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[2]


class TestInstallationAndPackaging(unittest.TestCase):
    def test_package_and_out_of_tree_install_with_spaces(self) -> None:
        res = subprocess.run(
            ["bash", str(ROOT / "scripts" / "package_release.sh"), "0.1.0-demo"],
            cwd=ROOT,
            capture_output=True,
            text=True,
            check=False,
        )
        self.assertEqual(res.returncode, 0, res.stderr)

        archive = ROOT / "dist" / "dxos-open-core-0.1.0-demo.tar.gz"
        checksum = ROOT / "dist" / "dxos-open-core-0.1.0-demo.tar.gz.sha256"
        self.assertTrue(archive.is_file())
        self.assertTrue(checksum.is_file())

        with tarfile.open(archive, "r:gz") as tar:
            names = tar.getnames()
            self.assertIn("dxos-open-core-0.1.0-demo/contracts/manifest-v1.schema.json", names)
            for excluded in (".agents", "AGENTS.md", "CLAUDE.md", "docs/superpowers"):
                self.assertFalse(
                    any(n.startswith(f"dxos-open-core-0.1.0-demo/{excluded}") for n in names),
                    f"Excluded path leaked into archive: {excluded}",
                )

            with tempfile.TemporaryDirectory() as tmp:
                tmp_path = pathlib.Path(tmp)
                extract_dir = tmp_path / "extracted source"
                extract_dir.mkdir()
                tar.extractall(extract_dir)
                extracted_root = extract_dir / "dxos-open-core-0.1.0-demo"
                self.assertFalse((extracted_root / ".git").exists())

                target_dir = tmp_path / "install target with spaces"
                env = os.environ.copy()
                env["GOCACHE"] = "/tmp/opendx-install-gocache"
                inst = subprocess.run(
                    ["bash", str(extracted_root / "scripts" / "install.sh"), str(target_dir)],
                    cwd=extracted_root,
                    env=env,
                    capture_output=True,
                    text=True,
                    check=False,
                )
                self.assertEqual(inst.returncode, 0, inst.stderr)
                self.assertTrue((target_dir / "bin" / "dxos-operator").is_file())
                self.assertTrue((target_dir / "bin" / "cap-process").is_file())
                self.assertTrue((target_dir / "contracts" / "manifest-v1.schema.json").is_file())

                # Missing prerequisite ('go' absent from PATH) must fail explicitly
                no_go_bin = tmp_path / "no-go-bin"
                no_go_bin.mkdir()
                for tool in ("bash", "sh", "dirname", "mkdir", "cp", "chmod", "cat", "rm"):
                    which = shutil.which(tool)
                    if which:
                        os.symlink(which, no_go_bin / tool)
                env_no_go = {"PATH": str(no_go_bin)}
                fail_go = subprocess.run(
                    [str(no_go_bin / "bash"), str(extracted_root / "scripts" / "install.sh"), str(tmp_path / "fail-go")],
                    cwd=extracted_root,
                    env=env_no_go,
                    capture_output=True,
                    text=True,
                    check=False,
                )
                self.assertNotEqual(fail_go.returncode, 0)

                # Missing runtime source directory must fail explicitly
                shutil.rmtree(extracted_root / "capabilities")
                fail_src = subprocess.run(
                    ["bash", str(extracted_root / "scripts" / "install.sh"), str(tmp_path / "fail-src")],
                    cwd=extracted_root,
                    env=env,
                    capture_output=True,
                    text=True,
                    check=False,
                )
                self.assertNotEqual(fail_src.returncode, 0)

    def test_restore_rejects_corrupted_object_digest(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            tmp_path = pathlib.Path(tmp)
            backup_dir = tmp_path / "backup"
            (backup_dir / "objects").mkdir(parents=True)
            (backup_dir / "database.sql").write_text("-- dummy\n", encoding="utf-8")
            obj_file = backup_dir / "objects" / "doc.pdf"
            obj_file.write_bytes(b"original-bytes")
            subprocess.run(
                ["bash", "-c", "find objects -type f -exec sha256sum {} + > objects.sha256"],
                cwd=backup_dir,
                check=True,
            )
            # Tamper with object bytes after checksum generation
            obj_file.write_bytes(b"tampered-bytes")

            env = os.environ.copy()
            env["DATABASE_URL"] = "postgres://invalid"
            env["OBJECTS_DIR"] = str(tmp_path / "restored-objects")
            res = subprocess.run(
                ["bash", str(ROOT / "scripts" / "restore.sh"), str(backup_dir)],
                cwd=ROOT,
                env=env,
                capture_output=True,
                text=True,
                check=False,
            )
            self.assertNotEqual(res.returncode, 0)


if __name__ == "__main__":
    unittest.main()
