#!/usr/bin/env python3
# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors
"""Disposable live metadata API/DB gate; source isolation must pass first."""
import json
import os
import sys
import time
import uuid
from concurrent.futures import ThreadPoolExecutor
import rag_sources as source


def call(method, path, who, role, body=None, key=None, revision=None):
    h = source.headers(who, role)
    h['Content-Type'] = 'application/json'
    if key:
        h['Idempotency-Key'] = key
    if revision:
        h['If-Match'] = '"' + str(revision) + '"'
    return source.request(os.environ['RAG_TEST_AI_API_URL'].rstrip('/') + '/api/cap/ai' + path,
                          None if body is None else json.dumps(body).encode(), h, method)


def main():
    source.main()
    row = source.db("SELECT app_id,spec#>>'{knowledge,personalLibrary,allowedRoles,0}' FROM core.apps WHERE status='Ready' AND spec#>>'{knowledge,personalLibrary,enabled}'='true'")
    app, role = row.split('|')
    file_id = source.upload('A', app, role, b'%PDF-1.7\nMetadata fixture\n')
    for _ in range(60):
        if source.db(f"SELECT scan_status FROM core.files WHERE id='{file_id}'") == 'CLEAN':
            break
        time.sleep(0.5)
    else:
        raise RuntimeError('NOT_RUN: clean metadata fixture unavailable')
    body = {'file_id': file_id, 'title': 'Owner metadata fixture', 'parser_profile': 'standard-text-v1'}
    prefix = uuid.uuid4().hex
    def register(i):
        return call('POST', '/documents', 'A', role, body, prefix + str(i))
    with ThreadPoolExecutor(max_workers=6) as pool:
        results = list(pool.map(register, range(6)))
    source.require(all(status == 202 for status, _ in results), 'concurrent registration failed')
    records = [json.loads(raw) for _, raw in results]
    source.require(len({r['document_id'] for r in records}) == 1 and len({r['version_id'] for r in records}) == 1 and len({r['job_id'] for r in records}) == 1, 'duplicate scoped registration')
    doc = str(uuid.UUID(records[0]['document_id']))
    job = str(uuid.UUID(records[0]['job_id']))
    status, raw = register(0)
    source.require(status == 202 and json.loads(raw) == records[0], 'receipt replay changed')
    status, _ = call('POST', '/documents', 'A', role, dict(body, title='changed'), prefix + '0')
    source.require(status == 409, 'idempotency conflict accepted')
    for who in ('B', 'ADMIN'):
        status, _ = call('GET', '/documents/' + doc, who, role)
        source.require(status == 404, 'private metadata disclosed')
        status, _ = call('GET', '/ingestions/' + job, who, role)
        source.require(status == 404, 'private job disclosed')
        status, raw = call('GET', '/documents?scope=PERSONAL', who, role)
        source.require(status == 200 and doc not in raw.decode() and 'Owner metadata fixture' not in raw.decode(), 'owner list disclosure')
    source.require(source.db(f"SELECT count(*) FROM cap_ai.knowledge_documents WHERE id='{doc}'") == '1', 'wrong canonical document count')
    source.require(source.db(f"SELECT count(*) FROM cap_ai.ingestion_jobs WHERE id='{job}'") == '1', 'wrong canonical job count')
    status, _ = call('DELETE', '/documents/' + doc, 'A', role, key=prefix + 'delete', revision=records[0]['revision'])
    source.require(status == 204, 'delete failed')
    status, _ = call('GET', '/documents/' + doc, 'A', role)
    source.require(status == 404, 'tombstoned document readable')
    source.require(source.db(f"SELECT state FROM cap_ai.ingestion_jobs WHERE id='{job}'") == 'CANCELLED', 'delete retained live job')
    source.require(source.db(f"SELECT count(*) FROM core.files WHERE id='{file_id}'") == '1', 'delete destroyed original')
    print('PASS: concurrent live metadata registration, receipts, ownership and durable delete')


if __name__ == '__main__':
    try:
        main()
    except Exception as exc:
        print(str(exc) if isinstance(exc, RuntimeError) else 'FAIL: metadata prerequisite or probe failed', file=sys.stderr)
        sys.exit(2)
