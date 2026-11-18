// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
// @vitest-environment jsdom
import {afterEach,expect,it,vi} from 'vitest';
import {cleanup,fireEvent,render,screen,waitFor} from '@testing-library/react';
import {AgentChat} from './AgentChat';
afterEach(()=>{cleanup();vi.unstubAllGlobals()});
it('continues a conversation with cited answers and safely handles denied previews',async()=>{
 const calls:{url:string;init?:RequestInit}[]=[];
 vi.stubGlobal('fetch',vi.fn(async(input:RequestInfo|URL,init?:RequestInit)=>{const url=String(input);calls.push({url,init});if(url.includes('/citations/'))return new Response('',{status:404});if(init?.method==='POST')return Response.json({status:'ANSWERED',answer:'Across apps',citations:[{title:'Policy',url:'/api/cap/ai/conversations/c1/citations/k?document_id=d&version_id=v&message_seq=2'}]});if(url.endsWith('/c1'))return Response.json({messages:[],next_after_seq:null});return Response.json({conversations:[{id:'c1',title:'Existing'}],next_cursor:null})}));
 render(<AgentChat token="token"/>);fireEvent.click(await screen.findByRole('button',{name:'Existing'}));fireEvent.change(await screen.findByLabelText('Question'),{target:{value:'What changed?'}});fireEvent.click(screen.getByRole('button',{name:'Send'}));await screen.findByText('Across apps');fireEvent.click(screen.getByRole('button',{name:'Policy'}));await screen.findByText(/citation is unavailable/i);expect(screen.queryByRole('combobox')).toBeNull();expect(calls.every(c=>new Headers(c.init?.headers).get('X-Hasura-Role')===null)).toBe(true);
});
it('does not display a delayed response after unmount',async()=>{
 let resolve!:(r:Response)=>void;vi.stubGlobal('fetch',vi.fn(()=>new Promise<Response>(r=>{resolve=r})));const view=render(<AgentChat token="token"/>);view.unmount();resolve(Response.json({conversations:[{id:'c',title:'Secret'}],next_cursor:null}));await waitFor(()=>expect(screen.queryByText('Secret')).toBeNull());
});

it('retries a pending or uncertain turn with the same idempotency key',async()=>{
 const keys:string[]=[];let tries=0;vi.stubGlobal('fetch',vi.fn(async(input:RequestInfo|URL,init?:RequestInit)=>{const url=String(input);if(init?.method==='POST'){keys.push(new Headers(init.headers).get('Idempotency-Key')!);tries++;if(tries===1)throw new TypeError('network');if(tries===2)return Response.json({status:'SEARCH_ONLY',answer:null,citations:[],reason:'TURN_PENDING'});return Response.json({status:'ANSWERED',answer:'Done',citations:[]})}if(url.endsWith('/c1'))return Response.json({messages:[],next_after_seq:null});return Response.json({conversations:[{id:'c1',title:'Existing'}],next_cursor:null})}));
 render(<AgentChat token="token"/>);fireEvent.click(await screen.findByRole('button',{name:'Existing'}));fireEvent.change(await screen.findByLabelText('Question'),{target:{value:'Question'}});fireEvent.click(screen.getByRole('button',{name:'Send'}));await screen.findByText(/UNCERTAIN/);fireEvent.click(screen.getByRole('button',{name:'Retry'}));await screen.findByText(/turn is pending/i);fireEvent.click(screen.getByRole('button',{name:'Retry'}));await screen.findByText('Done');expect(new Set(keys).size).toBe(1);
});
it('rejects an external citation without sending credentials',async()=>{
 const fetcher=vi.fn(async(input:RequestInfo|URL)=>{const url=String(input);if(url.endsWith('/c1'))return Response.json({messages:[{id:'m',seq:2,role:'ASSISTANT',content:'',status:'COMPLETE',response:{status:'ANSWERED',answer:'Old answer',citations:[{title:'External',url:'https://example.invalid/steal'}]}}],next_after_seq:null});return Response.json({conversations:[{id:'c1',title:'Existing'}],next_cursor:null})});vi.stubGlobal('fetch',fetcher);
 render(<AgentChat token="secret"/>);fireEvent.click(await screen.findByRole('button',{name:'Existing'}));fireEvent.click(await screen.findByRole('button',{name:'External'}));await screen.findByText(/citation is unavailable/i);expect(fetcher).toHaveBeenCalledTimes(2);
});
