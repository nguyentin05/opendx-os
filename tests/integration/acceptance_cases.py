# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors
"""Opt-in live acceptance subset. B prepares unique entities per named case."""
import json
import unittest
import uuid

from tests.integration.helpers import Client, db_scalar, domain_snapshot, required


class BackendAcceptance(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.requester = Client(required('TEST_REQUESTER_TOKEN'), 'role_procurement')
        cls.manager = Client(required('TEST_MANAGER_TOKEN'), 'role_manager_procurement')
        cls.noaccess = Client(required('TEST_NOACCESS_TOKEN'), 'role_noaccess')
        cls.selfapprover = Client(required('TEST_SELFAPPROVER_TOKEN'), 'role_manager_procurement')
        cls.beta = Client(required('TEST_BETA_REQUESTER_TOKEN'), 'role_procurement')
        cls.workflow = Client(required('TEST_WORKFLOW_TOKEN'), 'role_workflow')
        cls.cases = json.loads(required('TEST_CASES_JSON'))
        cls.workspace = str(uuid.UUID(required('TEST_WORKSPACE_ID')))
        if required('TEST_ISOLATED_WORKLOAD') != 'yes' or required('TEST_EFFECT_DELIVERY_PAUSED') != 'yes':
            raise AssertionError('B must isolate workspace writes and pause automatic effect execution')
        # Explicit read prerequisite; never silently skip an absent DB.
        if db_scalar('SELECT 1') != '1':
            raise AssertionError('Disposable DB not ready')

    def body(self, name):
        case = self.cases[name]
        if name == 'retry':
            source = domain_snapshot(case['entity_id'], self.workspace)
            values = {k: source[k] for k in ('work_item_id', 'vendor_name', 'item_description', 'quoted_amount', 'quote_file_id')}
            row = self.requester.graphql('mutation($object:domain_procurement_requests_insert_input!){insert_domain_procurement_requests_one(object:$object){id version}}', {'object': values})['insert_domain_procurement_requests_one']
            case = {'entity_id': row['id'], 'expected_version': row['version']}
        return {'entity_id': str(uuid.UUID(case['entity_id'])),
                'expected_version': case['expected_version'], 'payload': {}}

    def counts(self, body):
        entity = str(uuid.UUID(body['entity_id']))
        return db_scalar(f"SELECT json_build_array((SELECT count(*) FROM core.outbox_events WHERE entity_id='{entity}'), (SELECT count(*) FROM core.audit_logs WHERE entity_id='{entity}'))")

    def protected(self, body):
        return domain_snapshot(body['entity_id'], self.workspace), self.counts(body)

    def test_S01_denied_role_has_no_side_effect(self):
        body = self.body('denied')
        before = self.protected(body)
        status, result = self.noaccess.command('procurement', 'submit', body, str(uuid.uuid4()))
        self.assertEqual(status, 403)
        self.assertIn('error', result)
        self.assertEqual(before, self.protected(body))

    def test_S01_self_approval_and_other_workspace_denied(self):
        body = self.body('selfapprove')
        before = self.protected(body)
        self.assertEqual(self.selfapprover.command('procurement', 'approve', body, str(uuid.uuid4()))[0], 403)
        self.assertEqual(before, self.protected(body))
        before = self.protected(body)
        self.assertEqual(self.beta.command('procurement', 'submit', body, str(uuid.uuid4()))[0], 404)
        self.assertEqual(before, self.protected(body))

    def test_S03_same_key_replay_and_conflict(self):
        body = self.body('retry')
        key = str(uuid.uuid4())
        before = json.loads(self.counts(body))
        first = self.requester.command('procurement', 'submit', body, key)
        self.assertEqual(first[0], 200)
        self.assertEqual(first, self.requester.command('procurement', 'submit', body, key))
        self.assertEqual(first[1]['version'], body['expected_version'] + 1)
        changed = dict(body, expected_version=body['expected_version'] + 1)
        denied_before = self.protected(body)
        self.assertEqual(self.requester.command('procurement', 'submit', changed, key)[0], 409)
        self.assertEqual(denied_before, self.protected(body))
        self.assertEqual(json.loads(self.counts(body)), [x + 1 for x in before])
        event = db_scalar(f"SELECT id FROM core.outbox_events WHERE app_id='procurement' AND entity_id='{body['entity_id']}' AND entity_version={first[1]['version']}")
        event = str(uuid.UUID(event))
        effect = {'event_id': event, 'step_name': 'create_review'}
        url = self.workflow.process_url + '/api/cap/process/effects/work-item'
        work_count_sql = f"SELECT count(*) FROM core.work_items WHERE workspace_id='{self.workspace}'"
        work_count_before = int(db_scalar(work_count_sql))
        first_effect = self.workflow.post(url, effect)
        self.assertEqual(first_effect[0], 200)
        self.assertEqual(int(db_scalar(work_count_sql)), work_count_before + 1)
        self.assertEqual(first_effect, self.workflow.post(url, effect))
        self.assertEqual(int(db_scalar(work_count_sql)), work_count_before + 1)
        self.assertEqual(db_scalar(f"SELECT count(*) FROM core.processed_workflow_steps WHERE event_id='{event}' AND step_name='create_review'"), '1')
        work_item = str(uuid.UUID(first_effect[1]['work_item_id']))
        item = json.loads(db_scalar(f"SELECT json_build_object('workspace_id',workspace_id,'title',title,'description',description,'priority',priority,'assigned_employee_id',assigned_employee_id) FROM core.work_items WHERE id='{work_item}'"))
        self.assertEqual(item, {'workspace_id': self.workspace, 'title': 'Review procurement request',
            'description': 'Review the submitted request in its source application.',
            'priority': 'MEDIUM', 'assigned_employee_id': None})

    def test_S04_stale_has_no_side_effect(self):
        body = self.body('stale')  # Submitted; expected_version deliberately older.
        before = self.protected(body)
        self.assertEqual(self.manager.command('procurement', 'approve', body, str(uuid.uuid4()))[0], 409)
        self.assertEqual(before, self.protected(body))

    def test_S06_quarantine_has_no_side_effect(self):
        body = self.body('quarantined')
        before = self.protected(body)
        status, result = self.requester.command('procurement', 'submit', body, str(uuid.uuid4()))
        self.assertEqual(status, 422)
        self.assertIn('error', result)
        self.assertEqual(before, self.protected(body))

    def test_S01_authentication_and_permission_before_resource_details(self):
        body = self.body('quarantined')
        before = self.protected(body)
        invalid = Client('invalid-bearer', 'role_procurement')
        self.assertEqual(invalid.command('procurement', 'submit', body, str(uuid.uuid4()))[0], 401)
        self.assertEqual(self.protected(body), before)
        denied = self.noaccess.command('procurement', 'submit', body, str(uuid.uuid4()))
        self.assertEqual(denied[0], 403)
        self.assertEqual(self.protected(body), before)
        missing = dict(body, entity_id=str(uuid.uuid4()))
        self.assertEqual(self.noaccess.command('procurement', 'submit', missing, str(uuid.uuid4())), denied)
        self.assertEqual(self.protected(body), before)
