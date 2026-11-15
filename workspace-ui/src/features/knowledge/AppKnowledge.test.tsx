// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
// @vitest-environment jsdom
import {afterEach,expect,it,vi} from 'vitest';
import {cleanup,fireEvent,render,screen,waitFor} from '@testing-library/react';
import {AppKnowledge} from './AppKnowledge';
const props={userID:'An',token:'token-an',appID:'hr',role:'role_hr',contextKey:'hr'};
const json=(value:unknown)=>new Response(JSON.stringify(value),{headers:{'Content-Type':'application/json'}});
afterEach(()=>{cleanup();vi.unstubAllGlobals()});
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
 const view=render(<AppKnowledge {...props}/>);fireEvent.change(screen.getByLabelText('Search app documents'),{target:{value:'payroll'}});fireEvent.click(screen.getByRole('button',{name:'Search app'}));await waitFor(()=>expect(finish).toBeDefined());view.rerender(<AppKnowledge {...props} role="role_other" contextKey="other"/>);finish!(json({status:'LEXICAL',results:[{snippet:'restricted payroll excerpt'}],warnings:[]}));await new Promise(r=>setTimeout(r,0));expect(screen.queryByText('restricted payroll excerpt')).toBeNull();expect(screen.queryByLabelText('User ID')).toBeNull();
});
it('shows a grounded answer and protected citations, then clears it on role change',async()=>{
 vi.stubGlobal('fetch',vi.fn(async(input:RequestInfo|URL)=>String(input).endsWith('/answers')?json({status:'ANSWERED',answer:'Director approval is required.',results:[],citations:[{document_id:'d',version_id:'v',chunk_id:'c',title:'Policy',snippet:'Director approval',positions:[],section_path:[],element_ids:[],warnings:[],url:'/protected'}]}):json({sources:[]})));
 const view=render(<AppKnowledge {...props}/>);fireEvent.change(screen.getByLabelText('Search app documents'),{target:{value:'Who approves?'}});fireEvent.click(screen.getByRole('button',{name:'Ask app'}));await screen.findByText('Director approval is required.');expect(screen.getByRole('button',{name:'View answer citation 1'})).toBeTruthy();view.rerender(<AppKnowledge {...props} role="role_other" contextKey="other"/>);await waitFor(()=>expect(screen.queryByText('Director approval is required.')).toBeNull());
});
it('explains search-only answers without inventing generated text',async()=>{
 vi.stubGlobal('fetch',vi.fn(async(input:RequestInfo|URL)=>String(input).endsWith('/answers')?json({status:'SEARCH_ONLY',answer:null,reason:'POLICY_BLOCKED',results:[],citations:[]}):json({sources:[]})));
 render(<AppKnowledge {...props}/>);fireEvent.change(screen.getByLabelText('Search app documents'),{target:{value:'Who approves?'}});fireEvent.click(screen.getByRole('button',{name:'Ask app'}));await screen.findByText(/Search only.*generation is unavailable/i);
});
