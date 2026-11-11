# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors
"""Pinned real Kestra webhook authentication, positive control and no-effects."""
import base64
import json
import time
import unittest
import uuid
from urllib.error import HTTPError
from urllib.parse import urlsplit
from urllib.request import Request, urlopen

from scripts.integration.seed import ROOT, LiveSeed, private_json
from scripts.integration.workflows import api, executions_for, verify_ready


class SupportingServiceCases(unittest.TestCase):
    def wait(self, predicate, seconds=75):
        deadline = time.monotonic() + seconds
        while time.monotonic() < deadline:
            value = predicate()
            if value:
                return value
            time.sleep(.3)
        self.fail('bounded real webhook execution deadline exceeded')

    def test_actual_webhook_key_boundary(self):
        live = LiveSeed()
        verify_ready(live)  # explicit renewal/deployment is a prerequisite
        file = live.upload()
        case = live.submit(live.create(file_id=file))
        case['event_id'] = live.sql("SELECT id FROM core.outbox_events WHERE entity_id='{}'".format(case['entity_id']))
        successful = lambda: [e for e in executions_for(live, case) if e['state']['current'] == 'SUCCESS']
        self.wait(successful)
        self.wait(lambda: live.sql("SELECT delivery_status FROM core.outbox_events WHERE id='{}'".format(case['event_id'])) == 'delivered')
        payload = live.sql("SELECT payload FROM core.outbox_events WHERE id='{}'".format(case['event_id'])).encode()
        route = next(r for r in json.loads((ROOT / 'build/integration/workflow-routes.json').read_text()) if r['workspace_id'] == live.workspace and r['app_id'] == 'procurement')
        path = urlsplit(route['endpoint']).path
        base = 'http://localhost:' + live.config.get('KESTRA_PORT', '18082')
        evidence = []
        def post(p, authorization=None):
            headers = {'Content-Type': 'application/json'}
            if authorization:
                headers['Authorization'] = authorization
            try:
                response = urlopen(Request(base + p, payload, headers), timeout=15)
            except HTTPError as e:
                response = e
            with response:
                raw = response.read()
                return response.status, json.loads(raw) if raw else {}
        def effect_state():
            return live.sql("SELECT json_build_array((SELECT count(*) FROM core.work_items),(SELECT count(*) FROM core.processed_workflow_steps),(SELECT result FROM core.processed_workflow_steps WHERE event_id='{}' AND step_name='create_review'))".format(case['event_id']))
        before_effect = effect_state()
        before_exec = {e['id'] for e in executions_for(live, case)}
        before_total = api(live, '/api/v1/executions/search?size=1')['total']
        valid_basic = 'Basic ' + base64.b64encode((live.config.get('KESTRA_USER', 'admin@dxos.local') + ':' + live.config['KESTRA_PASSWORD']).encode()).decode()
        for label, p, authorization, message in [
            ('missing key/no Basic', path.rsplit('/', 1)[0] + '/', None, 'Not Found'),
            ('wrong key/no Basic', path.rsplit('/', 1)[0] + '/invalid-task7-' + uuid.uuid4().hex, None, 'Not Found: Webhook not found'),
            ('wrong key/valid Basic', path.rsplit('/', 1)[0] + '/invalid-task7-' + uuid.uuid4().hex, valid_basic, 'Not Found: Webhook not found'),
            ('wrong key/invalid Basic', path.rsplit('/', 1)[0] + '/invalid-task7-' + uuid.uuid4().hex, 'Basic aW52YWxpZDppbnZhbGlk', 'Not Found: Webhook not found'),
        ]:
            status, body = post(p, authorization)
            self.assertEqual(status, 404)
            self.assertEqual(body.get('message'), message)
            evidence.append({'case': label, 'status': status, 'message': body['message']})
        # No execution can be queued by a rejected request; inspect actual Kestra
        # repository results, not just HTTP responses or the effect deduplicator.
        self.assertEqual({e['id'] for e in executions_for(live, case)}, before_exec)
        self.assertEqual(api(live, '/api/v1/executions/search?size=1')['total'], before_total)
        self.assertEqual(effect_state(), before_effect)
        for label, authorization in [('correct key/no Basic', None), ('correct key/invalid Basic', 'Basic aW52YWxpZDppbnZhbGlk')]:
            status, body = post(path, authorization)
            self.assertEqual(status, 200)
            execution = body['id']
            self.wait(lambda execution=execution: any(e['id'] == execution and e['state']['current'] == 'SUCCESS' for e in executions_for(live, case)))
            self.assertEqual(effect_state(), before_effect)
            evidence.append({'case': label, 'status': status, 'execution': execution, 'effect': 'deduplicated'})
        private_json(ROOT / 'build/integration/webhook-auth-evidence.json', evidence)
