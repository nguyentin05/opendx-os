# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors
"""Check deploy/: the apply script's boundary and the rendered manifests, without a cluster."""
import contextlib
import importlib.util
import io
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
        doc = yaml.safe_load((ROOT / 'deploy/k8s/base/platform/postgres/configmap.yaml').read_text())
        restored = {key.replace('__', '/'): text for key, text in doc['data'].items()}
        base = ROOT / 'platform/postgres'
        expected = {f.name: f for f in (base / 'init').iterdir()}
        expected.update({str(f.relative_to(base)): f for top in ('capabilities', 'migrations')
                         for f in (base / top).rglob('*') if f.is_file()})
        self.assertEqual(set(restored), set(expected))
        for path, f in expected.items():
            # Byte for byte: the migration ledger checksums whole files.
            self.assertEqual(restored[path], f.read_text(), path)

    def test_configuration_keeps_everything_but_the_license_header(self):
        doc = yaml.safe_load((ROOT / 'deploy/k8s/base/platform/apisix/configmap.yaml').read_text())
        for key in ('apisix.yaml', 'config.yaml'):
            source = (ROOT / 'platform/apisix' / key).read_text()
            data = doc['data'][key]
            self.assertNotIn('SPDX-License-Identifier', data)
            self.assertTrue(source.endswith(data), key)
        # APISIX standalone mode loads apisix.yaml only once it sees the closing marker.
        self.assertEqual(doc['data']['apisix.yaml'].rstrip().splitlines()[-1], '#END')


def load_parity():
    spec = importlib.util.spec_from_file_location('parity', ROOT / 'scripts/k8s/check-compose-parity.py')
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


@unittest.skipUnless(shutil.which('kubectl'), 'kubectl is not installed')
class ComposeParity(unittest.TestCase):
    """compose.yaml and deploy/k8s/base must not drift apart."""

    def run_with(self, change):
        parity = load_parity()
        services = parity.compose_services()
        change(services, parity)
        parity.compose_services = lambda: services
        with contextlib.redirect_stderr(io.StringIO()) as err:
            code = parity.main()
        return code, err.getvalue()

    def test_current_tree_agrees(self):
        code, err = self.run_with(lambda services, parity: None)
        self.assertEqual(code, 0, err)

    def test_reports_each_kind_of_drift(self):
        cases = {
            'image': lambda s, p: s['keycloak'].update(image='quay.io/keycloak/keycloak:26.1'),
            'NEW_SETTING only in compose': lambda s, p: s['cap-hr']['env'].add('NEW_SETTING'),
            'not in deploy/k8s/base': lambda s, p: s.update(mailer={'image': 'mailhog', 'built': False,
                                                                    'env': set(), 'ports': set()}),
            'no longer needed': lambda s, p: s['keycloak']['env'].add('KC_PROXY_HEADERS'),
        }
        for expected, change in cases.items():
            with self.subTest(expected):
                code, err = self.run_with(change)
                self.assertEqual(code, 1)
                self.assertIn(expected, err)


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
        for workload in self.kinds('Deployment') + self.kinds('StatefulSet') + self.kinds('Job'):
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

    def test_workloads_are_hardened(self):
        # Accepted exceptions, each recorded with its reason in .trivyignore.yaml.
        writable_root = {'apisix', 'keycloak', 'clamav'}
        starts_as_root = {'clamav'}
        for workload in self.kinds('Deployment') + self.kinds('StatefulSet') + self.kinds('Job'):
            name = workload['metadata']['name']
            with self.subTest(name):
                pod = workload['spec']['template']['spec']
                context = pod.get('securityContext', {})
                self.assertEqual(context.get('seccompProfile'), {'type': 'RuntimeDefault'})
                if name not in starts_as_root:
                    self.assertTrue(context.get('runAsNonRoot'))
                    # A named image user cannot be checked by the kubelet; the controller's
                    # image already declares the numeric USER 10001:10001 (operator/Dockerfile).
                    if name != 'dxos-operator-controller-manager':
                        self.assertIsInstance(context.get('runAsUser'), int)
                for container in pod.get('containers', []) + pod.get('initContainers', []):
                    c = container.get('securityContext', {})
                    self.assertIs(c.get('allowPrivilegeEscalation'), False)
                    self.assertEqual(c.get('capabilities', {}).get('drop'), ['ALL'])
                    if name not in writable_root:
                        self.assertIs(c.get('readOnlyRootFilesystem'), True)
                    if name not in starts_as_root:
                        self.assertNotIn('add', c.get('capabilities', {}))

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


@unittest.skipUnless(shutil.which('kubectl'), 'kubectl is not installed')
class KedaComponent(unittest.TestCase):
    """components/keda (#175): opt-in; Tier 2 services and ClamAV run only while they have work,
    and the scaler never uses an administrator login."""

    @classmethod
    def setUpClass(cls):
        with tempfile.TemporaryDirectory(dir=ROOT / 'deploy') as overlay:
            rel = lambda p: os.path.relpath(ROOT / p, overlay)
            Path(overlay, 'kustomization.yaml').write_text(
                f"resources: [{rel('deploy/k8s/overlays/eks')}]\ncomponents: [{rel('deploy/k8s/components/keda')}]\n")
            out = subprocess.run(['kubectl', 'kustomize', overlay], capture_output=True, text=True, check=True).stdout
        cls.docs = [d for d in yaml.safe_load_all(out) if d]

    def one(self, kind, name):
        return next(d for d in self.docs if d['kind'] == kind and d['metadata']['name'] == name)

    def test_keda_owns_the_clamav_replicas(self):
        self.assertNotIn('replicas', self.one('Deployment', 'clamav')['spec'])
        scaled = self.one('ScaledObject', 'clamav')['spec']
        self.assertEqual((scaled['scaleTargetRef']['name'], scaled['minReplicaCount'], scaled['maxReplicaCount']),
                         ('clamav', 0, 1))

    def test_tier_2_services_run_on_demand(self):
        for name, port, most in (('cap-hr', 8083, 1), ('cap-process', 8081, 3)):
            with self.subTest(name):
                self.assertNotIn('replicas', self.one('Deployment', name)['spec'])
                # APISIX and Kestra keep calling <name>:<port>; it now leads to the interceptor.
                entry = self.one('Service', name)['spec']
                self.assertEqual((entry['type'], entry['externalName'], entry['ports'][0]['port']),
                                 ('ExternalName', f'dxos-{name}.keda.svc.cluster.local', port))
                gate = self.one('Service', f'dxos-{name}')
                self.assertEqual((gate['metadata']['namespace'], gate['spec']['ports'][0]),
                                 ('keda', {'name': 'http', 'port': port, 'targetPort': 'proxy'}))
                app = self.one('Service', f'{name}-app')['spec']
                self.assertEqual((app['selector'], app['ports'][0]['port']), ({'app': name}, port))
                http = self.one('HTTPScaledObject', name)['spec']
                self.assertEqual((http['scaleTargetRef']['service'], http['scaleTargetRef']['port'],
                                  http['replicas']), (f'{name}-app', port, {'min': 0, 'max': most}))
        # cap-process also wakes for background work, with the scaler's own login.
        scaled = self.one('ScaledObject', 'cap-process')['spec']
        kinds = {t['type']: t['metadata'] for t in scaled['triggers']}
        self.assertEqual(kinds['external-push']['httpScaledObject'], 'cap-process')
        self.assertEqual((kinds['postgresql']['userName'], kinds['postgresql']['query']),
                         ('dxos_keda', 'SELECT keda.process_work_waiting()'))
        self.assertEqual(self.one('HTTPScaledObject', 'cap-process')['metadata']['annotations'],
                         {'httpscaledobject.keda.sh/skip-scaledobject-creation': 'true'})

    def test_scaler_uses_its_own_login(self):
        trigger = self.one('ScaledObject', 'clamav')['spec']['triggers'][0]['metadata']
        self.assertEqual(trigger['userName'], 'dxos_keda')
        self.assertEqual(trigger['query'], 'SELECT keda.files_waiting_for_scan()')
        auth = self.one('TriggerAuthentication', 'dxos-keda-postgres')['spec']['secretTargetRef']
        self.assertEqual({(a['name'], a['key']) for a in auth}, {('dxos-keda', 'KEDA_DATABASE_PASSWORD')})

    def test_setup_job_is_hardened(self):
        pod = self.one('Job', 'keda-setup')['spec']['template']['spec']
        self.assertTrue(pod['securityContext']['runAsNonRoot'])
        c = pod['containers'][0]['securityContext']
        self.assertEqual((c['allowPrivilegeEscalation'], c['readOnlyRootFilesystem'], c['capabilities']),
                         (False, True, {'drop': ['ALL']}))


@unittest.skipUnless(shutil.which('kubectl'), 'kubectl is not installed')
class EveryOverlay(unittest.TestCase):
    """Each example overlay (eks, gke, aks, onprem, and any added later) keeps the same public surface."""

    @staticmethod
    def routes(docs):
        """(host, path, backend) for every Ingress path and HTTPRoute rule."""
        out = set()
        for ingress in (d for d in docs if d['kind'] == 'Ingress'):
            for rule in ingress['spec']['rules']:
                for path in rule['http']['paths']:
                    out.add((rule['host'], path['path'], path['backend']['service']['name']))
        for route in (d for d in docs if d['kind'] == 'HTTPRoute'):
            for host in route['spec'].get('hostnames', ['*']):
                for rule in route['spec']['rules']:
                    for backend in rule.get('backendRefs', []):
                        for match in rule.get('matches', [{'path': {'value': '/'}}]):
                            out.add((host, match['path']['value'], backend['name']))
        return out

    def test_public_surface_and_storage(self):
        overlays = sorted(p.parent for p in (ROOT / 'deploy/k8s/overlays').glob('*/kustomization.yaml'))
        self.assertGreaterEqual(len(overlays), 4)
        for overlay in overlays:
            with self.subTest(overlay.name):
                out = subprocess.run(['kubectl', 'kustomize', str(overlay)],
                                     capture_output=True, text=True, check=True).stdout
                docs = [d for d in yaml.safe_load_all(out) if d]
                for entry in (d for d in docs if d['kind'] in ('Ingress', 'Gateway', 'HTTPRoute')):
                    self.assertEqual(entry['metadata'].get('namespace'), 'dxos')
                routes = self.routes(docs)
                self.assertEqual({backend for _, _, backend in routes},
                                 {'apisix', 'workspace-ui', 'keycloak', 'not-found'})
                for path in ('/admin', '/realms/master'):
                    self.assertIn(('auth.example.com', path, 'not-found'), routes)
                self.assertIn(('auth.example.com', '/', 'keycloak'), routes)
                self.assertIn(('app.example.com', '/v1/graphql', 'apisix'), routes)
                # Sign-in breaks when one public origin disagrees with the hosts.
                public = next(d['data'] for d in docs if d['kind'] == 'ConfigMap'
                              and d['metadata']['name'].startswith('dxos-public'))
                auth = 'https://' + public['AUTH_HOST']
                self.assertEqual((public['KEYCLOAK_HOSTNAME'], public['DXOS_OIDC_ORIGIN'],
                                  public['OIDC_ISSUER_URL'], public['DXOS_APP_URL']),
                                 (auth, auth, auth + '/realms/dxos', 'https://' + public['APP_HOST']))
                # An overlay may define the default StorageClass or use the cluster's.
                defaults = [d for d in docs if d['kind'] == 'StorageClass' and d['metadata'].get(
                    'annotations', {}).get('storageclass.kubernetes.io/is-default-class') == 'true']
                self.assertLessEqual(len(defaults), 1)


if __name__ == '__main__':
    unittest.main()
