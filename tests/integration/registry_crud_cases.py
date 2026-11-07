# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors
"""Actual package publication and authenticated Hasura CRUD; no fixture substitution."""
import json
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import threading
from urllib.request import Request, urlopen
from urllib.error import HTTPError
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest
import uuid
from tests.integration import identity_cases

ROOT = Path(__file__).resolve().parents[2]

class RegistryCrudCases(unittest.TestCase):
    http = staticmethod(identity_cases.IdentityCases.http)

    @classmethod
    def setUpClass(cls):
        cls.config = dict(line.split('=',1) for line in (ROOT/'.env.integration').read_text().splitlines() if line and not line.startswith('#'))
        cls.connections = json.loads((ROOT/'build/integration/connections.json').read_text())
        cls.actors = json.loads((ROOT/'tests/fixtures/actors.json').read_text())
        cls.gateway = 'http://localhost:'+cls.config.get('APISIX_GATEWAY_PORT','19080')
        cls.env = {**os.environ,'DATABASE_URL':cls.connections['bootstrap'], 'HASURA_URL':'http://localhost:'+cls.config.get('HASURA_PORT','18080'),'HASURA_ADMIN_SECRET':cls.config['HASURA_ADMIN_SECRET']}
        subprocess.run(['python3','scripts/integration/identity.py','tokens'],cwd=ROOT,check=True,capture_output=True)
        cls.tokens = json.loads((ROOT/'build/integration/tokens.json').read_text())
        cls.operator(['validate','../apps/procurement/manifest.yaml'])
        cls.operator(['validate','../apps/process-fixture/manifest.yaml'])
        for name,ws in cls.actors['workspaces'].items():
            cls.sql("INSERT INTO core.workspaces(id,code,name) VALUES('%s','integration-%s','Integration %s') ON CONFLICT(id) DO NOTHING" % (ws,name,name),'bootstrap')
        for name,a in cls.actors['actors'].items():
            cls.sql("INSERT INTO core.employees(id,workspace_id,full_name,email,department) VALUES('%s','%s','%s','%s@integration.invalid','Integration') ON CONFLICT(id) DO NOTHING" % (a['employee_id'],a['workspace_id'],name,name),'bootstrap')
        for ws in cls.actors['workspaces'].values():
            for package in ('procurement','process-fixture'):
                cls.operator(['apply','-workspace',ws,'../apps/'+package+'/manifest.yaml'])

    @classmethod
    def operator(cls,args,success=True,env=None):
        result = subprocess.run(['go','run','./cmd/dxos-operator',*args],cwd=ROOT/'operator',env=env or cls.env,capture_output=True,text=True)
        if success and result.returncode:
            raise AssertionError('Operator failed: '+result.stderr)
        if not success and result.returncode == 0:
            raise AssertionError('Expected failed deployment')
        return result

    @classmethod
    def sql(cls,sql,role='assertion'):
        result = subprocess.run(['psql',cls.connections[role],'-XAt','-v','ON_ERROR_STOP=1'],input=sql,capture_output=True,text=True)
        if result.returncode:
            raise AssertionError('Database check failed: '+result.stderr)
        return result.stdout.strip()

    def gql(self,query,variables=None,actor='requester',role='role_procurement'):
        a = self.actors['actors'][actor]
        return self.http(self.gateway+'/v1/graphql',{'query':query,'variables':variables or {}},{'Authorization':'Bearer '+self.tokens[a['token_env']],'X-Hasura-Role':role})[1]

    def work_item(self,workspace=None):
        workspace = workspace or self.actors['workspaces']['alpha']
        work = str(uuid.uuid4())
        self.sql("INSERT INTO core.work_items(id,workspace_id,title,priority) VALUES('%s','%s','CRUD fixture','MEDIUM')" % (work,workspace),'bootstrap')
        return work

    def create(self,table='procurement_requests',role='role_procurement',actor='requester',**extra):
        values = {'work_item_id':self.work_item(self.actors['actors'][actor]['workspace_id']),'vendor_name':'Integration supplier','item_description':'Integration item','quoted_amount':42,**extra}
        return self.gql('mutation($object:domain_'+table+'_insert_input!){insert_domain_'+table+'_one(object:$object){id workspace_id requester_id status version}}',{'object':values},actor,role)

    def test_packages_publish_backend_readable_snapshots(self):
        for ws in self.actors['workspaces'].values():
            self.assertEqual(self.sql("SELECT count(*) FROM core.app_registry WHERE workspace_id='%s' AND status='Ready'" % ws),'2')
        # A real backend command resolves and strictly decodes the actual snapshot before entity lookup.
        for app,role in [('procurement','role_procurement'),('process_fixture','role_review')]:
            status,body = self.http(self.gateway+'/api/cap/process/commands/'+app+'/submit',{'entity_id':str(uuid.uuid4()),'expected_version':1,'payload':{}},{'Authorization':'Bearer '+self.tokens['TEST_REQUESTER_TOKEN'],'X-Hasura-Role':role,'Idempotency-Key':str(uuid.uuid4())})
            self.assertEqual(status,404,body)
        try:
            self.sql("UPDATE core.capability_registry SET enabled=false WHERE cap_id='cap-process'",'bootstrap')
            status,body = self.http(self.gateway+'/api/cap/process/commands/procurement/submit',{'entity_id':str(uuid.uuid4()),'expected_version':1,'payload':{}},{'Authorization':'Bearer '+self.tokens['TEST_REQUESTER_TOKEN'],'X-Hasura-Role':'role_procurement','Idempotency-Key':str(uuid.uuid4())})
            self.assertEqual(status,503,body)
        finally:
            self.sql("UPDATE core.capability_registry SET enabled=true WHERE cap_id='cap-process'",'bootstrap')


    def test_second_app_preserves_first_permissions(self):
        for table,role in [('procurement_requests','role_procurement'),('review_requests','role_review')]:
            body = self.create(table,role)
            self.assertNotIn('errors',body,body)
        with tempfile.TemporaryDirectory() as stage:
            for app in ('procurement','process-fixture'):
                shutil.copytree(ROOT/'apps'/app,Path(stage)/app)
            self.operator(['reconcile','-workspace',self.actors['workspaces']['alpha'],stage])
        for table,role in [('procurement_requests','role_procurement'),('review_requests','role_review')]:
            for actor in ('requester','beta_requester'):
                body=self.create(table,role,actor=actor)
                self.assertNotIn('errors',body,body)

    def test_failed_update_preserves_active_snapshot(self):
        before = self.sql("SELECT manifest_spec::text FROM core.app_registry WHERE app_id='procurement' ORDER BY workspace_id")
        upstream = self.env['HASURA_URL']
        calls = []
        class FaultProxy(BaseHTTPRequestHandler):
            def log_message(self,*args):
                pass
            def do_POST(self):
                payload = self.rfile.read(int(self.headers['Content-Length']))
                operation = json.loads(payload)['type']
                calls.append(operation)
                if operation == 'pg_create_insert_permission':
                    self.send_response(503); self.end_headers(); self.wfile.write(b'{"error":"injected dependency failure"}'); return
                request = Request(upstream+self.path,payload,headers={'Content-Type':'application/json','X-Hasura-Admin-Secret':self.headers['X-Hasura-Admin-Secret']})
                try:
                    response=urlopen(request,timeout=15)
                except HTTPError as error:
                    response=error
                with response:
                    body=response.read(); self.send_response(response.status); self.end_headers(); self.wfile.write(body)
        server=ThreadingHTTPServer(('127.0.0.1',0),FaultProxy)
        thread=threading.Thread(target=server.serve_forever,daemon=True); thread.start()
        try:
            env={**self.env,'HASURA_URL':'http://127.0.0.1:'+str(server.server_port)}
            self.operator(['apply','../apps/procurement/manifest.yaml'],success=False,env=env)
        finally:
            server.shutdown(); server.server_close(); thread.join()
        self.assertIn('pg_drop_insert_permission',calls)
        self.assertIn('replace_metadata',calls)
        self.assertEqual(self.sql("SELECT status FROM core.app_deployments WHERE workspace_id='%s' AND app_id='procurement' ORDER BY started_at DESC LIMIT 1" % self.actors['workspaces']['alpha']),'Failed')
        self.assertEqual(self.sql("SELECT manifest_spec::text FROM core.app_registry WHERE app_id='procurement' ORDER BY workspace_id"),before)
        self.assertNotIn('errors',self.create())

    def test_crud_presets_and_protected_fields(self):
        body = self.create()
        self.assertNotIn('errors',body,body)
        row = body['data']['insert_domain_procurement_requests_one']
        self.assertEqual((row['status'],row['version']),('DRAFT',1))
        self.assertEqual(row['requester_id'],self.actors['actors']['requester']['user_id'])
        self.assertEqual(row['workspace_id'],self.actors['workspaces']['alpha'])
        for field,value in [('status','APPROVED'),('version',99),('requester_id',self.actors['actors']['manager']['user_id']),('workspace_id',self.actors['workspaces']['beta'])]:
            self.assertIn('errors',self.create(**{field:value}),field)
        q='mutation($id:uuid!){update_domain_procurement_requests_by_pk(pk_columns:{id:$id},_set:{vendor_name:"Edited"}){version}}'
        self.assertEqual(self.gql(q,{'id':row['id']})['data']['update_domain_procurement_requests_by_pk']['version'],2)
        for mutation in ['mutation { insert_domain_procurement_requests_one(object:{}, on_conflict:{constraint:procurement_requests_pkey,update_columns:[status]}){id}}','mutation { delete_domain_procurement_requests(where:{}){affected_rows}}','mutation { update_domain_procurement_requests(where:{},_set:{status:"APPROVED"}){affected_rows}}']:
            self.assertIn('errors',self.gql(mutation))

    def test_workspace_relationship_and_fk_isolation(self):
        self.assertIn('errors',self.create(work_item_id=self.work_item(self.actors['workspaces']['beta'])))
        # Nested master-data inserts are absent from the role's schema.
        self.assertIn('errors',self.create(requester={'data':{'full_name':'Forged','email':'forged@example.invalid','department':'Ops'}}))
        for actor in ('requester','beta_requester'):
            body=self.gql('query {domain_procurement_requests{workspace_id requester {workspace_id}}}',actor=actor)
            self.assertNotIn('errors',body,body)
            self.assertTrue(all(r['workspace_id']==self.actors['actors'][actor]['workspace_id'] and r['requester']['workspace_id']==r['workspace_id'] for r in body['data']['domain_procurement_requests']))
        body=self.gql('query {core_employees{id workspace_id}}')
        self.assertNotIn('errors',body,body)
        self.assertTrue(all(r['workspace_id']==self.actors['workspaces']['alpha'] for r in body['data']['core_employees']))
