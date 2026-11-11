# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors
"""Explicitly configured HTTP clients and read-only disposable DB assertions."""
import json
import os
import subprocess
from urllib.error import HTTPError
from urllib.parse import quote, urlsplit
from urllib.request import Request, urlopen


class PrerequisiteError(RuntimeError):
    pass


class GraphQLError(RuntimeError):
    pass


def required(name):
    value = os.environ.get(name, '')
    if not value:
        raise PrerequisiteError('Required integration prerequisite: ' + name)
    return value


class Client:
    def __init__(self, token: str, role: str):
        self.process_url = required('TEST_PROCESS_URL').rstrip('/')
        self.graphql_url = required('TEST_GRAPHQL_URL')
        for url in (self.process_url, self.graphql_url):
            if urlsplit(url).scheme not in ('http', 'https') or not urlsplit(url).netloc:
                raise PrerequisiteError('Integration URLs must be absolute HTTP(S) URLs')
        if not token or not role:
            raise PrerequisiteError('Signed token and selected role are required')
        self.token, self.role = token, role

    def post(self, url, body, key=None):
        headers = {'Content-Type': 'application/json', 'Authorization': 'Bearer ' + self.token,
                   'X-Hasura-Role': self.role}
        if key is not None:
            headers['Idempotency-Key'] = key
        request = Request(url, json.dumps(body).encode(), headers, method='POST')
        try:
            response = urlopen(request, timeout=20)
        except HTTPError as error:
            response = error
        with response:
            return response.status, json.loads(response.read())

    def graphql(self, query: str, variables: dict) -> dict:
        status, body = self.post(self.graphql_url, {'query': query, 'variables': variables})
        if body.get('errors'):
            raise GraphQLError(json.dumps(body['errors']))
        if status != 200 or 'data' not in body:
            raise GraphQLError('Invalid GraphQL response: HTTP ' + str(status))
        return body['data']

    def command(self, app_id: str, command: str, body: dict, key: str) -> tuple[int, dict]:
        if not key:
            raise ValueError('A stable Idempotency-Key is required')
        route = '/api/cap/process/commands/' + quote(app_id, safe='') + '/' + quote(command, safe='')
        return self.post(self.process_url + route, body, key)


def db_scalar(sql):
    """Fixed test-owned SELECTs only; B supplies a restricted disposable DB role."""
    dsn = required('TEST_DATABASE_URL')
    if required('TEST_DISPOSABLE_DATABASE') != 'yes':
        raise PrerequisiteError('TEST_DISPOSABLE_DATABASE must explicitly equal yes')
    if not sql.lstrip().upper().startswith('SELECT '):
        raise ValueError('Only test-owned SELECT assertions are supported')
    result = subprocess.run(['psql', dsn, '-X', '-A', '-t', '-v', 'ON_ERROR_STOP=1', '-c', sql],
                            capture_output=True, text=True, check=False)
    if result.returncode:
        raise PrerequisiteError('Disposable PostgreSQL assertion failed; review B schema/privileges')
    return result.stdout.strip()


def domain_snapshot(entity_id, workspace_id):
    """Read the complete protected procurement row, including state/version/values."""
    import uuid
    entity, workspace = str(uuid.UUID(entity_id)), str(uuid.UUID(workspace_id))
    raw = db_scalar(f"SELECT to_jsonb(d) FROM domain.procurement_requests d WHERE id='{entity}' AND workspace_id='{workspace}'")
    if not raw:
        raise PrerequisiteError('Required disposable domain row is missing')
    return json.loads(raw)
