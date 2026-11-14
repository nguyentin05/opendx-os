#!/usr/bin/env python3
# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors
"""Run the real parser in a disposable network-disabled, resource-capped container."""
import hashlib
import http.client
import json
import os
from pathlib import Path
import socket
import subprocess
import tempfile
import time

ROOT = Path(__file__).resolve().parents[2]
TOKEN = 'disposable-parser-fixture-token-32-bytes'
class UnixHTTP(http.client.HTTPConnection):
    def __init__(self, path):
        super().__init__('localhost', timeout=125)
        self.path = path
    def connect(self):
        self.sock = socket.socket(socket.AF_UNIX)
        self.sock.settimeout(125)
        self.sock.connect(self.path)

def probe(sock, file, media, expected, limits=None, content=None):
    content = file.read_bytes() if content is None else content
    metadata = {'request_id': 'sandbox-fixture', 'media_type': media, 'sha256': hashlib.sha256(content).hexdigest(), 'recipe_id': 'standard-text-v1', 'ir_version': 1,
                'limits': {'max_source_bytes': 20 << 20, 'max_pages': 200, 'deadline': 120000000000, 'max_memory_bytes': 512 << 20, 'max_decoded_bytes': 200 << 20, 'max_image_pixels': 25000000, 'max_elements': 50000, 'max_chunks': 20000}}
    if limits:
        metadata['limits'].update(limits)
    boundary = 'dxosfixture'
    body = ('--'+boundary+'\r\nContent-Disposition: form-data; name="metadata"\r\n\r\n'+json.dumps(metadata)+'\r\n--'+boundary+'\r\nContent-Disposition: form-data; name="source"; filename="source"\r\n\r\n').encode()+content+('\r\n--'+boundary+'--\r\n').encode()
    conn = UnixHTTP(sock)
    conn.request('POST', '/internal/v1/parse', body, {'Authorization': 'Bearer '+TOKEN, 'Content-Type': 'multipart/form-data; boundary='+boundary})
    response = conn.getresponse(); result = json.loads(response.read()); conn.close()
    assert response.status == expected, (file.name, response.status, result.get('error'))
    if expected == 200 and media == 'application/pdf':
        assert any(e['kind']=='table' and e['page_no']==4 and 'Director' in e['text'] and '10,000,000' in e['text'] for e in result['elements'])
        print('PASS: sandbox PDF table/page provenance, parser version '+result['parser']['version'])
    elif expected == 200:
        print('PASS: sandbox Markdown structure')
    else:
        assert result['error'] in ('OCR_UNAVAILABLE','PARSER_LIMIT','INVALID_DOCIR','PARSER_REJECTED')
        print('PASS: sandbox explicit '+result['error'])

if __name__ == '__main__':
    with tempfile.TemporaryDirectory(prefix='dxos-parser-', dir=ROOT/'.scratch') as tmp:
        name = 'dxos-parser-qualification'
        subprocess.run(['docker','run','--rm','-d','--name',name,'--network','none','--memory','512m','--cpus','1','--pids-limit','64','--read-only','--tmpfs','/tmp:rw,noexec,nosuid,size=220m','--user',str(os.getuid())+':'+str(os.getgid()),'-v',tmp+':/run/parser','-e','PARSER_SOCKET=/run/parser/parser.sock','-e','PARSER_SERVICE_TOKEN='+TOKEN,'dxos-rag-parser:test'], check=True, stdout=subprocess.DEVNULL)
        try:
            for _ in range(100):
                if Path(tmp,'parser.sock').exists(): break
                time.sleep(.1)
            config = json.loads(subprocess.check_output(['docker','inspect',name]))[0]['HostConfig']
            assert config['NetworkMode']=='none' and config['Memory']==512<<20 and config['ReadonlyRootfs'] and config['PidsLimit']==64
            data = ROOT/'capabilities/internal/knowledge/docir/testdata'
            for file,media,status in [('policy.md','text/markdown',200),('table.pdf','application/pdf',200),('scan.pdf','application/pdf',422),('too-many-pages.pdf','application/pdf',422)]:
                probe(tmp+'/parser.sock',data/file,media,status)
            probe(tmp+'/parser.sock',data/'policy.md','text/markdown',422,{'deadline': 1})
            probe(tmp+'/parser.sock',data/'policy.md','text/markdown',422,{'max_decoded_bytes': 128})
            probe(tmp+'/parser.sock',data/'policy.md','text/markdown',422,{'max_source_bytes': 1024},b'x'*1025)
            probe(tmp+'/parser.sock',data/'policy.md','text/markdown',422,content=b'x'*((20<<20)+1))
        finally:
            subprocess.run(['docker','stop',name], check=True, stdout=subprocess.DEVNULL)
