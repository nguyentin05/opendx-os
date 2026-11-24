# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors
import unittest

from rag_models import browser_request_allowed


class BrowserRequestTest(unittest.TestCase):
    def test_browser_cannot_call_either_loopback_model_spy(self):
        ports = {80, 9101}
        for host in ('localhost', '127.0.0.1'):
            for port in ('', ':80', ':9101'):
                self.assertFalse(browser_request_allowed(f'http://{host}{port}/invoke', ports))
        self.assertTrue(browser_request_allowed('http://localhost:5173', ports))
        self.assertFalse(browser_request_allowed('https://external.example', ports))
