// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
import {useEffect,useRef,useState} from 'react';
import {KnowledgeClient,DocumentSummary,SearchHit} from './client';
interface Props {userID:string;token:string;appID:string;role:string;contextKey:string}
interface View {key:string;docs:DocumentSummary[];nextCursor?:string;hits:SearchHit[];query:string;message:string;citation?:SearchHit;busy:boolean}
interface Active {key:string;client:KnowledgeClient;abort:AbortController;pages:number;listSequence:number}
const empty=(key:string):View=>({key,docs:[],hits:[],query:'',message:'Đang tải tài liệu…',busy:false});
const documentStatus:Record<string,string>={QUEUED:'Đang chờ',PROCESSING:'Đang xử lý',READY:'Sẵn sàng',INDEXED:'Sẵn sàng',FAILED:'Thất bại'};
export function PersonalLibrary(props:Props) {
 const key=JSON.stringify([props.userID,props.token,props.appID,props.role,props.contextKey]);
 const current=useRef(key);current.current=key;
 const active=useRef<Active>();
 const [state,setState]=useState<View>(()=>empty(key));
 const view=state.key===key?state:empty(key);
 const update=(fn:(v:View)=>View)=>{if(current.current===key)setState(old=>fn(old.key===key?old:empty(key)))};
 // Refresh exactly the number of pages the user has requested, preserving their
 // loaded range. Each server request remains bounded to one page. A later list
 // request wins over a slower poll or upload refresh in the same context.
 async function refresh(context:Active,more=false) {
  const sequence=++context.listSequence;
  const desiredPages=context.pages+(more?1:0);
  let cursor='',count=0;
  const docs=new Map<string,DocumentSummary>();
  do {
   const page=await context.client.list(cursor);
   for(const doc of page.documents)docs.set(doc.document_id,doc);
   cursor=page.next_cursor??'';
   count++;
  } while(cursor&&count<desiredPages&&!context.abort.signal.aborted);
  if(current.current===context.key&&!context.abort.signal.aborted&&sequence===context.listSequence) {
   context.pages=count;
   setState(old=>({...old,docs:[...docs.values()],nextCursor:cursor||undefined,message:old.message==='Đang tải tài liệu…'?'':old.message}));
  }
 }
 useEffect(()=>{
  const abort=new AbortController();
  const context:Active={key,client:new KnowledgeClient({token:props.token,role:props.role,appID:props.appID,signal:abort.signal}),abort,pages:1,listSequence:0};
  active.current=context;
  setState(empty(key));
  const poll=async()=>{try{await refresh(context)}catch(e){if(!abort.signal.aborted&&current.current===key)setState(old=>({...old,docs:[],nextCursor:undefined,hits:[],citation:undefined,message:e instanceof Error?e.message:'Thư viện tạm thời không khả dụng.'}))}};
  void poll();const timer=setInterval(()=>void poll(),2500);
  return()=>{abort.abort();clearInterval(timer)};
 },[key,props.token,props.role,props.appID]);
 const run=async(action:(context:Active)=>Promise<void>)=>{
  const context=active.current;if(context?.key!==key)return;
  update(v=>({...v,busy:true,message:''}));
  try{await action(context)}catch(e){if(!context.abort.signal.aborted)update(v=>({...v,docs:[],nextCursor:undefined,hits:[],citation:undefined,message:e instanceof Error?e.message:'Yêu cầu thất bại. Hãy thử lại.'}))}
  finally{if(!context.abort.signal.aborted)update(v=>({...v,busy:false}))}
 };
 const upload=(file:File)=>run(async context=>{
  update(v=>({...v,message:'Đang tải lên…'}));
  const f=await context.client.uploadPrivate(file);
  update(v=>({...v,message:'Đang quét…'}));
  await context.client.waitClean(f.file_id);
  const result=await context.client.register(f.file_id);
  update(v=>({...v,message:result.status}));
  await refresh(context);
 });
 return <section aria-label="Thư viện cá nhân">
  <h1>Thư viện cá nhân</h1><p>Tìm trong PDF văn bản và Markdown của bạn. Ảnh quét cần OCR cục bộ được hỗ trợ.</p>
  <label>Tải tài liệu lên <input aria-label="Tải tài liệu lên" type="file" accept=".pdf,.md" disabled={view.busy} onChange={event=>{const file=event.target.files?.[0];if(file)void upload(file);event.target.value=''}}/></label>
  <p role="status">{view.message}</p>
  <form onSubmit={event=>{event.preventDefault();void run(async context=>{const result=await context.client.search(view.query);update(v=>({...v,hits:result.results,message:result.warnings.join(', ')}))})}}>
   <label>Tìm trong tài liệu của bạn <input aria-label="Tìm trong tài liệu của bạn" maxLength={2000} value={view.query} onChange={e=>update(v=>({...v,query:e.target.value}))}/></label>
   <button disabled={view.busy||!view.query.trim()}>Tìm</button>
  </form>
  <h2>Tài liệu của bạn</h2>
  {view.docs.length===0?<p>Chưa có tài liệu nào. Hãy tải lên tài liệu đầu tiên.</p>:<ul>{view.docs.map(doc=><li key={doc.document_id}>
   <strong>{doc.title}</strong> — {documentStatus[doc.active_version_status??doc.desired_version_status??'QUEUED']??doc.active_version_status??doc.desired_version_status??'Đang chờ'}
   {doc.warnings?.map(w=><span key={w}> {w==='OCR_UNAVAILABLE'?'OCR cục bộ không khả dụng.':w}</span>)}
   <button disabled={view.busy} onClick={()=>void run(async context=>{await context.client.remove(doc);await refresh(context);update(v=>({...v,hits:[],citation:undefined}))})}>Xoá {doc.title}</button>
   <button disabled={view.busy} onClick={()=>void run(async context=>{
    const blob=await context.client.original(doc);if(current.current!==key)return;
    const url=URL.createObjectURL(blob);const a=document.createElement('a');a.href=url;a.download=doc.title;a.click();setTimeout(()=>URL.revokeObjectURL(url),1000);
   })}>Tải {doc.title}</button>
  </li>)}</ul>}
  {view.nextCursor&&<button disabled={view.busy} onClick={()=>void run(context=>refresh(context,true))}>Tải thêm tài liệu</button>}
  <h2>Kết quả tìm kiếm</h2><ul>{view.hits.map(hit=><li key={hit.chunk_id}>
   <h3>{hit.title}</h3><p>{hit.snippet}</p><small>{hit.section_path?.join(' / ')} {hit.positions?.filter(p=>p.page_no).map(p=>'trang '+p.page_no).join(', ')}</small>
   <button onClick={()=>void run(async context=>{const citation=await context.client.citation(hit);update(v=>({...v,citation}))})}>Xem trích dẫn</button>
  </li>)}</ul>
  {view.citation&&<div role="dialog" aria-label="Trích dẫn được bảo vệ"><h2>{view.citation.title}</h2><blockquote>{view.citation.snippet}</blockquote><p>{view.citation.section_path?.join(' / ')}</p><button onClick={()=>update(v=>({...v,citation:undefined}))}>Đóng trích dẫn</button></div>}
 </section>;
}
