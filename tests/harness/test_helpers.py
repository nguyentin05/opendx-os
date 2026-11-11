# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors
"""Local transport checks; no fake identity verifier or live acceptance claims."""
import json
import os
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from threading import Thread
from unittest import TestCase
from unittest.mock import patch

from tests.integration.helpers import Client, GraphQLError, PrerequisiteError


class Transport(TestCase):
    def test_required_configuration(self):
        with patch.dict(os.environ, {}, clear=True), self.assertRaisesRegex(PrerequisiteError, 'TEST_PROCESS_URL'):
            Client('signed-token', 'role_procurement')

    def test_headers_retries_and_errors(self):
        seen = []
        class Handler(BaseHTTPRequestHandler):
            def do_POST(self):
                body = json.loads(self.rfile.read(int(self.headers['Content-Length'])))
                seen.append((self.path, dict(self.headers), body))
                self.send_response(409 if '/commands/' in self.path else 200)
                self.end_headers()
                self.wfile.write(json.dumps({'error': {'code': 'Conflict', 'message': 'Conflict'}}
                    if '/commands/' in self.path else {'errors': [{'message': 'denied'}]}).encode())
            def log_message(self, *args):
                pass
        server = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
        thread = Thread(target=server.serve_forever, daemon=True)
        thread.start()
        try:
            url = 'http://127.0.0.1:' + str(server.server_port)
            with patch.dict(os.environ, {'TEST_PROCESS_URL': url, 'TEST_GRAPHQL_URL': url + '/v1/graphql'}):
                client = Client('signed-token', 'role_procurement')
                body = {'entity_id': 'fixture', 'expected_version': 1, 'payload': {}}
                for _ in range(2):
                    status, result = client.command('procurement', 'submit', body, 'stable-key')
                    self.assertEqual(status, 409)
                    self.assertEqual(result['error']['code'], 'Conflict')
                with self.assertRaisesRegex(GraphQLError, 'denied'):
                    client.graphql('query { fixture }', {})
            self.assertEqual(seen[0], seen[1])
            headers = {k.lower(): v for k, v in seen[0][1].items()}
            self.assertEqual(headers['idempotency-key'], 'stable-key')
            self.assertEqual(headers['authorization'], 'Bearer signed-token')
            self.assertEqual(headers['x-hasura-role'], 'role_procurement')
        finally:
            server.shutdown()
            server.server_close()
            thread.join()


class DatabaseSnapshots(TestCase):
    def test_snapshot_maps_complete_domain_row_and_workspace(self):
        from tests.integration.helpers import domain_snapshot
        row = {'status': 'DRAFT', 'version': 2, 'vendor_name': 'Vendor', 'quote_file_id': None}
        with patch('tests.integration.helpers.db_scalar', return_value=json.dumps(row)) as scalar:
            self.assertEqual(domain_snapshot('33333333-3333-4333-8333-333333333333',
                '11111111-1111-4111-8111-111111111111'), row)
        sql = scalar.call_args.args[0]
        self.assertIn('to_jsonb(d)', sql)
        self.assertIn('domain.procurement_requests', sql)
        self.assertIn("workspace_id='11111111-1111-4111-8111-111111111111'", sql)

    def test_snapshot_rejects_missing_row_or_invalid_identifier(self):
        from tests.integration.helpers import domain_snapshot
        with patch('tests.integration.helpers.db_scalar', return_value=''), self.assertRaisesRegex(PrerequisiteError, 'domain row'):
            domain_snapshot('33333333-3333-4333-8333-333333333333',
                    '11111111-1111-4111-8111-111111111111')
        with patch('tests.integration.helpers.db_scalar') as scalar:
            with self.assertRaises(ValueError):
                domain_snapshot("injected'", '11111111-1111-4111-8111-111111111111')
            scalar.assert_not_called()
