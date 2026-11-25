#!/usr/bin/env python3
# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors
"""Write the Kubernetes ConfigMaps built from the configuration files Compose mounts.

    scripts/k8s/render-configmaps.py          rewrite deploy/k8s/base/tier1-platform/configmaps/
    scripts/k8s/render-configmaps.py --check  exit 1 if a committed file is out of date

platform/ stays the only copy of the configuration: edit the file there and run
`make k8s-config`; tests/test_deploy.py fails while the manifests are stale.
"""
import sys
from pathlib import Path

import yaml

ROOT = Path(__file__).resolve().parents[2]
OUT = ROOT / 'deploy/k8s/base/tier1-platform/configmaps'
SEED_DIRS = ('init', 'capabilities', 'migrations')


def seed_files():
    """The PostgreSQL init files in the layout Compose mounts into /docker-entrypoint-initdb.d.

    ConfigMap keys cannot contain '/', so 'migrations/core/0001.sql' becomes
    'migrations__core__0001.sql'; the init container restores the directories.
    """
    base = ROOT / 'platform/postgres'
    files = {}
    for top in SEED_DIRS:
        for path in sorted((base / top).rglob('*')):
            if path.is_file():
                relative = path.relative_to(base / 'init' if top == 'init' else base)
                key = '__'.join(relative.parts)
                assert '__' not in path.name, f'{path}: "__" is reserved for directory separators'
                files[key] = path
    return files


def sources():
    platform = ROOT / 'platform'
    return {
        'postgres-seed': seed_files(),
        'keycloak-realm': {'realm-export.json': platform / 'keycloak/realm-export.json'},
        'kestra-config': {'config.yaml': platform / 'kestra/config.yaml',
                          'start.sh': platform / 'kestra/start.sh'},
        'apisix-config': {'config.yaml': platform / 'apisix/config.yaml',
                          'apisix.yaml': platform / 'apisix/apisix.yaml'},
    }


class Dumper(yaml.SafeDumper):
    pass


# Multi-line file contents as literal blocks, so the manifest reads like the file.
Dumper.add_representer(str, lambda d, s: d.represent_scalar(
    'tag:yaml.org,2002:str', s, style='|' if '\n' in s else None))


def render(name, files):
    sources_list = sorted({str(p.relative_to(ROOT).parent) for p in files.values()})
    header = (
        '# SPDX-License-Identifier: Apache-2.0\n'
        '# Copyright 2026 DX-OS Open-Core Contributors\n'
        f'# Generated from {", ".join(sources_list)} by `make k8s-config`. Do not edit:\n'
        '# change the file under platform/ and regenerate.\n'
    )
    manifest = {
        'apiVersion': 'v1',
        'kind': 'ConfigMap',
        'metadata': {'name': name},
        'data': {key: path.read_text(encoding='utf-8') for key, path in sorted(files.items())},
    }
    return header + yaml.dump(manifest, Dumper=Dumper, sort_keys=False, allow_unicode=True, width=4096)


def main():
    check = sys.argv[1:] == ['--check']
    stale = []
    OUT.mkdir(parents=True, exist_ok=True)
    for name, files in sources().items():
        target = OUT / f'{name}.yaml'
        text = render(name, files)
        if target.exists() and target.read_text(encoding='utf-8') == text:
            continue
        stale.append(target.relative_to(ROOT))
        if not check:
            target.write_text(text, encoding='utf-8')
    if check and stale:
        print('Out of date (run `make k8s-config`): ' + ', '.join(map(str, stale)), file=sys.stderr)
        return 1
    return 0


if __name__ == '__main__':
    sys.exit(main())
