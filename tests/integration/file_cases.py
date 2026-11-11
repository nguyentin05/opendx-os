# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors
"""Real gateway, PostgreSQL, SeaweedFS and ClamAV lifecycle/recovery checks."""
import datetime
import hashlib
import ipaddress
import itertools
import json
import socket
import subprocess
import time
import unittest
import uuid
from pathlib import Path
from urllib.error import HTTPError
from urllib.request import Request, urlopen

from scripts.integration.environment import compose, run
from scripts.integration.seed import LiveSeed, private_json

ROOT = Path(__file__).resolve().parents[2]
PDF = b'%PDF-1.4\n1 0 obj<</Type/Catalog>>endobj\n%%EOF\n'
EICAR = b'X5O!P%@AP[4' + b'\\PZX54(P^)7CC)7}$EICAR-STANDARD-ANTIVIRUS-TEST-FILE!$H+H*'
# A PDF stream exercises the MIME allowlist and the actual PDF scanner.
INFECTED_PDF = b'%PDF-1.4\n1 0 obj\n<< /Length ' + str(len(EICAR)).encode() + b' >>\nstream\n' + EICAR + b'\nendstream\nendobj\n%%EOF\n'


def restore_services(*actions):
    """Attempt each restoration even when an earlier service cannot restart."""
    failures = []
    for action in actions:
        try:
            action()
        except Exception as error:  # noqa: BLE001 - finish every restoration before reporting failures
            failures.append(type(error).__name__)
    if failures:
        raise RuntimeError('service restoration failed: ' + ', '.join(failures))


class FileCases(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.live = LiveSeed()
        cls.context = json.loads((ROOT / 'build/integration/files.json').read_text())
        if cls.context['phase'] != 'files' or cls.context['TEST_EFFECT_DELIVERY_PAUSED'] != 'yes':
            raise RuntimeError('consume a fresh files phase with actual Kestra stopped')
        if run(compose('ps', '--status', 'running', '-q', 'kestra')).strip():
            raise RuntimeError('Kestra must be stopped for file/submit assertions')
        cls.evidence = []

    @classmethod
    def record(cls, action, state=None):
        cls.evidence.append({'time': datetime.datetime.now(datetime.UTC).isoformat(), 'action': action, 'state': state})
        private_json(ROOT / 'build/integration/file-recovery.json', cls.evidence)

    def wait(self, predicate, seconds=100):
        deadline = time.monotonic() + seconds
        while time.monotonic() < deadline:
            try:
                value = predicate()
            except OSError:
                value = None  # transient transport loss is expected during the real fault
            if value:
                return value
            time.sleep(.5)
        self.fail(f'bounded live file deadline exceeded ({seconds}s)')

    def address(self, service, port):
        container = json.loads(run(['docker', 'inspect', run(compose('ps', '-q', service)).strip()]))[0]
        return next(iter(container['NetworkSettings']['Networks'].values()))['IPAddress'], port

    def scanner_ready(self):
        try:
            with socket.create_connection(self.address('clamav', 3310), timeout=1) as s:
                s.settimeout(1); s.sendall(b'zPING\0')
                return s.recv(16) == b'PONG\0'
        except OSError:
            return False

    def start_scanner(self):
        run(compose('start', 'clamav'))
        self.wait(self.scanner_ready, 120)

    def row(self, file):
        return json.loads(self.live.sql(f"SELECT to_jsonb(f) FROM core.file_objects f WHERE id='{uuid.UUID(file)!s}'"))

    def wait_status(self, file, status):
        return self.wait(lambda: (r if (r := self.row(file))['scan_status'] == status else None))

    def upload(self, content=PDF, app='procurement', actor='requester'):
        role = 'role_procurement' if app == 'procurement' else 'role_review'
        client = self.live.client(actor, role)
        boundary = 'task7-' + uuid.uuid4().hex
        body = ('--' + boundary + '\r\nContent-Disposition: form-data; name="app_id"\r\n\r\n' + app + '\r\n--' + boundary + '\r\nContent-Disposition: form-data; name="file"; filename="quote.pdf"\r\nContent-Type: application/pdf\r\n\r\n').encode() + content + ('\r\n--' + boundary + '--\r\n').encode()
        req = Request(self.live.gateway + '/api/cap/process/files/upload', body, headers={'Authorization': 'Bearer ' + client.token, 'X-Hasura-Role': role, 'Content-Type': 'multipart/form-data; boundary=' + boundary})
        with urlopen(req, timeout=40) as response:
            result = json.load(response)
            self.assertEqual(result['scan_status'], 'QUARANTINED')
            return str(uuid.UUID(result['file_id']))

    def download(self, file, actor='requester', role='role_procurement', method='GET', body=None, direct=False):
        client = self.live.client(actor, role)
        base = 'http://' + self.address('cap-process', 8081)[0] + ':8081' if direct else self.live.gateway
        req = Request(base + '/api/cap/process/files/' + file, body, method=method, headers={'Authorization': 'Bearer ' + client.token, 'X-Hasura-Role': role, 'Content-Type': 'application/json'})
        try:
            response = urlopen(req, timeout=3)
        except HTTPError as e:
            response = e
        with response:
            return response.status, response.read()

    def assert_blocked_submit(self, file):
        case = self.live.create(file_id=file)
        entity = case['entity_id']
        snapshot = self.live.sql(f"SELECT to_jsonb(d) FROM domain.procurement_requests d WHERE id='{entity}'")
        count_sql = f"SELECT json_build_array((SELECT count(*) FROM core.audit_logs WHERE entity_id='{entity}'),(SELECT count(*) FROM core.outbox_events WHERE entity_id='{entity}'),(SELECT count(*) FROM core.command_receipts WHERE entity_id='{entity}'))"
        before = self.live.sql(count_sql)
        status, _ = self.live.client().command('procurement', 'submit', {'entity_id': entity, 'expected_version': case['expected_version'], 'payload': {}}, str(uuid.uuid4()))
        self.assertEqual(status, 422)
        self.assertEqual(self.live.sql(count_sql), before)
        self.assertEqual(self.live.sql(f"SELECT to_jsonb(d) FROM domain.procurement_requests d WHERE id='{entity}'"), snapshot)

    def test_quarantine_blocks_download_and_submit(self):
        try:
            run(compose('stop', '--timeout', '5', 'clamav'))
            file = self.upload()
            initial = self.row(file)
            self.wait(lambda: self.row(file)['scan_attempt_count'] > initial['scan_attempt_count'], 30)
            self.assertEqual(self.row(file)['scan_status'], 'QUARANTINED')
            self.assertIsNone(self.row(file)['scanned_at'])
            self.assertEqual(self.download(file)[0], 422)
            self.assert_blocked_submit(file)
            self.record('real scanner unavailable: persisted retry remains quarantined and download/submit blocked', self.row(file))
        finally:
            self.start_scanner()

    def test_missing_attachment_blocks_submit(self):
        self.assert_blocked_submit(None)

    def test_clean_download_matches_original_bytes(self):
        self.start_scanner()
        file = self.upload()
        row = self.wait_status(file, 'CLEAN')
        code, content = self.download(file)
        self.assertEqual(code, 200)
        self.assertEqual(content, PDF)
        self.assertEqual(row['sha256_hash'], hashlib.sha256(PDF).hexdigest())
        self.assertEqual(row['size_bytes'], len(PDF))

    def test_infected_never_accessible(self):
        self.start_scanner()
        file = self.upload(INFECTED_PDF)
        row = self.wait_status(file, 'INFECTED')
        self.assertEqual(row['sha256_hash'], hashlib.sha256(INFECTED_PDF).hexdigest())
        self.assertEqual(self.download(file)[0], 422)
        self.assert_blocked_submit(file)
        self.record('real ClamAV detects harmless EICAR PDF; download/submit blocked', row)

    def test_cross_app_workspace_file_denied(self):
        self.start_scanner()
        file = self.upload()
        self.wait_status(file, 'CLEAN')
        self.assertEqual(self.download(file, actor='beta_requester')[0], 403)
        self.assertEqual(self.download(file, role='role_review')[0], 403)
        case = self.live.create('process_fixture', file_id=file)
        status, _ = self.live.client(role='role_review').command('process_fixture', 'submit', {'entity_id': case['entity_id'], 'expected_version': case['expected_version'], 'payload': {}}, str(uuid.uuid4()))
        self.assertEqual(status, 404)
        # Foreign-workspace FK cannot be attached through authenticated Hasura.
        from tests.integration.helpers import GraphQLError
        with self.assertRaises(GraphQLError):
            self.live.create(actor='beta_requester', file_id=file)

    def test_ordinary_caller_cannot_mark_clean(self):
        try:
            run(compose('stop', '--timeout', '5', 'clamav'))
            file = self.upload()
            for role in ('caller', 'hasura'):
                result = subprocess.run(['psql', self.live.connections[role], '-XAt', '-v', 'ON_ERROR_STOP=1', '-c', f"UPDATE core.file_objects SET scan_status='CLEAN' WHERE id='{file}'"], capture_output=True, text=True, check=False)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn('permission denied', result.stderr)
            status, body = self.live.client().post(self.live.gateway + '/v1/graphql', {'query': 'mutation{update_core_file_objects(where:{id:{_eq:"' + file + '"}},_set:{scan_status:"CLEAN"}){affected_rows}}'})
            self.assertEqual(status, 200)
            self.assertTrue(body.get('errors'))
            self.assertEqual(self.row(file)['scan_status'], 'QUARANTINED')
            self.assertEqual(self.download(file)[0], 422)
        finally:
            self.start_scanner()

    def test_replacement_requires_new_identity(self):
        self.start_scanner()
        original = self.upload()
        original_row = self.wait_status(original, 'CLEAN')
        # Trusted runtime can update scan columns, but cannot rewrite immutable identity.
        for field, value in [('sha256_hash', 'bad'), ('object_key', 'changed'), ('app_id', 'process_fixture'), ('file_name', 'changed.pdf')]:
            result = subprocess.run(['psql', self.live.connections['process'], '-XAt', '-v', 'ON_ERROR_STOP=1', '-c', f"UPDATE core.file_objects SET {field}='{value}' WHERE id='{original}'"], capture_output=True, text=True, check=False)
            self.assertNotEqual(result.returncode, 0)
            self.assertIn('permission denied', result.stderr)
        for method in ('PUT', 'PATCH'):
            self.assertIn(self.download(original, method=method, body=b'{"scan_status":"CLEAN"}')[0], (404, 405))
        try:
            run(compose('stop', '--timeout', '5', 'clamav'))
            replacement = self.upload(PDF + b'new quote\n')
            self.assertNotEqual(replacement, original)
            self.assertNotEqual(self.row(replacement)['object_key'], original_row['object_key'])
            self.assertEqual(self.row(replacement)['scan_status'], 'QUARANTINED')
            self.assertEqual(self.download(replacement)[0], 422)
            self.assertEqual(self.row(original), original_row)
            self.assertEqual(self.download(original), (200, PDF))
        finally:
            self.start_scanner()
        self.wait_status(replacement, 'CLEAN')
        self.assertEqual(self.download(replacement), (200, PDF + b'new quote\n'))

    def test_pending_scan_recovers_after_service_restart(self):
        self.start_scanner()
        clean = self.upload()
        clean_row = self.wait_status(clean, 'CLEAN')
        infected = self.upload(INFECTED_PDF)
        self.wait_status(infected, 'INFECTED')
        try:
            run(compose('stop', '--timeout', '5', 'clamav'))
            self.record('stopped real ClamAV', {'process_address': self.address('cap-process', 8081)[0]})
            pending = self.upload(PDF + b'persist through restart\n')
            initial = self.row(pending)
            failed = self.wait(lambda: (r if (r := self.row(pending))['scan_attempt_count'] > initial['scan_attempt_count'] else None), 40)
            self.assertEqual(failed['scan_status'], 'QUARANTINED')
            self.record('scanner connection error persists unclean retry', failed)
            run(compose('stop', '--timeout', '5', 'seaweedfs'))
            self.record('stopped real SeaweedFS; volumes retained')
            self.assertEqual(self.download(clean)[0], 503)
            self.start_scanner()
            attempt = self.row(pending)['scan_attempt_count']
            retried = self.wait(lambda: (r if (r := self.row(pending))['scan_attempt_count'] > attempt else None), 60)
            self.assertEqual(retried['scan_status'], 'QUARANTINED')
            self.assertIsNone(retried['scanned_at'])
            self.assertEqual(self.download(pending)[0], 422)
            self.record('real storage outage prevents CLEAN despite ready scanner', retried)
            run(compose('restart', '--no-deps', '--timeout', '5', 'cap-process'))
            self.record('restarted real cap-process scanner worker; persisted rows retained', {'address': self.address('cap-process', 8081)[0]})
            run(compose('start', 'seaweedfs'))
            self.record('started real SeaweedFS using retained volume')
            final = self.wait_status(pending, 'CLEAN')
            immutable = lambda row: {k: v for k, v in row.items() if k not in ('scan_status', 'scanned_at', 'scan_attempt_count', 'scan_next_attempt_at')}
            self.assertEqual(immutable(final), immutable(initial))
            self.assertEqual(self.download(pending, direct=True), (200, PDF + b'persist through restart\n'))
            self.assertEqual(self.download(pending), (200, PDF + b'persist through restart\n'))
            self.assertEqual(self.row(clean), clean_row)
            self.assertEqual(self.download(clean), (200, PDF))
            self.assertEqual(self.row(infected)['scan_status'], 'INFECTED')
            self.assertEqual(self.download(infected)[0], 422)
            self.record('pending CLEAN with original identity/hash/bytes; prior clean and infected rows survive', final)
        finally:
            restore_services(
                lambda: run(compose('start', 'seaweedfs')),
                lambda: run(['docker', 'start', run(compose('ps', '-a', '-q', 'cap-process')).strip()]),
                self.start_scanner)

    def test_gateway_recovers_after_process_address_change(self):
        # Natural restart need not allocate a different address. This separate
        # bounded network fault proves DNS recovery without restarting APISIX.
        self.start_scanner()
        file = self.upload()
        original_row = self.wait_status(file, 'CLEAN')
        cid = run(compose('ps', '-q', 'cap-process')).strip()
        container = json.loads(run(['docker', 'inspect', cid]))[0]
        self.assertEqual(container['Config']['Labels']['com.docker.compose.project'], 'opendx-integration')
        self.assertEqual(container['Config']['Labels']['com.docker.compose.service'], 'cap-process')
        self.assertEqual(len(container['NetworkSettings']['Networks']), 1)
        name, endpoint = next(iter(container['NetworkSettings']['Networks'].items()))
        network = json.loads(run(['docker', 'network', 'inspect', name]))[0]
        self.assertEqual(network['Labels']['com.docker.compose.project'], 'opendx-integration')
        self.assertEqual(network['Labels']['com.docker.compose.network'], 'default')
        self.assertIn(cid, network['Containers'])
        original_ip, aliases = endpoint['IPAddress'], endpoint['Aliases'] or []
        ipv4 = [c for c in network['IPAM']['Config'] if ipaddress.ip_network(c['Subnet']).version == 4]
        self.assertEqual(len(ipv4), 1)
        subnet = ipaddress.ip_network(ipv4[0]['Subnet'])
        used = {v['IPv4Address'].split('/')[0] for v in network['Containers'].values()}
        used.update(c.get('Gateway', '') for c in network['IPAM']['Config'])
        for config in network['IPAM']['Config']:
            used.update(config.get('AuxiliaryAddresses', {}).values())
        candidate = next(str(ip) for ip in itertools.islice(subnet.hosts(), 100, 600) if str(ip) not in used)
        def connect(address):
            command = ['docker', 'network', 'connect', '--ip', address]
            for alias in aliases:
                command += ['--alias', alias]
            run(command + [name, cid])
        gateway_id = run(compose('ps', '-q', 'apisix')).strip()
        gateway_started = json.loads(run(['docker', 'inspect', gateway_id]))[0]['State']['StartedAt']
        self.assertEqual(self.download(file), (200, PDF))  # populate original DNS cache
        self.record('DNS fault baseline: original process endpoint', {'address': original_ip})
        try:
            run(['docker', 'network', 'disconnect', name, cid])
            connect(candidate)
            self.record('controlled process endpoint reassignment; gateway not restarted', {'address': candidate})
            actual = json.loads(run(['docker', 'inspect', cid]))[0]['NetworkSettings']['Networks'][name]
            self.assertEqual(actual['IPAddress'], candidate)
            self.assertNotEqual(candidate, original_ip)
            self.wait(lambda: self.download(file, direct=True) == (200, PDF), 15)
            self.wait(lambda: self.download(file) == (200, PDF), 25)
            self.assertEqual(self.row(file), original_row)
            self.assertEqual(json.loads(run(['docker', 'inspect', gateway_id]))[0]['State']['StartedAt'], gateway_started)
            self.record('direct and gateway original bytes recovered at changed IP without gateway restart')
        finally:
            current = json.loads(run(['docker', 'inspect', cid]))[0]['NetworkSettings']['Networks']
            if name in current:
                run(['docker', 'network', 'disconnect', name, cid])
            connect(original_ip)
            restored = json.loads(run(['docker', 'inspect', cid]))[0]['NetworkSettings']['Networks'][name]
            self.assertEqual(restored['IPAddress'], original_ip)
            self.assertEqual(sorted(restored['Aliases'] or []), sorted(aliases))
            self.wait(lambda: self.download(file, direct=True) == (200, PDF), 15)
            self.wait(lambda: self.download(file) == (200, PDF), 25)
            self.record('restored exact original process address/aliases and verified direct/gateway bytes', {'address': original_ip})
