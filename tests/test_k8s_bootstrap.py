# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors
"""Exercise ConfigMap generation and the PostgreSQL volume projection together."""
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

import yaml

ROOT = Path(__file__).resolve().parents[1]


class KubernetesBootstrapTests(unittest.TestCase):
    def test_projection_contains_all_bootstrap_migrations(self):
        with tempfile.TemporaryDirectory() as directory:
            tmp = Path(directory)
            # Model kubectl's --from-file/apply boundary; run the actual shell
            # provisioning function, without requiring a cluster or credentials.
            kubectl = tmp / 'kubectl'
            kubectl.write_text('''#!/usr/bin/env python3
import json, os, pathlib, sys
args=sys.argv[1:]
if 'create' in args:
    i=args.index('configmap'); name=args[i+1]; data={}
    for arg in args:
        if arg.startswith('--from-file='):
            key,path=arg[len('--from-file='):].split('=',1)
            data[key]=pathlib.Path(path).read_text()
    print(json.dumps({'metadata':{'name':name},'data':data}))
elif 'apply' in args:
    obj=json.load(sys.stdin)
    if '--server-side' not in args and len(json.dumps(obj, separators=(',', ':')).encode()) > 262144:
        raise SystemExit('last-applied annotation exceeds Kubernetes size limit')
    (pathlib.Path(os.environ['CAPTURE_CONFIGMAPS'])/(obj['metadata']['name']+'.json')).write_text(json.dumps(obj))
''')
            kubectl.chmod(0o755)
            script = (ROOT / 'scripts/k8s/dev-cluster.sh').read_text()
            definitions = script.split('\ncase "${1:-}" in\n', 1)[0]
            result = subprocess.run(['bash', '-c', definitions + '\ncreate_init_configmaps\n',
                                     str(ROOT / 'scripts/k8s/dev-cluster.sh')],
                                    env={**os.environ, 'PATH': str(tmp) + os.pathsep + os.environ['PATH'],
                                         'CAPTURE_CONFIGMAPS': str(tmp)}, capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            maps = {p.stem: json.loads(p.read_text())['data'] for p in tmp.glob('*.json')}
            deployment = list(yaml.safe_load_all((ROOT / 'platform/k8s/dev/postgres.yaml').read_text()))[1]
            volumes = deployment['spec']['template']['spec']['volumes']
            sources = next(v['projected']['sources'] for v in volumes if v['name'] == 'initdb')
            projected = {}
            for source in sources:
                config = source['configMap']
                data = maps[config['name']]
                for item in config.get('items', [{'key': key, 'path': key} for key in data]):
                    projected[item['path']] = data[item['key']]
            # Every SQL dependency, including the shared ledger, must arrive at
            # the same relative path the real bootstrap runner resolves.
            for path in (ROOT / 'platform/postgres/migrations').rglob('*.sql'):
                target = 'migrations/' + path.relative_to(ROOT / 'platform/postgres/migrations').as_posix()
                self.assertIn(target, projected.keys())
                self.assertEqual(projected[target], path.read_text())
