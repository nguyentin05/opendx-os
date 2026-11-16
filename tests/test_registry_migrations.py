# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors
"""Optional migration selection; fake psql captures SQL without a database."""
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]

class RegistryMigrationSelection(unittest.TestCase):
    def capture(self, args, **settings):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory)
            psql = path / 'psql'
            psql.write_text('#!/usr/bin/env python3\nimport sys,os\nfrom pathlib import Path\na=sys.argv\ns=Path(a[a.index("-f")+1]).read_text() if "-f" in a else sys.stdin.read()\nPath(os.environ["SQL_CAPTURE"]).write_text(s)\n')
            psql.chmod(0o755)
            env = dict(os.environ, PATH=str(path)+os.pathsep+os.environ['PATH'], SQL_CAPTURE=str(path/'sql'), MIGRATION_TEST_URL='postgres://test@localhost/registry_test')
            env.pop('RAG_MODELS_SCHEMA_ENABLED', None)
            env.pop('RAG_GLOBAL_MODEL_REGISTRY_SCHEMA_ENABLED', None)
            env.update(settings)
            result = subprocess.run(args, cwd=ROOT, env=env, capture_output=True, text=True)
            sql = (path/'sql').read_text() if (path/'sql').exists() else ''
            return result, sql

    def test_explicit_registry_dependency_and_checksums(self):
        result, sql = self.capture(['scripts/migrate-rag.sh','--database-url-env','MIGRATION_TEST_URL','--scope','cap-ai','--through','0005'])
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertLess(sql.index('CREATE TABLE cap_ai.model_profiles'), sql.index('CREATE TABLE cap_ai.model_candidates'))
        for version in ('0004', '0005'):
            self.assertIn("VALUES('cap-ai','"+version+"',", sql)
        _, sql = self.capture(['scripts/migrate-rag.sh','--database-url-env','MIGRATION_TEST_URL','--scope','cap-ai','--through','0003'])
        self.assertNotIn('CREATE TABLE cap_ai.model_profiles', sql)
        self.assertNotIn('CREATE TABLE cap_ai.model_candidates', sql)

    def test_bootstrap_default_and_independent_opt_in(self):
        command = ['sh','platform/postgres/init/09_rag_migrations.sh']
        result, sql = self.capture(command)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertNotIn('/0004_models_vectors.sql', sql)
        self.assertNotIn('/0005_model_registry.sql', sql)
        result, sql = self.capture(command, RAG_MODELS_SCHEMA_ENABLED='true')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn('/0004_models_vectors.sql', sql)
        self.assertNotIn('/0005_model_registry.sql', sql)
        result, sql = self.capture(command, RAG_MODELS_SCHEMA_ENABLED='true', RAG_GLOBAL_MODEL_REGISTRY_SCHEMA_ENABLED='true')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertLess(sql.index('/0004_models_vectors.sql'), sql.index('/0005_model_registry.sql'))
        self.assertIn("version='0005'", sql)
        result, _ = self.capture(command, RAG_GLOBAL_MODEL_REGISTRY_SCHEMA_ENABLED='true')
        self.assertNotEqual(result.returncode, 0)
