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
