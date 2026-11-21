#!/usr/bin/env python3
# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors
"""Real package publication helpers; no credentials or subprocess output logged."""
import json
import os
from pathlib import Path
import subprocess
import rag_sources as source


def publish(manifest, expected=True):
    # db() rejects a non-disposable target before any Operator side effect.
    source.db('SELECT current_database()')
    operator = os.environ['RAG_TEST_OPERATOR']
    source.require(Path(operator).is_file(), 'NOT_RUN: built Operator required')
    env = dict(os.environ, DATABASE_URL=os.environ['TEST_DATABASE_URL'],
               HASURA_URL=os.environ['RAG_TEST_HASURA_URL'],
               HASURA_ADMIN_SECRET=os.environ['RAG_TEST_HASURA_ADMIN_SECRET'])
    result = subprocess.run([operator, 'apply', str(manifest)], env=env,
                            capture_output=True, timeout=120)
    if expected:
        source.require(result.returncode == 0, 'FAIL: real package publication failed')
    return result.returncode == 0


def publication(app):
    source.require(all(c.isalnum() or c == '_' for c in app), 'invalid fixture application')
    return json.loads(source.db("SELECT spec->'_publication' FROM core.apps WHERE app_id='"+app+"'"))


def dispatch_once():
    source.db('SELECT current_database()')
    binary = os.environ['RAG_TEST_DISPATCHER']
    source.require(Path(binary).is_file(), 'NOT_RUN: built dispatcher required')
    for _ in range(100):
        due = source.db("SELECT count(*) FROM core.outbox_events WHERE event_type='knowledge.publication' AND ((delivery_status='pending' AND next_attempt_at<=now()) OR (delivery_status='in_flight' AND lease_until<=now()))")
        if due == '0':
            return
        result = subprocess.run([binary, '--once'], env={**os.environ,
                                'DATABASE_URL': os.environ['AI_DISPATCHER_DATABASE_URL']},
                                capture_output=True, timeout=30)
        source.require(result.returncode == 0, 'FAIL: real package delivery failed')
    raise RuntimeError('FAIL: bounded package notification drain exhausted')


def control_worker(running):
    config = Path(os.environ['RAG_TEST_COMPOSE_FILE'])
    values = json.loads(config.read_text())
    source.require(values.get('name', '').startswith('dxos-rag-') and
                   values.get('services', {}).get('worker', {}).get('entrypoint') == ['/live/bin/cap-ai-worker'],
                   'NOT_RUN: explicit disposable RAG worker stack required')
    result = subprocess.run(['docker', 'compose', '-f', str(config), 'start' if running else 'stop', 'worker'],
                            capture_output=True, timeout=30)
    source.require(result.returncode == 0, 'FAIL: disposable worker lifecycle failed')

import base64
import tempfile
import time
import uuid
import rag_metadata as metadata
import rag_personal as personal


def api(method, path, who, role, body=None, key=None, revision=None):
    return metadata.call(method, path, who, role, body, key, revision)


def wait_ready(document):
    for _ in range(150):
        if source.db("SELECT active_version_id IS NOT NULL FROM cap_ai.knowledge_documents WHERE id='"+document+"'") == 't':
            return
        time.sleep(1)
    raise RuntimeError('FAIL: real package worker did not activate')


def search(who, role, app, query):
    status, raw = api('POST', '/search', who, role,
                      {'scope': {'kind': 'APP', 'app_id': app}, 'query': query, 'retrieval': 'LEXICAL'})
    source.require(status == 200, 'FAIL: app search unavailable')
    return json.loads(raw)['results']


def grant(who, role, document, target, key):
    status, raw = api('GET', '/documents/'+document+'/grant-management', who, role)
    source.require(status == 200, 'FAIL: current grant management denied')
    page = json.loads(raw)
    source.require('title' not in raw.decode() and 'snippet' not in raw.decode() and 'artifact_ref' not in raw.decode(),
                   'FAIL: management leaked source content')
    status, _ = api('POST', '/documents/'+document+'/grants', who, role,
                    {'user_id': target}, key, page['revision'])
    source.require(status == 204, 'FAIL: current-generation named grant denied')


def main():
    source.main()
    # Explicit binaries and scoped service credentials are private test inputs.
    app = 'rag_pkg_' + uuid.uuid4().hex[:12]
    read_role, manager_role, admin_role = [app+'_'+suffix for suffix in ('reader', 'manager', 'admin')]
    read_role, manager_role, admin_role = ['role_'+value for value in (read_role, manager_role, admin_role)]
    actors = {}
    for who in ('A', 'B', 'ADMIN'):
        claims = json.loads(base64.urlsafe_b64decode(os.environ['RAG_TEST_OIDC_TOKEN_'+who].split('.')[1]+'==='))
        actors[who] = str(uuid.UUID(claims['sub']))
    marker = 'package' + uuid.uuid4().hex
    library, library_role = source.db("SELECT app_id,spec#>>'{knowledge,personalLibrary,allowedRoles,0}' FROM core.apps WHERE status='Ready' AND spec#>>'{knowledge,personalLibrary,enabled}'='true'").split('|')
    private_marker = 'private' + uuid.uuid4().hex
    private_file = personal.upload_markdown('A', library, library_role, ('# Personal\n\n'+private_marker+' owner evidence.\n').encode())
    personal.await_clean(private_file)
    status, raw = api('POST', '/documents', 'A', library_role, {'file_id': private_file, 'parser_profile': 'standard-text-v1'}, uuid.uuid4().hex)
    source.require(status == 202, 'FAIL: personal preservation fixture registration failed')
    private_document = json.loads(raw)['document_id']
    wait_ready(private_document)
    personal_before = source.db("SELECT count(*)||'|'||coalesce(string_agg(id::text||':'||revision::text,',' ORDER BY id),'') FROM cap_ai.knowledge_documents WHERE source_kind='UPLOAD'")
    with tempfile.TemporaryDirectory(prefix='rag-package-', dir=os.environ.get('RAG_TEST_FIXTURE_ROOT')) as directory:
        root = Path(directory)
        (root/'knowledge').mkdir()
        (root/'knowledge/general.md').write_text('# General policy\n\n'+marker+' General approval.\n')
        (root/'knowledge/payroll.md').write_text('# Payroll\n\n'+marker+'salary Restricted payroll evidence.\n')
        sources = [
            {'id': 'general', 'path': 'knowledge/general.md', 'mediaType': 'text/markdown', 'indexingProfile': 'standard-text-v1',
             'access': {'mode': 'ROLE', 'allowedRoles': [read_role]}},
            {'id': 'payroll', 'path': 'knowledge/payroll.md', 'mediaType': 'text/markdown', 'indexingProfile': 'standard-text-v1',
             'access': {'mode': 'RESTRICTED', 'allowedRoles': [read_role], 'grantsManagedBy': 'managers'}},
        ]
        manifest = {'apiVersion': 'dxos.io/v1alpha1', 'kind': 'DXApplication',
                    'metadata': {'name': app, 'displayName': 'Package qualification '+app, 'version': '1.0.0'},
                    'spec': {'capabilities': ['cap-process', 'cap-ai'],
                             'roles': [{'name': role} for role in (read_role, manager_role, admin_role)],
                             'tables': [{'name': 'domain.'+app+'_records', 'columns': [{'name': 'title', 'type': 'text', 'nullable': False}],
                                         'access': {'select': [{'role': role, 'scope': 'organization', 'columns': ['id', 'title', 'version']} for role in (read_role, manager_role, admin_role)], 'insert': [{'role': read_role, 'columns': ['title']}], 'update': [{'role': read_role, 'scope': 'own', 'columns': ['title']}]},
                                         'ui': {'listColumns': ['title'], 'formFields': [{'name':'title','label':'Title','type':'text','required':True}]}}],
                             'knowledge': {'version': 1, 'sources': sources, 'grantPolicies': {'managers': {'allowedRoles': [manager_role]}}}}}
        path = root/'manifest.yaml'
        revision = 0
        def apply():
            nonlocal revision
            revision += 1
            manifest['metadata']['version'] = '1.0.'+str(revision)
            path.write_text(json.dumps(manifest))
            publish(path)  # A fresh Operator process on every publication.
            source.require(source.db("SELECT status FROM core.apps WHERE app_id='"+app+"'") == 'Ready', 'FAIL: app readiness depends on indexing')
        apply()
        for who in ('A', 'B'):
            source.db("INSERT INTO core.user_roles(user_id,role_name) VALUES('"+actors[who]+"','"+read_role+"') ON CONFLICT DO NOTHING")
        for role in (manager_role, admin_role):
            source.db("INSERT INTO core.user_roles(user_id,role_name) VALUES('"+actors['ADMIN']+"','"+role+"') ON CONFLICT DO NOTHING")
        source.require(not search('A', read_role, app, marker), 'FAIL: unstaged index served content')
        control_worker(False)
        dispatch_once()
        documents = dict(line.split('|') for line in source.db("SELECT s.source_id,d.id FROM cap_ai.package_sources s JOIN cap_ai.knowledge_documents d ON d.package_source_id=s.id WHERE s.app_id='"+app+"'").splitlines())
        source.require(set(documents) == {'general', 'payroll'}, 'FAIL: package dispatch did not reconcile complete source set')
        source.require(source.db("SELECT count(*) FROM cap_ai.knowledge_documents d JOIN cap_ai.knowledge_collections c ON c.id=d.collection_id WHERE c.app_id='"+app+"' AND d.active_version_id IS NOT NULL") == '0', 'FAIL: stopped worker unexpectedly activated sources')
        control_worker(True)
        for document in documents.values():
            wait_ready(document)
        # A real malware rejection must preserve the last Ready publication.
        approved = (root/'knowledge/payroll.md').read_bytes()
        (root/'knowledge/payroll.md').write_bytes(b'X5O!P%@AP[4\\PZX54(P^)7CC)7}$EICAR-STANDARD-ANTIVIRUS-TEST-FILE!$H+H*')
        before_generation = publication(app)['generation']
        source.require(not publish(path, expected=False), 'FAIL: infected package source published')
        source.require(publication(app)['generation'] == before_generation, 'FAIL: scanner failure changed active publication')
        (root/'knowledge/payroll.md').write_bytes(approved)
        # Replay the authoritative snapshot directly with a real dispatcher token.
        current = publication(app)
        status, _ = source.request(os.environ['RAG_TEST_INTERNAL_AI_URL'].rstrip('/')+'/internal/v1/package-sources/reconcile',
                    json.dumps({'schema_version': 1, 'app_id': app, 'publication_generation': current['generation'], 'spec_digest': current['spec_digest']}).encode(),
                    {'Authorization': 'Bearer '+os.environ['RAG_DISPATCHER_BEARER_TOKEN'], 'Content-Type': 'application/json'})
        source.require(status == 204, 'FAIL: duplicate real package reconcile failed')
        status, _ = source.request(os.environ['RAG_TEST_INTERNAL_AI_URL'].rstrip('/')+'/internal/v1/package-sources/reconcile',
                    json.dumps({'schema_version': 1, 'app_id': app, 'publication_generation': current['generation'], 'spec_digest': current['spec_digest']}).encode(),
                    {**source.headers('A', read_role), 'Content-Type': 'application/json'})
        source.require(status == 403, 'FAIL: user token impersonated package dispatcher')
        source.require(source.db("SELECT count(*) FROM cap_ai.knowledge_documents d JOIN cap_ai.knowledge_collections c ON c.id=d.collection_id WHERE c.app_id='"+app+"'") == '2', 'FAIL: duplicate package documents')
        source.require(search('A', read_role, app, marker), 'FAIL: ROLE reader missing evidence')
        payroll = documents['payroll']
        source.require(not search('A', read_role, app, marker+'salary'), 'FAIL: An read restricted payroll without grant')
        grant('ADMIN', manager_role, payroll, actors['B'], 'grant-binh-'+uuid.uuid4().hex)
        hits = search('B', read_role, app, marker+'salary')
        source.require(len(hits) == 1, 'FAIL: Binh named grant did not permit exact evidence')
        hit = hits[0]
        protected = '/documents/'+payroll+'/citations/'+hit['chunk_id']+'?version_id='+hit['version_id']
        for who, role in (('A', read_role), ('ADMIN', manager_role), ('ADMIN', admin_role)):
            for route in (protected, '/documents/'+payroll+'/original', '/documents/'+payroll):
                status, _ = api('GET', route, who, role)
                source.require(status == 404, 'FAIL: manager/admin/other-reader bypassed READ')
        status, _ = api('GET', '/documents/'+private_document, 'ADMIN', manager_role)
        source.require(status == 404 and not search('A', read_role, app, private_marker), 'FAIL: app context exposed personal evidence')
        # Deliver neither intermediate publication: current Core must retire the grant.
        sources[1]['access'] = {'mode': 'ROLE', 'allowedRoles': [read_role]}
        apply()
        sources[1]['access'] = {'mode': 'RESTRICTED', 'allowedRoles': [read_role], 'grantsManagedBy': 'managers'}
        apply()
        source.require(not search('B', read_role, app, marker+'salary'), 'FAIL: missed A/B/A restored old grant')
        dispatch_once()
        grant('ADMIN', manager_role, payroll, actors['B'], 'new-generation-'+uuid.uuid4().hex)
        source.require(search('B', read_role, app, marker+'salary'), 'FAIL: fresh grant did not restore allowed read')
        # Remove and reintroduce while delivery is delayed. Old citations deny immediately.
        old_incarnation = next(s['incarnation'] for s in publication(app)['sources'] if s['source_id'] == 'payroll')
        saved = sources.pop()
        apply()
        status, _ = api('GET', protected, 'B', read_role)
        source.require(status == 404, 'FAIL: removed source citation survived delayed dispatch')
        dispatch_once()
        sources.append(saved)
        apply()
        new_incarnation = next(s['incarnation'] for s in publication(app)['sources'] if s['source_id'] == 'payroll')
        source.require(old_incarnation != new_incarnation, 'FAIL: reintroduced source reused incarnation')
        dispatch_once()
        source.require(not search('B', read_role, app, marker+'salary'), 'FAIL: reintroduced source revived retired grant')
        # Mutable package bytes are no longer the active original. Content update
        # keeps the previous eligible active version until the replacement activates.
        general = documents['general']
        # Finish work admitted by prior policy/source-set publications before
        # comparing the next content transition. Freeze activation while
        # checking retention through publication and reconciliation.
        for _ in range(150):
            if source.db("SELECT active_version_id=desired_version_id FROM cap_ai.knowledge_documents WHERE id='"+general+"'") == 't':
                break
            time.sleep(1)
        else:
            raise RuntimeError('FAIL: preceding publication never activated')
        control_worker(False)
        before = source.db("SELECT active_version_id FROM cap_ai.knowledge_documents WHERE id='"+general+"'")
        (root/'knowledge/general.md').write_text('# Updated policy\n\n'+marker+'updated New approved guidance.\n')
        apply()
        source.require(source.db("SELECT active_version_id FROM cap_ai.knowledge_documents WHERE id='"+general+"'") == before, 'FAIL: publication erased eligible active source')
        dispatch_once()
        source.require(source.db("SELECT active_version_id FROM cap_ai.knowledge_documents WHERE id='"+general+"'") == before, 'FAIL: reconciliation erased eligible active source')
        control_worker(True)
        for _ in range(150):
            if search('A', read_role, app, marker+'updated'):
                break
            time.sleep(1)
        else:
            raise RuntimeError('FAIL: content replacement never activated')
        # Completed A -> B -> A reuses immutable version identity but must
        # create fresh fenced work, rather than leave B active forever.
        original_general = '# General policy\n\n'+marker+' General approval.\n'
        updated_general = '# Updated policy\n\n'+marker+'updated New approved guidance.\n'
        (root/'knowledge/general.md').write_text(original_general)
        apply()
        dispatch_once()
        for _ in range(150):
            if source.db("SELECT active_version_id FROM cap_ai.knowledge_documents WHERE id='"+general+"'") == before:
                break
            time.sleep(1)
        else:
            raise RuntimeError('FAIL: historical completed content never reactivated')
        source.require(not search('A', read_role, app, marker+'updated'), 'FAIL: superseded B remained active after returning to A')
        # Also revisit a candidate cancelled before its first parse. Retry must
        # preserve its cancelled job history and get a distinct runnable job.
        control_worker(False)
        retry_general = '# Retry policy\n\n'+marker+'retry Approval retry.\n'
        (root/'knowledge/general.md').write_text(retry_general)
        apply()
        dispatch_once()
        retry_version = source.db("SELECT desired_version_id FROM cap_ai.knowledge_documents WHERE id='"+general+"'")
        (root/'knowledge/general.md').write_text('# Intervening policy\n\n'+marker+'cancel Cancelled candidate.\n')
        apply()
        dispatch_once()
        (root/'knowledge/general.md').write_text(retry_general)
        apply()
        dispatch_once()
        control_worker(True)
        for _ in range(150):
            if source.db("SELECT active_version_id FROM cap_ai.knowledge_documents WHERE id='"+general+"'") == retry_version:
                break
            time.sleep(1)
        else:
            raise RuntimeError('FAIL: historical cancelled content never activated')
        source.require(source.db("SELECT count(*) FROM cap_ai.ingestion_jobs WHERE version_id='"+retry_version+"' AND state='CANCELLED'") != '0', 'FAIL: cancelled job history was erased')
        (root/'knowledge/general.md').write_text(updated_general)
        apply()
        dispatch_once()
        for _ in range(150):
            if search('A', read_role, app, marker+'updated'):
                break
            time.sleep(1)
        else:
            raise RuntimeError('FAIL: final content replacement did not recover')
        # Revoke a role with the same JWT; no refresh is allowed as a workaround.
        source.db("DELETE FROM core.user_roles WHERE user_id='"+actors['A']+"' AND role_name='"+read_role+"'")
        status, _ = api('POST', '/search', 'A', read_role, {'scope': {'kind': 'APP', 'app_id': app}, 'query': marker})
        source.require(status in (401, 403, 404), 'FAIL: stale JWT retained package role')
        source.db("INSERT INTO core.user_roles(user_id,role_name) VALUES('"+actors['A']+"','"+read_role+"')")
        source.require(search('A', read_role, app, marker+'updated'), 'FAIL: fresh DB role grant required token refresh')
        personal_after = source.db("SELECT count(*)||'|'||coalesce(string_agg(id::text||':'||revision::text,',' ORDER BY id),'') FROM cap_ai.knowledge_documents WHERE source_kind='UPLOAD'")
        source.require(personal_before == personal_after, 'FAIL: package reconcile reset personal documents')
        source.require(len(personal.search('A', library_role, private_marker)) == 1, 'FAIL: unrelated package update broke personal search')
        browser_probe(app, manifest['metadata']['displayName'], read_role, manager_role, admin_role, actors['B'], marker)
        combined_library_browser()
        print('PASS: real package apply/replay/restart, ROLE/RESTRICTED An/Binh/manager/admin, missed ABA, delayed removal/reintroduction, completed/cancelled content ABA and current-role revoke; personal state preserved')


def browser_probe(app, display_name, read_role, manager_role, admin_role, binh, marker):
    try:
        from playwright.sync_api import sync_playwright
    except ImportError:
        raise RuntimeError('NOT_RUN: Playwright Chromium required')
    ui = os.environ['RAG_TEST_UI_URL']
    from urllib.parse import urlsplit
    with sync_playwright() as pw:
        browser = pw.chromium.launch()
        try:
            for who in ('A', 'ADMIN', 'B'):
                state = os.environ['RAG_TEST_UI_STORAGE_'+who]
                source.require(Path(state).is_file(), 'NOT_RUN: real IdP browser session required')
                context = browser.new_context(storage_state=state)
                page = context.new_page()
                external = []
                page.on('request', lambda request: external.append(request.url) if urlsplit(request.url).hostname not in ('localhost', '127.0.0.1') else None)
                page.goto(ui)
                page.get_by_role('button', name='Sign in', exact=True).click()
                page.get_by_role('button', name=display_name, exact=True).wait_for(timeout=30000)
                page.get_by_role('button', name=display_name, exact=True).click()
                page.get_by_label('Acting role', exact=True).select_option(manager_role if who == 'ADMIN' else read_role)
                page.get_by_role('heading', name='App knowledge', exact=True).wait_for()
                if who == 'ADMIN':
                    page.get_by_role('button', name='Manage grants payroll', exact=True).click()
                    section = page.get_by_role('region', name='Quản lý quyền đọc payroll', exact=True)
                    section.get_by_label('User ID', exact=True).fill(binh)
                    section.get_by_role('button', name='Grant read', exact=True).click()
                    section.get_by_text(binh+': ACTIVE', exact=False).wait_for(timeout=30000)
                    source.require('Restricted payroll evidence' not in page.inner_text('body'), 'FAIL: manager browser obtained source excerpt')
                    page.get_by_label('Acting role', exact=True).select_option(admin_role)
                    page.get_by_label('User ID', exact=True).wait_for(state='detached')
                    page.get_by_label('Tìm trong tài liệu của ứng dụng', exact=True).fill(marker+'salary')
                    page.get_by_role('button', name='Tìm trong ứng dụng', exact=True).click()
                    page.get_by_text('No accessible evidence found.', exact=True).wait_for()
                else:
                    page.get_by_label('Tìm trong tài liệu của ứng dụng', exact=True).fill(marker+('updated' if who == 'A' else 'salary'))
                    page.get_by_role('button', name='Tìm trong ứng dụng', exact=True).click()
                    page.get_by_role('button', name='View citation', exact=True).first.wait_for(timeout=30000)
                    page.get_by_role('button', name='View citation', exact=True).first.click()
                    dialog = page.get_by_role('dialog', name='Protected citation', exact=True)
                    source.require(marker in dialog.inner_text(), 'FAIL: app browser citation mismatch')
                    page.get_by_role('button', name='Thư viện cá nhân', exact=True).first.click()
                    dialog.wait_for(state='detached')
                    source.require(page.get_by_label('User ID', exact=True).count() == 0, 'FAIL: stale grant state survived app switch')
                source.require(not external, 'FAIL: browser contacted an external processing endpoint')
                context.close()
        finally:
            browser.close()
    print('PASS: real Chromium app search/citation, manager grant without READ, admin denial and app/role cancellation')


def combined_library_browser():
    """The same registered app may expose personal and package knowledge."""
    from playwright.sync_api import sync_playwright
    row = json.loads(source.db("SELECT jsonb_build_object('app_id',app_id,'display_name',display_name,'version',version,'spec',spec) FROM core.apps WHERE status='Ready' AND spec#>>'{knowledge,personalLibrary,enabled}'='true'"))
    app = row['app_id']
    original = row['spec']
    original.pop('_publication', None)
    desired = json.loads(json.dumps(original))
    manager = 'role_library_package_manager'
    desired['roles'].append({'name': manager})
    desired['knowledge']['grantPolicies'] = {'package-managers': {'allowedRoles': [manager]}}
    desired['knowledge']['sources'] = [{'id': 'guide', 'path': 'knowledge/guide.md', 'mediaType': 'text/markdown', 'indexingProfile': 'standard-text-v1',
                                       'access': {'mode': 'RESTRICTED', 'allowedRoles': desired['knowledge']['personalLibrary']['allowedRoles'], 'grantsManagedBy': 'package-managers'}}]
    claims = json.loads(base64.urlsafe_b64decode(os.environ['RAG_TEST_OIDC_TOKEN_ADMIN'].split('.')[1]+'==='))
    admin = str(uuid.UUID(claims['sub']))
    with tempfile.TemporaryDirectory(prefix='rag-combined-', dir=os.environ.get('RAG_TEST_FIXTURE_ROOT')) as directory:
        root = Path(directory)
        (root/'knowledge').mkdir()
        (root/'knowledge/guide.md').write_text('# Shared guide\n\nCombined app guidance.\n')
        manifest = {'apiVersion': 'dxos.io/v1alpha1', 'kind': 'DXApplication', 'metadata': {'name': app, 'displayName': row['display_name'], 'version': row['version']}, 'spec': desired}
        path = root/'manifest.yaml'
        path.write_text(json.dumps(manifest))
        publish(path)
        try:
            source.db("INSERT INTO core.user_roles(user_id,role_name) VALUES('"+admin+"','"+manager+"') ON CONFLICT DO NOTHING")
            dispatch_once()
            document = source.db("SELECT d.id FROM cap_ai.knowledge_documents d JOIN cap_ai.package_sources s ON s.id=d.package_source_id WHERE s.app_id='"+app+"' AND s.source_id='guide'")
            wait_ready(document)
            with sync_playwright() as pw:
                browser = pw.chromium.launch()
                try:
                    context = browser.new_context(storage_state=os.environ['RAG_TEST_UI_STORAGE_ADMIN'])
                    page = context.new_page()
                    page.goto(os.environ['RAG_TEST_UI_URL'])
                    page.get_by_role('button', name='Sign in', exact=True).click()
                    # The first button is the dedicated personal view; the last
                    # is the registry app entry, even when labels are identical.
                    page.get_by_role('button', name=row['display_name'], exact=True).last.click()
                    page.get_by_label('Acting role', exact=True).select_option(manager)
                    page.get_by_role('button', name='Manage grants guide', exact=True).wait_for(timeout=30000)
                    page.get_by_role('button', name='Thư viện cá nhân', exact=True).first.click()
                    page.get_by_label('Tải tài liệu lên', exact=True).wait_for()
                    page.get_by_role('button', name=row['display_name'], exact=True).last.click()
                    page.get_by_role('button', name='Manage grants guide', exact=True).wait_for(timeout=30000)
                    source.require(page.get_by_label('Tải tài liệu lên', exact=True).count() == 0, 'FAIL: combined app could not leave personal view')
                    context.close()
                finally:
                    browser.close()
        finally:
            manifest['spec'] = original
            path.write_text(json.dumps(manifest))
            publish(path)
            dispatch_once()
    print('PASS: real Chromium combined personal/package app navigation and manager controls')


if __name__ == '__main__':
    try:
        main()
    except Exception as exc:
        print(str(exc) if isinstance(exc, RuntimeError) else 'FAIL: package qualification prerequisite or probe failed', file=__import__('sys').stderr)
        raise SystemExit(2)
    finally:
        if os.environ.get('RAG_TEST_COMPOSE_FILE'):
            try:
                control_worker(True)
            except Exception:
                pass
