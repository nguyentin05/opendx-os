// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
// @vitest-environment jsdom
import {afterEach,it,expect,vi} from 'vitest';
import {render,screen,fireEvent,cleanup,waitFor,within} from '@testing-library/react';
import {App} from './App';
import {Session} from './auth';
import type { InstalledTable } from './types/registry';

function recordsTable(role: string): InstalledTable {
 return {
  name: 'domain.records',
  columns: [{ name: 'title', type: 'text', nullable: false }],
  access: {
   select: [{ role, scope: 'own', columns: ['title'] }],
   insert: [{ role, columns: ['title'] }],
  },
  ui: {
   listColumns: ['title'],
   formFields: [{ name: 'title', label: 'Title', type: 'text', required: true }],
  },
 };
}

function openApplication(name: string) {
 fireEvent.click(within(screen.getByRole('navigation', { name: 'Workspace' })).getByRole('link', { name }));
}

afterEach(()=>{
 cleanup();
 vi.unstubAllGlobals();
 window.history.replaceState(null, '', '/');
});
it('navigates a real registry table and capability-only library using the independent library role',async()=>{
 const headers:string[]=[];
 vi.stubGlobal('fetch',vi.fn(async(input:RequestInfo|URL,init?:RequestInit)=>{headers.push(new Headers(init?.headers).get('X-Hasura-Role')??'');return new Response(JSON.stringify(String(input)==='/v1/graphql'?{data:{domain_records:[{title:'A declared record'}]}}:String(input).endsWith('/search')?{status:'LEXICAL',results:[],warnings:[]}:{documents:[]}),{status:200})}));
 const session:Session={token:'test-token',user:{id:'A',email:'a@example.invalid',display_name:'Alice',roles:[]},apps:[{app_id:'sample',display_name:'Sample app',version:'1',capabilities:[],roles:[{name:'role_sample'}],tables:[recordsTable('role_sample')],knowledge:null},{app_id:'library',display_name:'My library',version:'1',capabilities:['cap-ai'],roles:[{name:'role_library_user'}],tables:[],knowledge:{version:1,personalLibrary:{enabled:true,allowedRoles:['role_library_user']}}}]};
 render(<App initialSession={session}/>);
 openApplication('Sample app');
 await screen.findByText('A declared record');
 fireEvent.click(screen.getByRole('button',{name:'Personal library'}));
 expect(screen.getByLabelText('Upload document')).toBeTruthy();
 fireEvent.change(screen.getByLabelText('Search your documents'),{target:{value:'policy'}});
 fireEvent.click(screen.getByRole('button',{name:'Search'}));
 await waitFor(()=>expect(headers).toContain('role_library_user'));
 expect(headers).toContain('role_sample');
 openApplication('Sample app');
 await screen.findByText('A declared record');
 expect(screen.queryByLabelText('Upload document')).toBeNull();
 fireEvent.click(screen.getByRole('button',{name:'Personal library'}));
 expect(screen.getByLabelText('Upload document')).toBeTruthy();
 expect(screen.queryByLabelText(/API key/i)).toBeNull();
});

it('opens shared sources and manager controls in an app that also owns the personal library',async()=>{
 vi.stubGlobal('fetch',vi.fn(async(input:RequestInfo|URL)=>new Response(JSON.stringify(String(input)==='/v1/graphql'?{data:{domain_records:[{title:'Declared shared record'}]}}:String(input).includes('/app-knowledge')?{sources:[{source_id:'policy',document_id:'d',status:'READY',readable:false,manageable:true,active_version_available:false}]}:{documents:[]}),{status:200})));
 const session:Session={token:'combined-token',user:{id:'A',email:'a@test.invalid',display_name:'Alice',roles:[]},apps:[{app_id:'combined',display_name:'Combined app',version:'1',capabilities:['cap-ai'],roles:[{name:'role_combined'}],tables:[recordsTable('role_combined')],knowledge:{version:1,personalLibrary:{enabled:true,allowedRoles:['role_combined']},sources:[{id:'policy',path:'knowledge/policy.md',mediaType:'text/markdown',indexingProfile:'standard-text-v1',access:{mode:'RESTRICTED',allowedRoles:['role_combined'],grantsManagedBy:'manager'}}],grantPolicies:{manager:{allowedRoles:['role_combined']}}}}]};
 render(<App initialSession={session}/>);openApplication('Combined app');
 await screen.findByRole('heading',{name:'App knowledge'});await screen.findByText('Declared shared record');await screen.findByRole('button',{name:'Manage grants policy'});
 fireEvent.click(screen.getByRole('button',{name:'Personal library'}));expect(screen.getByLabelText('Upload document')).toBeTruthy();expect(screen.queryByRole('heading',{name:'App knowledge'})).toBeNull();
 openApplication('Combined app');await screen.findByRole('heading',{name:'App knowledge'});expect(screen.queryByLabelText('Upload document')).toBeNull();
});

it('opens the global assistant independently and keeps applications usable when it is unavailable',async()=>{
 vi.stubGlobal('fetch',vi.fn(async(input:RequestInfo|URL)=>String(input).startsWith('/api/cap/ai/conversations')?new Response('',{status:503}):Response.json({data:{domain_records:[{title:'Available record'}]}})));
 const session:Session={token:'token',user:{id:'A',email:'a@test.invalid',display_name:'Alice',roles:[]},apps:[{app_id:'sample',display_name:'Sample app',version:'1',capabilities:[],roles:[{name:'role_sample'},{name:'role_manager'}],tables:[recordsTable('role_sample')],knowledge:null}]};
 render(<App initialSession={session}/>);openApplication('Sample app');await screen.findByText('Available record');expect(screen.getByRole('button',{name:'Acting role'})).toBeTruthy();fireEvent.click(screen.getByRole('button',{name:'Assistant'}));await screen.findByText('Assistant is temporarily unavailable.');expect(screen.queryByLabelText('Acting role')).toBeNull();openApplication('Sample app');await screen.findByText('Available record');
});

it('retains a pending turn key through a permission refresh',async()=>{
 const keys:string[]=[];const user={id:'A',email:'a@test.invalid',display_name:'Alice',roles:[]};const session:Session={token:'token',user,apps:[]};
 vi.stubGlobal('fetch',vi.fn(async(input:RequestInfo|URL,init?:RequestInit)=>{const url=String(input);if(url==='/api/auth/me')return Response.json(user);if(url==='/api/auth/apps')return Response.json({apps:[]});if(init?.method==='POST'){keys.push(new Headers(init.headers).get('Idempotency-Key')!);throw new TypeError('network')}if(url.endsWith('/c1'))return Response.json({messages:[],next_after_seq:null});return Response.json({conversations:[{id:'c1',title:'Existing'}],next_cursor:null})}));
 render(<App initialSession={session}/>);fireEvent.click(screen.getByRole('button',{name:'Assistant'}));fireEvent.click(await screen.findByRole('button',{name:'Existing'}));fireEvent.change(await screen.findByLabelText('Question'),{target:{value:'Question'}});fireEvent.click(screen.getByRole('button',{name:'Send'}));await screen.findByText(/UNCERTAIN/);fireEvent.click(screen.getByRole('button',{name:'Refresh permissions'}));fireEvent.click(await screen.findByRole('button',{name:'Existing'}));fireEvent.click(await screen.findByRole('button',{name:'Retry'}));await waitFor(()=>expect(keys).toHaveLength(2));expect(keys[1]).toBe(keys[0]);
});

it('returns to Home when a permission refresh revokes the open personal library', async () => {
 const user = { id: 'A', email: 'a@test.invalid', display_name: 'Alice', roles: [] };
 const session: Session = {
  token: 'token', user,
  apps: [{
   app_id: 'library', display_name: 'Library app', version: '1', capabilities: ['cap-ai'],
   roles: [{ name: 'role_library_user' }], tables: [],
   knowledge: { version: 1, personalLibrary: { enabled: true, allowedRoles: ['role_library_user'] } },
  }],
 };
 vi.stubGlobal('fetch', async (input: string) => {
  if (input === '/api/auth/me') return Response.json(user);
  if (input === '/api/auth/apps') return Response.json({ apps: [] });
  return Response.json({ documents: [] });
 });

 render(<App initialSession={session} />);
 fireEvent.click(screen.getByRole('button', { name: 'Personal library' }));
 expect(screen.getByLabelText('Upload document')).toBeTruthy();
 fireEvent.click(screen.getByRole('button', { name: 'Refresh permissions' }));

 expect(screen.queryByLabelText('Upload document')).toBeNull();
 await screen.findByRole('heading', { name: 'My Workspace' });
 expect(screen.queryByRole('button', { name: 'Personal library' })).toBeNull();
});
