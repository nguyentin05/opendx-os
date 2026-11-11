# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors
"""Executable probe semantics; controlled servers are not live acceptance."""
import http.server
import os
import socket
import socketserver
import subprocess
import threading
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]


class SupportingServices(unittest.TestCase):
    def probe(self, status=403, kestra=401, scanner=b'PONG\0', unavailable=False):
        class HTTP(http.server.BaseHTTPRequestHandler):
            def do_GET(self):
                code = kestra if self.path == '/kestra' else status
                self.send_response(code)
                self.end_headers()
                self.wfile.write(b'<Error><Code>AccessDenied</Code></Error>')
            do_POST = do_GET
            def log_message(self, *args):
                pass
        class Clam(socketserver.BaseRequestHandler):
            def handle(self):
                self.request.settimeout(2)
                if self.request.recv(100) == b'zPING\0':
                    self.request.sendall(scanner)
        with http.server.ThreadingHTTPServer(('127.0.0.1', 0), HTTP) as web, socketserver.TCPServer(('127.0.0.1', 0), Clam) as clam:
            for server in (web, clam):
                threading.Thread(target=server.serve_forever, daemon=True).start()
            url = 'http://127.0.0.1:' + str(web.server_port)
            if unavailable:
                with socket.socket() as s:
                    s.bind(('127.0.0.1', 0)); port = s.getsockname()[1]
                url = 'http://127.0.0.1:' + str(port)
            env = {**os.environ, 'S3_ENDPOINT': url, 'CLAMAV_ADDRESS': '127.0.0.1:' + str(clam.server_address[1]), 'KESTRA_URL': url + '/kestra'}
            try:
                return subprocess.run(['sh', 'scripts/test-supporting-services.sh'], cwd=ROOT, env=env, capture_output=True, text=True, timeout=12, check=False)
            finally:
                web.shutdown(); clam.shutdown()

    def test_connection_failure_not_pass(self):
        self.assertNotEqual(self.probe(unavailable=True).returncode, 0)

    def test_redirect_not_pass(self):
        self.assertNotEqual(self.probe(status=302).returncode, 0)

    def test_not_found_not_pass(self):
        self.assertNotEqual(self.probe(status=404).returncode, 0)

    def test_server_error_not_pass(self):
        self.assertNotEqual(self.probe(status=500).returncode, 0)

    def test_expected_auth_rejection_pass(self):
        result = self.probe()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn('PASS', result.stdout)

    def test_kestra_redirect_not_pass(self):
        self.assertNotEqual(self.probe(kestra=302).returncode, 0)

    def test_kestra_not_found_not_pass(self):
        self.assertNotEqual(self.probe(kestra=404).returncode, 0)

    def test_kestra_server_error_not_pass(self):
        self.assertNotEqual(self.probe(kestra=500).returncode, 0)

    def test_scanner_not_ready_not_pass(self):
        self.assertNotEqual(self.probe(scanner=b'ERROR\0').returncode, 0)
