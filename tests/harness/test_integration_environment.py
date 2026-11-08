# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors
"""Disposable-stack guards; removing a guard must permit an observed mutation."""
import importlib.util
from pathlib import Path
import unittest

ROOT = Path(__file__).resolve().parents[2]


def load(name):
    path = ROOT / 'scripts' / 'integration' / (name + '.py')
    if not path.exists():
        raise AssertionError('missing integration entrypoint: ' + name)
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


class EnvironmentTests(unittest.TestCase):
    def test_reset_refuses_missing_ack(self):
        env = load('environment')
        calls = []
        with self.assertRaises(env.Refused):
            env.operate('reset', {'TEST_DISPOSABLE_DATABASE': 'yes'}, calls.append)
        self.assertEqual(calls, [])

    def test_reset_refuses_foreign_project_or_database(self):
        env = load('environment')
        for override in ({'COMPOSE_PROJECT_NAME': 'production'}, {'POSTGRES_DB': 'dxos_db'}):
            calls = []
            values = dict(TEST_DISPOSABLE_DATABASE='yes', TEST_ALLOW_RESET='opendx-integration',
                          POSTGRES_DB='opendx_integration', COMPOSE_PROJECT_NAME='opendx-integration')
            values.update(override)
            with self.assertRaises(env.Refused):
                env.operate('reset', values, calls.append)
            self.assertEqual(calls, [])

    def test_inspect_redacts_credentials(self):
        env = load('environment')
        values = dict(TEST_DISPOSABLE_DATABASE='yes', POSTGRES_DB='opendx_integration',
                      POSTGRES_PASSWORD='must-never-print', DATABASE_URL='postgres://a:secret@host/db')
        result = env.operate('inspect', values, lambda command: '')
        self.assertNotIn('must-never-print', str(result))
        self.assertNotIn('secret', str(result))
        self.assertEqual(result['database'], 'opendx_integration')

    def test_phase_seed_refuses_missing_ready_apps(self):
        seed = load('seed')
        mutations = []
        with self.assertRaises(seed.Refused):
            seed.seed_phase('manual', {'TEST_DISPOSABLE_DATABASE': 'yes'},
                            ready_apps=[], create=mutations.append)
        self.assertEqual(mutations, [])

    def test_private_config_creates_restricted_files(self):
        import tempfile
        env = load('environment')
        with tempfile.TemporaryDirectory() as directory:
            env.ROOT = Path(directory)
            env.private_config()
            self.assertEqual((env.ROOT / '.env.integration').stat().st_mode & 0o777, 0o600)
            self.assertEqual((env.ROOT / 'build/integration/s3.json').stat().st_mode & 0o777, 0o600)
            for name in ('kestra.env', 'workflow-routes.json'):
                self.assertEqual((env.ROOT / 'build/integration' / name).stat().st_mode & 0o777, 0o600)

    def test_reset_refuses_foreign_volume_before_down(self):
        import json
        env = load('environment')
        calls = []
        def runner(command):
            calls.append(command)
            if 'config' in command:
                return json.dumps({'name': 'opendx-integration', 'services': {'postgres': {'environment': {'POSTGRES_DB': 'opendx_integration'}}},
                                   'volumes': {'pg-data': {'name': 'opendx-integration_pg-data'}}})
            if command[1:3] == ['ps', '-aq']:
                return ''
            if command[1:4] == ['volume', 'ls', '-q']:
                return '' if '--filter' in command else 'opendx-integration_pg-data\n'
            if command[1:3] == ['volume', 'inspect']:
                return json.dumps([{'Name': 'opendx-integration_pg-data', 'Labels': {'com.docker.compose.project': 'foreign'}}])
            self.fail('unexpected mutation or command: ' + repr(command))
        with self.assertRaises(env.Refused):
            env.operate('reset', {'TEST_DISPOSABLE_DATABASE': 'yes', 'TEST_ALLOW_RESET': 'opendx-integration'}, runner)
        self.assertFalse(any('down' in call for call in calls))

    def test_private_config_refuses_existing_readable_s3_file(self):
        import tempfile
        env = load('environment')
        with tempfile.TemporaryDirectory() as directory:
            env.ROOT = Path(directory)
            config = env.ROOT / 'build/integration/s3.json'
            config.parent.mkdir(parents=True)
            config.write_text('existing public config')
            config.chmod(0o644)
            with self.assertRaises(env.Refused):
                env.private_config()
            self.assertEqual(config.read_text(), 'existing public config')
            self.assertFalse((env.ROOT / '.env.integration').exists())

    def test_role_bootstrap_uses_verified_environment_and_preserves_sql_stdin(self):
        import json
        import os
        import tempfile
        from types import SimpleNamespace
        from unittest.mock import patch
        env = load('environment')
        observed = []

        def execute(command, **kwargs):
            effective = dict(kwargs.get('env', os.environ))
            observed.append((command, kwargs, effective))
            if 'config' in command:
                stdout = json.dumps({'name': 'opendx-integration', 'services': {
                    'postgres': {'environment': {'POSTGRES_DB': 'opendx_integration'}}}, 'volumes': {}})
            else:
                stdout = ''
            return SimpleNamespace(stdout=stdout, returncode=0)

        inherited = {'DOCKER_HOST': 'tcp://foreign:2375', 'DOCKER_CONTEXT': 'foreign',
                     'COMPOSE_FILE': '/foreign/compose.yaml', 'POSTGRES_HOST': 'foreign',
                     'PATH': '/usr/bin'}
        with tempfile.TemporaryDirectory() as directory:
            env.ROOT = Path(directory)
            env.private_config()
            sql_file = env.ROOT / 'platform/postgres/init/06_runtime_roles.sql'
            sql_file.parent.mkdir(parents=True)
            sql_file.write_text('SELECT 17; -- reviewed bootstrap marker\n')
            values = dict(line.split('=', 1) for line in
                          (env.ROOT / '.env.integration').read_text().splitlines())
            values['TEST_DISPOSABLE_DATABASE'] = 'yes'
            with patch.dict(os.environ, inherited, clear=True), patch.object(
                    env.subprocess, 'run', side_effect=execute):
                env.configure_roles(values)

        self.assertGreater(len(observed), 1)
        verification_environment = observed[0][2]
        mutation = next(item for item in observed if 'psql' in item[0])
        self.assertEqual(mutation[2], verification_environment)
        self.assertEqual(mutation[2], {'PATH': '/usr/bin'})
        self.assertIn('SELECT 17; -- reviewed bootstrap marker', mutation[1]['input'])
        self.assertIn('ALTER ROLE dxos_process PASSWORD', mutation[1]['input'])
        self.assertTrue(mutation[1]['capture_output'])
        self.assertTrue(mutation[1]['text'])

    def test_private_config_reconstructs_missing_s3_from_existing_credentials(self):
        import json
        import tempfile
        env = load('environment')
        with tempfile.TemporaryDirectory() as directory:
            env.ROOT = Path(directory)
            env.private_config()
            before = (env.ROOT / '.env.integration').read_bytes()
            s3 = env.ROOT / 'build/integration/s3.json'
            expected = json.loads(s3.read_text())
            s3.unlink()
            env.private_config()
            self.assertEqual(json.loads(s3.read_text()), expected)
            self.assertEqual(s3.stat().st_mode & 0o777, 0o600)
            self.assertEqual((env.ROOT / '.env.integration').read_bytes(), before)

    def test_fresh_infrastructure_does_not_start_unprovisioned_kestra(self):
        import json
        env = load('environment')
        calls = []
        def runner(command):
            calls.append(command)
            if 'config' in command:
                return json.dumps({'name': env.PROJECT, 'services': {'postgres': {
                    'environment': {'POSTGRES_DB': env.DATABASE}}}, 'volumes': {}})
            if 'up' in command and 'kestra' in command:
                raise env.Refused('dedicated Kestra database does not exist before deployment')
            return ''
        env.operate('up', {'TEST_DISPOSABLE_DATABASE': 'yes'}, runner)
        self.assertTrue(any('up' in c and 'postgres' in c for c in calls))
