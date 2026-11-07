# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors
"""Contract consistency checks; these do not exercise runtime authorization."""
import json
from pathlib import Path
import unittest
import yaml

ROOT = Path(__file__).resolve().parents[1]


class MachineContracts(unittest.TestCase):
    def test_contract_artifacts_exist(self):
        for name in ('manifest-v1.schema.json', 'events-v1.schema.json', 'runtime-v1.openapi.yaml'):
            self.assertTrue((ROOT / 'contracts' / name).is_file(), name)

    def test_registry_snapshot_matches_manifest(self):
        manifest = yaml.safe_load((ROOT / 'contracts/fixtures/procurement.yaml').read_text())
        registry = json.loads((ROOT / 'contracts/fixtures/registry-ready.json').read_text())
        self.assertEqual(registry['manifest_spec'], manifest['spec'])
        self.assertEqual(registry['state_machine'], manifest['spec']['stateMachine'])
        self.assertEqual(registry['ui_schema'], manifest['spec']['uiSchema'])
        self.assertEqual(registry['status'], 'Ready')

    def test_wire_event_and_retry_keep_identity(self):
        event = json.loads((ROOT / 'contracts/fixtures/event-command-committed.json').read_text())
        retry = json.loads((ROOT / 'contracts/fixtures/event-retry.json').read_text())
        self.assertEqual(event, retry)
        schema = json.loads((ROOT / 'contracts/events-v1.schema.json').read_text())
        self.assertEqual(set(event), set(schema['required']))
        self.assertFalse(schema['additionalProperties'])

    def test_command_body_does_not_allow_server_fields(self):
        api = yaml.safe_load((ROOT / 'contracts/runtime-v1.openapi.yaml').read_text())
        request = api['components']['schemas']['CommandRequest']
        self.assertFalse(request['additionalProperties'])
        self.assertEqual(set(request['properties']), {'entity_id', 'expected_version', 'payload'})
        response = json.loads((ROOT / 'contracts/fixtures/command-committed.json').read_text())
        self.assertEqual(set(response), set(api['components']['schemas']['CommandResponse']['required']))

    def test_procurement_presets_and_self_approval_guard(self):
        spec = yaml.safe_load((ROOT / 'contracts/fixtures/procurement.yaml').read_text())['spec']
        insert = spec['domainAccess']['insert'][0]
        self.assertNotIn('requester_id', insert['columns'])
        self.assertEqual(insert['presets']['requester_id'], 'X-Hasura-User-Id')
        approve = next(c for c in spec['stateMachine']['commands'] if c['name'] == 'approve')
        self.assertEqual(approve['recordFilter'], {'requester_id': {'_neq': 'X-Hasura-User-Id'}})
