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
