# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors
"""Run after B publishes both apps and stops Operator; never stops services itself."""
import json
import unittest
import uuid
from tests.integration.helpers import Client, required, db_scalar


class RuntimeIndependence(unittest.TestCase):
    def test_I1_I2_S05_fourth_app_while_operator_stopped(self):
        self.assertEqual(required('TEST_OPERATOR_STOPPED'), 'yes')
        client = Client(required('TEST_REQUESTER_TOKEN'), 'role_review')
        case = json.loads(required('TEST_INDEPENDENCE_JSON'))
        fixture = str(uuid.UUID(case['fixture_entity_id']))
        procurement = str(uuid.UUID(case['procurement_entity_id']))
        query = "SELECT status || ':' || version FROM domain.procurement_requests WHERE id='%s'" % procurement
        before = db_scalar(query)
        self.assertTrue(before, 'B must seed the procurement comparison record')
        status, result = client.command('process_fixture', 'submit',
            {'entity_id': fixture, 'expected_version': case['expected_version'], 'payload': {}}, str(uuid.uuid4()))
        self.assertEqual(status, 200)
        self.assertEqual(result['status'], 'IN_REVIEW')
        self.assertEqual(db_scalar(query), before)
        count = db_scalar("SELECT count(*) FROM core.outbox_events WHERE app_id='process_fixture' AND entity_id='%s' AND entity_version=%d" % (fixture, result['version']))
        self.assertEqual(count, '1')
