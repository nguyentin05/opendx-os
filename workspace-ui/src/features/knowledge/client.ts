// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
import { bearer } from '../../auth';
export interface Registration { document_id:string;version_id:string;job_id:string;revision:number;status:string }
export interface DocumentSummary {document_id:string;title:string;revision:number;desired_version_status?:string;active_version_status?:string;warnings:string[]}
export interface DocumentPage {documents:DocumentSummary[];next_cursor?:string}
export interface SearchHit {document_id:string;version_id:string;chunk_id:string;title:string;snippet:string;section_path:string[];element_ids:string[];positions:Array<{page_no?:number;provenance:string}>;warnings:string[];url:string}
export interface SearchResult {status:string;results:SearchHit[];warnings:string[]}
export interface ClientContext {token:string;role:string;appID:string;signal:AbortSignal}
export class KnowledgeRequestError extends Error {
 constructor(readonly status: number, message: string) {
  super(message);
  this.name = 'KnowledgeRequestError';
 }
}

export class KnowledgeClient {
 private keys=new Map<string,string>();
 constructor(readonly context:ClientContext){}
 private key(action:string){let key=this.keys.get(action);if(!key){key=crypto.randomUUID();this.keys.set(action,key)}return key}
 private async request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const { signal, token, role } = this.context;
  signal.throwIfAborted();
  const response = await fetch(path, {
   ...init, signal, headers: { Authorization: bearer(token), 'X-Hasura-Role': role, ...init.headers },
  });
  signal.throwIfAborted();
  if (!response.ok) {
   const messages: Record<number, string> = {
    401: 'Phiên đăng nhập đã hết hạn. Hãy đăng nhập lại.', 403: 'Vai trò thư viện của bạn không khả dụng. Hãy chọn vai trò khác hoặc liên hệ quản trị viên.',
    404: 'Quyền xem tài liệu đã thay đổi hoặc không khả dụng.', 409: 'Tài liệu vừa thay đổi. Hãy làm mới rồi thử lại.',
    415: 'Định dạng hoặc quy trình xử lý tài liệu này chưa được hỗ trợ.', 422: 'Tài liệu chưa sạch hoặc chưa xử lý được.',
    503: 'Thư viện cá nhân tạm thời không khả dụng. Hãy thử lại sau ít phút.',
   };
   throw new KnowledgeRequestError(response.status, messages[response.status] ?? 'Yêu cầu tài liệu thất bại.');
  }
  const data = response.status === 204 ? undefined : await response.json();
  signal.throwIfAborted();
  return data as T;
 }
 async uploadPrivate(file:File):Promise<{file_id:string}>{if(!/\.(md|pdf)$/i.test(file.name)||file.size>20*1024*1024)throw new Error('Chọn tệp PDF văn bản hoặc Markdown tối đa 20 MiB.');const form=new FormData();form.append('app_id',this.context.appID);form.append('file',file);return this.request('/api/cap/process/files/private/upload',{method:'POST',body:form})}
 async waitClean(fileID:string):Promise<void>{const deadline=Date.now()+60000;while(Date.now()<deadline){const reply=await this.request<{data?:{core_files_by_pk?:{scan_status:string}|null};errors?:unknown[]}>('/v1/graphql',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({query:'query PrivateScan($id:uuid!){core_files_by_pk(id:$id){scan_status}}',variables:{id:fileID}})});if(reply.errors?.length||!reply.data?.core_files_by_pk)throw new Error('Không lấy được trạng thái quét tài liệu.');const status=reply.data.core_files_by_pk.scan_status;if(status==='CLEAN')return;if(status!=='QUARANTINED')throw new Error('Tài liệu không vượt qua bước quét an toàn.');await new Promise<void>((resolve,reject)=>{const abort=()=>{clearTimeout(timer);reject(new DOMException('Context changed','AbortError'))};const timer=setTimeout(()=>{this.context.signal.removeEventListener('abort',abort);resolve()},500);this.context.signal.addEventListener('abort',abort,{once:true});if(this.context.signal.aborted)abort()})};throw new Error('Tài liệu vẫn đang được quét. Hãy tải lại sau.')}
 register(fileID:string):Promise<Registration>{return this.request('/api/cap/ai/documents',{method:'POST',headers:{'Content-Type':'application/json','Idempotency-Key':this.key('register:'+fileID)},body:JSON.stringify({file_id:fileID,parser_profile:'standard-text-v1'})})}
 list(cursor=''):Promise<DocumentPage>{return this.request('/api/cap/ai/documents?scope=PERSONAL'+(cursor?'&cursor='+encodeURIComponent(cursor):''))}
 search(query:string):Promise<SearchResult>{return this.request('/api/cap/ai/search',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({scope:{kind:'PERSONAL'},query,limit:10,retrieval:'LEXICAL'})})}
 remove(doc:DocumentSummary):Promise<void>{return this.request('/api/cap/ai/documents/'+encodeURIComponent(doc.document_id),{method:'DELETE',headers:{'Idempotency-Key':this.key('delete:'+doc.document_id+':'+doc.revision),'If-Match':`"${doc.revision}"`}})}
 citation(hit:SearchHit):Promise<SearchHit>{return this.request('/api/cap/ai/documents/'+encodeURIComponent(hit.document_id)+'/citations/'+encodeURIComponent(hit.chunk_id)+'?version_id='+encodeURIComponent(hit.version_id))}
 async original(doc:DocumentSummary):Promise<Blob>{const response=await fetch('/api/cap/ai/documents/'+encodeURIComponent(doc.document_id)+'/original',{signal:this.context.signal,headers:{Authorization:bearer(this.context.token),'X-Hasura-Role':this.context.role}});if(!response.ok)throw new Error('Không tải được tệp gốc.');const blob=await response.blob();if(this.context.signal.aborted)throw new DOMException('Context changed','AbortError');return blob}
 appSources():Promise<{sources:AppSource[]}>{return this.request('/api/cap/ai/app-knowledge?app_id='+encodeURIComponent(this.context.appID))}
 searchApp(query:string):Promise<SearchResult>{return this.request('/api/cap/ai/search',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({scope:{kind:'APP',app_id:this.context.appID},query,limit:10,retrieval:'LEXICAL'})})}
 answerApp(query:string):Promise<AnswerResponse>{return this.request('/api/cap/ai/answers',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({scope:{kind:'APP',app_id:this.context.appID},query,max_citations:8})})}
 management(documentID:string,cursor=''):Promise<GrantPage>{return this.request('/api/cap/ai/documents/'+encodeURIComponent(documentID)+'/grant-management'+(cursor?'?cursor='+encodeURIComponent(cursor):''))}
 changeGrant(documentID:string,userID:string,revision:number,revoke=false):Promise<void>{return this.request('/api/cap/ai/documents/'+encodeURIComponent(documentID)+'/grants'+(revoke?'/'+encodeURIComponent(userID):''),{method:revoke?'DELETE':'POST',headers:{'Content-Type':'application/json','If-Match':`"${revision}"`,'Idempotency-Key':this.key([revoke?'revoke':'grant',documentID,userID,revision].join(':'))},...(revoke?{}:{body:JSON.stringify({user_id:userID})})})}

}

export interface AppSource {source_id:string;document_id?:string;title?:string;status:'PENDING'|'INDEXING'|'READY'|'FAILED';readable:boolean;manageable:boolean;active_version_available:boolean}
export interface GrantPage {document_id:string;revision:number;authorization_generation:number;ceiling:{mode:string;allowedRoles:string[];grantsManagedBy:string};pending_publication:boolean;grants:Array<{user_id:string;status:string}>;next_cursor?:string}

export interface AnswerResponse {status:"ANSWERED"|"SEARCH_ONLY"|"NO_EVIDENCE";answer:string|null;results:SearchHit[];citations:SearchHit[];reason?:string;model_profile?:{id:string;revision:number};usage?:{input_tokens:number;output_tokens:number;units:number;pricing_revision:string}}
