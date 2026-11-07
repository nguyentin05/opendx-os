# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors
"""Guarded local Compose lifecycle. Never prints subprocess output or secrets."""
import argparse
import json
import os
from pathlib import Path
import secrets
import subprocess

ROOT = Path(__file__).resolve().parents[2]
PROJECT = 'opendx-integration'
DATABASE = 'opendx_integration'
INFRA = ['postgres', 'keycloak', 'seaweedfs', 'clamav', 'kestra']


class Refused(RuntimeError):
    pass


def run(command, input=None):
    try:
        return subprocess.run(command, cwd=ROOT, check=True, capture_output=True,
                              text=True, input=input, timeout=600, env={k: v for k, v in os.environ.items()
                              if not k.startswith(('COMPOSE_', 'POSTGRES_', 'DOCKER_'))}).stdout
    except (subprocess.SubprocessError, OSError):
        raise Refused('service command failed; output withheld to protect credentials') from None


def guard(values, reset=False):
    if values.get('TEST_DISPOSABLE_DATABASE') != 'yes':
        raise Refused('TEST_DISPOSABLE_DATABASE=yes is required')
    if values.get('COMPOSE_PROJECT_NAME', PROJECT) != PROJECT or values.get('POSTGRES_DB', DATABASE) != DATABASE:
        raise Refused('foreign project or database refused')
    if reset and values.get('TEST_ALLOW_RESET') != PROJECT:
        raise Refused('TEST_ALLOW_RESET=opendx-integration is required')


def compose(*args):
    return ['docker', 'compose', '--env-file', str(ROOT / '.env.integration'), '-p', PROJECT,
            '-f', 'compose.yaml', '-f', 'compose.integration.yaml', *args]


def verify_target(runner):
    config = json.loads(runner(compose('config', '--format', 'json')))
    if config.get('name') != PROJECT or config['services']['postgres']['environment']['POSTGRES_DB'] != DATABASE:
        raise Refused('effective Compose target is foreign')
    for volume in config.get('volumes', {}).values():
        if volume.get('external') or not volume.get('name', '').startswith(PROJECT + '_'):
            raise Refused('external or foreign volume refused')
    for service in config['services'].values():
        if service.get('container_name'):
            raise Refused('fixed container name refused')
    containers = runner(['docker', 'ps', '-aq', '--filter', 'label=com.docker.compose.project=' + PROJECT]).split()
    if containers:
        for item in json.loads(runner(['docker', 'inspect', *containers])):
            labels = item['Config'].get('Labels', {})
            if labels.get('com.docker.compose.project') != PROJECT or labels.get('com.docker.compose.service') not in config['services']:
                raise Refused('foreign container refused')
            for mount in item.get('Mounts', []):
                if mount['Type'] == 'volume' and mount['Name'] not in {v['name'] for v in config['volumes'].values()}:
                    raise Refused('foreign mounted volume refused')
    expected = {v['name'] for v in config['volumes'].values()}
    volumes = [name for name in runner(['docker', 'volume', 'ls', '-q']).split() if name in expected]
    if volumes:
        for item in json.loads(runner(['docker', 'volume', 'inspect', *volumes])):
            if item.get('Labels', {}).get('com.docker.compose.project') != PROJECT or item['Name'] not in {v['name'] for v in config['volumes'].values()}:
                raise Refused('foreign volume label refused')
    return containers


def operate(action, values, runner=run):
    guard(values, reset=action == 'reset')
    if action == 'inspect':
        return {'project': PROJECT, 'database': DATABASE, 'scope': 'infrastructure only; app readiness unverified'}
    containers = verify_target(runner)
    if action == 'reset' and containers:
        target = runner(compose('exec', '-T', 'postgres', 'psql', '-U', 'integration_bootstrap', '-d', DATABASE,
                                '-Atqc', 'SELECT current_database()')).strip()
        if target != DATABASE:
            raise Refused('actual database target mismatch')
    if action in ('down', 'reset'):
        runner(compose('down', *(['--volumes'] if action == 'reset' else []), '--timeout', '30'))
    if action in ('up', 'reset'):
        runner(compose('up', '-d', '--wait', '--wait-timeout', '180', *INFRA))
    return {'project': PROJECT, 'action': action, 'result': 'completed', 'app_readiness': 'NOT_RUN'}


def private_config():
    companion = ROOT / 'build/integration'
    companion.mkdir(parents=True, exist_ok=True, mode=0o700)
    for name, initial in [('kestra.env', ''), ('workflow-routes.json', '[]')]:
        target = companion / name
        if target.is_symlink() or (target.exists() and target.stat().st_mode & 0o077):
            raise Refused('workflow companion configuration must have mode 0600')
        if not target.exists():
            with open(target, 'x', opener=lambda p, f: os.open(p, f, 0o600)) as stream:
                stream.write(initial)
    s3 = ROOT / 'build/integration/s3.json'
    if s3.is_symlink() or (s3.exists() and s3.stat().st_mode & 0o077):
        raise Refused('build/integration/s3.json must be a private regular file with mode 0600')
    path = ROOT / '.env.integration'
    if path.exists():
        if path.stat().st_mode & 0o077:
            raise Refused('.env.integration must have mode 0600')
        values = dict(line.split('=', 1) for line in path.read_text().splitlines()
                      if line and not line.startswith('#'))
        if not s3.exists():
            write_s3_config(s3, values)
        return
    values = dict(POSTGRES_DB=DATABASE, POSTGRES_USER='integration_bootstrap', COMPOSE_PROJECT_NAME=PROJECT,
                  POSTGRES_PORT='15432', HASURA_PORT='18080', KEYCLOAK_PORT='18085', KESTRA_PORT='18082',
                  CAP_PROCESS_PORT='18091', APISIX_GATEWAY_PORT='19080', WORKSPACE_UI_PORT='15173',
                  OIDC_ISSUER_URL='http://localhost:18085/realms/dxos')
    for key in ('POSTGRES_PASSWORD', 'HASURA_ADMIN_SECRET', 'KEYCLOAK_ADMIN_PASSWORD', 'KESTRA_PASSWORD',
                'INTEGRATION_RUNTIME_PASSWORD', 'S3_ACCESS_KEY_ID', 'S3_SECRET_ACCESS_KEY'):
        values[key] = secrets.token_hex(24)
    with open(path, 'x', opener=lambda p, flags: os.open(p, flags, 0o600)) as stream:
        stream.write(''.join(k + '=' + v + '\n' for k, v in values.items()))
    write_s3_config(s3, values)


def write_s3_config(path, values):
    if not values.get('S3_ACCESS_KEY_ID') or not values.get('S3_SECRET_ACCESS_KEY'):
        raise Refused('existing integration environment lacks S3 credentials')
    path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    with open(path, 'x', opener=lambda p, flags: os.open(p, flags, 0o600)) as stream:
        json.dump({'identities': [{'name': 'integration', 'credentials': [{'accessKey': values['S3_ACCESS_KEY_ID'],
                   'secretKey': values['S3_SECRET_ACCESS_KEY']}], 'actions': ['Admin', 'Read', 'List', 'Tagging', 'Write']}]}, stream)


def configure_roles(values):
    guard(values)
    verify_target(run)
    sql = (ROOT / 'platform/postgres/init/06_runtime_roles.sql').read_text()
    for role, key in [('dxos_process','INTEGRATION_PROCESS_PASSWORD'), ('dxos_hasura','INTEGRATION_HASURA_PASSWORD'), ('dxos_reader','INTEGRATION_READER_PASSWORD'), ('dxos_caller','INTEGRATION_CALLER_PASSWORD'), ('dxos_module_test','INTEGRATION_MODULE_TEST_PASSWORD')]:
        # Generated hex avoids SQL/URL quoting ambiguity; existing private values are checked.
        value = values.setdefault(key, secrets.token_hex(24))
        if not value.isalnum():
            raise Refused('runtime passwords must be alphanumeric')
        sql += "\nALTER ROLE " + role + " PASSWORD '" + value + "';"
    run(compose('exec','-T','postgres','psql','-v','ON_ERROR_STOP=1','-U','integration_bootstrap','-d',DATABASE), input=sql)
    anchor = run(compose('exec','-T','postgres','psql','-U','integration_bootstrap','-d',DATABASE,
                         '-Atqc', "SELECT 1 FROM pg_database WHERE datname='opendx_module_test'")).strip()
    if anchor != '1':
        run(compose('exec','-T','postgres','psql','-v','ON_ERROR_STOP=1','-U','integration_bootstrap','-d',DATABASE,
                    '-c','CREATE DATABASE opendx_module_test OWNER dxos_module_test'))
    path = ROOT / '.env.integration'
    persisted = dict(line.split('=', 1) for line in path.read_text().splitlines() if line and not line.startswith('#'))
    persisted.update({k: v for k, v in values.items() if k.startswith('INTEGRATION_')})
    with open(path, 'w') as stream:
        stream.write(''.join(k + '=' + v + '\n' for k,v in persisted.items()))
    os.chmod(path, 0o600)
    connections = {}
    for name, role, key in [('bootstrap','integration_bootstrap','POSTGRES_PASSWORD'), ('process','dxos_process','INTEGRATION_PROCESS_PASSWORD'), ('hasura','dxos_hasura','INTEGRATION_HASURA_PASSWORD'), ('assertion','dxos_reader','INTEGRATION_READER_PASSWORD'), ('caller','dxos_caller','INTEGRATION_CALLER_PASSWORD'), ('module_test','dxos_module_test','INTEGRATION_MODULE_TEST_PASSWORD')]:
        connections[name] = 'postgres://' + role + ':' + values[key] + '@localhost:' + values.get('POSTGRES_PORT','15432') + '/' + ('opendx_module_test' if name == 'module_test' else DATABASE) + '?sslmode=disable'
    output = ROOT / 'build/integration/connections.json'
    if output.is_symlink():
        raise Refused('private connections file cannot be a symlink')
    with open(output, 'w', opener=lambda p, flags: os.open(p, flags, 0o600)) as stream:
        os.fchmod(stream.fileno(), 0o600)
        json.dump(connections, stream)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=['up', 'down', 'reset', 'inspect', 'roles'])
    action = parser.parse_args().action
    try:
        guard(os.environ, action == 'reset')
        private_config()
        values = dict(line.split('=', 1) for line in (ROOT / '.env.integration').read_text().splitlines() if line and not line.startswith('#'))
        values.update(os.environ)
        if action == 'roles':
            configure_roles(values)
            print(json.dumps({'action': 'roles', 'result': 'completed'}))
        else:
            print(json.dumps(operate(action, values)))
    except Refused as exc:
        parser.exit(1, str(exc) + '\n')


if __name__ == '__main__':
    main()
