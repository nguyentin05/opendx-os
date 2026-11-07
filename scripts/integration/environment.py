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


def run(command):
    try:
        return subprocess.run(command, cwd=ROOT, check=True, capture_output=True,
                              text=True, timeout=600, env={k: v for k, v in os.environ.items()
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
    path = ROOT / '.env.integration'
    if path.exists():
        if path.stat().st_mode & 0o077:
            raise Refused('.env.integration must have mode 0600')
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
    folder = ROOT / 'build/integration'
    folder.mkdir(parents=True, exist_ok=True, mode=0o700)
    s3 = folder / 's3.json'
    with open(s3, 'w', opener=lambda p, flags: os.open(p, flags, 0o600)) as stream:
        json.dump({'identities': [{'name': 'integration', 'credentials': [{'accessKey': values['S3_ACCESS_KEY_ID'],
                   'secretKey': values['S3_SECRET_ACCESS_KEY']}], 'actions': ['Admin', 'Read', 'List', 'Tagging', 'Write']}]}, stream)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=['up', 'down', 'reset', 'inspect'])
    action = parser.parse_args().action
    try:
        guard(os.environ, action == 'reset')
        private_config()
        values = dict(line.split('=', 1) for line in (ROOT / '.env.integration').read_text().splitlines() if line and not line.startswith('#'))
        values.update(os.environ)
        print(json.dumps(operate(action, values)))
    except Refused as exc:
        parser.exit(1, str(exc) + '\n')


if __name__ == '__main__':
    main()
