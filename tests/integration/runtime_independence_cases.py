# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors
"""Run after B publishes both apps and stops Operator; never stops services itself."""
import json
import unittest
import uuid
from tests.integration.helpers import Client, required, db_scalar, domain_snapshot


class RuntimeIndependence(unittest.TestCase):
    def test_I1_I2_S05_fourth_app_while_operator_stopped(self):
        self.assertEqual(required('TEST_OPERATOR_STOPPED'), 'yes')
        client = Client(required('TEST_REQUESTER_TOKEN'), 'role_review')
        case = json.loads(required('TEST_INDEPENDENCE_JSON'))
        fixture = str(uuid.UUID(case['fixture_entity_id']))
        procurement = str(uuid.UUID(case['procurement_entity_id']))
        workspace = str(uuid.UUID(required('TEST_WORKSPACE_ID')))
        before = domain_snapshot(procurement, workspace)
        counts_query = "SELECT json_build_array((SELECT count(*) FROM core.audit_logs WHERE entity_id='%s'),(SELECT count(*) FROM core.outbox_events WHERE entity_id='%s'))" % (procurement, procurement)
        counts_before = db_scalar(counts_query)
        fixture_before = json.loads(db_scalar("SELECT to_jsonb(d) FROM domain.review_requests d WHERE id='%s' AND workspace_id='%s'" % (fixture, workspace)))
        status, result = client.command('process_fixture', 'submit',
            {'entity_id': fixture, 'expected_version': case['expected_version'], 'payload': {}}, str(uuid.uuid4()))
        self.assertEqual(status, 200)
        self.assertEqual(result['status'], 'IN_REVIEW')
        self.assertEqual(result['version'], case['expected_version'] + 1)
        self.assertEqual(domain_snapshot(procurement, workspace), before)
        self.assertEqual(db_scalar(counts_query), counts_before)
        fixture_after = json.loads(db_scalar("SELECT to_jsonb(d) FROM domain.review_requests d WHERE id='%s' AND workspace_id='%s'" % (fixture, workspace)))
        self.assertEqual((fixture_after['status'], fixture_after['version']), ('IN_REVIEW', fixture_before['version'] + 1))
        # Also exercise both apps sharing the same work item and requester.
        fields = {k: fixture_before[k] for k in ('vendor_name', 'item_description', 'quoted_amount', 'quote_file_id')}
        fields['work_item_id'] = before['work_item_id']
        shared = client.graphql('mutation($object:domain_review_requests_insert_input!){insert_domain_review_requests_one(object:$object){id version requester_id work_item_id}}', {'object': fields})['insert_domain_review_requests_one']
        self.assertEqual(shared['requester_id'], before['requester_id'])
        self.assertEqual(shared['work_item_id'], before['work_item_id'])
        shared_status, shared_result = client.command('process_fixture', 'submit', {'entity_id': shared['id'], 'expected_version': shared['version'], 'payload': {}}, str(uuid.uuid4()))
        self.assertEqual(shared_status, 200, shared_result)
        self.assertEqual(shared_result['status'], 'IN_REVIEW')
        self.assertEqual(shared_result['version'], shared['version'] + 1)
        self.assertEqual(domain_snapshot(procurement, workspace), before)
        self.assertEqual(db_scalar(counts_query), counts_before)
        count = db_scalar("SELECT count(*) FROM core.outbox_events WHERE app_id='process_fixture' AND entity_id='%s' AND entity_version=%d" % (fixture, result['version']))
        self.assertEqual(count, '1')
