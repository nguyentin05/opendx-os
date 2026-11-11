# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors
"""Real seed orchestration; missing services cause failure rather than skip."""
import json
import os
import subprocess
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]

class SeedCases(unittest.TestCase):
    def test_manual_phase_writes_only_real_context(self):
        result = subprocess.run(['python3','scripts/integration/seed.py','--phase','manual'],cwd=ROOT,env={**os.environ,'TEST_DISPOSABLE_DATABASE':'yes'},capture_output=True,text=True, check=False)
        self.assertEqual(result.returncode,0,result.stderr)
        path=ROOT/'build/integration/manual.json'
        self.assertEqual(path.stat().st_mode & 0o777,0o600)
        context=json.loads(path.read_text())
        self.assertEqual(context['TEST_EFFECT_DELIVERY_PAUSED'],'yes')
        cases=json.loads(context['TEST_CASES_JSON'])
        connections=json.loads((ROOT/'build/integration/connections.json').read_text())
        for name,case in cases.items():
            query="SELECT status||':'||version FROM domain.procurement_requests WHERE id='{}'".format(case['entity_id'])
            row=subprocess.run(['psql',connections['assertion'],'-XAtqc',query],capture_output=True,text=True,check=True).stdout.strip()
            self.assertTrue(row,name)
            self.assertGreater(case['expected_version'],0)
