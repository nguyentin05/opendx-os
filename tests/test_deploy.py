# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors
"""Check deploy/: the apply script's boundary and the rendered manifests, without a cluster."""
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tempfile
import unittest

import yaml

ROOT = Path(__file__).resolve().parents[1]
SCRIPT = ROOT / 'deploy/k8s/apply.sh'
SECRETS = re.search(r'SECRETS=\(\n(.*?)\n\)', SCRIPT.read_text(), re.S).group(1).split()

# Records every call and keeps the files passed with --from-file / --from-env-file.
FAKE_KUBECTL = '''#!/usr/bin/env python3
import os, pathlib, shutil, sys
args = sys.argv[1:]
out = pathlib.Path(os.environ['CAPTURE'])
with open(out / 'calls', 'a') as log:
    log.write(' '.join(args) + '\\n')
for arg in args:
    for flag in ('--from-file=', '--from-env-file='):
        if arg.startswith(flag):
            path = arg[len(flag):].split('=', 1)[-1]
            shutil.copy(path, out / pathlib.Path(path).name)
if 'apply' in args:
    sys.stdin.read()
elif 'kustomize' in args:
    print('kind: List')
else:
    print('kind: Stub')
'''


def env_file(directory, **overrides):
    values = {key: 'A1b2C3d4E5f6G7h8' + key.replace('_', '') for key in SECRETS}
    values.update(overrides)
    path = Path(directory) / 'deploy.env'
    path.write_text('# comment\n' + ''.join(f'{k}={v}\n' for k, v in values.items()))
    return path


class ApplyScript(unittest.TestCase):
    def run_script(self, directory, env):
        capture = Path(directory) / 'capture'
        capture.mkdir()
        kubectl = Path(directory) / 'kubectl'
        kubectl.write_text(FAKE_KUBECTL)
        kubectl.chmod(0o755)
        result = subprocess.run(
            [str(SCRIPT), str(ROOT / 'deploy/k8s/overlays/eks'), str(env)],
            capture_output=True, text=True,
            env={**os.environ, 'KUBECTL': str(kubectl), 'CAPTURE': str(capture)},
        )
        return result, capture

    def test_refuses_development_defaults_before_touching_the_cluster(self):
        for key, bad in [('KEYCLOAK_ADMIN_PASSWORD', 'admin'),
                         ('S3_ACCESS_KEY_ID', 'dxos_s3_access_key'),
                         ('KESTRA_PASSWORD', 'change-me-local-kestra'),
                         ('HASURA_ADMIN_SECRET', '')]:
            with self.subTest(key=key), tempfile.TemporaryDirectory() as directory:
                result, capture = self.run_script(directory, env_file(directory, **{key: bad}))
                self.assertEqual(result.returncode, 1)
                self.assertIn(key, result.stderr)
                self.assertFalse((capture / 'calls').exists())

    def test_creates_only_the_secrets(self):
        with tempfile.TemporaryDirectory() as directory:
            env = env_file(directory)
            result, capture = self.run_script(directory, env)
            self.assertEqual(result.returncode, 0, result.stderr)
            calls = (capture / 'calls').read_text()
            for name in ('dxos-secrets', 'seaweedfs-s3', 'dxos-operator-config'):
                self.assertIn(f' {name} ', calls)
            # ConfigMaps are committed manifests now, not built by the script.
            self.assertNotIn('create configmap', calls)
            self.assertIn(f'kustomize {ROOT / "deploy/k8s/overlays/eks"}\n', calls)

            secrets = dict(l.split('=', 1) for l in (capture / 'secrets.env').read_text().split())
            self.assertEqual(set(secrets), set(SECRETS))
            s3 = (capture / 's3.json').read_text()
            self.assertNotIn('dxos_s3_', s3)
            self.assertIn(secrets['S3_SECRET_ACCESS_KEY'], s3)
            self.assertIn('@postgres.dxos.svc:5432/dxos_db', (capture / 'operator.env').read_text())


class GeneratedConfigMaps(unittest.TestCase):
    """The ConfigMaps are generated from platform/, the only copy of the configuration."""

    def test_committed_manifests_are_up_to_date(self):
        result = subprocess.run([sys.executable, str(ROOT / 'scripts/k8s/render-configmaps.py'), '--check'],
                                capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_postgres_seed_restores_the_compose_layout(self):
        doc = yaml.safe_load((ROOT / 'deploy/k8s/base/tier1-platform/configmaps/postgres-seed.yaml').read_text())
        restored = {key.replace('__', '/'): text for key, text in doc['data'].items()}
        base = ROOT / 'platform/postgres'
        expected = {f.name: f for f in (base / 'init').iterdir()}
        expected.update({str(f.relative_to(base)): f for top in ('capabilities', 'migrations')
                         for f in (base / top).rglob('*') if f.is_file()})
        self.assertEqual(set(restored), set(expected))
        for path, f in expected.items():
            self.assertEqual(restored[path], f.read_text(), path)


@unittest.skipUnless(shutil.which('kubectl'), 'kubectl is not installed')
class RenderedManifests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        out = subprocess.run(['kubectl', 'kustomize', str(ROOT / 'deploy/k8s/overlays/eks')],
                             capture_output=True, text=True, check=True).stdout
        cls.docs = [d for d in yaml.safe_load_all(out) if d]

    def kinds(self, kind):
        return [d for d in self.docs if d['kind'] == kind]

    def test_base_has_nothing_provider_specific(self):
        # Provider settings belong in an overlay, so the base runs on any cluster.
        base = subprocess.run(['kubectl', 'kustomize', str(ROOT / 'deploy/k8s/base')],
                              capture_output=True, text=True, check=True).stdout.lower()
        for marker in ('amazonaws', 'alb.ingress', 'ebs.csi', 'eks.', 'gke.', 'azure', 'storageclassname'):
            self.assertNotIn(marker, base)

    def test_no_secret_is_rendered(self):
        self.assertEqual(self.kinds('Secret'), [])

    def test_workloads_read_only_what_apply_sh_creates(self):
        created = {c['metadata']['name'] for c in self.kinds('ConfigMap')}
        for workload in self.kinds('Deployment') + self.kinds('StatefulSet'):
            spec = workload['spec']['template']['spec']
            for container in spec.get('containers', []) + spec.get('initContainers', []):
                for env in container.get('env', []):
                    ref = env.get('valueFrom', {})
                    if 'secretKeyRef' in ref:
                        self.assertEqual(ref['secretKeyRef']['name'], 'dxos-secrets')
                        self.assertIn(ref['secretKeyRef']['key'], SECRETS)
                    if 'configMapKeyRef' in ref:
                        self.assertTrue(ref['configMapKeyRef']['name'].startswith('dxos-public-'))
            for volume in spec.get('volumes', []):
                if 'configMap' in volume:
                    self.assertIn(volume['configMap']['name'], created)
                if 'secret' in volume:
                    self.assertEqual(volume['secret']['secretName'], 'seaweedfs-s3')

    def test_gateway_upstreams_exist(self):
        services = {(s['metadata']['name'], p['port']) for s in self.kinds('Service')
                    if s['metadata'].get('namespace') == 'dxos' for p in s['spec']['ports']}
        apisix = (ROOT / 'platform/apisix/apisix.yaml').read_text()
        upstreams = {(h, int(p)) for h, p in re.findall(r'"([a-z0-9-]+):(\d+)": 1', apisix)}
        # cap-ai runs only with the RAG overlay of Compose and is not deployed here.
        self.assertEqual(upstreams - services, {('cap-ai', 8084)})

    def test_only_the_ui_gateway_and_keycloak_are_public(self):
        backends = set()
        for rule in self.kinds('Ingress')[0]['spec']['rules']:
            for path in rule['http']['paths']:
                backends.add((path['path'], path['backend']['service']['name']))
        self.assertEqual({name for _, name in backends}, {'apisix', 'workspace-ui', 'keycloak', 'not-found'})
        self.assertIn(('/admin', 'not-found'), backends)
        self.assertIn(('/realms/master', 'not-found'), backends)


if __name__ == '__main__':
    unittest.main()
