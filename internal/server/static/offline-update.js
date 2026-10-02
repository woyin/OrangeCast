(function(root){'use strict';
 // Ordinary online pages participate in a waiting worker's fresh idle vote.
 // This bridge neither registers a worker nor caches any authenticated page.
 if(!root.navigator?.serviceWorker)return;
 root.navigator.serviceWorker.addEventListener('message',event=>{
  if(event.data?.type==='OFFLINE_REVOKED'){for(const media of document.querySelectorAll('audio,video')){media.pause();media.removeAttribute('src');media.load();}return;}
  if(event.data?.type!=='OFFLINE_UPDATE_CHECK')return;
  const recordingPanel=document.getElementById('voice-panel');
  const safe=Array.from(document.querySelectorAll('audio,video')).every(media=>media.paused)
   && !(root.CWPVoice?.busy?.()) && !(root.CWPOfflineOperations?.busy?.())
   && !(recordingPanel&&!recordingPanel.hidden&&!root.CWPVoice?.busy);
  event.source?.postMessage({type:'OFFLINE_UPDATE_VOTE',proposal:event.data.proposal,safe});
 });
})(globalThis);
(function(root){'use strict';
 const db=root.CWPOfflineStorage,core=root.CWPOfflineCore;if(!db||!core||!root.CWPListening)return;
 let loading=false;
 async function checkedFetch(url){try{return await fetch(url,{credentials:'same-origin',cache:'no-store'});}catch(error){if(error instanceof TypeError)error.cwpNetworkFailure=true;throw error;}}
 async function authorizedGrant(){const grant=await db.get('meta','grant');if(!core.validGrant(grant,Date.now(),grant?.last_seen||0)){root.CWPListening.stop();throw Error('离线授权已到期，请到离线页面核对。');}try{const sessionResponse=await checkedFetch('/api/offline/session');if(!sessionResponse.ok){root.CWPListening.stop();if([401,403,409,410].includes(sessionResponse.status))await db.clearNamespace(grant.namespace,{keepOutbox:true});throw Error('请登录后核对本设备授权。');}const session=await sessionResponse.json();if(session.session_namespace!==grant.owner_session_namespace){root.CWPListening.stop();await db.clearNamespace(grant.namespace);throw Error('新登录会话不能继承旧下载副本。');}const response=await checkedFetch('/api/offline/status?device_id='+encodeURIComponent(grant.device_id));if(!response.ok){await db.clearNamespace(grant.namespace,{keepOutbox:true});root.CWPListening.stop();throw Error('来源或授权已变化；旧草稿已锁定，请到离线页面复制。');}const status=await response.json(),auth=status.authorization;if(!auth||auth.namespace!==grant.namespace||auth.epoch!==grant.epoch){root.CWPListening.stop();await db.clearNamespace(grant.namespace,{keepOutbox:true});throw Error('离线授权已变化。');}}catch(error){if(error.cwpNetworkFailure!==true)throw error;/* Network failure permits bounded existing local grant only. */}return grant;}
 async function startDownloaded(fileId,{excerptId,paused=false}={}){const grant=await authorizedGrant(),file=grant.files?.find(f=>f.id===fileId&&f.kind==='audio');if(!file)throw Error('已下载原音不在当前授权中。');await root.CWPOfflinePacks.readable(file,grant);return root.CWPListening.startOffline({file,grant,excerpt:excerptId?{excerpt_id:excerptId}:undefined,paused});}
 root.CWPOfflineDownloads={startDownloaded,authorizedGrant};
 const section=document.createElement('details'),summary=document.createElement('summary'),content=document.createElement('div');summary.textContent='已下载原音';section.append(summary,content);const navigation=document.querySelector('nav');(navigation||document.body).append(section);
 section.addEventListener('toggle',async()=>{if(!section.open||loading)return;loading=true;content.textContent='正在核对本地下载…';try{const grant=await authorizedGrant(),audio=(grant.files||[]).filter(f=>f.kind==='audio');content.replaceChildren();if(!audio.length)content.textContent='尚无已下载原音，请到离线页面选择下载。';for(const file of audio){const button=document.createElement('button');button.type='button';button.textContent=file.title||'播放已下载原音';button.onclick=()=>startDownloaded(file.id).catch(error=>root.CWPListening.notify(error.message));content.append(button);for(const excerpt of grant.excerpts||[])if(excerpt.source_type===file.source_type&&excerpt.source_id===file.source_id&&excerpt.snapshot_id===file.snapshot_id){const part=document.createElement('button');part.type='button';part.textContent='补听 '+Math.floor(excerpt.start_seconds)+'–'+Math.ceil(excerpt.end_seconds)+'秒';part.onclick=()=>startDownloaded(file.id,{excerptId:excerpt.excerpt_id}).catch(error=>root.CWPListening.notify(error.message));content.append(part);}}}catch(error){content.textContent=error.message;}finally{loading=false;}});
})(globalThis);
