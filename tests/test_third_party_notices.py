# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors

import pathlib
import tempfile
import unittest

from scripts import third_party_notices as notices


class PythonNoticesTest(unittest.TestCase):
    def test_collects_wheel_files_for_locked_runtime_only(self):
        with tempfile.TemporaryDirectory() as temporary:
            site = pathlib.Path(temporary)
            for name in ("direct", "transitive", "dev"):
                metadata = site / f"{name}-1.dist-info"
                licenses = metadata / "licenses"
                licenses.mkdir(parents=True)
                (metadata / "METADATA").write_text(
                    f"Name: {name}\nVersion: 1\nLicense-File: LICENSE\n"
                )
                (metadata / "RECORD").write_text(
                    f"{metadata.name}/licenses/LICENSE,,\n"
                    f"{metadata.name}/licenses/NOTICE,,\n"
                )
                for filename in ("LICENSE", "NOTICE"):
                    (licenses / filename).write_text(f"{name} upstream {filename}")

            result = "".join(notices.python_sections(
                [("transitive", "1"), ("direct", "1"), ("missing", "1"), ("direct", "2")], site
            ))
            self.assertIn("direct upstream LICENSE", result)
            self.assertIn("transitive upstream NOTICE", result)
            self.assertNotIn("dev upstream", result)
            self.assertIn("missing 1", result)
            self.assertIn("direct 2", result)
            self.assertEqual(result.count("locked distribution metadata unavailable"), 2)
            self.assertIn("license expression unverified", result)

    def test_reports_missing_license_text_even_with_notice(self):
        with tempfile.TemporaryDirectory() as temporary:
            site = pathlib.Path(temporary)
            metadata = site / "example-1.dist-info"
            metadata.mkdir()
            (metadata / "METADATA").write_text(
                "Name: example\nVersion: 1\nLicense-Expression: MIT\nLicense-File: LICENSE\n"
            )
            (metadata / "RECORD").write_text("example-1.dist-info/NOTICE,,\n")
            (metadata / "NOTICE").write_text("upstream notice")
            result, = notices.python_sections([("example", "1")], site)
            self.assertIn("upstream notice", result)
            self.assertIn("license text unavailable", result)
            self.assertIn("declared file unavailable: LICENSE", result)

    def test_uses_reviewed_source_license_only_for_exact_version(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = pathlib.Path(temporary)
            site = root / "site"
            metadata = site / "example-1.dist-info"
            metadata.mkdir(parents=True)
            (metadata / "METADATA").write_text("Name: example\nVersion: 1\n")
            license_file = root / "LICENSE"
            license_file.write_text("upstream copyright and license")
            reviewed = {"example 1": {
                "license": "MIT", "source": "https://example.org/exact/LICENSE",
                "files": ["LICENSE"],
            }}
            first, second = notices.python_sections(
                [("example", "1"), ("example", "2")], site, reviewed, root
            )
            self.assertIn("upstream copyright and license", first)
            self.assertIn("https://example.org/exact/LICENSE", first)
            self.assertNotIn("UNVERIFIED:", first)
            self.assertIn("locked distribution metadata unavailable", second)
