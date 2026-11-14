# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors
"""Sample consistency only; B schema/semantic validator is a separate prerequisite."""
import json
import unittest
from pathlib import Path

import yaml

ROOT = Path(__file__).resolve().parents[1]


class AppSamples(unittest.TestCase):
    def test_procurement_matches_runtime_contract_sample(self):
        app = yaml.safe_load((ROOT / 'apps/procurement/manifest.yaml').read_text())
        contract = yaml.safe_load((ROOT / 'contracts/fixtures/procurement.yaml').read_text())
        self.assertEqual(app, contract)

    def test_fourth_app_lifecycle_and_effect(self):
        app = yaml.safe_load((ROOT / 'apps/process-fixture/manifest.yaml').read_text())
        self.assertEqual(app['metadata']['name'], 'process_fixture')
        spec = app['spec']
        self.assertEqual(spec['capabilities'], ['cap-process', 'cap-work'])
        self.assertEqual(spec['stateMachine']['initial'], 'OPEN')
        effect = spec['workflows'][0]['effects'][0]
        self.assertEqual(effect['stepName'], 'create_review')
        self.assertEqual(effect['whenCommands'], ['submit'])
        workflow = yaml.safe_load((ROOT / 'apps/process-fixture/workflows/review_flow.yaml').read_text())
        request = workflow['tasks'][0]['then'][0]
        self.assertIn('trigger.body.event_id', request['body'])
        self.assertIn('DXOS_WORKFLOW_TOKEN', request['headers']['Authorization'])

    def test_actor_signed_identity_mapping(self):
        actors = json.loads((ROOT / 'tests/fixtures/actors.json').read_text())
        for actor in actors['actors'].values():
            self.assertEqual(actor['sub'], actor['user_id'])
            self.assertEqual(actor['sub'], actor['employee_id'])
            self.assertIn(actor['workspace_id'], actors['workspaces'].values())
