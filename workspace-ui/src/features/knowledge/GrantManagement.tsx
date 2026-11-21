// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
import { useEffect, useState } from 'react';
import { GrantPage, KnowledgeClient } from './client';

export function GrantManagement({client,documentID,sourceID,onClose}:{client:KnowledgeClient;documentID:string;sourceID:string;onClose:()=>void}) {
 const [page,setPage]=useState<GrantPage|null>(null);
 const [userID,setUserID]=useState('');
 const [message,setMessage]=useState('');
 const [busy,setBusy]=useState(false);
 useEffect(()=>{
  let current=true;
  setPage(null);setMessage('');setUserID('');
  void client.management(documentID).then(value=>{if(current)setPage(value)}).catch(()=>{if(current&&!client.context.signal.aborted)setMessage('Quản lý quyền đọc tạm thời không khả dụng. Hãy thử lại sau ít phút.')});
  return()=>{current=false};
 },[client,documentID]);
 const refresh=async()=>{try{const value=await client.management(documentID);if(!client.context.signal.aborted){setPage(value);setMessage('')}}catch{if(!client.context.signal.aborted)setMessage('Quyền quản lý hiện tại không khả dụng. Hãy làm mới quyền.')}};
 const mutate=async(target:string,revoke:boolean)=>{
  if(!page||busy||page.pending_publication)return;
  setBusy(true);setMessage('');
  try{await client.changeGrant(documentID,target,page.revision,revoke);await refresh()}
  catch(error){if(!client.context.signal.aborted)setMessage(error instanceof Error?error.message:'Không thay đổi được quyền đọc. Hãy thử lại.')}
  finally{if(!client.context.signal.aborted)setBusy(false)}
 };
 return <section aria-label={'Quản lý quyền đọc '+sourceID}>
  <h3>Quyền đọc: {sourceID}</h3><p>Quyền quản lý không cho phép xem nội dung tài liệu.</p>
  <button onClick={onClose}>Đóng quản lý quyền</button><button onClick={()=>void refresh()} disabled={busy}>Làm mới quyền</button>
  {message&&<p role="status">{message}</p>}
  {page&&<><p>Vai trò được đọc: {page.ceiling.allowedRoles.join(', ')}</p>
   {page.pending_publication&&<p>Bản công bố đang cập nhật. Hãy làm mới trước khi thay đổi quyền.</p>}
   <label>Mã người dùng <input value={userID} onChange={event=>setUserID(event.target.value)}/></label>
   <button disabled={busy||page.pending_publication||!userID.trim()} onClick={()=>void mutate(userID.trim(),false)}>Cấp quyền đọc</button>
   <ul>{page.grants.map(grant=><li key={grant.user_id}>{grant.user_id}: {grant.status==='ACTIVE'?'Đang hiệu lực':grant.status} {grant.status==='ACTIVE'&&<button disabled={busy||page.pending_publication} onClick={()=>void mutate(grant.user_id,true)}>Thu hồi quyền đọc {grant.user_id}</button>}</li>)}</ul>
   {page.next_cursor&&<button onClick={()=>void client.management(documentID,page.next_cursor).then(next=>setPage({...next,grants:[...page.grants,...next.grants]})).catch(()=>setMessage('Trang quyền đọc tạm thời không khả dụng.'))}>Tải thêm quyền đọc</button>}
  </>}
 </section>
}
