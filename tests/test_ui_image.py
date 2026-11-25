# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors

"""Static checks for the Workspace UI image and its response headers."""
import re
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]


class WorkspaceUIImage(unittest.TestCase):
    def setUp(self):
        self.conf = (ROOT / 'workspace-ui/nginx.conf').read_text()
        self.dockerfile = (ROOT / 'workspace-ui/Dockerfile').read_text()

    def test_every_response_carries_the_security_headers(self):
        server = self.conf.split('\n    location ', 1)[0]
        self.assertIn('server_tokens off;', server)
        csp = re.search(r'add_header Content-Security-Policy "([^"]+)" always;', server).group(1)
        for directive in ("default-src 'self'", "script-src 'self'", "frame-ancestors 'none'",
                          "connect-src 'self' ${DXOS_OIDC_ORIGIN}"):
            self.assertIn(directive, csp)
        self.assertNotIn('unsafe-inline', csp)
        self.assertIn('add_header X-Content-Type-Options nosniff always;', server)
        self.assertIn('add_header Referrer-Policy no-referrer always;', server)
        # A location with its own add_header would drop the server-level ones.
        self.assertNotIn('add_header', self.conf.split('\n    location ', 1)[1])

    def test_api_location_fails_fast_but_allows_long_reads(self):
        api = self.conf.split('\n    location ', 1)[1]
        self.assertIn('proxy_connect_timeout 3s;', api)
        self.assertIn('proxy_read_timeout 490s;', api)

    def test_runtime_stage_is_pinned_and_renders_the_template(self):
        self.assertRegex(self.dockerfile, r'FROM nginxinc/nginx-unprivileged:[\w.-]+@sha256:[0-9a-f]{64}')
        self.assertIn('COPY nginx.conf /etc/nginx/templates/default.conf.template', self.dockerfile)
        self.assertIn('ENV DXOS_OIDC_ORIGIN=', self.dockerfile)


if __name__ == '__main__':
    unittest.main()
