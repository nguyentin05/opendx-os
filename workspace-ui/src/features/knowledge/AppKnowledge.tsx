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
 const [pollMessage, setPollMessage] = useState('Đang tải tri thức của ứng dụng…');
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
     setPollMessage('Không kiểm tra được quyền truy cập tri thức của ứng dụng. Hãy làm mới quyền.');
    } else {
     setPollMessage('Tri thức của ứng dụng tạm thời không khả dụng. Hãy thử lại sau ít phút.');
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
    if (!result.results.length) setMessage('Không tìm thấy nguồn nào bạn được xem.');
   }
  } catch (error) {
   if (!signal.aborted) setMessage(error instanceof Error ? error.message : 'Tìm kiếm tạm thời không khả dụng.');
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
   if (!signal.aborted) setMessage(error instanceof Error ? error.message : 'Chưa tạo được câu trả lời. Hãy thử lại.');
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
   if (!signal.aborted) setMessage('Quyền xem trích dẫn đã thay đổi hoặc không khả dụng.');
  }
 }

 return <section aria-label="Tri thức của ứng dụng">
  <h2>Tri thức của ứng dụng</h2>
  {pollMessage&&<p role="status">{pollMessage}</p>}
  {message&&<p role="status">{message}</p>}
  <ul>{sources.map(source=><li key={source.source_id}>
   <strong>{source.source_id}</strong>: <span>{source.status==='READY'?'Sẵn sàng':source.status==='FAILED'?'Đánh chỉ mục thất bại':'Đang đánh chỉ mục'}</span>
   {source.active_version_available&&source.status!=='READY'&&<p>Đang hiển thị phiên bản đã duyệt trước đó trong khi bản cập nhật được đánh chỉ mục.</p>}
   {source.manageable&&source.document_id&&<button onClick={()=>setManage(source)}>Quản lý quyền đọc {source.source_id}</button>}
  </li>)}</ul>
  <label>Tìm trong tài liệu của ứng dụng <input value={query} onChange={event=>setQuery(event.target.value)}/></label>
  <button disabled={!client||busy||!query.trim()} onClick={()=>void search()}>Tìm trong ứng dụng</button>
  <button disabled={!client||busy||!query.trim()} onClick={()=>void ask()}>Hỏi ứng dụng</button>
  {answer&&<div aria-label="Câu trả lời của ứng dụng">
   {answer.status==='ANSWERED'&&answer.answer&&<p>{answer.answer}</p>}
   {answer.status==='SEARCH_ONLY'&&<p>Chỉ tìm được nguồn: {answer.reason==='AUTHORIZATION_CHANGED'?'quyền truy cập đã thay đổi; hãy hỏi lại.':answer.reason==='UNSUPPORTED_ANSWER'?'câu trả lời chưa được nguồn chứng minh.':answer.reason==='MODEL_CALL_UNCERTAIN'?'chưa xác nhận được kết quả gọi mô hình; các nguồn đã tìm vẫn được giữ.':'tính năng tạo câu trả lời không khả dụng cho yêu cầu này.'}</p>}
   {answer.status==='NO_EVIDENCE'&&<p>Không tìm thấy nguồn nào bạn được xem.</p>}
   {answer.citations.map((hit,index)=><button key={hit.chunk_id} onClick={()=>void showCitation(hit)}>Xem trích dẫn {index+1}</button>)}
  </div>}
  <ul>{hits.map(hit=><li key={hit.chunk_id}><h3>{hit.title}</h3><p>{hit.snippet}</p><button onClick={()=>void showCitation(hit)}>Xem trích dẫn</button></li>)}</ul>
  {citation&&<div role="dialog" aria-label="Trích dẫn được bảo vệ"><h3>{citation.title}</h3><p>{citation.snippet}</p><p>{citation.section_path.join(' / ')}</p>{citation.positions.map((position,index)=><p key={index}>{position.page_no?'Trang '+position.page_no:''} {position.provenance}</p>)}<button onClick={()=>setCitation(null)}>Đóng trích dẫn</button></div>}
  {client&&manage?.document_id&&<GrantManagement key={manage.document_id} client={client} documentID={manage.document_id} sourceID={manage.source_id} onClose={()=>setManage(null)}/>}
 </section>
}
