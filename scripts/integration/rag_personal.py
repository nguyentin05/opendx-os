#!/usr/bin/env python3
# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors
"""Disposable live personal-library API and real browser qualification.

Uses real OIDC sessions, scanner, File Service, parser worker and PostgreSQL.
Browser state files contain IdP cookies only; the UI obtains its own PKCE token.
"""
import json
import os
from pathlib import Path
import sys
import time
import uuid
from urllib.parse import urlsplit
import rag_metadata as metadata
import rag_sources as source


def upload_markdown(who, app, role, content):
    boundary = 'rag' + uuid.uuid4().hex
    body = (f'--{boundary}\r\nContent-Disposition: form-data; name="app_id"\r\n\r\n{app}\r\n'
            f'--{boundary}\r\nContent-Disposition: form-data; name="file"; filename="personal.md"\r\n'
            'Content-Type: text/markdown\r\n\r\n').encode() + content + f'\r\n--{boundary}--\r\n'.encode()
    headers = source.headers(who, role)
    headers['Content-Type'] = 'multipart/form-data; boundary=' + boundary
    status, raw = source.request(os.environ['RAG_TEST_FILE_API_URL'].rstrip('/') + '/api/cap/process/files/private/upload', body, headers)
    source.require(status == 200, 'private Markdown upload failed')
    return str(uuid.UUID(json.loads(raw)['file_id']))


def await_clean(file):
    for _ in range(120):
        if source.db(f"SELECT scan_status FROM core.files WHERE id='{file}'") == 'CLEAN':
            return
        time.sleep(.5)
    raise RuntimeError('FAIL: scanner did not produce CLEAN')


def search(who, role, query):
    status, raw = metadata.call('POST', '/search', who, role,
                               {'scope': {'kind': 'PERSONAL'}, 'query': query, 'limit': 20, 'retrieval': 'LEXICAL'})
    source.require(status == 200, 'personal search unavailable')
    return json.loads(raw)['results']


def browser_probe(app, role, marker):
    try:
        from playwright.sync_api import sync_playwright
    except ImportError:
        raise RuntimeError('NOT_RUN: playwright and its Chromium browser required')
    ui = os.environ['RAG_TEST_UI_URL']
    with sync_playwright() as pw:
        browser = pw.chromium.launch()
        try:
            for who in ('A', 'B'):
                state = os.environ['RAG_TEST_UI_STORAGE_' + who]
                source.require(Path(state).is_file(), 'NOT_RUN: missing OIDC browser storage state')
                context = browser.new_context(storage_state=state)
                page = context.new_page()
                # No browser request may contact a hosted AI endpoint.
                hosted = []
                page.on('request', lambda req: hosted.append(req.url) if any(x in urlsplit(req.url).netloc for x in ('openai.com', 'anthropic.com', 'generativelanguage.googleapis.com')) else None)
                page.goto(ui)
                page.get_by_role('button', name='Sign in', exact=True).click()
                page.get_by_role('button', name='Thư viện cá nhân', exact=True).first.wait_for(timeout=30000)
                # Verify the IdP session belongs to the same API fixture actor.
                source.require(page.get_by_role('button', name='Sign out', exact=True).is_visible(), 'browser sign-in failed')
                page.get_by_role('button', name='Thư viện cá nhân', exact=True).first.click()
                name = f'browser-{who}-{uuid.uuid4().hex}.md'
                own_marker = marker + who.lower()
                page.get_by_label('Tải tài liệu lên', exact=True).set_input_files({'name': name, 'mimeType': 'text/markdown', 'buffer': ('# Browser policy\n\n'+own_marker+' Director approval.\n').encode()})
                page.get_by_text(name, exact=True).wait_for(timeout=90000)
                row = page.get_by_role('listitem').filter(has=page.get_by_text(name, exact=True))
                page.wait_for_function("name => [...document.querySelectorAll('li')].some(e => e.textContent.includes(name) && e.textContent.includes('READY'))", arg=name, timeout=150000)
                page.get_by_label('Tìm trong tài liệu của bạn', exact=True).fill(own_marker)
                page.get_by_role('button', name='Search', exact=True).click()
                page.get_by_role('button', name='View citation', exact=True).first.wait_for(timeout=30000)
                page.get_by_role('button', name='View citation', exact=True).first.click()
                page.get_by_role('dialog', name='Protected citation').wait_for()
                source.require(own_marker in page.get_by_role('dialog').inner_text(), 'protected citation did not match owner')
                page.get_by_role('button', name='Close citation', exact=True).click()
                page.get_by_label('Tìm trong tài liệu của bạn', exact=True).fill(marker + ('b' if who == 'A' else 'a'))
                page.get_by_role('button', name='Search', exact=True).click()
                page.wait_for_timeout(1000)
                source.require(page.get_by_role('button', name='View citation', exact=True).count() == 0, 'browser disclosed other-owner citation')
                with page.expect_download():
                    row.get_by_role('button', name='Download '+name, exact=True).click()
                row.get_by_role('button', name='Delete '+name, exact=True).click()
                row.wait_for(state='detached')
                source.require(not hosted, 'browser contacted hosted AI')
                context.close()
        finally:
            browser.close()
    print('PASS: real browser PKCE upload/CLEAN/READY/search/citation/download/delete A/B flow')


def main():
    metadata.main()  # Includes real source guards and scanner qualification.
    app, role = source.db("SELECT app_id,spec#>>'{knowledge,personalLibrary,allowedRoles,0}' FROM core.apps WHERE status='Ready' AND spec#>>'{knowledge,personalLibrary,enabled}'='true'").split('|')
    marker = 'native' + uuid.uuid4().hex
    content = ('# Personal policy\n\n'+marker+' Director approval.\n').encode()
    docs = {}
    for who in ('A', 'B'):
        file = upload_markdown(who, app, role, content)
        await_clean(file)
        status, raw = metadata.call('POST', '/documents', who, role, {'file_id': file, 'title': 'Personal policy', 'parser_profile': 'standard-text-v1'}, uuid.uuid4().hex)
        source.require(status == 202, 'personal registration failed')
        doc = json.loads(raw)
        for _ in range(150):
            status, raw = metadata.call('GET', '/documents/'+doc['document_id'], who, role)
            source.require(status == 200, 'registered document unavailable')
            if json.loads(raw).get('active_version_status') == 'READY':
                break
            time.sleep(1)
        else:
            raise RuntimeError('FAIL: worker never activated text fixture')
        hits = search(who, role, marker)
        own = [h for h in hits if h['document_id'] == doc['document_id']]
        source.require(len(own) == 1, 'owner lexical result missing')
        docs[who] = (doc, own[0])
    source.require(docs['A'][0]['document_id'] != docs['B'][0]['document_id'], 'same bytes merged across owners')
    for who, (doc, hit) in docs.items():
        other = 'B' if who == 'A' else 'A'
        paths = ['/documents/'+doc['document_id']+'/original', '/documents/'+doc['document_id']+'/citations/'+hit['chunk_id']+'?version_id='+hit['version_id'], '/documents/'+doc['document_id']+'/assets/'+str(uuid.uuid4())]
        for path in paths:
            status, _ = metadata.call('GET', path, other, role)
            source.require(status == 404, 'guessed protected route disclosed content')
        status, raw = metadata.call('GET', paths[0], who, role)
        source.require(status == 200 and raw == content, 'protected original mismatch')
        status, _ = metadata.call('GET', paths[1], who, role)
        source.require(status == 200, 'protected citation unavailable')
        source.require(all(h['document_id'] != doc['document_id'] for h in search(other, role, marker)), 'other-owner search disclosure')
    status, raw = metadata.call('POST', '/answers', 'A', role, {'scope': {'kind': 'PERSONAL'}, 'query': marker})
    source.require(status == 200 and json.loads(raw)['status'] == 'SEARCH_ONLY', 'personal answers used generation')
    status, _ = metadata.call('POST', '/answers', 'A', role, {'scope': {'kind': 'PERSONAL'}, 'query': marker, 'require_generation': True})
    source.require(status == 403, 'personal generation policy bypass')
    print('PASS: live local parser worker, scoped lexical A/B same-byte isolation, protected routes and SEARCH_ONLY')
    browser_probe(app, role, marker)


if __name__ == '__main__':
    try:
        main()
    except Exception as exc:
        print(str(exc) if isinstance(exc, RuntimeError) else 'FAIL: personal prerequisite or probe failed', file=sys.stderr)
        sys.exit(2)
