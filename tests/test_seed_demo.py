# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors
"""The demo quote must be a real one-page PDF, not a placeholder."""

import importlib.util
import re
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]


def load_seed_demo():
    spec = importlib.util.spec_from_file_location('seed_demo', ROOT / 'scripts/seed-demo.py')
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


class SeedDemoPdf(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.seed_demo = load_seed_demo()

    def test_quote_is_a_valid_one_page_pdf(self):
        pdf = self.seed_demo.demo_pdf('FPT Shop', 'Laptop cho nhan vien moi', 18500000)

        self.assertTrue(pdf.startswith(b'%PDF-'))
        self.assertEqual(len(re.findall(rb'/Type\s*/Page\b', pdf)), 1)
        self.assertIn(b'QUOTATION / Bao gia', pdf)
        self.assertIn(b'FPT Shop', pdf)
        self.assertIn(b'(Item: Thiet bi cho nhan vien moi) Tj', self.seed_demo.demo_pdf('X', 'Thiết bị cho nhân viên mới', 1))

        match = re.search(rb'startxref\s+(\d+)\s+%%EOF', pdf)
        self.assertIsNotNone(match)
        xref = int(match.group(1))
        self.assertEqual(pdf[xref:xref + 4], b'xref')
        entries = pdf[xref:].split(b'\n')[3:8]
        for number, entry in enumerate(entries, 1):
            offset = int(entry[:10])
            self.assertTrue(pdf[offset:].startswith(f'{number} 0 obj'.encode()))


if __name__ == '__main__':
    unittest.main()
