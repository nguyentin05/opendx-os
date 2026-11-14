# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors
"""Contract consistency checks; these do not exercise runtime authorization."""
import json
import subprocess
import sys
import unittest
from pathlib import Path

import yaml

ROOT = Path(__file__).resolve().parents[1]


class MachineContracts(unittest.TestCase):
    def test_contract_artifacts_exist(self):
        for name in ('manifest-v1.schema.json', 'events-v1.schema.json', 'runtime-v1.openapi.yaml'):
            self.assertTrue((ROOT / 'contracts' / name).is_file(), name)

    def test_manifest_schema_is_generated_from_the_crd(self):
        result = subprocess.run(
            [sys.executable, str(ROOT / 'scripts/gen-manifest-schema.py'), '--check'],
            capture_output=True, text=True,
        )
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_registry_snapshot_matches_manifest(self):
        manifest = yaml.safe_load((ROOT / 'contracts/fixtures/procurement.yaml').read_text())
        app = json.loads((ROOT / 'contracts/fixtures/app-ready.json').read_text())
        self.assertEqual(app['spec'], manifest['spec'])
        self.assertEqual(app['app_id'], manifest['metadata']['name'])
        self.assertEqual(app['version'], manifest['metadata']['version'])
        self.assertEqual(app['status'], 'Ready')

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

    def test_procurement_scopes_and_self_approval_guard(self):
        spec = yaml.safe_load((ROOT / 'contracts/fixtures/procurement.yaml').read_text())['spec']
        table = next(t for t in spec['tables'] if t['name'] == 'domain.procurement_requests')
        insert = table['access']['insert'][0]
        self.assertNotIn('created_by', insert['columns'])
        self.assertNotIn('org_unit_id', insert['columns'])
        scopes = {rule['role']: rule['scope'] for rule in table['access']['select']}
        self.assertEqual(scopes, {
            'role_procurement_requester': 'own',
            'role_procurement_manager': 'department',
            'role_procurement_finance': 'organization',
        })
        for name in ('approve', 'approve_budget'):
            command = next(c for c in table['stateMachine']['commands'] if c['name'] == name)
            self.assertEqual(command['recordFilter'], {'created_by': {'_neq': 'X-Hasura-User-Id'}})
