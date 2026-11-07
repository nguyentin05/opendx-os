# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors
"""Outage cleanup must restore later services after an earlier failure."""
import unittest
from tests.integration.file_cases import restore_services


class CleanupTests(unittest.TestCase):
    def test_all_restorations_attempted_and_failure_reported(self):
        attempted = []
        def failed():
            attempted.append('storage')
            raise OSError('private diagnostic must not escape')
        with self.assertRaisesRegex(RuntimeError, 'OSError') as caught:
            restore_services(failed, lambda: attempted.append('process'),
                             lambda: attempted.append('scanner'))
        self.assertEqual(attempted, ['storage', 'process', 'scanner'])
        self.assertNotIn('private diagnostic', str(caught.exception))
