#!/usr/bin/env python3
# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors
"""Write the Kubernetes ConfigMaps built from the configuration files Compose mounts.

    scripts/k8s/render-configmaps.py          rewrite deploy/k8s/base/platform/<service>/configmap.yaml
    scripts/k8s/render-configmaps.py --check  exit 1 if a committed file is out of date

platform/ stays the only copy of the configuration: edit the file there and run
`make k8s-config`; tests/test_deploy.py fails while the manifests are stale.
The license header of a configuration file is left out of the ConfigMap (the
manifest carries its own), except in postgres-seed: migration checksums are
taken over the whole file, so those files stay byte for byte as in platform/.
"""
import sys
from pathlib import Path

import yaml

ROOT = Path(__file__).resolve().parents[2]
OUT = ROOT / 'deploy/k8s/base/platform'
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
    """ConfigMap name -> (service folder, keep files verbatim, {key: file})."""
    platform = ROOT / 'platform'
    return {
        'postgres-seed': ('postgres', True, seed_files()),
        'keycloak-realm': ('keycloak', False, {'realm-export.json': platform / 'keycloak/realm-export.json',
                                               'directory-client.sh': platform / 'keycloak/directory-client.sh'}),
        'kestra-config': ('kestra', False, {'config.yaml': platform / 'kestra/config.yaml',
                                            'start.sh': platform / 'kestra/start.sh'}),
        'apisix-config': ('apisix', False, {'config.yaml': platform / 'apisix/config.yaml',
                                            'apisix.yaml': platform / 'apisix/apisix.yaml'}),
    }


def without_license(text):
    """Drops the Apache license comment block (SPDX line to 'limitations under the License.',
    or the SPDX and Copyright lines of a short header) and the blank or bare-comment
    line after it; a shebang stays first."""
    lines = text.splitlines(keepends=True)
    start = next((i for i, l in enumerate(lines[:3]) if 'SPDX-License-Identifier' in l), None)
    if start is None:
        return text
    end = next((i for i, l in enumerate(lines) if 'limitations under the License.' in l), start + 1) + 1
    if end < len(lines) and lines[end].strip() in ('', '#'):
        end += 1
    return ''.join(lines[:start] + lines[end:])


class Dumper(yaml.SafeDumper):
    pass


# Multi-line file contents as literal blocks, so the manifest reads like the file.
Dumper.add_representer(str, lambda d, s: d.represent_scalar(
    'tag:yaml.org,2002:str', s, style='|' if '\n' in s else None))


def render(name, verbatim, files):
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
        'data': {key: (lambda t: t if verbatim else without_license(t))(path.read_text(encoding='utf-8'))
                 for key, path in sorted(files.items())},
    }
    return header + yaml.dump(manifest, Dumper=Dumper, sort_keys=False, allow_unicode=True, width=4096)


def main():
    check = sys.argv[1:] == ['--check']
    stale = []
    for name, (folder, verbatim, files) in sources().items():
        target = OUT / folder / 'configmap.yaml'
        text = render(name, verbatim, files)
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
