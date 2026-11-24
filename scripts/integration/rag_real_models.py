#!/usr/bin/env python3
# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors
"""Explicit, owner-reviewed hosted qualification on a disposable installation."""
import json
import os
from pathlib import Path
import re
import subprocess
import stat
import time
import uuid
from urllib.parse import urlsplit, parse_qs, unquote

import rag_sources as source
import rag_personal as personal
import rag_package as package


class NotRun(RuntimeError):
    pass


def literal(value):
    return "'" + str(value).replace("'", "''") + "'"


def db(sql):
    return source.db("SET statement_timeout='5s'; " + sql)


def check_target(url):
    target = urlsplit(url)
    if target.scheme not in ('postgres', 'postgresql') or not re.fullmatch(r'(test_[a-zA-Z0-9_]+|[a-zA-Z0-9_]+_test)', target.path.lstrip('/')):
        raise NotRun('NOT_RUN: disposable test database required')


def check_write_target(read, write):
    check_target(read)
    check_target(write)
    try:
        source_target, write_target = urlsplit(read), urlsplit(write)
        allowed = {'sslmode', 'sslrootcert', 'sslcert', 'sslkey', 'connect_timeout', 'application_name'}
        for target in (source_target, write_target):
            if not target.hostname or not set(parse_qs(target.query, keep_blank_values=True)) <= allowed:
                raise ValueError
        if ((source_target.hostname.lower(), source_target.port or 5432, unquote(source_target.path)) !=
                (write_target.hostname.lower(), write_target.port or 5432, unquote(write_target.path)) or
                unquote(write_target.username or '') != 'dxos_authz'):
            raise ValueError
    except ValueError:
        raise NotRun('NOT_RUN: approval connection must target the same disposable database') from None


def check_cli_inputs():
    executable = Path(os.environ['RAG_REAL_MODEL_CLI'])
    token = Path(os.environ['RAG_REAL_ADMIN_TOKEN_FILE'])
    try:
        info = token.stat()
        valid = (executable.is_file() and os.access(executable, os.X_OK) and
                 stat.S_ISREG(info.st_mode) and info.st_mode & 0o400 and
                 not info.st_mode & 0o077 and 0 < info.st_size <= 16384 and os.access(token, os.R_OK))
    except OSError:
        valid = False
    if not valid:
        raise NotRun('NOT_RUN: executable CLI and protected token required')
    for name in ('OIDC_ISSUER_URL', 'OIDC_JWKS_URL'):
        target = urlsplit(os.environ[name])
        if target.scheme not in ('http', 'https') or not target.hostname:
            raise NotRun('NOT_RUN: valid ' + name + ' required')


def check_state(state, expected, review):
    for name, purpose in (('embed', 'EMBED'), ('generate', 'GENERATE')):
        profile = state.get(name) or {}
        if ((profile.get('id'), profile.get('revision')) != expected[name] or
                profile.get('purpose') != purpose or profile.get('execution') != 'HOSTED' or
                not profile.get('enabled') or not profile.get('slot_matches') or
                not profile.get('approval_matches') or profile.get('review_ref') != review or
                not profile.get('provider_id') or 'fake' in profile['provider_id'].lower()):
            raise NotRun('NOT_RUN: exact reviewed live profiles, slots and approvals required')
    if not isinstance(state['embed'].get('dimension'), int) or state['embed']['dimension'] < 1:
        raise NotRun('NOT_RUN: reviewed embedding dimension required')


def check_semantic(reply, document, lexical_match):
    source.require(not lexical_match and reply.get('status') == 'HYBRID' and
                   any(hit.get('document_id') == document for hit in reply.get('results', [])),
                   'semantic contribution missing')


def check_answer(reply, document, chunks, profile):
    source.require(reply.get('status') == 'ANSWERED' and isinstance(reply.get('answer'), str) and 'BLUE-17' in reply['answer'] and
                   reply.get('model_profile') == {'id': profile[0], 'revision': profile[1]} and
                   reply.get('citations') and all(c.get('document_id') == document and c.get('chunk_id') in chunks
                       for c in reply['citations']), 'grounded generation evidence missing')


def check_denied(reply):
    source.require(reply.get('status') == 'NO_EVIDENCE' and reply.get('answer') is None and
                   reply.get('results') == [] and reply.get('citations') == [], 'denied response disclosed evidence')


def check_withdrawal(result, approval):
    source.require(result.get('approval') == approval and result.get('changed') is True,
                   'exact approval withdrawal missing')


def check_revoked(reply, document):
    source.require(reply.get('status') == 'SEARCH_ONLY' and reply.get('reason') == 'POLICY_BLOCKED' and
                   reply.get('answer') is None and reply.get('citations') == [] and
                   any(hit.get('document_id') == document for hit in reply.get('results', [])),
                   'visible source with blocked generation required')


def await_generation(document, profile, dimension):
    deadline = time.monotonic() + 300
    while time.monotonic() < deadline:
        raw = db("SELECT json_build_object('run',g.run_id,'generation',g.id,'chunks',"
                        "(SELECT json_agg(e.chunk_id) FROM cap_ai.chunk_embeddings e WHERE e.generation_id=g.id)) "
                        "FROM cap_ai.embedding_generations g JOIN cap_ai.document_versions v ON v.active_run_id=g.run_id "
                        "JOIN cap_ai.knowledge_documents d ON d.active_version_id=v.id "
                        "WHERE d.id=" + literal(document) + " AND g.model_profile_id=" + literal(profile[0]) +
                        " AND g.profile_revision=" + str(profile[1]) + " AND g.dimension=" + str(dimension) +
                        " AND g.state='READY' AND g.completed_count=g.expected_count AND NOT EXISTS "
                        "(SELECT FROM cap_ai.chunk_embeddings e WHERE e.generation_id=g.id AND vector_dims(e.embedding)<>g.dimension)")
        if raw:
            result = json.loads(raw)
            source.require(result.get('chunks'), 'complete embedding generation has no vectors')
            return result
        time.sleep(1)
    raise RuntimeError('embedding generation deadline exceeded')


def preflight():
    required = ('TEST_DATABASE_URL', 'RAG_TEST_AI_API_URL', 'RAG_TEST_FILE_API_URL',
                'RAG_TEST_OIDC_TOKEN_A', 'RAG_TEST_OIDC_TOKEN_B',
                'RAG_REAL_EMBED_PROFILE', 'RAG_REAL_EMBED_REVISION',
                'RAG_REAL_GENERATE_PROFILE', 'RAG_REAL_GENERATE_REVISION',
                'RAG_REAL_MODELS_REVIEW_REF', 'RAG_REAL_TEST_APP', 'RAG_REAL_TEST_ROLE',
                'RAG_REAL_EMBED_APPROVAL', 'RAG_REAL_GENERATE_APPROVAL',
                'RAG_REAL_MODEL_CLI', 'RAG_REAL_ADMIN_TOKEN_FILE', 'AI_MODEL_APPROVAL_DATABASE_URL',
                'OIDC_ISSUER_URL', 'OIDC_JWKS_URL', 'OIDC_AUDIENCE',
                'CLAMAV_ADDRESS', 'RAG_INTERNAL_AI_URL', 'RAG_PARSER_QUALIFIED',
                'RAG_SOURCE_QUALIFIED', 'RAG_METADATA_QUALIFIED')
    for name in required:
        if not os.environ.get(name):
            raise NotRun('NOT_RUN: missing ' + name)
    for name in ('RAG_PARSER_QUALIFIED', 'RAG_SOURCE_QUALIFIED', 'RAG_METADATA_QUALIFIED'):
        if os.environ[name] != 'true':
            raise NotRun('NOT_RUN: missing ' + name)
    check_write_target(os.environ['TEST_DATABASE_URL'], os.environ['AI_MODEL_APPROVAL_DATABASE_URL'])
    if any(os.environ.get(name) for name in ('PGHOSTADDR', 'PGSERVICE', 'PGSERVICEFILE')):
        raise NotRun('NOT_RUN: database target overrides must be unset')
    check_cli_inputs()
    app = os.environ['RAG_REAL_TEST_APP']
    if not re.fullmatch(r'ragreal[a-z0-9_-]{1,50}', app):
        raise NotRun('NOT_RUN: distinct ragreal synthetic library required')
    expected = {}
    state = {}
    for name, purpose, slot in (('embed', 'EMBED', 'EMBEDDING'), ('generate', 'GENERATE', 'LLM')):
        prefix = 'RAG_REAL_' + name.upper()
        try:
            revision = int(os.environ[prefix + '_REVISION'])
            approval = str(uuid.UUID(os.environ[prefix + '_APPROVAL']))
            source.require(revision > 0, 'invalid revision')
        except (ValueError, RuntimeError):
            raise NotRun('NOT_RUN: valid exact profile revision and approval required') from None
        profile = os.environ[prefix + '_PROFILE']
        expected[name] = (profile, revision)
        raw = db("SELECT json_build_object('id',p.id,'revision',p.revision,'purpose',p.purpose,"
                        "'provider_id',p.provider_id,'execution',p.execution,'dimension',p.dimension,'enabled',p.enabled,"
                        "'slot_matches',s.profile_id=p.id AND s.profile_revision=p.revision,"
                        "'review_ref',e.review->>'attestation_ref','approval_matches',EXISTS("
                        "SELECT FROM cap_ai.agent_processing_approvals a JOIN core.users u ON u.id=a.approved_by "
                        "WHERE a.id=" + literal(approval) + " AND a.enabled AND a.profile_id=p.id AND a.profile_revision=p.revision "
                        "AND a.provider_id=p.provider_id AND a.purpose=" + literal(purpose) + " AND a.query_app_id=" + literal(app) +
                        " AND a.source_policy_id='local-only' AND a.source_policy_version=1 "
                        "AND u.status='ACTIVE' AND EXISTS(SELECT FROM core.user_roles ur WHERE ur.user_id=u.id AND ur.role_name='role_operator'))) "
                        "FROM cap_ai.model_profiles p JOIN cap_ai.global_model_slots s ON s.capability=" + literal(slot) +
                        " JOIN cap_ai.model_slot_events e ON e.capability=s.capability AND e.slot_revision=s.slot_revision "
                        "WHERE p.id=" + literal(profile) + " AND p.revision=" + str(revision))
        state[name] = json.loads(raw) if raw else None
    check_state(state, expected, os.environ['RAG_REAL_MODELS_REVIEW_REF'])
    ready = db("SELECT count(*) FROM core.apps WHERE app_id=" + literal(app) +
                      " AND status='Ready' AND spec#>>'{knowledge,personalLibrary,enabled}'='true' AND spec#>'{knowledge,personalLibrary,allowedRoles}' ? " + literal(os.environ['RAG_REAL_TEST_ROLE']))
    if ready != '1':
        raise NotRun('NOT_RUN: Ready synthetic personal library required')
    return expected, state


def api(path, role, body, who='A'):
    status, raw = package.api('POST', path, who, role, body)
    source.require(status == 200, 'authenticated qualification request failed')
    return json.loads(raw)


def operation_count(app, purpose=None, succeeded=False):
    query = "SELECT count(*) FROM cap_ai.model_operations WHERE app_id=" + literal(app) + " AND intent_at IS NOT NULL"
    if purpose:
        query += " AND purpose=" + literal(purpose)
    if succeeded:
        prefix = 'RAG_REAL_' + ('EMBED' if purpose == 'EMBED' else 'GENERATE')
        query += (" AND state='SUCCEEDED' AND profile_id=" + literal(os.environ[prefix + '_PROFILE']) +
                  " AND profile_revision=" + str(int(os.environ[prefix + '_REVISION'])))
    return int(db(query))


def run():
    expected, state = preflight()
    app, role = os.environ['RAG_REAL_TEST_APP'], os.environ['RAG_REAL_TEST_ROLE']
    # The isolated library must have no earlier content or active operation.
    source.require(db("SELECT count(*) FROM cap_ai.knowledge_documents d JOIN cap_ai.knowledge_collections c ON c.id=d.collection_id WHERE c.app_id=" + literal(app)) == '0',
                   'synthetic library must be empty')
    marker = 'qualification' + uuid.uuid4().hex
    file = personal.upload_markdown('A', app, role,
                                    ('# Synthetic fixture\n\nThe synthetic project code is BLUE-17. ' + marker +
                                     '\n\nThe unrelated example colour is orange.\n').encode())
    personal.await_clean(file)
    status, raw = package.api('POST', '/documents', 'A', role,
                              {'file_id': file, 'parser_profile': 'standard-text-v1'}, uuid.uuid4().hex)
    source.require(status == 202, 'synthetic document registration failed')
    document = str(uuid.UUID(json.loads(raw)['document_id']))
    generation = await_generation(document, expected['embed'], state['embed']['dimension'])
    scope = {'kind': 'PERSONAL'}
    # Require an actual semantic contribution by excluding a lexical match.
    query = 'Which identifier belongs to the fictitious initiative?'
    lexical = api('/search', role, {'scope': scope, 'query': query, 'retrieval': 'LEXICAL'})
    hybrid = api('/search', role, {'scope': scope, 'query': query, 'retrieval': 'HYBRID'})
    check_semantic(hybrid, document, any(h.get('document_id') == document for h in lexical.get('results', [])))
    before = operation_count(app, 'GENERATE', True)
    reply = api('/answers', role, {'scope': scope, 'query': 'What is the synthetic project code?', 'require_generation': True})
    check_answer(reply, document, set(generation['chunks']), expected['generate'])
    source.require(operation_count(app, 'GENERATE', True) == before + 1 and operation_count(app, 'EMBED', True) > 0,
                   'successful real model operations missing')
    before = operation_count(app)
    denied = api('/answers', role, {'scope': scope, 'query': 'What is the synthetic project code?'}, 'B')
    check_denied(denied)
    source.require(operation_count(app) == before,
                   'denied user caused dispatch or disclosure')
    # The explicitly consented disposable approval stays revoked after this gate.
    approval = str(uuid.UUID(os.environ['RAG_REAL_GENERATE_APPROVAL']))
    result = subprocess.run([os.environ['RAG_REAL_MODEL_CLI'], 'policy', 'revoke',
                             '--approval', approval, '--json',
                             '--reason', 'synthetic live qualification', '--token-file',
                             os.environ['RAG_REAL_ADMIN_TOKEN_FILE']], capture_output=True, timeout=65)
    source.require(result.returncode == 0, 'qualification approval revocation failed')
    check_withdrawal(json.loads(result.stdout), approval)
    audited = db("SELECT count(*) FROM cap_ai.agent_processing_approvals a JOIN cap_ai.agent_processing_approval_events e ON e.approval_id=a.id WHERE a.id=" + literal(approval) + " AND NOT a.enabled AND e.action='DISABLE' AND e.session_identity='dxos_authz' AND e.reason='synthetic live qualification'")
    source.require(audited == '1', 'audited withdrawal missing from disposable database')
    before = operation_count(app, 'GENERATE')
    revoked = api('/answers', role, {'scope': scope, 'query': 'What is the synthetic project code?'})
    check_revoked(revoked, document)
    source.require(operation_count(app, 'GENERATE') == before,
                   'revoked processing approval caused generation')
    print(json.dumps({'status': 'PASS', 'document_id': document, 'run_id': generation['run'],
                      'generation_id': generation['generation'], 'profiles': expected}))
    print('NOT_RUN: live revision switch requires a second independently reviewed revision')


def main():
    if os.environ.get('RAG_REAL_MODELS_OPT_IN') != 'true':
        print('NOT_RUN: missing RAG_REAL_MODELS_OPT_IN')
        return 2
    try:
        run()
    except NotRun as error:
        print(str(error))
        return 2
    except Exception:
        # Transport, CLI and database errors may contain credentials or content.
        print('FAIL: real model qualification failed')
        return 1
    return 0


if __name__ == '__main__':
    raise SystemExit(main())
