// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest';
import { render, screen, fireEvent, waitFor, cleanup } from '@testing-library/react';
import { PersonalLibrary } from './PersonalLibrary';
import { KnowledgeClient } from './client';
const props={userID:'A',token:'token-A',appID:'library',role:'role_library_user',contextKey:'app-A'};
afterEach(()=>{cleanup();vi.unstubAllGlobals()});
const json=(value:unknown,status=200)=>new Response(JSON.stringify(value),{status,headers:{'Content-Type':'application/json'}});
describe('PersonalLibrary',()=>{
 it('owner flow needs CLEAN, queues and searches without a model setup',async()=>{let clean=false;const calls:string[]=[];vi.stubGlobal('fetch',vi.fn(async(input:RequestInfo|URL,init?:RequestInit)=>{const url=String(input);calls.push(url);if(url.includes('private/upload'))return json({file_id:'file-A'});if(url==='/v1/graphql'){clean=true;return json({data:{core_files_by_pk:{scan_status:'CLEAN'}}})}if(url.includes('/citations/'))return json({document_id:'d',version_id:'v',chunk_id:'c',title:'Protected policy',snippet:'Verified Director citation',section_path:['Policy'],positions:[],warnings:[]});if(url.endsWith('/search'))return json({status:'LEXICAL',results:[{document_id:'d',version_id:'v',chunk_id:'c',title:'Policy',snippet:'Director approval',element_ids:['e'],positions:[],section_path:['Policy'],warnings:[],url:'/api/cap/ai/documents/d/citations/c?version_id=v'}],warnings:[]});if(url.endsWith('/documents')&&init?.method==='POST'){expect(clean).toBe(true);return json({document_id:'d',version_id:'v',job_id:'j',revision:1,status:'QUEUED'},202)}return json({documents:[]})}));render(<PersonalLibrary {...props}/>);fireEvent.change(screen.getByLabelText('Upload document'),{target:{files:[new File(['# policy'],'policy.md',{type:'text/markdown'})]}});await screen.findByText(/QUEUED/);fireEvent.change(screen.getByLabelText('Search your documents'),{target:{value:'Director'}});fireEvent.click(screen.getByRole('button',{name:'Search'}));await screen.findByText('Director approval');fireEvent.click(screen.getByRole('button',{name:'View citation'}));await screen.findByRole('dialog',{name:'Protected citation'});expect(screen.getByText('Verified Director citation')).toBeTruthy();expect(screen.queryByLabelText(/API key/i)).toBeNull();expect(calls.some(x=>/embed|generate|provider/.test(x))).toBe(false)});
 it('context switch discards stale A result even when transport ignores cancellation',async()=>{let finish:((r:Response)=>void)|undefined;vi.stubGlobal('fetch',vi.fn((input:RequestInfo|URL)=>String(input).endsWith('/search')?new Promise<Response>(r=>{finish=r}):Promise.resolve(json({documents:[]}))));const view=render(<PersonalLibrary {...props}/>);fireEvent.change(screen.getByLabelText('Search your documents'),{target:{value:'private'}});fireEvent.click(screen.getByRole('button',{name:'Search'}));await waitFor(()=>expect(finish).toBeDefined());view.rerender(<PersonalLibrary {...props} userID="B" token="token-B" contextKey="app-B"/>);finish!(json({status:'LEXICAL',results:[{snippet:'A private excerpt'}],warnings:[]}));await new Promise(r=>setTimeout(r,0));expect(screen.queryByText('A private excerpt')).toBeNull()});
 it('dirty source cannot register',async()=>{let registered=false;vi.stubGlobal('fetch',vi.fn(async(input:RequestInfo|URL,init?:RequestInit)=>{const u=String(input);if(u.includes('private/upload'))return json({file_id:'dirty'});if(u==='/v1/graphql')return json({data:{core_files_by_pk:{scan_status:'INFECTED'}}});if(init?.method==='POST'&&u.endsWith('/documents'))registered=true;return json({documents:[]})}));const client=new KnowledgeClient({token:'token',role:'role_library_user',appID:'library',signal:new AbortController().signal});await expect(client.waitClean('dirty')).rejects.toThrow();expect(registered).toBe(false)});
});

it('keeps all 21 documents manageable across pagination, refresh and context reset',async()=>{
 const docs=Array.from({length:21},(_,i)=>({document_id:'d'+i,title:'Document '+i,revision:1,warnings:[],active_version_status:'READY'}));
 let secondPageCalls=0;
 vi.stubGlobal('fetch',vi.fn(async(input:RequestInfo|URL)=>{const next=String(input).includes('cursor=page2');if(next)secondPageCalls++;return json(next?{documents:docs.slice(20)}:{documents:docs.slice(0,20),next_cursor:'page2'})}));
 const view=render(<PersonalLibrary {...props}/>);
 await screen.findByText('Document 0');
 expect(screen.queryByText('Document 20')).toBeNull();
 fireEvent.click(screen.getByRole('button',{name:'Load more documents'}));
 await screen.findByText('Document 20');
 expect(screen.getByRole('button',{name:'Delete Document 20'})).toBeTruthy();
 await waitFor(()=>expect(secondPageCalls).toBeGreaterThan(1),{timeout:4000});
 expect(screen.getByText('Document 20')).toBeTruthy();
 view.rerender(<PersonalLibrary {...props} userID="B" token="token-B" contextKey="app-B"/>);
 expect(screen.queryByText('Document 20')).toBeNull();
});
