# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors
"""Offline guards for the explicit live-provider gate."""
import contextlib
import io
import os
import unittest
from unittest.mock import patch
import rag_real_models as real


class RealModelsTests(unittest.TestCase):
    def test_real_models_requires_opt_in(self):
        with patch.dict(os.environ, {}, clear=True), patch.object(real.source, 'db') as db:
            output = io.StringIO()
            with contextlib.redirect_stdout(output):
                self.assertEqual(real.main(), 2)
            self.assertIn('NOT_RUN: missing RAG_REAL_MODELS_OPT_IN', output.getvalue())
            db.assert_not_called()

    def test_real_models_missing_prerequisites(self):
        with patch.dict(os.environ, {'RAG_REAL_MODELS_OPT_IN': 'true'}, clear=True), patch.object(real.source, 'request') as request:
            with contextlib.redirect_stdout(io.StringIO()):
                self.assertEqual(real.main(), 2)
            request.assert_not_called()

    def test_real_models_rejects_non_disposable_or_fake_profile(self):
        for url in ('postgres://localhost/production', 'postgres://localhost/contestproduction'):
            with self.assertRaises(real.NotRun):
                real.check_target(url)
        state = self.state()
        state['embed']['provider_id'] = 'rag-test-fake'
        with self.assertRaises(real.NotRun):
            real.check_state(state, self.expected(), 'review-17')

    def expected(self):
        return {'embed': ('embed-real', 2), 'generate': ('generate-real', 3)}

    def state(self):
        return {name: {'id': profile, 'revision': rev, 'purpose': purpose,
                       'provider_id': provider, 'enabled': True, 'execution': 'HOSTED',
                       'slot_matches': True, 'review_ref': 'review-17', 'approval_matches': True,
                       'dimension': dimension}
                for name, profile, rev, purpose, provider, dimension in (
                    ('embed', 'embed-real', 2, 'EMBED', 'vendor-a', 3),
                    ('generate', 'generate-real', 3, 'GENERATE', 'vendor-b', None))}

    def test_real_models_requires_review_and_exact_slots(self):
        real.check_state(self.state(), self.expected(), 'review-17')
        for field, value in (('slot_matches', False), ('approval_matches', False), ('review_ref', 'wrong'), ('revision', 99)):
            state = self.state()
            state['embed'][field] = value
            with self.subTest(field=field), self.assertRaises(real.NotRun):
                real.check_state(state, self.expected(), 'review-17')

    def test_real_models_requires_real_semantic_and_generation_evidence(self):
        reply = {'status': 'GROUNDED', 'answer': 'BLUE-17', 'model_profile': {'id': 'generate-real', 'revision': 3},
                 'citations': [{'document_id': 'doc', 'chunk_id': 'chunk'}]}
        real.check_answer(reply, 'doc', {'chunk'}, ('generate-real', 3))
        for field in ('answer', 'citations', 'model_profile'):
            bad = dict(reply)
            bad.pop(field)
            with self.assertRaises(RuntimeError):
                real.check_answer(bad, 'doc', {'chunk'}, ('generate-real', 3))
        with self.assertRaises(RuntimeError):
            real.check_semantic({'status': 'LEXICAL_ONLY', 'results': [{'document_id': 'doc'}]}, 'doc', False)
        with self.assertRaises(RuntimeError):
            real.check_semantic({'status': 'HYBRID', 'results': [{'document_id': 'doc'}]}, 'doc', True)

    def test_real_models_redacts_secrets(self):
        with patch.dict(os.environ, {'RAG_REAL_MODELS_OPT_IN': 'true'}, clear=True), patch.object(real, 'run', side_effect=RuntimeError('vendor-secret-canary')):
            output = io.StringIO()
            with contextlib.redirect_stdout(output):
                self.assertEqual(real.main(), 1)
            self.assertNotIn('vendor-secret-canary', output.getvalue())

    def test_real_models_deadline(self):
        with patch.object(real.time, 'monotonic', side_effect=[0, 301]), patch.object(real.source, 'db') as db:
            with self.assertRaises(RuntimeError):
                real.await_generation('doc', ('embed-real', 2), 3)
            db.assert_not_called()

class LiveDispatchTests(unittest.TestCase):
    def test_transport_failure_is_not_retried(self):
        with patch.object(real.package, 'api', side_effect=TimeoutError) as request:
            with self.assertRaises(TimeoutError):
                real.api('/answers', 'role_test', {})
            self.assertEqual(request.call_count, 1)

    def test_fake_and_live_shell_dispatch(self):
        import subprocess
        import tempfile
        from pathlib import Path
        required = '''TEST_DATABASE_URL RAG_TEST_HASURA_URL RAG_TEST_HASURA_ADMIN_SECRET
        RAG_TEST_FILE_API_URL RAG_TEST_OIDC_TOKEN_A RAG_TEST_OIDC_TOKEN_B RAG_TEST_OIDC_TOKEN_ADMIN
        RAG_TEST_AI_API_URL RAG_TEST_MODEL_FAKE_URL RAG_TEST_MODEL_WORKER_FAKE_URL
        RAG_TEST_EMBED_PROFILE RAG_TEST_GENERATE_PROFILE RAG_TEST_UI_URL RAG_TEST_UI_STORAGE_A
        RAG_TEST_OPERATOR RAG_TEST_DISPATCHER RAG_DISPATCHER_BEARER_TOKEN AI_DISPATCHER_DATABASE_URL
        RAG_PACKAGE_ARTIFACT_ROOT CLAMAV_ADDRESS RAG_INTERNAL_AI_URL RAG_SOURCE_QUALIFIED
        RAG_METADATA_QUALIFIED RAG_PARSER_QUALIFIED'''.split()
        with tempfile.TemporaryDirectory() as directory:
            python = Path(directory) / 'python3'
            python.write_text('#!/bin/sh\nbasename "$1"\n')
            python.chmod(0o700)
            env = {name: 'synthetic' for name in required}
            env['PATH'] = directory + ':/usr/bin:/bin'
            script = Path(__file__).resolve().parents[1] / 'test-rag.sh'
            for mode, filename in (('models', 'rag_models.py'), ('real-models', 'rag_real_models.py')):
                result = subprocess.run(['bash', str(script), mode], env=env, capture_output=True, text=True)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(result.stdout.strip(), filename)


if __name__ == '__main__':
    unittest.main()
