# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors
"""Actual Chromium HTTP smoke; only sanitized status/error data reaches stdout."""
import json
import subprocess
import tempfile
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

root=Path(__file__).resolve().parents[2]
config=dict(x.split('=',1) for x in (root/'.env.integration').read_text().splitlines() if x and not x.startswith('#'))
tokens=json.loads((root/'build/integration/tokens.json').read_text())
files=json.loads((root/'build/integration/files.json').read_text())
html='''<!doctype html><body>RUNNING<script>
(async()=>{const c=await (await fetch('/config')).json();const results=[];
for(const [name,url,body] of [
['gateway-business',c.gateway+'/v1/graphql',{query:'query { domain_procurement_requests(limit: 1) { id status version } }'}],
['gateway-registry',c.gateway+'/v1/graphql',{query:'query { core_app_registry { app_id version status manifest_spec } }'}],
['direct-business',c.hasura+'/v1/graphql',{query:'query { domain_procurement_requests(limit: 1) { id status version } }'}],
['direct-registry',c.hasura+'/v1/graphql',{query:'query { core_app_registry { app_id version status manifest_spec } }'}],
['gateway-process',c.gateway+'/api/cap/process/commands/procurement/submit',{entity_id:'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',expected_version:1,payload:{}}],
['direct-process',c.process+'/api/cap/process/commands/procurement/submit',{entity_id:'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',expected_version:1,payload:{}}]]){
try {const r=await fetch(url,{method:'POST',headers:{Authorization:'Bearer '+c.token,'X-Hasura-Role':'role_procurement','Content-Type':'application/json','Idempotency-Key':crypto.randomUUID()},body:JSON.stringify(body)});const b=await r.json();results.push({name,status:r.status,graphql_errors:(b.errors||[]).map(e=>({code:e.extensions?.code,message:e.message})),has_data:!!b.data});}catch(e){results.push({name,error:e.name});}}
try {const r=await fetch(c.gateway+'/api/cap/process/files/'+c.clean_file,{headers:{Authorization:'Bearer '+c.token,'X-Hasura-Role':'role_procurement'}});const bytes=await r.arrayBuffer();const digest=Array.from(new Uint8Array(await crypto.subtle.digest('SHA-256',bytes))).map(b=>b.toString(16).padStart(2,'0')).join('');results.push({name:'gateway-clean-download',status:r.status,bytes:bytes.byteLength,sha256:digest,matches_fixture:digest===c.expected_sha256});}catch(e){results.push({name:'gateway-clean-download',error:e.name});}
document.body.textContent=JSON.stringify(results);fetch('/result',{method:'POST',body:JSON.stringify(results)});
})();</script>'''
result=[];done=threading.Event()
class Handler(BaseHTTPRequestHandler):
 def log_message(self,*a):pass
 def do_GET(self):
  body=json.dumps({'gateway':'http://localhost:'+config.get('APISIX_GATEWAY_PORT','19080'),'hasura':'http://localhost:'+config.get('HASURA_PORT','18080'),'process':'http://localhost:'+config.get('CAP_PROCESS_PORT','18091'),'token':tokens['TEST_REQUESTER_TOKEN'],'clean_file':files['files']['clean_procurement'],'expected_sha256':__import__('hashlib').sha256(b'%PDF-1.4\n1 0 obj<</Type/Catalog>>endobj\n%%EOF\n').hexdigest()}).encode() if self.path=='/config' else html.encode()
  self.send_response(200);self.send_header('Content-Type','application/json' if self.path=='/config' else 'text/html');self.end_headers();self.wfile.write(body)
 def do_POST(self):
  result.extend(json.loads(self.rfile.read(int(self.headers['Content-Length']))));self.send_response(204);self.end_headers();done.set()
server=ThreadingHTTPServer(('127.0.0.1',15173),Handler);threading.Thread(target=server.serve_forever,daemon=True).start()
with tempfile.TemporaryDirectory(prefix='task8-browser-') as profile:
 p=subprocess.Popen(['/usr/bin/chromium','--headless','--no-sandbox','--disable-gpu','--no-first-run','--user-data-dir='+profile,'http://localhost:15173'],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
 try:
  if not done.wait(40):raise RuntimeError('browser smoke timed out')
  print(json.dumps({'origin':'http://localhost:15173','results':result}))
 finally:p.terminate();p.wait(timeout=10);server.shutdown()
