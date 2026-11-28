#!/usr/bin/env python3
# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors
"""Compare compose.yaml with the Kubernetes base, so the two deployments do not drift.

    scripts/k8s/check-compose-parity.py

For every service: it exists on both sides, runs the same image (third-party
images) or the image built from the same source (ghcr.io/<owner>/opendx-<name>),
listens on the same container port, and gets the same environment variable
names. Values are not compared: they differ by design (localhost against a real
domain, secrets). Every intended difference is listed in ALLOWED with its reason;
anything else fails. Needs kubectl (for `kubectl kustomize`) and PyYAML.
"""
import re
import subprocess
import sys
from pathlib import Path

import yaml

ROOT = Path(__file__).resolve().parents[2]

# Compose service -> Kubernetes workload, when the names differ.
RENAMED = {'operator': 'dxos-operator-controller-manager'}

# (service, variable): why it exists on one side only.
ALLOWED = {
    ('postgres', 'PGDATA'): 'k8s only: a fresh volume has lost+found at its root, so the data lives in a subdirectory',
    ('postgres', 'AI_DATABASE_PASSWORD'): 'compose only: cap-ai is not deployed on Kubernetes yet',
    ('postgres', 'AI_WORKER_DATABASE_PASSWORD'): 'compose only: cap-ai is not deployed on Kubernetes yet',
    ('postgres', 'AI_DISPATCHER_DATABASE_PASSWORD'): 'compose only: cap-ai is not deployed on Kubernetes yet',
    ('hasura', 'DB_PASSWORD'): 'k8s only: the connection URL is built from the Secret with $(DB_PASSWORD)',
    ('authz', 'DB_PASSWORD'): 'k8s only: the connection URL is built from the Secret with $(DB_PASSWORD)',
    ('cap-hr', 'DB_PASSWORD'): 'k8s only: the connection URL is built from the Secret with $(DB_PASSWORD)',
    ('cap-process', 'DB_PASSWORD'): 'k8s only: the connection URL is built from the Secret with $(DB_PASSWORD)',
    ('keycloak', 'KC_PROXY_HEADERS'): 'k8s only: Keycloak runs behind the Ingress, which sets X-Forwarded-*',
    ('keycloak', 'KC_DB'): 'k8s only: production mode on PostgreSQL (#170); Compose keeps start-dev with a file store',
    ('keycloak', 'KC_DB_URL'): 'k8s only: production mode on PostgreSQL (#170); Compose keeps start-dev with a file store',
    ('keycloak', 'KC_DB_USERNAME'): 'k8s only: production mode on PostgreSQL (#170); Compose keeps start-dev with a file store',
    ('keycloak', 'KC_DB_PASSWORD'): 'k8s only: production mode on PostgreSQL (#170); Compose keeps start-dev with a file store',
    ('keycloak', 'KC_HTTP_ENABLED'): 'k8s only: production mode on PostgreSQL (#170); Compose keeps start-dev with a file store',
    ('keycloak', 'KC_CACHE'): 'k8s only: production mode would form a cluster cache; one replica keeps it local',
    ('keycloak', 'KC_HOSTNAME_ADMIN'): 'k8s only: the admin console answers only through a port-forward; Compose serves it on localhost',
    # Telemetry is opt-in on Kubernetes: the components/otel overlay sets these
    # on the workloads, while Compose reads them from .env. The base stays
    # unchanged when the component is not used.
    ('authz', 'OTEL_EXPORTER_OTLP_ENDPOINT'): 'compose only: the opt-in deploy/k8s/components/otel component sets it on Kubernetes',
    ('authz', 'OTEL_SERVICE_NAME'): 'compose only: the opt-in deploy/k8s/components/otel component sets it on Kubernetes',
    ('authz', 'OTEL_RESOURCE_ATTRIBUTES'): 'compose only: the opt-in deploy/k8s/components/otel component sets it on Kubernetes',
    ('cap-hr', 'OTEL_EXPORTER_OTLP_ENDPOINT'): 'compose only: the opt-in deploy/k8s/components/otel component sets it on Kubernetes',
    ('cap-hr', 'OTEL_SERVICE_NAME'): 'compose only: the opt-in deploy/k8s/components/otel component sets it on Kubernetes',
    ('cap-hr', 'OTEL_RESOURCE_ATTRIBUTES'): 'compose only: the opt-in deploy/k8s/components/otel component sets it on Kubernetes',
    ('cap-process', 'OTEL_EXPORTER_OTLP_ENDPOINT'): 'compose only: the opt-in deploy/k8s/components/otel component sets it on Kubernetes',
    ('cap-process', 'OTEL_SERVICE_NAME'): 'compose only: the opt-in deploy/k8s/components/otel component sets it on Kubernetes',
    ('cap-process', 'OTEL_RESOURCE_ATTRIBUTES'): 'compose only: the opt-in deploy/k8s/components/otel component sets it on Kubernetes',
    ('keycloak', 'KC_FEATURES'): 'compose only: the opt-in deploy/k8s/components/otel component sets it on Kubernetes',
    ('keycloak', 'KC_TRACING_ENABLED'): 'compose only: the opt-in deploy/k8s/components/otel component sets it on Kubernetes',
    ('keycloak', 'KC_TRACING_ENDPOINT'): 'compose only: the opt-in deploy/k8s/components/otel component sets it on Kubernetes',
    ('keycloak', 'KC_TRACING_PROTOCOL'): 'compose only: the opt-in deploy/k8s/components/otel component sets it on Kubernetes',
    ('keycloak', 'KC_METRICS_ENABLED'): 'compose only: the opt-in deploy/k8s/components/otel component sets it on Kubernetes',
    ('keycloak', 'KC_LOG_CONSOLE_OUTPUT'): 'compose only: the opt-in deploy/k8s/components/otel component sets it on Kubernetes',
    ('kestra', 'KESTRA_OTEL_ENABLED'): 'compose only: the opt-in deploy/k8s/components/otel component sets it on Kubernetes',
    ('kestra', 'KESTRA_TRACES_ROOT'): 'compose only: the opt-in deploy/k8s/components/otel component sets it on Kubernetes',
    ('kestra', 'KESTRA_OTLP_METRICS_ENABLED'): 'compose only: the opt-in deploy/k8s/components/otel component sets it on Kubernetes',
    ('kestra', 'OTEL_EXPORTER_OTLP_ENDPOINT'): 'compose only: the opt-in deploy/k8s/components/otel component sets it on Kubernetes',
    ('kestra', 'KESTRA_OTEL_ENDPOINT'): 'compose only: the opt-in deploy/k8s/components/otel component sets it on Kubernetes',
    ('kestra', 'OTEL_TRACES_EXPORTER'): 'compose only: the opt-in deploy/k8s/components/otel component sets it on Kubernetes',
    ('apisix', 'OTEL_APISIX_SAMPLER'): 'compose only: the opt-in deploy/k8s/components/otel component sets it on Kubernetes',
    ('apisix', 'OTEL_COLLECTOR_ADDRESS'): 'compose only: the opt-in deploy/k8s/components/otel component sets it on Kubernetes',
}
# The controller is configured from its own Secret (dxos-operator-config) and
# operator/config/default, not from per-variable environment entries.
SKIP_ENV = {'operator'}

# Compose builds an image; Kubernetes runs the upstream one. Services in this
# set compare ports and variables as usual but not the image.
BUILT_VS_UPSTREAM = {
    'postgres': 'Kubernetes keeps postgres:17-alpine without pgvector and PGMQ; '
                'the AI database image follows in a separate issue',
}


def default(value):
    """'${VAR:-default}' -> 'default'; anything else unchanged."""
    match = re.fullmatch(r'\$\{[A-Z0-9_]+:-([^}]*)\}', value)
    return match.group(1) if match else value


def compose_services():
    services = {}
    for name, svc in yaml.safe_load((ROOT / 'compose.yaml').read_text())['services'].items():
        env = svc.get('environment') or {}
        names = set(env) if isinstance(env, dict) else {e.split('=', 1)[0] for e in env}
        build = svc.get('build')
        ports = set()
        for entry in svc.get('ports') or []:
            ports.add(int(str(entry).rsplit(':', 1)[-1].split('/')[0]))
        services[name] = {'image': default(svc['image']) if 'image' in svc else None,
                          'built': build is not None, 'env': names, 'ports': ports}
    return services


def kubernetes_workloads():
    out = subprocess.run(['kubectl', 'kustomize', str(ROOT / 'deploy/k8s/base')],
                         capture_output=True, text=True, check=True).stdout
    docs = [d for d in yaml.safe_load_all(out) if d]
    target_ports = {}
    for svc in (d for d in docs if d['kind'] == 'Service'):
        for port in svc['spec'].get('ports', []):
            target_ports.setdefault(svc['metadata']['name'], set()).add(
                port.get('targetPort', port['port']) if isinstance(port.get('targetPort', 0), int) else port['port'])
    workloads = {}
    for d in docs:
        if d['kind'] not in ('Deployment', 'StatefulSet', 'Job'):
            continue
        container = d['spec']['template']['spec']['containers'][0]
        name = d['metadata']['name']
        workloads[name] = {'image': container['image'],
                           'env': {e['name'] for e in container.get('env', [])},
                           'ports': target_ports.get(name, set())}
    return workloads


def main():
    compose, k8s = compose_services(), kubernetes_workloads()
    problems = []
    unused = set(ALLOWED)
    for name, c in compose.items():
        kname = RENAMED.get(name, name)
        k = k8s.pop(kname, None)
        if k is None:
            problems.append(f'{name}: in compose.yaml but not in deploy/k8s/base')
            continue
        if c['built'] and name in BUILT_VS_UPSTREAM:
            pass
        elif c['built']:
            if not re.fullmatch(rf'ghcr\.io/[^/]+/opendx-(dxos-)?{re.escape(name if name != "operator" else "dxos-controller")}:.+',
                                k['image']):
                problems.append(f'{name}: built from source in compose, but k8s runs {k["image"]}')
        elif c['image'] != k['image']:
            problems.append(f'{name}: image {c["image"]} in compose, {k["image"]} in k8s')
        missing = c['ports'] - k['ports']
        if missing:
            problems.append(f'{name}: container port {sorted(missing)} published in compose, no k8s Service port')
        if name in SKIP_ENV:
            continue
        for var in sorted(c['env'] ^ k['env']):
            side = 'compose' if var in c['env'] else 'k8s'
            if (name, var) in ALLOWED:
                unused.discard((name, var))
            else:
                problems.append(f'{name}: {var} only in {side}; add it to the other side or to ALLOWED with a reason')
    for kname in k8s:
        problems.append(f'{kname}: in deploy/k8s/base but not in compose.yaml')
    for entry in sorted(unused):
        problems.append(f'{entry[0]}: ALLOWED entry for {entry[1]} no longer needed; remove it')
    for p in problems:
        print('[parity] ' + p, file=sys.stderr)
    if not problems:
        print(f'[parity] compose.yaml and deploy/k8s/base agree on {len(compose)} services')
    return 1 if problems else 0


if __name__ == '__main__':
    sys.exit(main())
