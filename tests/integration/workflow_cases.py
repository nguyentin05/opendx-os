# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors
"""Actual Kestra delivery and durable effects. Never run alongside manual cases."""
import datetime
import json
import os
from pathlib import Path
import subprocess
import time
import unittest
import uuid

from scripts.integration.environment import compose, run
from scripts.integration.seed import LiveSeed
from tests.integration.helpers import Client

ROOT = Path(__file__).resolve().parents[2]


class WorkflowCases(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.live = LiveSeed()
        cls.context = json.loads((ROOT / 'build/integration/automatic.json').read_text())
        if cls.context['phase'] != 'automatic' or cls.context['TEST_EFFECT_DELIVERY_PAUSED'] != 'no':
            raise RuntimeError('fresh automatic phase required; never use manual-phase events')
        cls.files = cls.context['files']

    def fresh(self, app='procurement', submit=True):
        case = self.live.create(app, file_id=self.files['clean_' + app])
        if submit:
            case = self.live.submit(case)
            case['event_id'] = self.live.sql("SELECT id FROM core.outbox_events WHERE entity_id='%s' AND event_type='command.committed'" % str(uuid.UUID(case['entity_id'])))
        return case

    def state(self, event):
        return json.loads(self.live.sql("SELECT json_build_object('id',id,'payload',payload,'delivery_status',delivery_status,'attempt_count',attempt_count,'lease_until',lease_until) FROM core.outbox_events WHERE id='%s'" % str(uuid.UUID(event))))

    def wait(self, predicate, seconds=75):
        deadline = time.monotonic() + seconds
        while time.monotonic() < deadline:
            result = predicate()
            if result:
                return result
            time.sleep(.3)
        self.fail('bounded live workflow deadline exceeded (%ss)' % seconds)

    def effect(self, case):
        raw = self.live.sql("SELECT json_build_object('count',count(*),'rows',json_agg(json_build_object('id',w.id,'workspace',w.workspace_id,'title',w.title,'description',w.description,'priority',w.priority,'assigned',w.assigned_employee_id,'result',s.result,'outcome',s.outcome))) FROM core.processed_workflow_steps s JOIN core.work_items w ON w.id=s.work_item_id AND w.workspace_id=s.workspace_id WHERE event_id='%s' AND step_name='create_review'" % str(uuid.UUID(case['event_id'])))
        value = json.loads(raw)
        return value['rows'][0] if value['count'] == 1 else None

    def assert_effect(self, case):
        row = self.wait(lambda: self.effect(case), int(os.environ.get('TEST_WORKFLOW_DEADLINE', '75')))
        self.assertEqual(row['workspace'], self.live.workspace)
        self.assertEqual(row['title'], 'Review procurement request' if case['app_id'] == 'procurement' else 'Review fixture request')
        self.assertEqual(row['description'], 'Review the submitted request in its source application.')
        self.assertEqual(row['priority'], 'MEDIUM')
        self.assertIsNone(row['assigned'])
        self.assertEqual(row['result'], {'work_item_id': row['id']})
        self.assertEqual(row['outcome'], 'success')
        return row

    def effect_call(self, case, token):
        client = Client(token, 'role_workflow')
        return client.post(client.process_url + '/api/cap/process/effects/work-item', {'event_id': case['event_id'], 'step_name': 'create_review'})

    def test_command_reaches_real_workflow(self):
        for app in ('procurement', 'process_fixture'):
            with self.subTest(app=app):
                case = self.fresh(app)
                self.assert_effect(case)
                self.wait(lambda: self.state(case['event_id'])['delivery_status'] == 'delivered')
                # Deployment helper queries actual Kestra execution state and event identity.
                from scripts.integration.workflows import executions_for
                executions = self.wait(lambda: [e for e in executions_for(self.live, case) if e['state']['current'] == 'SUCCESS'])
                self.assertTrue(any(e['state']['current'] == 'SUCCESS' for e in executions))

    def test_duplicate_delivery_one_effect(self):
        from scripts.integration.workflows import deliver
        case = self.fresh()
        original = self.assert_effect(case)
        for _ in range(2):
            deliver(self.live, case)
        from scripts.integration.workflows import executions_for
        self.wait(lambda: len([e for e in executions_for(self.live, case) if e['state']['current'] == 'SUCCESS']) >= 3)
        self.assertEqual(self.assert_effect(case), original)

    def test_effect_replay_after_commit(self):
        case = self.fresh()
        original = self.assert_effect(case)
        for _ in range(2):
            status, body = self.effect_call(case, self.live.tokens['TEST_WORKFLOW_TOKEN'])
            self.assertEqual(status, 200)
            self.assertEqual(body['work_item_id'], original['id'])
        self.assertEqual(self.assert_effect(case), original)

    def test_workflow_token_renewal(self):
        from scripts.integration.workflows import deploy
        persisted = self.fresh()
        self.assert_effect(persisted)
        from scripts.integration.workflows import executions_for
        existing_ids = {e['id'] for e in self.wait(lambda: executions_for(self.live, persisted))}
        previous = self.live.tokens['TEST_WORKFLOW_TOKEN']
        time.sleep(1)
        self.live = LiveSeed()
        self.assertNotEqual(previous, self.live.tokens['TEST_WORKFLOW_TOKEN'])
        deploy(self.live, reload=True)
        import base64
        from scripts.integration.workflows import verify_ready
        verify_ready(self.live)
        container = json.loads(run(['docker', 'inspect', run(compose('ps', '-q', 'kestra')).strip()]))[0]
        environment = dict(v.split('=', 1) for v in container['Config']['Env'])
        self.assertEqual(base64.b64decode(environment['SECRET_DXOS_WORKFLOW_TOKEN']).decode(), self.live.tokens['TEST_WORKFLOW_TOKEN'])
        self.assertTrue(existing_ids.issubset({e['id'] for e in executions_for(self.live, persisted)}), 'actual Kestra recreation lost persisted execution')
        # A fresh execution proves Kestra consumed the replacement private token.
        self.assert_effect(self.fresh('process_fixture'))

    def test_wrong_workflow_principal_rejected(self):
        # Pause actual workers to establish a zero-effect window for this fresh event.
        run(compose('pause', 'kestra'))
        try:
            case = self.fresh()
            before = self.live.sql('SELECT count(*) FROM core.work_items')
            status, _ = self.effect_call(case, self.live.tokens['TEST_WRONG_WORKFLOW_TOKEN'])
            self.assertEqual(status, 403)
            self.assertIsNone(self.effect(case))
            self.assertEqual(self.live.sql('SELECT count(*) FROM core.work_items'), before)
            # The configured alpha principal must not act on a beta source event.
            beta_file = self.live.upload(actor='beta_requester')
            beta = self.live.submit(self.live.create(actor='beta_requester', file_id=beta_file), actor='beta_requester')
            beta['event_id'] = self.live.sql("SELECT id FROM core.outbox_events WHERE entity_id='%s'" % beta['entity_id'])
            beta_before = self.live.sql('SELECT count(*) FROM core.work_items')
            status, _ = self.effect_call(beta, self.live.tokens['TEST_WORKFLOW_TOKEN'])
            self.assertEqual(status, 403)
            self.assertIsNone(self.effect(beta))
            self.assertEqual(self.live.sql('SELECT count(*) FROM core.work_items'), beta_before)
        finally:
            run(compose('unpause', 'kestra'))
        self.assert_effect(case)

    def test_publisher_restart_preserves_event_identity(self):
        evidence = []
        def record(action, state=None):
            evidence.append({'time': datetime.datetime.now(datetime.timezone.utc).isoformat(), 'action': action, 'state': state})
            from scripts.integration.seed import private_json
            private_json(ROOT / 'build/integration/workflow-recovery.json', evidence)
        run(compose('pause', 'kestra'))
        record('pause real Kestra: no effect completion or transport acknowledgment')
        try:
            case = self.fresh()
            initial = self.wait(lambda: (s if (s := self.state(case['event_id']))['delivery_status'] == 'in_flight' else None), 25)
            record('observed persisted in_flight lease', initial)
            run(compose('kill', '-s', 'SIGKILL', 'cap-process'))
            record('SIGKILL real cap-process')
            # restart policy may already restart it; stopping/restarting explicitly
            # keeps recovery on the same container database and storage volumes.
            run(compose('stop', '--timeout', '2', 'cap-process'))
            killed = self.state(case['event_id'])
            self.assertEqual(killed['id'], initial['id'])
            self.assertEqual(killed['payload'], initial['payload'])
            run(compose('unpause', 'kestra'))
            record('restored real Kestra')
            run(compose('start', 'cap-process'))
            record('started real cap-process without database reset')
            final = self.wait(lambda: (s if (s := self.state(case['event_id']))['delivery_status'] == 'delivered' else None), 100)
            self.assertGreater(final['attempt_count'], initial['attempt_count'])
            self.assertEqual(final['payload'], initial['payload'])
            self.assertEqual(final['id'], initial['id'])
            self.assert_effect(case)
            record('delivered with one persisted effect/result', final)
        finally:
            container = json.loads(run(['docker', 'inspect', run(compose('ps', '-q', 'kestra')).strip()]))[0]
            if container['State']['Paused']:
                run(compose('unpause', 'kestra'))
            run(compose('start', 'cap-process'))
            # Payloads contain no credentials, but operational evidence remains private.
            from scripts.integration.seed import private_json
            private_json(ROOT / 'build/integration/workflow-recovery.json', evidence)
