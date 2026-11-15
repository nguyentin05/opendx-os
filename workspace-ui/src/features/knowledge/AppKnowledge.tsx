// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
import { useEffect, useMemo, useState } from 'react';
import { AppSource, KnowledgeClient, SearchHit } from './client';
import { GrantManagement } from './GrantManagement';

export function AppKnowledge(props:{userID:string;token:string;appID:string;role:string;contextKey:string}) {
 const controller=useMemo(()=>new AbortController(),[props.userID,props.token,props.appID,props.role,props.contextKey]);
 const client=useMemo(()=>new KnowledgeClient({token:props.token,appID:props.appID,role:props.role,signal:controller.signal}),[controller,props.token,props.appID,props.role]);
 const [sources,setSources]=useState<AppSource[]>([]);
 const [query,setQuery]=useState('');
 const [hits,setHits]=useState<SearchHit[]>([]);
 const [citation,setCitation]=useState<SearchHit|null>(null);
 const [manage,setManage]=useState<AppSource|null>(null);
 const [message,setMessage]=useState('');
 const [busy,setBusy]=useState(false);
 useEffect(()=>{
  setSources([]);setHits([]);setCitation(null);setManage(null);setQuery('');setMessage('');setBusy(false);
  const load=()=>void client.appSources().then(page=>{if(!controller.signal.aborted)setSources(page.sources)}).catch(()=>{if(!controller.signal.aborted)setMessage('App knowledge is unavailable.')});
  load();const timer=setInterval(load,2000);
  return()=>{controller.abort();clearInterval(timer)};
 },[client,controller]);
 const search=async()=>{setBusy(true);setHits([]);setCitation(null);setMessage('');try{const result=await client.searchApp(query);if(!controller.signal.aborted){setHits(result.results);if(!result.results.length)setMessage('No accessible evidence found.')}}catch(error){if(!controller.signal.aborted)setMessage(error instanceof Error?error.message:'Search unavailable.')}finally{if(!controller.signal.aborted)setBusy(false)}};
 const showCitation=async(hit:SearchHit)=>{setCitation(null);try{const result=await client.citation(hit);if(!controller.signal.aborted)setCitation(result)}catch{if(!controller.signal.aborted)setMessage('Citation access changed or is unavailable.')}};
 return <section aria-label="App knowledge">
  <h2>App knowledge</h2>
  {message&&<p role="status">{message}</p>}
  <ul>{sources.map(source=><li key={source.source_id}>
   <strong>{source.source_id}</strong>: <span>{source.status==='READY'?'Ready':source.status==='FAILED'?'Indexing failed':'Indexing'}</span>
   {source.active_version_available&&source.status!=='READY'&&<p>Showing the previous approved version while the update is indexing.</p>}
   {source.manageable&&source.document_id&&<button onClick={()=>setManage(source)}>Manage grants {source.source_id}</button>}
  </li>)}</ul>
  <label>Search app documents <input value={query} onChange={event=>setQuery(event.target.value)}/></label>
  <button disabled={busy||!query.trim()} onClick={()=>void search()}>Search app</button>
  <ul>{hits.map(hit=><li key={hit.chunk_id}><h3>{hit.title}</h3><p>{hit.snippet}</p><button onClick={()=>void showCitation(hit)}>View citation</button></li>)}</ul>
  {citation&&<div role="dialog" aria-label="Protected citation"><h3>{citation.title}</h3><p>{citation.snippet}</p><p>{citation.section_path.join(' / ')}</p>{citation.positions.map((position,index)=><p key={index}>{position.page_no?'Page '+position.page_no:''} {position.provenance}</p>)}<button onClick={()=>setCitation(null)}>Close citation</button></div>}
  {manage?.document_id&&<GrantManagement key={manage.document_id} client={client} documentID={manage.document_id} sourceID={manage.source_id} onClose={()=>setManage(null)}/>}
 </section>
}
