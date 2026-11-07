# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors
"""Provision disposable fixture identities and acquire renewable Keycloak tokens."""
import argparse
import json
import os
from pathlib import Path
import secrets
import time
import urllib.error
import urllib.parse
import urllib.request

ROOT = Path(__file__).resolve().parents[2]
PRIVATE = ROOT / 'build/integration'


def request(url, data=None, token=None, form=False):
    headers = {}
    if token:
        headers['Authorization'] = 'Bearer ' + token
    if data is not None:
        headers['Content-Type'] = 'application/x-www-form-urlencoded' if form else 'application/json'
        data = urllib.parse.urlencode(data).encode() if form else json.dumps(data).encode()
    with urllib.request.urlopen(urllib.request.Request(url, data=data, headers=headers), timeout=30) as response:
        body = response.read()
        return json.loads(body) if body else None


def private_write(path, value):
    if path.is_symlink():
        raise RuntimeError('private output cannot be a symlink')
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    os.fchmod(fd, 0o600)
    with os.fdopen(fd, 'w') as stream:
        json.dump(value, stream)


def execute(action):
    config = ROOT / '.env.integration'
    if config.stat().st_mode & 0o077:
        raise RuntimeError('private configuration must have mode 0600')
    values = dict(line.split('=', 1) for line in config.read_text().splitlines() if line and not line.startswith('#'))
    if values.get('POSTGRES_DB') != 'opendx_integration':
        raise RuntimeError('disposable database required')
    base = 'http://localhost:' + values.get('KEYCLOAK_PORT', '18085')
    fixture = json.loads((ROOT / 'tests/fixtures/actors.json').read_text())['actors']
    credentials_path = PRIVATE / 'actor-credentials.json'
    credentials = json.loads(credentials_path.read_text()) if credentials_path.exists() else {name: secrets.token_urlsafe(32) for name in fixture}
    for name in fixture:
        credentials.setdefault(name, secrets.token_urlsafe(32))
    if action == 'provision':
        admin = request(base + '/realms/master/protocol/openid-connect/token', {'grant_type': 'password', 'client_id': 'admin-cli', 'username': values.get('KEYCLOAK_ADMIN', 'admin'), 'password': values['KEYCLOAK_ADMIN_PASSWORD']}, form=True)['access_token']
        clients = request(base + '/admin/realms/dxos/clients?clientId=dxos-portal', token=admin)
        client = json.loads((ROOT / 'platform/keycloak/realm-export.json').read_text())['clients'][0]
        client['id'] = clients[0]['id']
        req = urllib.request.Request(base + '/admin/realms/dxos/clients/' + client['id'], data=json.dumps(client).encode(), headers={'Authorization': 'Bearer ' + admin, 'Content-Type': 'application/json'}, method='PUT')
        with urllib.request.urlopen(req, timeout=30):
            pass
        users = []
        for name, actor in fixture.items():
            users.append({'id': actor['sub'], 'username': name, 'enabled': True, 'email': name+'@integration.invalid', 'emailVerified': True, 'firstName': name, 'lastName': 'Integration', 'requiredActions': [], 'attributes': {'workspace_id': [actor['workspace_id']], 'default_role': [actor['roles'][0]], 'allowed_roles': actor['roles']}, 'credentials': [{'type': 'password', 'value': credentials[name], 'temporary': False}]})
        result = request(base + '/admin/realms/dxos/partialImport', {'ifResourceExists': 'OVERWRITE', 'users': users}, admin)
        if result.get('errors'):
            raise RuntimeError('identity import rejected')
        private_write(credentials_path, credentials)
    else:
        tokens, metadata = {}, {}
        for name, actor in fixture.items():
            issued = request(base + '/realms/dxos/protocol/openid-connect/token', {'grant_type': 'password', 'client_id': 'dxos-portal', 'username': name, 'password': credentials[name], 'scope': 'openid'}, form=True)
            tokens[actor['token_env']] = issued['access_token']
            metadata[actor['token_env']] = {'expires_at': int(time.time()) + issued['expires_in'], 'renew_before': int(time.time()) + max(0, issued['expires_in'] - 60)}
        private_write(PRIVATE / 'tokens.json', tokens)
        private_write(PRIVATE / 'token-expiry.json', metadata)
    print(json.dumps({'action': action, 'actors': len(fixture), 'result': 'completed'}))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=['provision', 'tokens'])
    args = parser.parse_args()
    try:
        execute(args.action)
    except (OSError, ValueError, RuntimeError, urllib.error.URLError):
        parser.exit(1, 'identity operation failed; check private configuration and Keycloak readiness (details withheld)\n')

if __name__ == '__main__':
    main()
