# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors
"""Seed fresh disposable phase data through real authenticated APIs."""
import argparse
import datetime
import hashlib
import hmac
import json
import os
import subprocess
import sys
import time
import uuid
from pathlib import Path
from urllib.error import HTTPError
from urllib.request import Request, urlopen

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT))
from scripts.integration.environment import (
    compose,
    guard,
    private_config,
    run,
    verify_target,
)
from tests.integration.helpers import Client


class Refused(RuntimeError):
    pass


def seed_phase(phase, values, ready_apps, create):
    if values.get('TEST_DISPOSABLE_DATABASE') != 'yes':
        raise Refused('TEST_DISPOSABLE_DATABASE=yes is required')
    if phase not in ('manual', 'automatic', 'files'):
        raise Refused('unknown phase')
    if not {'procurement', 'process_fixture'}.issubset(set(ready_apps)):
        raise Refused('both apps require verified Ready snapshots before seeding')
    if create is None:
        raise Refused('live API creator is required')
    return create(phase)


def private_json(path, value):
    if path.is_symlink():
        raise Refused('private output cannot be a symlink')
    path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    with open(path, 'w', opener=lambda p, f: os.open(p, f, 0o600)) as stream:
        os.fchmod(stream.fileno(), 0o600)
        json.dump(value, stream, indent=2)


class LiveSeed:
    def __init__(self):
        guard(os.environ)
        private_config()
        verify_target(run)
        self.config = dict(line.split('=', 1) for line in (ROOT / '.env.integration').read_text().splitlines() if line and not line.startswith('#'))
        self.connections = json.loads((ROOT / 'build/integration/connections.json').read_text())
        self.fixture = json.loads((ROOT / 'tests/fixtures/actors.json').read_text())
        self.workspace = self.fixture['workspaces']['alpha']
        self.gateway = 'http://localhost:' + self.config.get('APISIX_GATEWAY_PORT', '19080')
        os.environ['TEST_PROCESS_URL'] = self.gateway
        os.environ['TEST_GRAPHQL_URL'] = self.gateway + '/v1/graphql'
        result = subprocess.run([sys.executable, 'scripts/integration/identity.py', 'tokens'], cwd=ROOT, capture_output=True, text=True, check=False)
        if result.returncode:
            raise Refused('fixture token renewal failed; credentials withheld')
        self.tokens = json.loads((ROOT / 'build/integration/tokens.json').read_text())

    def sql(self, query, role='assertion'):
        result = subprocess.run(['psql', self.connections[role], '-XAt', '-v', 'ON_ERROR_STOP=1'], input=query, capture_output=True, text=True, check=False)
        if result.returncode:
            raise Refused('seed database operation failed; credentials withheld')
        return result.stdout.strip()

    def ready_apps(self):
        # Every workspace must have both actual published packages with enabled dependencies.
        for workspace in self.fixture['workspaces'].values():
            apps = self.sql(f"SELECT app_id FROM core.app_registry a WHERE workspace_id='{workspace}' AND status='Ready' AND NOT EXISTS (SELECT 1 FROM unnest(a.dependent_capabilities) d(cap_id) LEFT JOIN core.capability_registry c USING(cap_id) WHERE c.enabled IS DISTINCT FROM true) ORDER BY app_id").splitlines()
            if not {'procurement', 'process_fixture'}.issubset(apps):
                raise Refused('publish both actual packages in both fixture workspaces before seeding')
        return apps

    def client(self, actor='requester', role='role_procurement'):
        a = self.fixture['actors'][actor]
        return Client(self.tokens[a['token_env']], role)

    def foundation(self):
        # Shared master data is provisioned by bootstrap, never by business callers.
        for name, workspace in self.fixture['workspaces'].items():
            self.sql(f"INSERT INTO core.workspaces(id,code,name) VALUES('{workspace}','integration-{name}','Integration {name}') ON CONFLICT(id) DO NOTHING", 'bootstrap')
        for name, actor in self.fixture['actors'].items():
            self.sql("INSERT INTO core.employees(id,workspace_id,full_name,email,department) VALUES('{}','{}','{}','{}@integration.invalid','Integration') ON CONFLICT(id) DO NOTHING".format(actor['employee_id'], actor['workspace_id'], name, name), 'bootstrap')

    def pause_effects(self):
        run(compose('stop', '--timeout', '30', 'kestra'))
        if run(compose('ps', '--status', 'running', '-q', 'kestra')).strip():
            raise Refused('manual effects are not paused')
        # Stopping the full standalone server stops its scheduler, workers and queued executions.

    def automatic_ready(self):
        from scripts.integration.workflows import verify_ready
        verify_ready(self)

    def provision_bucket(self):
        # Host-side access uses the verified project's private container address.
        cid = run(compose('ps', '-q', 'seaweedfs')).strip()
        if not cid:
            raise Refused('SeaweedFS is not running')
        container = json.loads(run(['docker', 'inspect', cid]))[0]
        address = next(iter(container['NetworkSettings']['Networks'].values()))['IPAddress']
        host = address + ':8333'
        bucket = self.config.get('S3_BUCKET', 'dxos-attachments')
        if not bucket or any(c not in 'abcdefghijklmnopqrstuvwxyz0123456789.-' for c in bucket):
            raise Refused('invalid disposable S3 bucket')
        path = '/' + bucket
        for method in ('HEAD', 'PUT'):
            stamp = datetime.datetime.now(datetime.UTC).strftime('%Y%m%dT%H%M%SZ')
            date = stamp[:8]
            digest = hashlib.sha256(b'').hexdigest()
            signed = 'host;x-amz-content-sha256;x-amz-date'
            headers = 'host:' + host + '\nx-amz-content-sha256:' + digest + '\nx-amz-date:' + stamp + '\n'
            canonical = method + '\n' + path + '\n\n' + headers + '\n' + signed + '\n' + digest
            region = self.config.get('S3_REGION', 'us-east-1')
            scope = date + '/' + region + '/s3/aws4_request'
            key = ('AWS4' + self.config['S3_SECRET_ACCESS_KEY']).encode()
            for piece in (date, region, 's3', 'aws4_request'):
                key = hmac.new(key, piece.encode(), hashlib.sha256).digest()
            signature = hmac.new(key, ('AWS4-HMAC-SHA256\n' + stamp + '\n' + scope + '\n' + hashlib.sha256(canonical.encode()).hexdigest()).encode(), hashlib.sha256).hexdigest()
            auth = 'AWS4-HMAC-SHA256 Credential=' + self.config['S3_ACCESS_KEY_ID'] + '/' + scope + ', SignedHeaders=' + signed + ', Signature=' + signature
            req = Request('http://' + host + path, data=b'', method=method, headers={'Authorization': auth, 'X-Amz-Date': stamp, 'X-Amz-Content-Sha256': digest})
            try:
                with urlopen(req, timeout=20) as response:
                    if response.status not in (200, 204):
                        raise Refused('private bucket provisioning rejected')
                    return
            except HTTPError as error:
                if method == 'HEAD' and error.code == 404:
                    continue
                raise Refused('private S3 bucket provisioning failed: HTTP ' + str(error.code)) from None

    def upload(self, app='procurement', actor='requester', clean=True):
        role = 'role_procurement' if app == 'procurement' else 'role_review'
        client = self.client(actor, role)
        boundary = 'dxos-' + uuid.uuid4().hex
        content = b'%PDF-1.4\n1 0 obj<</Type/Catalog>>endobj\n%%EOF\n'
        body = ('--' + boundary + '\r\nContent-Disposition: form-data; name="app_id"\r\n\r\n' + app + '\r\n--' + boundary + '\r\nContent-Disposition: form-data; name="file"; filename="integration-quote.pdf"\r\nContent-Type: application/pdf\r\n\r\n').encode() + content + ('\r\n--' + boundary + '--\r\n').encode()
        req = Request(self.gateway + '/api/cap/process/files/upload', body, headers={'Authorization': 'Bearer ' + client.token, 'X-Hasura-Role': role, 'Content-Type': 'multipart/form-data; boundary=' + boundary})
        try:
            with urlopen(req, timeout=40) as response:
                file = json.load(response)
        except OSError:
            raise Refused('authorized file upload failed; storage/scanner prerequisites unresolved') from None
        file_id = str(uuid.UUID(file['file_id']))
        if clean:
            deadline = time.monotonic() + 75
            while time.monotonic() < deadline:
                status = self.sql(f"SELECT scan_status FROM core.file_objects WHERE id='{file_id}'")
                if status == 'CLEAN':
                    return file_id
                if status == 'INFECTED':
                    raise Refused('clean seed content rejected by real scanner')
                time.sleep(1)
            raise Refused('file did not become CLEAN through real scanner within 75 seconds')
        return file_id

    def create(self, app='procurement', actor='requester', file_id=None):
        table, role = ('procurement_requests', 'role_procurement') if app == 'procurement' else ('review_requests', 'role_review')
        workspace = self.fixture['actors'][actor]['workspace_id']
        work = str(uuid.uuid4())
        self.sql(f"INSERT INTO core.work_items(id,workspace_id,title,priority) VALUES('{work}','{workspace}','Phase seed work item','MEDIUM')", 'bootstrap')
        values = {'work_item_id': work, 'vendor_name': 'Integration vendor', 'item_description': 'Phase seed request', 'quoted_amount': 42, 'quote_file_id': file_id}
        query = 'mutation($object:domain_' + table + '_insert_input!){insert_domain_' + table + '_one(object:$object){id status version}}'
        row = self.client(actor, role).graphql(query, {'object': values})['insert_domain_' + table + '_one']
        return {'entity_id': row['id'], 'expected_version': row['version'], 'status': row['status'], 'app_id': app}

    def submit(self, case, actor='requester'):
        role = 'role_procurement' if case['app_id'] == 'procurement' else 'role_review'
        status, body = self.client(actor, role).command(case['app_id'], 'submit', {k: case[k] for k in ('entity_id', 'expected_version')} | {'payload': {}}, str(uuid.uuid4()))
        if status != 200:
            raise Refused('authorized seed submit failed: HTTP ' + str(status))
        return {**case, 'expected_version': body['version'], 'status': body['status'], 'event_id': body.get('event_id')}

    def phase(self, phase):
        if phase == 'automatic':
            self.automatic_ready()
        else:
            self.pause_effects()
        self.foundation()
        self.provision_bucket()
        run(compose('start', 'clamav'))
        clean = self.upload()
        fixture_clean = self.upload('process_fixture')
        cases = {}
        cases['denied'] = self.create(file_id=clean)
        cases['retry'] = self.create(file_id=clean)
        self_file = self.upload(actor='selfapprover')
        cases['selfapprove'] = self.submit(self.create(actor='selfapprover', file_id=self_file), actor='selfapprover')
        submitted = self.submit(self.create(file_id=clean))
        cases['stale'] = {**submitted, 'actual_version': submitted['expected_version'], 'expected_version': submitted['expected_version'] - 1}
        # Keep a real unscanned file stable for negative attachment cases. The full
        # scanner daemon is stopped only after clean fixtures have been observed.
        if phase != 'automatic':
            run(compose('stop', '--timeout', '30', 'clamav'))
            if run(compose('ps', '--status', 'running', '-q', 'clamav')).strip():
                raise Refused('scanner could not be stopped for the quarantine fixture')
            quarantine = self.upload(clean=False)
            if self.sql(f"SELECT scan_status FROM core.file_objects WHERE id='{quarantine}'") != 'QUARANTINED':
                raise Refused('quarantine fixture was not observed QUARANTINED')
            cases['quarantined'] = self.create(file_id=quarantine)
        else:
            cases['quarantined'] = self.create()  # missing attachment must fail closed
            quarantine = None
        fixture_case = self.create('process_fixture', file_id=fixture_clean)
        comparison = self.create(file_id=clean)
        context = {'phase': phase, 'TEST_DISPOSABLE_DATABASE': 'yes', 'TEST_DATABASE_URL': self.connections['assertion'],
                   'TEST_PROCESS_URL': self.gateway, 'TEST_GRAPHQL_URL': self.gateway + '/v1/graphql',
                   'TEST_WORKSPACE_ID': self.workspace, 'TEST_ISOLATED_WORKLOAD': 'yes',
                   'TEST_EFFECT_DELIVERY_PAUSED': 'no' if phase == 'automatic' else 'yes',
                   'TEST_CASES_JSON': json.dumps(cases), 'TEST_INDEPENDENCE_JSON': json.dumps({'fixture_entity_id': fixture_case['entity_id'], 'procurement_entity_id': comparison['entity_id'], 'expected_version': fixture_case['expected_version']}),
                   'files': {'clean_procurement': clean, 'clean_process_fixture': fixture_clean, 'quarantined': quarantine},
                   'scanner_stopped': phase != 'automatic'}
        if phase == 'automatic':
            context['automatic'] = self.submit(cases['retry'])
        private_json(ROOT / 'build/integration' / (phase + '.json'), context)
        return {'phase': phase, 'result': 'seeded', 'context': 'build/integration/' + phase + '.json', 'scanner_stopped': context['scanner_stopped']}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--phase', required=True, choices=['manual', 'automatic', 'files'])
    args = parser.parse_args()
    try:
        live = LiveSeed()
        print(json.dumps(seed_phase(args.phase, os.environ, live.ready_apps(), live.phase)))
    except (Refused, RuntimeError) as exc:
        parser.exit(1, str(exc) + '\n')


if __name__ == '__main__':
    main()
