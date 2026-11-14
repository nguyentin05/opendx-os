#!/usr/bin/env python3
# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors
"""Direct real-service source qualification. Never logs credentials or responses."""
import base64
import json
import os
import subprocess
import sys
import time
import uuid
from urllib.parse import urlsplit, unquote, parse_qs, quote
from urllib.request import Request, urlopen
from urllib.error import HTTPError


def require(condition, message):
    if not condition:
        raise RuntimeError(message)


def request(url, body=None, headers=None, method=None):
    req = Request(url, data=body, headers=headers or {}, method=method)
    try:
        with urlopen(req, timeout=15) as r:
            return r.status, r.read(22 << 20)
    except HTTPError as e:
        return e.code, e.read(1 << 20)


def db(sql):
    u = urlsplit(os.environ['TEST_DATABASE_URL'])
    require(u.scheme in ('postgres', 'postgresql') and 'test' in u.path,
            'NOT_RUN: disposable PostgreSQL test database required')
    env = dict(os.environ, PGHOST=u.hostname or '', PGPORT=str(u.port or 5432),
               PGDATABASE=unquote(u.path[1:]), PGUSER=unquote(u.username or ''),
               PGPASSWORD=unquote(u.password or ''), PGCONNECT_TIMEOUT='5')
    env['PGSSLMODE'] = parse_qs(u.query).get('sslmode', ['prefer'])[0]
    result = subprocess.run(['psql', '-X', '-Atq', '-v', 'ON_ERROR_STOP=1'],
                            input=sql, env=env, capture_output=True, text=True)
    require(result.returncode == 0, 'database fixture operation failed')
    return result.stdout.strip()


def headers(who, role):
    return {'Authorization': 'Bearer ' + os.environ['RAG_TEST_OIDC_TOKEN_' + who],
            'X-Hasura-Role': role}


def gql(query, who, role):
    h = headers(who, role)
    h['Content-Type'] = 'application/json'
    status, raw = request(os.environ['RAG_TEST_HASURA_URL'].rstrip('/') + '/v1/graphql',
                          json.dumps({'query': query}).encode(), h)
    require(status == 200, 'Hasura request failed')
    result = json.loads(raw)
    require(not result.get('errors'), 'Hasura query rejected: qualification incomplete')
    return result['data']


def assert_revoked_metadata(status, raw):
    if status in (401, 403):
        return
    require(status == 200, 'revoked metadata probe unavailable')
    result = json.loads(raw)
    if result.get('errors'):
        require(not result.get('data') and all(
            e.get('extensions', {}).get('code') in ('access-denied', 'invalid-headers', 'invalid-jwt', 'permission-denied')
            for e in result['errors']), 'unexpected revoked-session error')
        return
    require(result.get('data', {}).get('core_files_by_pk', 'missing') is None,
            'stale session retained metadata')


def upload(who, app, role, content):
    boundary = 'rag' + uuid.uuid4().hex
    body = (f'--{boundary}\r\nContent-Disposition: form-data; name="app_id"\r\n\r\n{app}\r\n'
            f'--{boundary}\r\nContent-Disposition: form-data; name="file"; filename="source.pdf"\r\n'
            'Content-Type: application/pdf\r\n\r\n').encode() + content + f'\r\n--{boundary}--\r\n'.encode()
    h = headers(who, role)
    h['Content-Type'] = 'multipart/form-data; boundary=' + boundary
    status, raw = request(os.environ['RAG_TEST_FILE_API_URL'].rstrip('/') + '/api/cap/process/files/private/upload', body, h)
    require(status == 200, 'private upload unavailable or denied')
    result = json.loads(raw)
    return str(uuid.UUID(result['file_id']))


def main():
    try:
        import websocket
    except ImportError:
        raise RuntimeError('NOT_RUN: websocket-client required')
    require(int(db('SHOW server_version_num;')) // 10000 == 17, 'PostgreSQL 17 required')
    # Real metadata endpoint must be reachable with deployment-owned authority.
    status, _ = request(os.environ['RAG_TEST_HASURA_URL'].rstrip('/') + '/v1/metadata',
                        b'{"type":"export_metadata","args":{}}',
                        {'Content-Type': 'application/json', 'X-Hasura-Admin-Secret': os.environ['RAG_TEST_HASURA_ADMIN_SECRET']})
    require(status == 200, 'Hasura metadata prerequisite unavailable')
    app_raw = db("SELECT app_id,spec#>>'{knowledge,personalLibrary,allowedRoles,0}' FROM core.apps WHERE status='Ready' AND spec#>>'{knowledge,personalLibrary,enabled}'='true';")
    require(len(app_raw.splitlines()) == 1 and '|' in app_raw, 'NOT_RUN: exactly one registered library required')
    app, role = app_raw.split('|')
    require(all(c.isalnum() or c == '_' for c in app + role), 'invalid library identifiers')
    actors = {}
    for who in ('A', 'B', 'ADMIN'):
        # Decoding is fixture identity selection only; the real services verify JWTs.
        token = os.environ['RAG_TEST_OIDC_TOKEN_' + who]
        claims = json.loads(base64.urlsafe_b64decode(token.split('.')[1] + '==='))
        actors[who] = str(uuid.UUID(claims['sub']))
    require(len(set(actors.values())) == 3, 'NOT_RUN: three distinct fixture users required')
    for who, actor in actors.items():
        # UUID-derived fixture email avoids interpolation of untrusted token strings.
        db(f"INSERT INTO core.users(id,email,display_name) VALUES('{actor}','{actor}@rag-test.invalid','RAG fixture') ON CONFLICT(id) DO NOTHING; INSERT INTO core.user_roles(user_id,role_name) VALUES('{actor}','{role}') ON CONFLICT DO NOTHING;")
    content = b'%PDF-1.7\nNative source isolation fixture\n'
    a = upload('A', app, role, content)
    b = upload('B', app, role, content)
    require(a != b, 'same-byte owners shared a source identity')
    require(db(f"SELECT count(*) FROM core.files WHERE id IN ('{a}','{b}') AND read_policy='OWNER_ONLY'") == '2', 'private policy not stamped')
    for _ in range(60):
        if db(f"SELECT count(*) FROM core.files WHERE id IN ('{a}','{b}') AND scan_status='CLEAN'") == '2':
            break
        time.sleep(0.5)
    else:
        raise RuntimeError('scanner did not qualify clean fixtures')
    endpoint = os.environ['RAG_TEST_FILE_API_URL'].rstrip('/') + '/api/cap/process/files/' + a
    status, body = request(endpoint, headers=headers('A', role))
    require(status == 200 and body == content, 'owner could not read clean original')
    query = f'{{core_files_by_pk(id:"{a}"){{id file_name scanned_at}}}}'
    for who in ('B', 'ADMIN'):
        status, _ = request(endpoint, headers=headers(who, role))
        require(status == 404, 'other owner/admin source was not hidden')
        require(gql(query, who, role)['core_files_by_pk'] is None, 'private metadata disclosed')
    require(gql(query, 'A', role)['core_files_by_pk'] is not None, 'owner metadata unavailable')
    key = db(f"SELECT object_key FROM core.files WHERE id='{a}'")
    object_url = os.environ['RAG_TEST_S3_ENDPOINT'].rstrip('/') + '/' + quote(os.environ['RAG_TEST_S3_BUCKET']) + '/' + quote(key, safe='/')
    status, _ = request(object_url)
    require(status in (401, 403, 404), 'anonymous object read permitted')
    ws_url = os.environ['RAG_TEST_HASURA_URL'].rstrip('/').replace('https://', 'wss://').replace('http://', 'ws://') + '/v1/graphql'
    ws = websocket.create_connection(ws_url, subprotocols=['graphql-ws'], timeout=10)
    try:
        ws.send(json.dumps({'type': 'connection_init', 'payload': {'headers': headers('A', role)}}))
        while json.loads(ws.recv()).get('type') != 'connection_ack':
            pass
        ws.send(json.dumps({'id': 'private', 'type': 'start', 'payload': {'query': 'subscription ' + query}}))
        def next_data():
            for _ in range(20):
                msg = json.loads(ws.recv())
                if msg.get('type') == 'data':
                    require(not msg.get('payload', {}).get('errors'), 'subscription query rejected')
                    return msg['payload']['data']['core_files_by_pk']
            raise RuntimeError('subscription produced no data')
        require(next_data() is not None, 'initial owner subscription missing')
        db(f"DELETE FROM core.user_roles WHERE user_id='{actors['A']}' AND role_name='{role}'; UPDATE core.files SET scanned_at=now() WHERE id='{a}';")
        require(next_data() is None, 'stale subscription disclosed revoked metadata')
        status, _ = request(endpoint, headers=headers('A', role))
        require(status in (401, 403, 404), 'stale JWT retained original access')
        revoked_headers = headers('A', role)
        revoked_headers['Content-Type'] = 'application/json'
        status, raw = request(os.environ['RAG_TEST_HASURA_URL'].rstrip('/') + '/v1/graphql',
                              json.dumps({'query': query}).encode(), revoked_headers)
        assert_revoked_metadata(status, raw)
    finally:
        ws.close()
        db(f"INSERT INTO core.user_roles(user_id,role_name) VALUES('{actors['A']}','{role}') ON CONFLICT DO NOTHING;")
    infected = upload('A', app, role, b'%PDF-1.7\n' + b'X5O!P%@AP[4\\PZX54(P^)7CC)7}$EICAR-STANDARD-ANTIVIRUS-TEST-FILE!$H+H*')
    for _ in range(60):
        state = db(f"SELECT scan_status FROM core.files WHERE id='{infected}'")
        require(state != 'CLEAN', 'infected source blessed CLEAN')
        if state == 'INFECTED':
            break
        time.sleep(0.5)
    else:
        raise RuntimeError('scanner did not detect infected fixture')
    status, _ = request(os.environ['RAG_TEST_FILE_API_URL'].rstrip('/') + '/api/cap/process/files/' + infected, headers=headers('A', role))
    require(status == 422, 'infected original accessible')
    print('PASS: live source original/metadata/subscription/storage/scanner isolation')


if __name__ == '__main__':
    try:
        main()
    except Exception as exc:
        # Exceptions from network libraries can contain credentials/response data.
        if isinstance(exc, RuntimeError):
            print(str(exc), file=sys.stderr)
        else:
            print('FAIL: source qualification prerequisite or probe failed', file=sys.stderr)
        sys.exit(2)
