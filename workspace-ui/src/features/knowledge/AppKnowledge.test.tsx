// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
// @vitest-environment jsdom
import {afterEach,expect,it,vi} from 'vitest';
import {act,cleanup,fireEvent,render,screen,waitFor} from '@testing-library/react';
import {StrictMode} from 'react';
import {AppKnowledge} from './AppKnowledge';
const props={userID:'An',token:'token-an',appID:'hr',role:'role_hr',contextKey:'hr'};
const json=(value:unknown)=>new Response(JSON.stringify(value),{headers:{'Content-Type':'application/json'}});
afterEach(()=>{cleanup();vi.useRealTimers();vi.unstubAllGlobals()});
it('shows indexing and failed candidate independently of app readiness',async()=>{
 vi.stubGlobal('fetch',vi.fn(async()=>json({sources:[{source_id:'policy',document_id:'d',status:'INDEXING',readable:true,manageable:false,active_version_available:true},{source_id:'scan',document_id:'f',status:'FAILED',readable:true,manageable:false,active_version_available:false}]})));
 render(<AppKnowledge {...props}/>);await screen.findByText('Indexing');expect(screen.getByText('Indexing failed')).toBeTruthy();expect(screen.getByText(/previous approved version/i)).toBeTruthy();expect(screen.queryByText('Knowledge Ready')).toBeNull();
});
it('manager can grant without receiving READ excerpts',async()=>{
 let granted=false;
 vi.stubGlobal('fetch',vi.fn(async(input:RequestInfo|URL,init?:RequestInit)=>{
  const url=String(input);if(url.endsWith('/grants')&&init?.method==='POST'){granted=true;expect(init.headers).toMatchObject({'If-Match':'"4"'});return new Response(null,{status:204})}
  if(url.includes('/grant-management'))return json({document_id:'d',revision:4,authorization_generation:2,ceiling:{mode:'RESTRICTED',allowedRoles:['role_hr'],grantsManagedBy:'managers'},pending_publication:false,grants:[]});
  return json({sources:[{source_id:'payroll',document_id:'d',status:'READY',readable:false,manageable:true,active_version_available:false}]});
 }));
 render(<AppKnowledge {...props} role="role_hr_manager"/>);fireEvent.click(await screen.findByRole('button',{name:'Manage grants payroll'}));fireEvent.change(await screen.findByLabelText('User ID'),{target:{value:'binh-id'}});fireEvent.click(screen.getByRole('button',{name:'Grant read'}));await waitFor(()=>expect(granted).toBe(true));expect(screen.queryByText('restricted payroll excerpt')).toBeNull();expect(screen.queryByRole('button',{name:'View citation'})).toBeNull();
});
it('role switch discards stale search and grant state',async()=>{
 let finish:((response:Response)=>void)|undefined;
 vi.stubGlobal('fetch',vi.fn((input:RequestInfo|URL)=>String(input).endsWith('/search')?new Promise<Response>(resolve=>{finish=resolve}):Promise.resolve(json({sources:[]}))));
 const view=render(<AppKnowledge {...props}/>);fireEvent.change(screen.getByLabelText('Search app documents'),{target:{value:'payroll'}});await clickReady('Search app');await waitFor(()=>expect(finish).toBeDefined());view.rerender(<AppKnowledge {...props} role="role_other" contextKey="other"/>);finish!(json({status:'LEXICAL',results:[{snippet:'restricted payroll excerpt'}],warnings:[]}));await new Promise(r=>setTimeout(r,0));expect(screen.queryByText('restricted payroll excerpt')).toBeNull();expect(screen.queryByLabelText('User ID')).toBeNull();
});
it('shows a grounded answer and protected citations, then clears it on role change',async()=>{
 vi.stubGlobal('fetch',vi.fn(async(input:RequestInfo|URL)=>String(input).endsWith('/answers')?json({status:'ANSWERED',answer:'Director approval is required.',results:[],citations:[{document_id:'d',version_id:'v',chunk_id:'c',title:'Policy',snippet:'Director approval',positions:[],section_path:[],element_ids:[],warnings:[],url:'/protected'}]}):json({sources:[]})));
 const view=render(<AppKnowledge {...props}/>);fireEvent.change(screen.getByLabelText('Search app documents'),{target:{value:'Who approves?'}});await clickReady('Ask app');await screen.findByText('Director approval is required.');expect(screen.getByRole('button',{name:'View answer citation 1'})).toBeTruthy();view.rerender(<AppKnowledge {...props} role="role_other" contextKey="other"/>);await waitFor(()=>expect(screen.queryByText('Director approval is required.')).toBeNull());
});
it('explains search-only answers without inventing generated text',async()=>{
 vi.stubGlobal('fetch',vi.fn(async(input:RequestInfo|URL)=>String(input).endsWith('/answers')?json({status:'SEARCH_ONLY',answer:null,reason:'POLICY_BLOCKED',results:[],citations:[]}):json({sources:[]})));
 render(<AppKnowledge {...props}/>);fireEvent.change(screen.getByLabelText('Search app documents'),{target:{value:'Who approves?'}});await clickReady('Ask app');await screen.findByText(/Search only.*generation is unavailable/i);
});

it('loads sources and permits searching after Strict Mode replays effect setup', async () => {
 vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
  if (init?.signal?.aborted) throw new DOMException('Context changed', 'AbortError');
  if (String(input).endsWith('/search')) {
   return json({status: 'LEXICAL', results: [], warnings: []});
  }
  return json({sources: [{source_id: 'policy', status: 'READY', readable: true,
   manageable: false, active_version_available: true}]});
 }));
 render(<StrictMode><AppKnowledge {...props} /></StrictMode>);
 await screen.findByText('policy');
 fireEvent.change(screen.getByLabelText('Search app documents'), {target: {value: 'holiday'}});
 fireEvent.click(screen.getByRole('button', {name: 'Search app'}));
 await screen.findByText('No accessible evidence found.');
});

async function clickReady(name: string) {
 const button = screen.getByRole('button', {name}) as HTMLButtonElement;
 await waitFor(() => expect(button.disabled).toBe(false));
 fireEvent.click(button);
}

const source = {
 source_id: 'policy', document_id: 'd', status: 'READY', readable: true,
 manageable: true, active_version_available: true,
};
const evidence = {
 document_id: 'd', version_id: 'v', chunk_id: 'c', title: 'Protected evidence',
 snippet: 'Restricted excerpt', positions: [], section_path: [], element_ids: [], warnings: [],
 url: '/protected',
};
const answer = {status: 'ANSWERED', answer: 'Protected answer', results: [evidence], citations: [evidence]};
const grants = {
 document_id: 'd', revision: 4, authorization_generation: 2,
 ceiling: {mode: 'RESTRICTED', allowedRoles: ['role_hr'], grantsManagedBy: 'managers'},
 pending_publication: false, grants: [{user_id: 'private-user', status: 'ACTIVE'}],
};

async function showProtectedKnowledge() {
 await act(async () => {});
 fireEvent.change(screen.getByLabelText('Search app documents'), {target: {value: 'Who approves?'}});
 await act(async () => {
  fireEvent.click(screen.getByRole('button', {name: 'Ask app'}));
  fireEvent.click(screen.getByRole('button', {name: 'Manage grants policy'}));
 });
 expect(screen.getByText('Protected answer')).toBeTruthy();
 expect(screen.getByText('Protected evidence')).toBeTruthy();
 expect(screen.getByText(/private-user: ACTIVE/)).toBeTruthy();
}

function expectProtectedKnowledgeCleared() {
 expect(screen.queryByText('Protected answer')).toBeNull();
 expect(screen.queryByText('Protected evidence')).toBeNull();
 expect(screen.queryByRole('button', {name: 'View answer citation 1'})).toBeNull();
 expect(screen.queryByRole('dialog', {name: 'Protected citation'})).toBeNull();
 expect(screen.queryByText(/private-user: ACTIVE/)).toBeNull();
}

it.each([401, 403, 404, 409])('clears protected knowledge and aborts a pending citation after poll denial (%s)', async status => {
 vi.useFakeTimers();
 let denied = false;
 let finishCitation: (response: Response) => void = () => {};
 let citationSignal: AbortSignal | null | undefined;
 const unrelatedAction = vi.fn();
 vi.stubGlobal('fetch', (input: RequestInfo | URL, init?: RequestInit) => {
  const url = String(input);
  if (url.endsWith('/answers')) return Promise.resolve(json(answer));
  if (url.endsWith('/grant-management')) return Promise.resolve(json(grants));
  if (url.includes('/citations/')) {
   citationSignal = init?.signal;
   return new Promise<Response>(resolve => { finishCitation = resolve; });
  }
  return Promise.resolve(denied ? new Response(null, {status}) : json({sources: [source]}));
 });
 render(<><AppKnowledge {...props}/><button onClick={unrelatedAction}>Open Assistant</button></>);
 await showProtectedKnowledge();
 fireEvent.click(screen.getByRole('button', {name: 'View answer citation 1'}));
 expect(citationSignal?.aborted).toBe(false);

 denied = true;
 await act(async () => vi.advanceTimersByTimeAsync(2000));
 expectProtectedKnowledgeCleared();
 expect(citationSignal?.aborted).toBe(true);
 expect((screen.getByRole('button', {name: 'Ask app'}) as HTMLButtonElement).disabled).toBe(true);
 expect(screen.queryByRole('button', {name: 'Manage grants policy'})).toBeNull();
 fireEvent.click(screen.getByRole('button', {name: 'Open Assistant'}));
 expect(unrelatedAction).toHaveBeenCalledOnce();

 denied = false;
 await act(async () => vi.advanceTimersByTimeAsync(2000));
 expect((screen.getByRole('button', {name: 'Ask app'}) as HTMLButtonElement).disabled).toBe(false);
 await act(async () => finishCitation(json(evidence)));
 expectProtectedKnowledgeCleared();
 await act(async () => fireEvent.click(screen.getByRole('button', {name: 'Ask app'})));
 expect(screen.getByText('Protected answer')).toBeTruthy();
});

it.each([
 ['readable', {...source, readable: false}],
 ['manageable', {...source, manageable: false}],
 ['removed', null],
])('reconciles prior protected content when a successful poll reports %s authority loss', async (_change, nextSource) => {
 vi.useFakeTimers();
 let downgraded = false;
 vi.stubGlobal('fetch', async (input: RequestInfo | URL) => {
  const url = String(input);
  if (url.endsWith('/answers')) return json(answer);
  if (url.endsWith('/grant-management')) return json(grants);
  if (url.includes('/citations/')) return json({...evidence, title: 'Protected preview'});
  return json({sources: downgraded ? (nextSource ? [nextSource] : []) : [source]});
 });
 render(<AppKnowledge {...props}/>);
 await showProtectedKnowledge();
 await act(async () => fireEvent.click(screen.getByRole('button', {name: 'View answer citation 1'})));
 expect(screen.getByRole('dialog', {name: 'Protected citation'})).toBeTruthy();

 downgraded = true;
 await act(async () => vi.advanceTimersByTimeAsync(2000));

 expectProtectedKnowledgeCleared();
 expect((screen.getByRole('button', {name: 'Ask app'}) as HTMLButtonElement).disabled).toBe(false);
 expect(Boolean(screen.queryByRole('button', {name: 'Manage grants policy'}))).toBe(Boolean(nextSource?.manageable));
});

it('keeps protected app actions disabled until the first source poll succeeds', async () => {
 vi.useFakeTimers();
 let healthy = false;
 vi.stubGlobal('fetch', async () => healthy ? json({sources: [source]}) : new Response(null, {status: 503}));
 render(<AppKnowledge {...props}/>);
 fireEvent.change(screen.getByLabelText('Search app documents'), {target: {value: 'Who approves?'}});
 await act(async () => {});

 expect((screen.getByRole('button', {name: 'Ask app'}) as HTMLButtonElement).disabled).toBe(true);
 healthy = true;
 await act(async () => vi.advanceTimersByTimeAsync(2000));
 expect((screen.getByRole('button', {name: 'Ask app'}) as HTMLButtonElement).disabled).toBe(false);
});
