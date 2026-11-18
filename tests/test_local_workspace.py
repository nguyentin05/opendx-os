# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors

"""Local planning must not become source-hygiene or distribution input."""

import importlib.util
from pathlib import Path
import shutil
import subprocess
import tarfile
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
SPEC = importlib.util.spec_from_file_location("workspace_hygiene", ROOT / "scripts/check_hygiene.py")
HYGIENE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(HYGIENE)


class LocalWorkspaceTests(unittest.TestCase):
    def test_hygiene_skips_local_material_but_checks_project_source(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / ".scratch").mkdir()
            (root / ".scratch/private.py").write_bytes(b"private draft\r\n")
            (root / "public.py").write_text("print('checked')\n")
            errors = []
            HYGIENE.check_headers_and_lf(root, errors)
            self.assertEqual(len(errors), 1, errors)
            self.assertIn("public.py", errors[0])
            self.assertEqual(
                [p.relative_to(root).as_posix() for p in HYGIENE.iter_repo_files(root)],
                ["public.py"],
            )

    def test_release_excludes_private_notes_and_local_guidance(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / "scripts").mkdir()
            shutil.copy2(ROOT / "scripts/package_release.sh", root / "scripts/package_release.sh")
            (root / ".scratch").mkdir()
            (root / ".scratch/private.md").write_text("PRIVATE-NOTES\n")
            (root / "AGENTS.local.md").write_text("PRIVATE-USER-GUIDANCE\n")
            (root / "docs/contracts").mkdir(parents=True)
            (root / "docs/contracts/public.md").write_text("public contract\n")
            (root / "README.md").write_text("public readme\n")
            (root / "docs/design").mkdir()
            (root / "docs/handoff").mkdir()
            (root / "docs/design/shared.md").write_text("shared project design\n")
            (root / "docs/handoff/shared.md").write_text("shared integration handoff\n")
            result = subprocess.run(
                ["bash", str(root / "scripts/package_release.sh"), "local-test"],
                cwd=root, capture_output=True, text=True, check=False,
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            with tarfile.open(root / "dist/dxos-open-core-local-test.tar.gz") as archive:
                names = [name.removeprefix("dxos-open-core-local-test/") for name in archive.getnames()]
                self.assertIn("docs/contracts/public.md", names)
                self.assertIn("README.md", names)
                self.assertIn("docs/design/shared.md", names)
                self.assertIn("docs/handoff/shared.md", names)
                self.assertFalse(any(".scratch" in Path(name).parts for name in names), names)
                self.assertNotIn("AGENTS.local.md", names)


if __name__ == "__main__":
    unittest.main()
