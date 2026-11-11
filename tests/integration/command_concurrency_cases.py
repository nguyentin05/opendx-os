# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors
"""Live transactional command checks against the frozen manual phase."""
import json
import threading
import unittest
import uuid
from concurrent.futures import ThreadPoolExecutor

from tests.integration import acceptance_cases
from tests.integration.helpers import Client, GraphQLError, domain_snapshot, required


class CommandConcurrency(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        acceptance_cases.BackendAcceptance.setUpClass.__func__(cls)

    counts = acceptance_cases.BackendAcceptance.counts
    protected = acceptance_cases.BackendAcceptance.protected

    def create(self):
        source = domain_snapshot(self.cases['denied']['entity_id'], self.workspace)
        values = {k: source[k] for k in ('work_item_id', 'vendor_name', 'item_description', 'quoted_amount', 'quote_file_id')}
        row = self.requester.graphql('mutation($object:domain_procurement_requests_insert_input!){insert_domain_procurement_requests_one(object:$object){id version status}}', {'object': values})['insert_domain_procurement_requests_one']
        self.assertEqual(row['status'], 'DRAFT')
        return {'entity_id': row['id'], 'expected_version': row['version'], 'payload': {}}

    def test_concurrent_same_version_one_commit(self):
        body = self.create()
        before = json.loads(self.counts(body))
        barrier = threading.Barrier(2)
        def submit(key):
            barrier.wait(timeout=10)
            return self.requester.command('procurement', 'submit', body, key)
        keys = [str(uuid.uuid4()), str(uuid.uuid4())]
        with ThreadPoolExecutor(max_workers=2) as pool:
            futures = [pool.submit(submit, key) for key in keys]
            responses = [future.result(timeout=30) for future in futures]
        self.assertEqual(sorted(status for status, _ in responses), [200, 409], responses)
        row = domain_snapshot(body['entity_id'], self.workspace)
        self.assertEqual((row['status'], row['version']), ('SUBMITTED', body['expected_version'] + 1))
        self.assertEqual(json.loads(self.counts(body)), [n + 1 for n in before])
        # Repeating the losing key must leave the complete committed snapshot intact.
        loser = next(key for key, response in zip(keys, responses) if response[0] == 409)
        snapshot = self.protected(body)
        self.assertEqual(self.requester.command('procurement', 'submit', body, loser)[0], 409)
        self.assertEqual(self.protected(body), snapshot)

    def test_crud_version_invalidates_command(self):
        body = self.create()
        counts = json.loads(self.counts(body))
        result = self.requester.graphql('mutation($id:uuid!){update_domain_procurement_requests_by_pk(pk_columns:{id:$id},_set:{vendor_name:"Version edit"}){version}}', {'id': body['entity_id']})
        self.assertEqual(result['update_domain_procurement_requests_by_pk']['version'], body['expected_version'] + 1)
        self.assertEqual(json.loads(self.counts(body)), [counts[0], counts[1] + 1])
        snapshot = self.protected(body)
        self.assertEqual(self.requester.command('procurement', 'submit', body, str(uuid.uuid4()))[0], 409)
        self.assertEqual(self.protected(body), snapshot)

    def test_replay_rechecks_permission(self):
        body, key = self.create(), str(uuid.uuid4())
        first = self.requester.command('procurement', 'submit', body, key)
        self.assertEqual(first[0], 200)
        before = self.protected(body)
        # Same signed actor, key and payload, but a different granted current role.
        changed_role = Client(required('TEST_REQUESTER_TOKEN'), 'role_review')
        self.assertEqual(changed_role.command('procurement', 'submit', body, key)[0], 403)
        self.assertEqual(self.protected(body), before)
        self.assertEqual(self.requester.command('procurement', 'submit', body, key), first)
        self.assertEqual(self.protected(body), before)

    def test_protected_updates_and_submitted_edit_are_denied(self):
        body = self.create()
        for field, value in [('version', 99), ('requester_id', '22222222-2222-4222-8222-000000000002')]:
            before = self.protected(body)
            with self.assertRaises(GraphQLError):
                self.requester.graphql('mutation($id:uuid!,$set:domain_procurement_requests_set_input!){update_domain_procurement_requests_by_pk(pk_columns:{id:$id},_set:$set){id}}', {'id': body['entity_id'], 'set': {field: value}})
            self.assertEqual(self.protected(body), before)
        self.assertEqual(self.requester.command('procurement', 'submit', body, str(uuid.uuid4()))[0], 200)
        before = self.protected(body)
        result = self.requester.graphql('mutation($id:uuid!){update_domain_procurement_requests_by_pk(pk_columns:{id:$id},_set:{vendor_name:"Forbidden edit"}){id}}', {'id': body['entity_id']})
        self.assertIsNone(result['update_domain_procurement_requests_by_pk'])
        self.assertEqual(self.protected(body), before)
