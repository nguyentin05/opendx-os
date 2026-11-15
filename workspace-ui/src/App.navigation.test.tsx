// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
// @vitest-environment jsdom
import {afterEach,it,expect,vi} from 'vitest';
import {render,screen,fireEvent,cleanup,waitFor} from '@testing-library/react';
import {App} from './App';
import {Session} from './auth';
afterEach(()=>{cleanup();vi.unstubAllGlobals()});
it('navigates a real registry table and capability-only library using the independent library role',async()=>{
 const headers:string[]=[];
 vi.stubGlobal('fetch',vi.fn(async(input:RequestInfo|URL,init?:RequestInit)=>{headers.push(new Headers(init?.headers).get('X-Hasura-Role')??'');return new Response(JSON.stringify(String(input)==='/v1/graphql'?{data:{domain_records:[{title:'A declared record'}]}}:String(input).endsWith('/search')?{status:'LEXICAL',results:[],warnings:[]}:{documents:[]}),{status:200})}));
 const session:Session={token:'test-token',user:{id:'A',email:'a@example.invalid',display_name:'Alice',roles:[]},apps:[{app_id:'sample',display_name:'Sample app',version:'1',capabilities:[],roles:[{name:'role_sample'}],tables:[{name:'domain.records',access:{select:[{role:'role_sample',columns:['title']}]},ui:{listColumns:['title']}}]},{app_id:'library',display_name:'My library',version:'1',capabilities:['cap-ai'],roles:[{name:'role_library_user'}],tables:[],knowledge:{version:1,personalLibrary:{enabled:true,allowedRoles:['role_library_user']}}}]};
 render(<App initialSession={session}/>);
 fireEvent.click(screen.getByRole('button',{name:'Sample app'}));
 await screen.findByText('A declared record');
 fireEvent.click(screen.getByRole('button',{name:'Personal library'}));
 expect(screen.getByLabelText('Upload document')).toBeTruthy();
 fireEvent.change(screen.getByLabelText('Search your documents'),{target:{value:'policy'}});
 fireEvent.click(screen.getByRole('button',{name:'Search'}));
 await waitFor(()=>expect(headers).toContain('role_library_user'));
 expect(headers).toContain('role_sample');
 fireEvent.click(screen.getByRole('button',{name:'Sample app'}));
 await screen.findByText('A declared record');
 expect(screen.queryByLabelText('Upload document')).toBeNull();
 fireEvent.click(screen.getByRole('button',{name:'Personal library'}));
 expect(screen.getByLabelText('Upload document')).toBeTruthy();
 expect(screen.queryByLabelText(/API key/i)).toBeNull();
});

it('opens shared sources and manager controls in an app that also owns the personal library',async()=>{
 vi.stubGlobal('fetch',vi.fn(async(input:RequestInfo|URL)=>new Response(JSON.stringify(String(input)==='/v1/graphql'?{data:{domain_records:[{title:'Declared shared record'}]}}:String(input).includes('/app-knowledge')?{sources:[{source_id:'policy',document_id:'d',status:'READY',readable:false,manageable:true,active_version_available:false}]}:{documents:[]}),{status:200})));
 const session:Session={token:'combined-token',user:{id:'A',email:'a@test.invalid',display_name:'Alice',roles:[]},apps:[{app_id:'combined',display_name:'Combined app',version:'1',capabilities:['cap-ai'],roles:[{name:'role_combined'}],tables:[{name:'domain.records',access:{select:[{role:'role_combined',columns:['title']}]},ui:{listColumns:['title']}}],knowledge:{version:1,personalLibrary:{enabled:true,allowedRoles:['role_combined']},sources:[{id:'policy',path:'knowledge/policy.md',mediaType:'text/markdown',indexingProfile:'standard-text-v1',access:{mode:'RESTRICTED',allowedRoles:['role_combined'],grantsManagedBy:'manager'}}],grantPolicies:{manager:{allowedRoles:['role_combined']}}}}]};
 render(<App initialSession={session}/>);fireEvent.click(screen.getByRole('button',{name:'Combined app'}));
 await screen.findByRole('heading',{name:'App knowledge'});await screen.findByText('Declared shared record');await screen.findByRole('button',{name:'Manage grants policy'});
 fireEvent.click(screen.getByRole('button',{name:'Personal library'}));expect(screen.getByLabelText('Upload document')).toBeTruthy();expect(screen.queryByRole('heading',{name:'App knowledge'})).toBeNull();
 fireEvent.click(screen.getByRole('button',{name:'Combined app'}));await screen.findByRole('heading',{name:'App knowledge'});expect(screen.queryByLabelText('Upload document')).toBeNull();
});
