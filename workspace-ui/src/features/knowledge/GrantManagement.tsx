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
  void client.management(documentID).then(value=>{if(current)setPage(value)}).catch(()=>{if(current&&!client.context.signal.aborted)setMessage('Grant management is unavailable.')});
  return()=>{current=false};
 },[client,documentID]);
 const refresh=async()=>{try{const value=await client.management(documentID);if(!client.context.signal.aborted){setPage(value);setMessage('')}}catch{if(!client.context.signal.aborted)setMessage('Current grant permissions are unavailable.')}};
 const mutate=async(target:string,revoke:boolean)=>{
  if(!page||busy||page.pending_publication)return;
  setBusy(true);setMessage('');
  try{await client.changeGrant(documentID,target,page.revision,revoke);await refresh()}
  catch(error){if(!client.context.signal.aborted)setMessage(error instanceof Error?error.message:'Grant change failed.')}
  finally{if(!client.context.signal.aborted)setBusy(false)}
 };
 return <section aria-label={'Grant management '+sourceID}>
  <h3>Read grants: {sourceID}</h3><p>Management permission does not grant access to document content.</p>
  <button onClick={onClose}>Close grants</button><button onClick={()=>void refresh()} disabled={busy}>Refresh grants</button>
  {message&&<p role="status">{message}</p>}
  {page&&<><p>Allowed read roles: {page.ceiling.allowedRoles.join(', ')}</p>
   {page.pending_publication&&<p>Publication is updating. Refresh before changing grants.</p>}
   <label>User ID <input value={userID} onChange={event=>setUserID(event.target.value)}/></label>
   <button disabled={busy||page.pending_publication||!userID.trim()} onClick={()=>void mutate(userID.trim(),false)}>Grant read</button>
   <ul>{page.grants.map(grant=><li key={grant.user_id}>{grant.user_id}: {grant.status} {grant.status==='ACTIVE'&&<button disabled={busy||page.pending_publication} onClick={()=>void mutate(grant.user_id,true)}>Revoke read {grant.user_id}</button>}</li>)}</ul>
   {page.next_cursor&&<button onClick={()=>void client.management(documentID,page.next_cursor).then(next=>setPage({...next,grants:[...page.grants,...next.grants]})).catch(()=>setMessage('Grant page is unavailable.'))}>Load more grants</button>}
  </>}
 </section>
}
