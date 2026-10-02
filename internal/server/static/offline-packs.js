(function(root){'use strict';
const core=root.CWPOfflineCore,db=root.CWPOfflineStorage;
const identityKeys=['kind','source_type','source_id','snapshot_id','audio_sha','sha256','size'];
function sameIdentity(a,b){return identityKeys.every(key=>a?.[key]===b?.[key]);}
async function readable(file,grant){
 if(!core.validGrant(grant))throw new Error('offline_expired');
 const row=await db.get('files',grant.namespace+':'+file.id);
 if(!row||row.namespace!==grant.namespace||row.epoch!==grant.epoch||row.state!=='ready'||!sameIdentity(row.file,file)||!row.blob||row.blob.size!==file.size||await core.sha(await row.blob.arrayBuffer())!==file.sha256){
  if(row&&(!row.blob||row.blob.size!==file.size))await db.remove('files',row.id);
  throw new Error('material_unavailable');
 }
 return row;
}
async function boundedBlob(response,max){
 if(Number(response.headers?.get('Content-Length'))>max)throw new Error('size_mismatch');
 if(!response.body?.getReader)return response.blob();
 const reader=response.body.getReader(),chunks=[];let total=0;
 try{for(;;){const next=await reader.read();if(next.done)break;total+=next.value.byteLength;if(total>max){await reader.cancel();throw new Error('size_mismatch');}chunks.push(next.value);}return new Blob(chunks,{type:response.headers.get('Content-Type')||'application/octet-stream'});}finally{reader.releaseLock();}
}
async function download(manifest,{signal,onProgress=()=>{}}={}){
 if(signal?.aborted)throw new DOMException('Cancelled','AbortError');
 const total=core.validateManifest(manifest),old=await db.get('meta','grant');
 if(old&&old.namespace!==manifest.namespace)await db.clearNamespace(old.namespace);
 const existing=await db.all('files'),incoming=new Set(manifest.files.map(f=>manifest.namespace+':'+f.id)),retained=existing.filter(f=>f.namespace===manifest.namespace&&!incoming.has(f.id));
 if(new Set([...retained.filter(f=>f.file?.source_id).map(f=>f.file.source_type+':'+f.file.source_id),...manifest.files.filter(f=>f.source_id).map(f=>f.source_type+':'+f.source_id)]).size>core.MAX_SOURCES)throw new Error('source_limit');
 if(retained.reduce((n,f)=>n+(f.blob?.size||0),0)+total>core.MAX_BYTES)throw new Error('pack_limit');
 // A file ID is immutable even if a new manifest arrives. Reuse complete matching
 // copies; require a new ID for a changed snapshot/byte identity.
 const reusable=new Map();
 for(const file of manifest.files){const previous=existing.find(row=>row.id===manifest.namespace+':'+file.id);if(previous?.state==='ready'){if(!sameIdentity(previous.file,file))throw new Error('file_identity_conflict');try{reusable.set(file.id,await readable(file,manifest));}catch(error){if(error.message!=='material_unavailable')throw error;}}}
 const newBytes=manifest.files.filter(f=>!reusable.has(f.id)).reduce((n,f)=>n+f.size,0);
 if(navigator.storage?.estimate){const estimate=await navigator.storage.estimate();if(estimate.quota&&((estimate.usage||0)+newBytes)>estimate.quota*.5)throw new Error('quota_limit');}
 await db.put('meta',{...manifest,files:old?.namespace===manifest.namespace&&old.epoch===manifest.epoch?(old.files||[]):[],excerpts:old?.namespace===manifest.namespace&&old.epoch===manifest.epoch?(old.excerpts||[]):[],id:'grant',last_seen:Date.now()});
 const packId=manifest.namespace+':'+manifest.pack_id,staged=[],previousPack=await db.get('packs',packId);
 if(previousPack?.state!=='ready')await db.put('packs',{id:packId,namespace:manifest.namespace,state:'downloading',manifest});
 try{
  for(const file of manifest.files){
   if(signal?.aborted)throw new DOMException('Cancelled','AbortError');
   if(reusable.has(file.id)){staged.push({...reusable.get(file.id),file});onProgress(file);continue;}
   const id=manifest.namespace+':staging:'+manifest.pack_id+':'+file.id;
   await db.put('files',{id,namespace:manifest.namespace,epoch:manifest.epoch,state:'downloading',file});
   const response=await fetch(file.url,{credentials:'same-origin',cache:'no-store',signal});
   if(!response.ok)throw new Error(response.status===401?'unauthorized':'download_failed');
   const blob=await boundedBlob(response,file.size);
   if(blob.size!==file.size||await core.sha(await blob.arrayBuffer())!==file.sha256)throw new Error('hash_mismatch');
   if(!core.validGrant(manifest))throw new Error('offline_expired');
   const current=await db.get('meta','grant');
   if(current?.namespace!==manifest.namespace||current.epoch!==manifest.epoch)throw new Error('scope_changed');
   const row={id,namespace:manifest.namespace,epoch:manifest.epoch,state:'staged',blob,file};
   await db.put('files',row);staged.push(row);onProgress(file);
  }
  if(signal?.aborted)throw new DOMException('Cancelled','AbortError');
  const finalGrant=await db.get('meta','grant');if(!core.validGrant(finalGrant,Date.now(),finalGrant?.last_seen||0)||!core.validGrant(manifest,Date.now(),finalGrant?.last_seen||0))throw new Error('offline_expired');
  await db.publishPack(manifest,staged);
 }catch(error){
  for(const file of manifest.files)await db.remove('files',manifest.namespace+':staging:'+manifest.pack_id+':'+file.id);
  if(previousPack?.state!=='ready')await db.put('packs',{id:packId,namespace:manifest.namespace,state:error.name==='AbortError'?'cancelled':'failed',error:error.message,manifest});
  throw error;
 }
}
async function rebuild(namespace,authorization){
 const packs=(await db.all('packs')).filter(p=>p.namespace===namespace&&p.state==='ready'),files=new Map(),excerpts=new Map();
 for(const pack of packs){for(const file of pack.manifest.files)files.set(file.id,file);for(const excerpt of pack.manifest.excerpts||[])excerpts.set(excerpt.excerpt_id,excerpt);}
 const grant=authorization||await db.get('meta','grant');
 if(grant?.namespace===namespace)await db.put('meta',{...grant,id:'grant',files:[...files.values()],excerpts:[...excerpts.values()]});
}
async function removePack(pack){
 for(const row of await db.all('files'))if(row.id.startsWith(pack.namespace+':staging:'+pack.manifest.pack_id+':'))await db.remove('files',row.id);
 const others=(await db.all('packs')).filter(p=>p.id!==pack.id&&p.namespace===pack.namespace&&p.state==='ready'),shared=new Set(others.flatMap(p=>p.manifest.files.map(f=>f.id)));
 for(const file of pack.manifest.files)if(!shared.has(file.id))await db.remove('files',pack.namespace+':'+file.id);
 await db.remove('packs',pack.id);await rebuild(pack.namespace);
}
async function cancelIncomplete(namespace){for(const pack of await db.all('packs'))if(pack.namespace===namespace&&pack.state!=='ready')await removePack(pack);for(const row of await db.all('files'))if(row.namespace===namespace&&row.id.startsWith(namespace+':staging:'))await db.remove('files',row.id);}
root.CWPOfflinePacks={download,readable,removePack,cancelIncomplete};
})(globalThis);
