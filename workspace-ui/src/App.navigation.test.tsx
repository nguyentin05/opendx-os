// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
// @vitest-environment jsdom
import {afterEach,it,expect,vi} from 'vitest';
import {act,render,screen,fireEvent,cleanup,waitFor,within} from '@testing-library/react';
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

it('keeps the open record and acting role usable while the drawer reports an AI outage', async () => {
 const role = 'role_sample';
 const id = '3451ed47-6023-416a-afad-883b14403613';
 const table = recordsTable(role);
 table.access.select[0].columns.push('id');
 const session: Session = {
  token: 'token',
  user: {id: 'A', email: 'a@test.invalid', display_name: 'Alice', roles: []},
  apps: [{app_id: 'sample', display_name: 'Sample app', version: '1', capabilities: [],
   roles: [{name: role}, {name: 'role_manager'}], tables: [table], knowledge: null}],
 };
 window.history.replaceState(null, '', '/apps/sample/tables/domain.records/' + id + '?role=' + role);
 vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => String(input).startsWith('/api/cap/ai/conversations')
  ? new Response('', {status: 503})
  : Response.json({data: {domain_records_by_pk: {id, title: 'Available record'}}})));

 render(<App initialSession={session} />);
 const record = within(screen.getByRole('region', {name: 'Record details'}));
 await record.findByText('Available record');
 const trigger = screen.getByRole('button', {name: 'Assistant'});
 trigger.focus();
 fireEvent.click(trigger);
 const drawer = within(screen.getByRole('dialog', {name: 'Assistant'}));
 await drawer.findByText('Assistant is temporarily unavailable.');
 expect(record.getByText('Available record')).toBeTruthy();
 expect(screen.getByRole('button', {name: 'Acting role'})).toBeTruthy();
 expect(window.location.pathname).toBe('/apps/sample/tables/domain.records/' + id);
 fireEvent.keyDown(drawer.getByRole('button', {name: 'Close assistant'}), {key: 'Escape'});
 expect(screen.queryByRole('dialog', {name: 'Assistant'})).toBeNull();
 expect(document.activeElement).toBe(trigger);
 expect(record.getByText('Available record')).toBeTruthy();

 fireEvent.click(trigger);
 fireEvent.click(screen.getByRole('link', {name: 'My Workspace'}));
 expect(screen.getByRole('heading', {name: 'My Workspace'})).toBeTruthy();
 expect(drawer.getByText('Assistant is temporarily unavailable.')).toBeTruthy();
});

it('withholds an open drawer during verification and retains its retry key for the same verified user', async () => {
 const keys: string[] = [];
 const user = {id: 'A', email: 'a@test.invalid', display_name: 'Alice', roles: []};
 const session: Session = {token: 'token', user, apps: []};
 let completeVerification!: (response: Response) => void;
 const verification = new Promise<Response>(resolve => {completeVerification = resolve});
 vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
  const url = String(input);
  if (url === '/api/auth/me') return verification;
  if (url === '/api/auth/apps') return Response.json({apps: []});
  if (init?.method === 'POST') {
   keys.push(new Headers(init.headers).get('Idempotency-Key')!);
   throw new TypeError('network');
  }
  if (url.endsWith('/c1')) return Response.json({messages: [], next_after_seq: null});
  return Response.json({conversations: [{id: 'c1', title: 'Existing'}], next_cursor: null});
 }));

 render(<App initialSession={session} />);
 fireEvent.click(screen.getByRole('button', {name: 'Assistant'}));
 fireEvent.click(await screen.findByRole('button', {name: 'Existing'}));
 fireEvent.change(await screen.findByLabelText('Question'), {target: {value: 'Question'}});
 fireEvent.click(screen.getByRole('button', {name: 'Send'}));
 await screen.findByText(/UNCERTAIN/);
 fireEvent.click(screen.getByRole('button', {name: 'Refresh permissions'}));
 expect(screen.queryByRole('dialog', {name: 'Assistant'})).toBeNull();
 expect(screen.queryByRole('button', {name: 'Assistant'})).toBeNull();
 await act(async () => completeVerification(Response.json(user)));
 fireEvent.click(await screen.findByRole('button', {name: 'Retry'}));
 await waitFor(() => expect(keys).toHaveLength(2));
 expect(keys[1]).toBe(keys[0]);
});

it('aborts protected chat requests and discards drawer state when verification identifies a different user', async () => {
 const alice = {id: 'A', email: 'a@test.invalid', display_name: 'Alice', roles: []};
 const bob = {id: 'B', email: 'b@test.invalid', display_name: 'Bob', roles: []};
 const session: Session = {token: 'same-token', user: alice, apps: []};
 let verified = false;
 let completeTranscript!: (response: Response) => void;
 let transcriptSignal: AbortSignal | undefined;
 vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
  const url = String(input);
  if (url === '/api/auth/me') {
   verified = true;
   return Response.json(bob);
  }
  if (url === '/api/auth/apps') return Response.json({apps: []});
  if (url.endsWith('/alice')) {
   transcriptSignal = init?.signal ?? undefined;
   return new Promise<Response>(resolve => {completeTranscript = resolve});
  }
  return Response.json({
   conversations: [{id: verified ? 'bob' : 'alice', title: verified ? 'Bob chat' : 'Alice chat'}],
   next_cursor: null,
  });
 }));

 render(<App initialSession={session} />);
 fireEvent.click(screen.getByRole('button', {name: 'Assistant'}));
 fireEvent.click(await screen.findByRole('button', {name: 'Alice chat'}));
 fireEvent.change(screen.getByRole('textbox', {name: 'Question'}), {target: {value: 'Private draft'}});
 fireEvent.click(screen.getByRole('button', {name: 'Refresh permissions'}));
 await screen.findByText('Welcome, Bob.');
 expect(transcriptSignal?.aborted).toBe(true);
 expect(screen.queryByRole('dialog', {name: 'Assistant'})).toBeNull();
 await act(async () => completeTranscript(Response.json({
  messages: [{id: 'm1', seq: 1, role: 'ASSISTANT', status: 'COMPLETE', content: 'Private transcript'}],
  next_after_seq: null,
 })));
 fireEvent.click(screen.getByRole('button', {name: 'Assistant'}));
 await screen.findByRole('button', {name: 'Bob chat'});
 expect(screen.queryByText('Private transcript')).toBeNull();
 expect(screen.queryByRole('textbox', {name: 'Question'})).toBeNull();
 expect(screen.queryByRole('button', {name: 'Alice chat'})).toBeNull();
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

it('opens the canonical detail and creation routes from the generic table outlet', async () => {
 const role = 'role_sample';
 const id = '3451ed47-6023-416a-afad-883b14403613';
 const table = recordsTable(role);
 table.access.select[0].columns.push('id');
 const session: Session = {
  token: 'test-token',
  user: {id: 'A', email: 'a@example.invalid', display_name: 'Alice', roles: []},
  apps: [{
   app_id: 'sample', display_name: 'Sample app', version: '1', capabilities: [],
   roles: [{name: role}], tables: [table], knowledge: null,
  }],
 };
 vi.stubGlobal('fetch', async () => Response.json({data: {domain_records: [{id, title: 'Sample record'}]}}));
 render(<App initialSession={session} />);
 openApplication('Sample app');

 fireEvent.click(await screen.findByRole('button', {name: 'Open'}));
 expect(window.location.pathname).toBe('/apps/sample/tables/domain.records/' + id);
 expect(window.location.search).toBe('?role=role_sample');
 openApplication('Sample app');
 fireEvent.click(await screen.findByRole('button', {name: 'New record'}));
 expect(window.location.pathname).toBe('/apps/sample/tables/domain.records/new');
 expect(window.location.search).toBe('?role=role_sample');
});

it('opens a knowledge-only app with its verified acting role and records its app root as recent', async () => {
 const role = 'role_reader';
 const user = {
  id: 'A', email: 'a@test.invalid', display_name: 'Alice', roles: [{name: role, app_id: 'handbook'}],
 };
 const session: Session = {
  token: 'token', user,
  apps: [{
   app_id: 'handbook', display_name: 'Handbook', version: '1', capabilities: ['cap-ai'], roles: [{name: role}], tables: [],
   knowledge: {version: 1, sources: [{
    id: 'policy', path: 'knowledge/policy.md', mediaType: 'text/markdown', indexingProfile: 'standard-text-v1',
    access: {mode: 'ROLE', allowedRoles: [role]},
   }]},
  }],
 };
 const requests: { url: string; role: string | null }[] = [];
 let granted = true;
 vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
  const url = String(input);
  if (url === '/api/auth/me') return Response.json(user);
  if (url === '/api/auth/apps') return Response.json({apps: granted ? session.apps : []});
  requests.push({url, role: new Headers(init?.headers).get('X-Hasura-Role')});
  return Response.json({sources: [{source_id: 'policy', status: 'READY', readable: true,
   manageable: false, active_version_available: true}]});
 }));

 render(<App initialSession={session} />);
 const shortcuts = within(screen.getByRole('region', {name: 'Your apps'}));
 fireEvent.click(shortcuts.getByRole('link', {name: 'Handbook'}));
 await screen.findByText('policy');
 expect(screen.queryByRole('navigation', {name: 'Application views'})).toBeNull();
 expect(requests).toEqual([{url: '/api/cap/ai/app-knowledge?app_id=handbook', role}]);
 fireEvent.click(screen.getByRole('link', {name: 'My Workspace'}));
 const recent = within(screen.getByRole('region', {name: 'Recently viewed'}));
 const link = recent.getByRole('link', {name: 'Handbook'});
 expect(link.getAttribute('href')).toBe('/apps/handbook?role=role_reader');
 fireEvent.click(link);
 await screen.findByText('policy');
 fireEvent.click(screen.getByRole('link', {name: 'My Workspace'}));

 granted = false;
 const priorRequests = requests.length;
 const currentRecent = within(screen.getByRole('region', {name: 'Recently viewed'}));
 fireEvent.click(currentRecent.getByRole('link', {name: 'Handbook'}));
 await screen.findByRole('heading', {name: 'Access unavailable'});
 expect(screen.queryByRole('region', {name: 'App knowledge'})).toBeNull();
 expect(requests).toHaveLength(priorRequests);
});

it('honors a personal-library app URL only for its permitted acting role', async () => {
 const role = 'role_library_user';
 const session: Session = {
  token: 'token', user: {id: 'A', email: 'a@test.invalid', display_name: 'Alice', roles: []},
  apps: [{
   app_id: 'library', display_name: 'Library app', version: '1', capabilities: ['cap-ai'],
   roles: [{name: role}, {name: 'role_observer'}], tables: [],
   knowledge: {version: 1, personalLibrary: {enabled: true, allowedRoles: [role]}},
  }],
 };
 const fetcher = vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => Response.json({documents: []}));
 vi.stubGlobal('fetch', fetcher);
 window.history.replaceState(null, '', '/apps/library?role=role_observer');
 render(<App initialSession={session} />);
 expect(screen.queryByLabelText('Upload document')).toBeNull();
 expect(fetcher).toHaveBeenCalledTimes(0);

 openApplication('Library app');
 expect(screen.getByLabelText('Upload document')).toBeTruthy();
 await waitFor(() => expect(fetcher).toHaveBeenCalledTimes(1));
 expect(new Headers(fetcher.mock.calls[0][1]?.headers).get('X-Hasura-Role')).toBe(role);
});
