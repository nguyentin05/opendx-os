// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
import { useEffect, useState } from 'react';
import { AnswerResponse, AppSource, KnowledgeClient, KnowledgeRequestError, SearchHit } from './client';
import { GrantManagement } from './GrantManagement';

interface Props {
 userID: string;
 token: string;
 appID: string;
 role: string;
 contextKey: string;
}

export function AppKnowledge(props: Props) {
 const key = JSON.stringify([props.userID, props.token, props.appID, props.role, props.contextKey]);
 return <ScopedAppKnowledge key={key} token={props.token} appID={props.appID} role={props.role} />;
}

function sourceAuthorityChanged(previous: AppSource[], next: AppSource[]): boolean {
 const sources = new Map(next.map(source => [source.source_id, source]));
 return previous.some(before => {
  const after = sources.get(before.source_id);
  return (before.readable || before.manageable) && (
   !after || after.document_id !== before.document_id
   || (before.readable && !after.readable) || (before.manageable && !after.manageable)
   || (before.active_version_available && !after.active_version_available)
  );
 });
}

function ScopedAppKnowledge({token, appID, role}: Pick<Props, 'token' | 'appID' | 'role'>) {
 const [client, setClient] = useState<KnowledgeClient>();
 const [answer, setAnswer] = useState<AnswerResponse | null>(null);
 const [sources, setSources] = useState<AppSource[]>([]);
 const [query, setQuery] = useState('');
 const [hits, setHits] = useState<SearchHit[]>([]);
 const [citation, setCitation] = useState<SearchHit | null>(null);
 const [manage, setManage] = useState<AppSource | null>(null);
 const [message, setMessage] = useState('');
 const [pollMessage, setPollMessage] = useState('');
 const [busy, setBusy] = useState(false);

 useEffect(() => {
  const controller = new AbortController();
  const context = { token, appID, role };
  const pollClient = new KnowledgeClient({ ...context, signal: controller.signal });
  let interactions: AbortController | undefined;
  let verifiedSources: AppSource[] = [];
  let timer: ReturnType<typeof setTimeout>;

  function clearInteractions() {
   interactions?.abort();
   interactions = undefined;
   setClient(undefined);
   setAnswer(null);
   setHits([]);
   setCitation(null);
   setManage(null);
   setBusy(false);
   setMessage('');
  }

  async function load() {
   try {
    const page = await pollClient.appSources();
    if (controller.signal.aborted) return;
    // An answer can combine several sources. Discard the old app interaction
    // when any source loses authority and use a fresh client for new requests.
    if (sourceAuthorityChanged(verifiedSources, page.sources)) clearInteractions();
    verifiedSources = page.sources;
    setSources(verifiedSources);
    setPollMessage('');
    if (!interactions) {
     interactions = new AbortController();
     setClient(new KnowledgeClient({ ...context, signal: interactions.signal }));
    }
   } catch (error) {
    if (controller.signal.aborted) return;
    if (error instanceof KnowledgeRequestError && [401, 403, 404, 409].includes(error.status)) {
     clearInteractions();
     verifiedSources = [];
     setSources([]);
     setPollMessage('App knowledge access could not be verified.');
    } else {
     setPollMessage('App knowledge is temporarily unavailable.');
    }
   } finally {
    if (!controller.signal.aborted) timer = setTimeout(() => void load(), 2000);
   }
  }

  void load();
  return () => {
   controller.abort();
   interactions?.abort();
   clearTimeout(timer);
  };
 }, [token, appID, role]);

 async function search() {
  if (!client || client.context.signal.aborted) return;
  const { signal } = client.context;
  setAnswer(null);
  setBusy(true);
  setHits([]);
  setCitation(null);
  setMessage('');
  try {
   const result = await client.searchApp(query);
   if (!signal.aborted) {
    setHits(result.results);
    if (!result.results.length) setMessage('No accessible evidence found.');
   }
  } catch (error) {
   if (!signal.aborted) setMessage(error instanceof Error ? error.message : 'Search unavailable.');
  } finally {
   if (!signal.aborted) setBusy(false);
  }
 }

 async function ask() {
  if (!client || client.context.signal.aborted) return;
  const { signal } = client.context;
  setBusy(true);
  setAnswer(null);
  setHits([]);
  setCitation(null);
  setMessage('');
  try {
   const reply = await client.answerApp(query);
   if (!signal.aborted) {
    setAnswer(reply);
    setHits(reply.results);
   }
  } catch (error) {
   if (!signal.aborted) setMessage(error instanceof Error ? error.message : 'Answer unavailable.');
  } finally {
   if (!signal.aborted) setBusy(false);
  }
 }

 async function showCitation(hit: SearchHit) {
  if (!client || client.context.signal.aborted) return;
  const { signal } = client.context;
  setCitation(null);
  try {
   const result = await client.citation(hit);
   if (!signal.aborted) setCitation(result);
  } catch {
   if (!signal.aborted) setMessage('Citation access changed or is unavailable.');
  }
 }

 return <section aria-label="App knowledge">
  <h2>App knowledge</h2>
  {pollMessage&&<p role="status">{pollMessage}</p>}
  {message&&<p role="status">{message}</p>}
  <ul>{sources.map(source=><li key={source.source_id}>
   <strong>{source.source_id}</strong>: <span>{source.status==='READY'?'Ready':source.status==='FAILED'?'Indexing failed':'Indexing'}</span>
   {source.active_version_available&&source.status!=='READY'&&<p>Showing the previous approved version while the update is indexing.</p>}
   {source.manageable&&source.document_id&&<button onClick={()=>setManage(source)}>Manage grants {source.source_id}</button>}
  </li>)}</ul>
  <label>Search app documents <input value={query} onChange={event=>setQuery(event.target.value)}/></label>
  <button disabled={!client||busy||!query.trim()} onClick={()=>void search()}>Search app</button>
  <button disabled={!client||busy||!query.trim()} onClick={()=>void ask()}>Ask app</button>
  {answer&&<div aria-label="App answer">
   {answer.status==='ANSWERED'&&answer.answer&&<p>{answer.answer}</p>}
   {answer.status==='SEARCH_ONLY'&&<p>Search only: {answer.reason==='AUTHORIZATION_CHANGED'?'access changed; ask again.':answer.reason==='UNSUPPORTED_ANSWER'?'the answer could not be supported by the evidence.':'generation is unavailable for this request.'}</p>}
   {answer.status==='NO_EVIDENCE'&&<p>No accessible evidence found.</p>}
   {answer.citations.map((hit,index)=><button key={hit.chunk_id} onClick={()=>void showCitation(hit)}>View answer citation {index+1}</button>)}
  </div>}
  <ul>{hits.map(hit=><li key={hit.chunk_id}><h3>{hit.title}</h3><p>{hit.snippet}</p><button onClick={()=>void showCitation(hit)}>View citation</button></li>)}</ul>
  {citation&&<div role="dialog" aria-label="Protected citation"><h3>{citation.title}</h3><p>{citation.snippet}</p><p>{citation.section_path.join(' / ')}</p>{citation.positions.map((position,index)=><p key={index}>{position.page_no?'Page '+position.page_no:''} {position.provenance}</p>)}<button onClick={()=>setCitation(null)}>Close citation</button></div>}
  {client&&manage?.document_id&&<GrantManagement key={manage.document_id} client={client} documentID={manage.document_id} sourceID={manage.source_id} onClose={()=>setManage(null)}/>}
 </section>
}
