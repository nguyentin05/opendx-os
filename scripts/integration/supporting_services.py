# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors
"""Run probes or real file adapters against the guarded disposable stack."""
import argparse
import json
import os
import subprocess

from scripts.integration.environment import compose, guard, private_config, ROOT, run, verify_target


def service_address(service, port):
    container = json.loads(run(['docker', 'inspect', run(compose('ps', '-q', service)).strip()]))[0]
    return next(iter(container['NetworkSettings']['Networks'].values()))['IPAddress'] + ':' + str(port)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=['probe', 'file-adapters'])
    args = parser.parse_args()
    guard(os.environ)
    private_config()
    verify_target(run)
    config = dict(line.split('=', 1) for line in (ROOT / '.env.integration').read_text().splitlines() if line and not line.startswith('#'))
    environment = {**os.environ, **{key: value for key, value in config.items() if key.startswith('S3_')},
                   'S3_ENDPOINT': 'http://' + service_address('seaweedfs', 8333),
                   'S3_BUCKET': config.get('S3_BUCKET', 'dxos-attachments'),
                   'S3_REGION': config.get('S3_REGION', 'us-east-1'),
                   'CLAMAV_ADDRESS': service_address('clamav', 3310),
                   'KESTRA_URL': 'http://localhost:' + config.get('KESTRA_PORT', '18082') + '/api/v1/flows/search'}
    if args.action == 'probe':
        command, cwd = ['sh', 'scripts/test-supporting-services.sh'], ROOT
    else:
        command, cwd = ['go', 'test', '-tags=serviceintegration', './internal/files', '-run', 'TestRealSeaweedFSClamAV', '-count=1', '-v'], ROOT / 'capabilities'
    raise SystemExit(subprocess.run(command, cwd=cwd, env=environment).returncode)


if __name__ == '__main__':
    main()
