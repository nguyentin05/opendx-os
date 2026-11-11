# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors
"""Workflow deployment lifecycle at the Compose boundary; no live acceptance claim."""
import base64
import contextlib
import io
import json
import tempfile
import time
import unittest
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import patch

from scripts.integration import workflows


class WorkflowDeploymentTests(unittest.TestCase):
    def test_route_version_change_forces_process_startup_reload(self):
        # A private mounted-file rewrite alone is invisible to Compose's image/
        # environment change detection. Deployment must request recreation.
        workspace, subject = 'workspace', 'service-subject'
        claims = {'sub': subject, 'exp': int(time.time()) + 300,
                  'https://hasura.io/jwt/claims': {
                      'x-hasura-workspace-id': workspace,
                      'x-hasura-allowed-roles': ['role_workflow']}}
        payload = base64.urlsafe_b64encode(json.dumps(claims).encode()).decode().rstrip('=')
        live = SimpleNamespace(config={'KESTRA_PASSWORD': 'fixture-only-password'},
                               tokens={'TEST_WORKFLOW_TOKEN': 'header.' + payload + '.signature'},
                               fixture={'actors': {'workflow': {'sub': subject, 'workspace_id': workspace,
                                                                'roles': ['role_workflow']}}})
        published = {'workspace_id': workspace, 'app_id': 'example', 'version': '1'}
        flow = {'namespace': 'example', 'id': 'review'}
        calls = []
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / 'build/integration').mkdir(parents=True)
            (root / '.env.integration').touch(mode=0o600)
            with patch.object(workflows, 'ROOT', root), \
                 patch.object(workflows, 'declarations', return_value=[(published, flow)]), \
                 patch.object(workflows, 'provision_database'), \
                 patch.object(workflows, 'api'), \
                 patch.object(workflows, 'run', side_effect=lambda command: calls.append(command) or ''), \
                 contextlib.redirect_stdout(io.StringIO()):
                workflows.deploy(live)
                environment = (root / '.env.integration').read_text()
                before = json.loads((root / 'build/integration/workflow-routes.json').read_text())
                published['version'] = '2'
                workflows.deploy(live)
                after = json.loads((root / 'build/integration/workflow-routes.json').read_text())
                self.assertEqual((root / '.env.integration').read_text(), environment)
            self.assertEqual(before[0]['version'], '1')
            self.assertEqual(after[0]['version'], '2')
        process_updates = [c for c in calls if c[-1] == 'cap-process']
        self.assertEqual(len(process_updates), 2)
        for command in process_updates:
            self.assertIn('--force-recreate', command,
                          'startup-loaded routes require recreation even with unchanged image/environment')


    def test_existing_replication_role_is_refused(self):
        queries = []
        def sql(query, role):
            queries.append(query)
            if "count(*)" in query:
                return '1'
            # PostgreSQL expression has replication=true and all other flags=false.
            return 't' if 'rolreplication' in query else 'f'
        live = SimpleNamespace(config={'KESTRA_DATABASE_PASSWORD': 'fixtureonly'}, sql=sql)
        with self.assertRaises(workflows.Refused):
            workflows.provision_database(live)

    def test_automatic_phase_refuses_stopped_or_paused_publisher(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / 'build/integration').mkdir(parents=True)
            (root / 'build/integration/workflow-routes.json').write_text('[]')
            live = SimpleNamespace(workspace='workspace', config={
                'KESTRA_AUTH_SCHEME': 'Basic',
                'KESTRA_ROUTES_FILE': '/app/config/workflow-routes.json'})
            for state in ({'Running': False, 'Paused': False}, {'Running': True, 'Paused': True}):
                container = {'State': state, 'Config': {'Env': []}}
                def run(command, container=container):
                    return json.dumps([container, container]) if 'inspect' in command else 'container'
                with self.subTest(state=state), patch.object(workflows, 'ROOT', root), \
                     patch.object(workflows, 'declarations', return_value=[]), \
                     patch.object(workflows, 'run', side_effect=run), \
                     self.assertRaisesRegex(workflows.Refused, 'running.*unpaused'):
                    workflows.verify_ready(live)
