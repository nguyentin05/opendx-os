# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors
"""Live identity checks. Missing infrastructure is an error, never a skip."""
import base64
import json
from pathlib import Path
import subprocess
import unittest
import urllib.request

ROOT = Path(__file__).resolve().parents[2]

class IdentityCases(unittest.TestCase):
    def test_signed_actor_claims_match_fixture(self):
        fixture = json.loads((ROOT / 'tests/fixtures/actors.json').read_text())
        tokens = json.loads((ROOT / 'build/integration/tokens.json').read_text())
        for actor in fixture['actors'].values():
            token = tokens[actor['token_env']]
            claims = json.loads(base64.urlsafe_b64decode(token.split('.')[1] + '=='))
            req = urllib.request.Request(self.keycloak+'/realms/dxos/protocol/openid-connect/userinfo', headers={'Authorization': 'Bearer ' + token})
            with urllib.request.urlopen(req, timeout=10) as response:
                verified = json.load(response)
            self.assertEqual(verified['sub'], actor['sub'])
            self.assertEqual(claims['sub'], actor['sub'])
            signed = claims['https://hasura.io/jwt/claims']
            self.assertEqual(signed['x-hasura-user-id'], actor['user_id'])
            self.assertEqual(signed['x-hasura-workspace-id'], actor['workspace_id'])
            self.assertEqual(set(signed['x-hasura-allowed-roles']), set(actor['roles']))

    def test_runtime_role_flags(self):
        from scripts.integration.environment import compose
        sql = "SELECT rolname,rolsuper,rolbypassrls FROM pg_roles WHERE rolname IN ('dxos_process','dxos_hasura','dxos_reader','dxos_caller');"
        result = subprocess.run(compose('exec','-T','postgres','psql','-U','integration_bootstrap','-d','opendx_integration','-Atqc',sql), capture_output=True,text=True,check=True)
        rows = result.stdout.splitlines()
        self.assertEqual(len(rows), 4)
        self.assertTrue(all(row.endswith('|f|f') for row in rows))

    @classmethod
    def setUpClass(cls):
        from scripts.integration.environment import compose
        cls.compose = staticmethod(compose)
        cls.config = dict(line.split('=',1) for line in (ROOT/'.env.integration').read_text().splitlines() if line and not line.startswith('#'))
        cls.keycloak = 'http://localhost:' + cls.config.get('KEYCLOAK_PORT','18085')
        cls.gateway = 'http://localhost:' + cls.config.get('APISIX_GATEWAY_PORT','19080')
        cls.hasura = 'http://localhost:' + cls.config.get('HASURA_PORT','18080')
        cls.db("""INSERT INTO core.workspaces(id,code,name) VALUES('11111111-1111-4111-8111-111111111111','identity-alpha','Identity Alpha') ON CONFLICT(id) DO NOTHING;
CREATE TABLE IF NOT EXISTS domain.identity_boundary(id uuid PRIMARY KEY,workspace_id uuid NOT NULL REFERENCES core.workspaces(id),version bigint NOT NULL DEFAULT 1,updated_at timestamptz DEFAULT now(),label text);
INSERT INTO domain.identity_boundary(id,workspace_id,label) VALUES('33333333-3333-4333-8333-333333333333','11111111-1111-4111-8111-111111111111','identity-protected') ON CONFLICT(id) DO NOTHING;
DROP TRIGGER IF EXISTS identity_audit ON domain.identity_boundary;
CREATE TRIGGER identity_audit AFTER UPDATE ON domain.identity_boundary FOR EACH ROW EXECUTE FUNCTION core.record_domain_update_audit();
GRANT SELECT,UPDATE ON domain.identity_boundary TO dxos_caller,dxos_process,dxos_hasura;
GRANT SELECT ON domain.identity_boundary TO dxos_reader;""")
        status, doc = cls.http(cls.hasura+'/v1/metadata', {'type':'export_metadata','args':{}}, {'X-Hasura-Admin-Secret':cls.config['HASURA_ADMIN_SECRET']})
        if status != 200:
            raise RuntimeError('Hasura metadata prerequisite unavailable')
        sources = doc.get('sources',doc.get('metadata',{}).get('sources',[]))
        if not any(x['name']=='default' for x in sources):
            cls.metadata('pg_add_source',{'name':'default','configuration':{'connection_info':{'database_url':{'from_env':'HASURA_GRAPHQL_DATABASE_URL'}}}})
        tables = next((x.get('tables',[]) for x in sources if x['name']=='default'),[])
        table = next((x for x in tables if x['table']=={'schema':'domain','name':'identity_boundary'}),None)
        if table is None:
            cls.metadata('pg_track_table',{'source':'default','table':{'schema':'domain','name':'identity_boundary'}})
        if table is None or not any(p['role']=='role_procurement' for p in table.get('select_permissions',[])):
            cls.metadata('pg_create_select_permission',{'source':'default','table':{'schema':'domain','name':'identity_boundary'},'role':'role_procurement','permission':{'columns':['id','label'],'filter':{'workspace_id':{'_eq':'X-Hasura-Workspace-Id'}}}})

    @classmethod
    def metadata(cls, kind, args):
        status, body = cls.http(cls.hasura+'/v1/metadata',{'type':kind,'args':args},{'X-Hasura-Admin-Secret':cls.config['HASURA_ADMIN_SECRET']})
        if status != 200:
            raise RuntimeError('identity fixture metadata operation failed: '+kind)

    @classmethod
    def db(cls, sql, role='integration_bootstrap', success=True):
        result = subprocess.run(cls.compose('exec','-T','postgres','psql','-v','ON_ERROR_STOP=1','-U',role,'-d','opendx_integration','-At'),input=sql,capture_output=True,text=True)
        if success and result.returncode:
            raise RuntimeError('database fixture/check failed: '+result.stderr)
        if not success and result.returncode == 0:
            raise AssertionError('forbidden database operation succeeded')
        return result.stdout

    @staticmethod
    def http(url, data=None, headers=None, method=None):
        import urllib.error
        req = urllib.request.Request(url,data=json.dumps(data).encode() if data is not None else None,headers={'Content-Type':'application/json',**(headers or {})},method=method)
        try:
            with urllib.request.urlopen(req,timeout=15) as response:
                body = response.read()
                return response.status,json.loads(body) if body else {}
        except urllib.error.HTTPError as error:
            with error:
                return error.code,json.load(error)

    def token(self):
        return json.loads((ROOT/'build/integration/tokens.json').read_text())['TEST_REQUESTER_TOKEN']

    def denied(self, token=None, selected=None, spoof=False):
        headers = {'X-Hasura-Role': selected or 'role_procurement'}
        if token:
            headers['Authorization']='Bearer '+token
        if spoof:
            headers.update({'X-Hasura-User-Id':'22222222-2222-4222-8222-222222222222','X-Hasura-Workspace-Id':'11111111-1111-4111-8111-111111111111','X-Hasura-Admin-Secret':self.config['HASURA_ADMIN_SECRET']})
        status, body = self.http(self.gateway+'/api/cap/process/commands/procurement/submit',{},headers)
        self.assertEqual(status,403 if selected else 401)
        status, body = self.http(self.gateway+'/v1/graphql',{'query':'query { domain_identity_boundary { id label } }'},headers)
        self.assertTrue(status in (200,400,401,403))
        self.assertFalse(body.get('data',{}).get('domain_identity_boundary'))
        status, body = self.http(self.gateway+'/v1/graphql',{'query':'mutation { update_domain_identity_boundary(where: {}, _set: {label: "forged"}) { affected_rows } }'},headers)
        self.assertFalse(body.get('data'))

    def test_spoofed_headers_cannot_read(self):
        self.denied(spoof=True)

    def test_ungranted_role_rejected(self):
        self.denied(token=self.token(),selected='role_operator')

    def test_renewed_token_works(self):
        result = subprocess.run(['python3','scripts/integration/identity.py','tokens'],cwd=ROOT,capture_output=True,text=True)
        self.assertEqual(result.returncode,0)
        status, body = self.http(self.gateway+'/v1/graphql',{'query':'query { domain_identity_boundary { label } }'},{'Authorization':'Bearer '+self.token(),'X-Hasura-Role':'role_procurement'})
        self.assertEqual(status,200)
        self.assertEqual(body.get('data',{}).get('domain_identity_boundary'),[{'label':'identity-protected'}])

    def test_expired_token_rejected(self):
        import time
        from scripts.integration.identity import request
        admin = request(self.keycloak+'/realms/master/protocol/openid-connect/token',{'grant_type':'password','client_id':'admin-cli','username':self.config.get('KEYCLOAK_ADMIN','admin'),'password':self.config['KEYCLOAK_ADMIN_PASSWORD']},form=True)['access_token']
        realm_url = self.keycloak+'/admin/realms/dxos'
        original = request(realm_url,token=admin)['accessTokenLifespan']
        credentials = json.loads((ROOT/'build/integration/actor-credentials.json').read_text())
        try:
            self.assertEqual(self.http(realm_url,{'accessTokenLifespan':2},{'Authorization':'Bearer '+admin},'PUT')[0],204)
            token = request(self.keycloak+'/realms/dxos/protocol/openid-connect/token',{'grant_type':'password','client_id':'dxos-portal','username':'requester','password':credentials['requester'],'scope':'openid'},form=True)['access_token']
        finally:
            self.assertEqual(self.http(realm_url,{'accessTokenLifespan':original},{'Authorization':'Bearer '+admin},'PUT')[0],204)
        time.sleep(3)
        self.denied(token=token)

    def test_jwks_host_gateway_and_container_reachability(self):
        for base in (self.keycloak,self.gateway):
            status,doc = self.http(base+'/realms/dxos/protocol/openid-connect/certs')
            self.assertEqual(status,200)
            self.assertTrue(doc['keys'])
        result = subprocess.run(self.compose('exec','-T','cap-process','wget','-qO-','http://keycloak:8085/realms/dxos/protocol/openid-connect/certs'),capture_output=True,text=True)
        self.assertEqual(result.returncode,0)
        self.assertTrue(json.loads(result.stdout)['keys'])

    def test_runtime_roles_cannot_impersonate_or_mark_clean(self):
        self.db("SET dxos.actor_id='forged'; SET dxos.action='forged'; UPDATE domain.identity_boundary SET label='forged';",'dxos_caller',False)
        self.db("SET hasura.user='{}'; UPDATE domain.identity_boundary SET label='forged';",'dxos_caller',False)
        self.db("UPDATE core.file_objects SET scan_status='CLEAN' WHERE false;",'dxos_caller',False)
        self.db('SET ROLE dxos_process;','dxos_caller',False)
        self.db('INSERT INTO core.audit_logs(workspace_id) VALUES(NULL);','dxos_caller',False)
        self.db("UPDATE core.file_objects SET scan_status='CLEAN' WHERE false;",'dxos_hasura',False)
        self.db("UPDATE core.file_objects SET scan_status='CLEAN' WHERE false;",'dxos_reader',False)

    def test_trusted_process_audit_context_is_recorded(self):
        result = self.db("BEGIN; SET LOCAL dxos.actor_id='22222222-2222-4222-8222-222222222222'; SET LOCAL dxos.action='identity_boundary_check'; UPDATE domain.identity_boundary SET label='trusted'; SELECT actor_id FROM core.audit_logs WHERE action='identity_boundary_check'; ROLLBACK;",'dxos_process')
        self.assertIn('22222222-2222-4222-8222-222222222222',result)
        self.db("SET dxos.actor_id='forged'; SET dxos.action='forged'; UPDATE domain.identity_boundary SET label='forged';",'dxos_hasura',False)
        self.db("UPDATE core.file_objects SET scan_status='CLEAN' WHERE false;",'dxos_process')
