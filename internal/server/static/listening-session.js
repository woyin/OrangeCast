// A single listening owner survives view replacement. Views only subscribe and issue explicit actions.
(function(root){
 'use strict';
 const shell=document.getElementById('listening-shell');if(!shell)return;
 const audio=document.createElement('audio'),narration=document.createElement('audio');
 audio.id='audio-player';narration.id='listening-narration';audio.preload=narration.preload='metadata';
 shell.append(audio,narration);
 const specCache=new WeakMap(),subscribers=new Set(),sessionKey='cwp-listening-session:v1';
 let active=null,transport=null,player=null,queueId='',ready=false,loop=null,resume=null,notice='',noticeAction=null,token=0,prepared=null,suppressSave=false;
 const byId=id=>document.getElementById(id);
 function fmt(n){n=Math.max(0,Math.floor(n||0));return Math.floor(n/60)+':'+String(n%60).padStart(2,'0');}
 function same(a,b){return !!a&&!!b&&a.sourceType===b.sourceType&&a.sourceId===b.sourceId&&a.mode===b.mode&&a.planId===b.planId&&a.planVersion===b.planVersion&&a.audioSHA===b.audioSHA&&a.excerptId===b.excerptId&&a.snapshot===b.snapshot&&a.audioURL===b.audioURL&&a.offline?.namespace===b.offline?.namespace&&a.offline?.epoch===b.offline?.epoch;}
 function state(){return {spec:active,audio,narration,transport,player,queueId,ready,loop,resume,notice,noticeAction,paused:audio.paused&&narration.paused,time:audio.currentTime,duration:audio.duration};}
 function remember(){if(!active)return;try{localStorage.setItem(sessionKey,JSON.stringify({sourceType:active.sourceType,sourceId:active.sourceId,mode:active.mode,planId:active.planId,planVersion:active.planVersion,audioSHA:active.audioSHA,snapshot:active.snapshot,excerptId:active.excerptId,startSeconds:active.startSeconds,endSeconds:active.endSeconds,segmentIds:active.segmentIds,offline:active.offline,queueId,loop,position:audio.currentTime,itemPosition:player?.position().item?.type==='evidence'?player.position().item.position:prepared?.item_position,highlightId:player?.position().item?.type==='evidence'?player.position().item.highlightId:prepared?.highlight_id,revision:transport?.status().revision||0}));}catch(_){}}
 function emit(){shell.hidden=!active;try{if(navigator.mediaSession)navigator.mediaSession.playbackState=active?(audio.paused&&narration.paused?'paused':'playing'):'none';}catch(_){}if(active){byId('listening-title').textContent=active.title+' · '+(active.mode==='dj'?'DJ精听':active.mode==='excerpt'?'区间补听':'原音');byId('listening-title').href=urlFor(active);byId('listening-position').textContent=' '+fmt(audio.currentTime);byId('listening-toggle').textContent=audio.paused&&narration.paused?'播放':'暂停';if(loop){byId('listening-loop-start').value=loop.start;byId('listening-loop-end').value=loop.end;}const deadline=transport?.status().deadline||0;byId('listening-sleep').value=deadline?String([15,30,60].find(n=>n>=Math.ceil((deadline-Date.now())/60000))||60):'0';}
  const feedback=byId('listening-feedback');feedback.textContent=notice;
  if(noticeAction){const button=document.createElement('button');button.type='button';button.textContent='处理续听位置';button.onclick=noticeAction;feedback.append(button);}
  subscribers.forEach(fn=>fn(state()));}
 function notify(text,action){notice=text;noticeAction=action||null;emit();}
 function urlFor(spec){return '/sources/'+encodeURIComponent(spec.sourceType)+'/'+encodeURIComponent(spec.sourceId)+(spec.mode==='dj'?'/dj?plan_id='+encodeURIComponent(spec.planId)+'&plan_version='+spec.planVersion:'');}
 function specFrom(view){if(specCache.has(view))return specCache.get(view);
  const original=view.querySelector('#source-audio'),pl=view.querySelector('#playlist');
  if(original){const d=original.dataset;const spec={title:view.querySelector('h1')?.textContent||'原音',sourceType:d.sourceType,sourceId:d.sourceId,mode:'original',planId:'',planVersion:0,audioSHA:d.audioSha||'',audioURL:original.getAttribute('src'),csrf:d.csrf,snapshot:d.snapshot||'',snapshotVersion:Number(d.version)||0,segments:Array.from(view.querySelectorAll('.transcript .seg')).map(s=>({id:s.dataset.id,start:Number(s.dataset.start),end:Number(s.dataset.end),text:s.textContent})),items:[]};specCache.set(view,spec);return spec;}
  if(pl){const d=pl.dataset,items=[];view.querySelectorAll('.dj-item').forEach((el,i)=>{const e=el.dataset,segs=JSON.parse(e.segments||'[]')||[],base={idx:e.idx||String(i),position:Number(e.position),highlightId:e.highlightId||'',segmentIds:segs};if(e.kind==='narration'){if(e.narration)items.push({...base,type:'narration',url:e.narration});}else{if(e.narration)items.push({...base,type:'narration',url:e.narration});items.push({...base,type:'evidence',start:Number(e.start),end:Number(e.end)});}});
   const spec={title:view.querySelector('h1')?.textContent||'DJ',sourceType:d.sourceType,sourceId:d.sourceId,mode:'dj',planId:d.planId||'',planVersion:Number(d.planVersionBound)||0,audioSHA:d.audioSha||'',audioURL:d.audioUrl||'',csrf:d.csrf||'',snapshot:d.snapshot||'',snapshotVersion:Number(d.snapshotVersion)||0,segments:JSON.parse(d.noteSegments||'[]')||[],items};specCache.set(view,spec);return spec;}
  return null;
 }
 function seek(value){const current=token;const apply=()=>{if(!active||current!==token)return;const item=player?.position().item;let min=0,max=Number.isFinite(audio.duration)?audio.duration:Number(value);if(active.mode==='excerpt'){min=active.startSeconds;max=Math.min(max,active.endSeconds);}if(item?.type==='evidence'){min=item.start;max=item.end===null?max:item.end;}
  audio.currentTime=Math.max(min,Math.min(max,Number(value)||0));};if(ready)apply();else audio.addEventListener('loadedmetadata',apply,{once:true});}
 function restorePosition(saved){if(player){const index=active.items.findIndex(i=>i.type==='evidence'&&i.position===saved.item_position&&i.highlightId===saved.highlight_id);if(index<0){notify('旧清单位置无法匹配，请重新选择片段。');return false;}const items=structuredClone(active.items.slice(index));items[0].start=Math.max(items[0].start,Math.min(items[0].end,saved.item_offset_seconds));prepared={item_position:saved.item_position,highlight_id:saved.highlight_id};player.load(items);}seek(saved.item_offset_seconds);return true;}
 function install(spec,options={}){
  if(!spec?.audioURL){notify('原音不可用，请先检查来源状态。');return false;}
  if(same(active,spec)){if(options.queueId)queueId=options.queueId;remember();return true;}
  const previousSleep=transport?.status().deadline||0;
  ++token;transport?.pause();transport?.destroy();player?.stop();audio.pause();narration.pause();
  audio.removeAttribute('src');narration.removeAttribute('src');audio.load();narration.load();
  active=structuredClone(spec);queueId=options.queueId||'';ready=false;loop=null;resume=null;noticeAction=null;prepared=null;suppressSave=!!options.restoring;
  notice=spec.audioSHA?'':'当前音频尚未冻结指纹，旧位置须明确确认。';player=null;
  audio.src=spec.audioURL;audio.load();
  if(spec.mode==='dj'){
   player=root.DJPlayer.create({evidence:audio,narration,onState:(status,detail)=>{if(status==='paused'&&detail?.name==='NotAllowedError')notify('浏览器需要明确播放操作，请点击播放继续当前片段。');emit();if(status==='ended')ended();},onItem:item=>{if(item?.type==='evidence')prepared={item_position:item?.position??prepared.item_position,highlight_id:item?.highlightId||prepared?.highlight_id||''};emit();}});player.load(spec.items);
  }
  const current=token;
  transport=root.CWPPlayback.create({title:spec.title,sourceType:spec.sourceType,sourceId:spec.sourceId,mode:spec.mode,csrf:spec.csrf,audioSHA:spec.audioSHA,planId:spec.planId,planVersion:spec.planVersion,localRevision:options.localRevision,sleepDeadline:previousSleep,fetch:options.progressFetch,excerptId:spec.excerptId,snapshotId:spec.snapshot,
   adapter:{play:()=>{if(spec.mode==='excerpt'&&(audio.currentTime<spec.startSeconds||audio.currentTime>=spec.endSeconds))seek(spec.startSeconds);return player?player.play():audio.play();},pause:()=>{if(player)player.pause();audio.pause();narration.pause();},paused:()=>audio.paused&&narration.paused,time:()=>audio.currentTime,seek,
    setRate:value=>{audio.playbackRate=narration.playbackRate=value;if(player)player.setRate(value);emit();},getRate:()=>audio.playbackRate,
    next:()=>stepQueue(1),prev:()=>stepQueue(-1),
    snapshot:()=>{if(!ready)return null;const item=player?.position().item;if(player&&item?.type==='narration')return null;if(player&&!item&&!prepared)return null;return {item_offset_seconds:audio.currentTime,...(player?{plan_id:spec.planId,plan_version:spec.planVersion,item_position:item?.position??prepared.item_position,highlight_id:item?.highlightId||prepared?.highlight_id||''}:{})};},
    restore:restorePosition},
   notify:(text,action)=>{if(current===token)notify(text,action);},onSaved:()=>{if(current===token)remember();},onRate:()=>{if(current===token)emit();},
   onResume:(saved,fn)=>{if(current!==token)return;const run=()=>{resume=null;noticeAction=null;fn();emit();};resume={saved,run};notify('已找到上次位置；继续听需要明确点击。',run);}});
  remember();emit();return true;
 }
 async function offlineAuthorization(){
  const db=root.CWPOfflineStorage,grant=await db.get('meta','grant');
  try{
   if(!root.CWPOfflineCore?.validGrant(grant,Date.now(),grant?.last_seen||0))throw Error('离线授权已到期');
   if(root.CWPOfflineDownloads?.authorizedGrant)return await root.CWPOfflineDownloads.authorizedGrant();
   async function online(url){try{return await fetch(url,{credentials:'same-origin',cache:'no-store'});}catch(error){if(error instanceof TypeError)return null;throw error;}}
   const sessionResponse=await online('/api/offline/session');if(!sessionResponse)return grant;
   if(!sessionResponse.ok||sessionResponse.redirected)throw Error('在线登录身份无法确认');
   const session=await sessionResponse.json();if(!session?.session_namespace||session.session_namespace!==grant.owner_session_namespace)throw Error('新登录会话不能继承旧下载副本');
   const statusResponse=await online('/api/offline/status?device_id='+encodeURIComponent(grant.device_id));if(!statusResponse)return grant;
   if(!statusResponse.ok||statusResponse.redirected)throw Error('来源或离线授权已撤回');
   const status=await statusResponse.json(),auth=status?.authorization;if(!auth||auth.namespace!==grant.namespace||auth.epoch!==grant.epoch)throw Error('离线授权已变化');
   return grant;
  }catch(error){stop();if(grant?.namespace&&db.clearNamespace)await db.clearNamespace(grant.namespace,{keepOutbox:true});throw error;}
 }
 async function startOffline({file,grant,excerpt,paused=false}={}){
  if(!root.CWPOfflinePacks||!root.CWPOfflineStorage||!file||file.kind!=='audio'||!file.id||!file.source_type||!file.source_id||!file.snapshot_id||!file.audio_sha||file.audio_sha!==file.sha256)throw Error('离线原音身份不完整');
  const current=token,db=root.CWPOfflineStorage,authorization=await offlineAuthorization();
  if(!authorization||authorization.namespace!==grant?.namespace||authorization.epoch!==grant?.epoch)throw Error('离线授权已变化');
  const member=authorization.files?.find(f=>f.id===file.id);
  if(!member||!['kind','source_type','source_id','snapshot_id','audio_sha','sha256','size'].every(key=>member[key]===file[key]))throw Error('原音未包含在当前离线授权中');
  await root.CWPOfflinePacks.readable(member,authorization);
  let interval=null;
  if(excerpt){interval=(member.excerpts||authorization.excerpts||[]).find(e=>e.excerpt_id===excerpt.excerpt_id);if(!interval||interval.source_type!==member.source_type||interval.source_id!==member.source_id||interval.snapshot_id!==member.snapshot_id||interval.audio_sha256!==member.audio_sha||!Number.isFinite(interval.start_seconds)||!Number.isFinite(interval.end_seconds)||interval.start_seconds<0||interval.end_seconds<=interval.start_seconds||!Array.isArray(interval.segment_ids)||!interval.segment_ids.length)throw Error('补听区间未包含在冻结离线授权中');}
  const spec={title:file.title||'离线原音',sourceType:file.source_type,sourceId:file.source_id,mode:interval?'excerpt':'original',planId:'',planVersion:0,audioSHA:file.audio_sha,audioURL:'/offline-audio?file_id='+encodeURIComponent(file.id),snapshot:file.snapshot_id,snapshotVersion:file.snapshot_version||0,segments:[],items:[],csrf:'',offline:{file:member,namespace:authorization.namespace,epoch:authorization.epoch},...(interval?{excerptId:interval.excerpt_id,startSeconds:interval.start_seconds,endSeconds:interval.end_seconds,segmentIds:interval.segment_ids}:{})};
  const key=authorization.namespace+':progress:'+spec.sourceType+':'+spec.sourceId+':'+spec.mode+':'+(spec.excerptId||'')+':'+spec.audioSHA;
  let saved=await db.get('meta',key)||{revision:interval?.progress_revision||file.progress_revision||0};
  const progressFetch=async(url,options={})=>{const latest=await db.get('meta','grant');if(latest?.namespace!==authorization.namespace||latest.epoch!==authorization.epoch)throw Error('离线授权已变化');await root.CWPOfflinePacks.readable(member,latest);if(options.method!=='POST')return {ok:true,json:async()=>saved};const payload=JSON.parse(options.body);if(spec.mode==='excerpt'&&!root.CWPOfflineCore?.supportsExcerptProgress)throw Error('离线补听位置同步尚不可用，未写入整集进度');const frozen={...payload,mode:spec.mode,audio_sha256:spec.audioSHA,snapshot_id:spec.snapshot,snapshot_version:spec.snapshotVersion,...(interval?{excerpt_id:spec.excerptId,start_seconds:spec.startSeconds,end_seconds:spec.endSeconds,segment_ids:spec.segmentIds}:{})};const queued=await root.CWPOfflineOperations.enqueue(spec.mode==='excerpt'?'excerpt_progress':'original_progress',frozen,{source_type:spec.sourceType,source_id:spec.sourceId,snapshot_id:spec.snapshot},payload.expected_revision);saved={...frozen,id:key,revision:queued.expected_revision+1};await db.put('meta',{...saved,id:key});return {ok:true,json:async()=>saved};};
  if(current!==token)throw Error('播放选择已变化');
  if(!install(spec,{progressFetch,localRevision:saved.revision}))throw Error('离线原音无法播放');
  if(!paused)await transport.play();emit();return state();
 }
 function act(spec,fn){if(!install(spec))return;fn(state());emit();}
 let lastNaturalEnd=-1;
 function ended(){
  if(loop&&(active?.mode==='original'||active?.mode==='excerpt')&&!transport?.status().sleepExpired&&(!transport?.status().deadline||Date.now()<transport.status().deadline)){seek(loop.start);transport?.play();return;}
  if(root.CWPPlayback.canOfferReflection(state())&&lastNaturalEnd!==token){lastNaturalEnd=token;const captured=anchor();if(captured)root.dispatchEvent(new CustomEvent('cwp-listening-ended',{detail:{capture:captured,playbackId:token}}));}
  loop=null;remember();emit();stepQueue(1,true);
 }
 async function readQueue(){const response=await fetch('/api/listening-queue',{cache:'no-store'});if(!response.ok){if(response.status===401||response.redirected)root.CWPNavigation?.expire();throw Error('无法读取队列');}return response.json();}
 async function changeQueue(change,revision){const response=await fetch('/api/listening-queue',{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':active?.csrf||document.querySelector('[name=_csrf]')?.value||document.querySelector('#listening-queue')?.dataset.csrf||''},body:JSON.stringify({...change,expected_revision:revision})});if(!response.ok){if(response.status===409)throw Error('队列已被其他窗口修改，请重载后操作。');throw Error(await response.text());}return response.json();}
 async function confirmSession(spec,signal){const query=new URLSearchParams({source_type:spec.sourceType,source_id:spec.sourceId,mode:spec.mode,plan_id:spec.planId||'',plan_version:String(spec.planVersion||0),audio_sha256:spec.audioSHA,excerpt_id:spec.excerptId||''});const response=await fetch('/api/listening-session?'+query,{signal,cache:'no-store'});if(!response.ok||response.redirected)throw Error('无法确认播放身份');const result=await response.json();if(!result.available)throw Error(result.reason||'播放依据已失效');return result;}
 async function loadSpec(identity,signal){const response=await fetch(urlFor({sourceType:identity.sourceType||identity.source_type,sourceId:identity.sourceId||identity.source_id,mode:identity.mode,planId:identity.planId||identity.plan_id,planVersion:identity.planVersion||identity.plan_version}),{signal,cache:'no-store'});if(response.redirected||response.status===401){root.CWPNavigation?.expire();throw Error('登录已失效');}if(!response.ok)throw Error('来源或清单不可用');const view=new DOMParser().parseFromString(await response.text(),'text/html').querySelector('#page-view');const spec=view&&specFrom(view);if(!spec)throw Error('没有可播放的音频');if(identity.mode==='excerpt'){const excerptId=identity.excerptId||identity.excerpt_id,start=Number(identity.startSeconds??identity.start_seconds),end=Number(identity.endSeconds??identity.end_seconds),snapshot=identity.snapshot||identity.snapshot_id;if(!excerptId||!snapshot||!Number.isFinite(start)||!Number.isFinite(end)||start<0||end<=start)throw Error('补听区间依据无法确认');if((identity.audioSHA||identity.audio_sha256)!==spec.audioSHA)throw Error('补听原音身份已变化');Object.assign(spec,{mode:'excerpt',excerptId,snapshot,startSeconds:start,endSeconds:end,segmentIds:identity.segmentIds||identity.segment_ids||[]});}const confirmed=await confirmSession(spec,signal);if(spec.mode==='excerpt'){if(confirmed.snapshot_id!==spec.snapshot||confirmed.excerpt_id!==spec.excerptId||Number(confirmed.start_seconds)!==spec.startSeconds||Number(confirmed.end_seconds)!==spec.endSeconds)throw Error('补听区间身份已变化');spec.snapshotVersion=Number(confirmed.snapshot_version||confirmed.version)||0;spec.segmentIds=confirmed.segment_ids||spec.segmentIds;spec.segments=[];}return spec;}
 async function playQueue(item,q){const current=token;const spec=await loadSpec(item);if(current!==token)throw Error("播放选择已变化");if(item.audio_sha256&&spec.audioSHA!==item.audio_sha256)throw Error('原音身份已变更，无法播放旧队列项');if(item.mode==='dj'&&(spec.planId!==item.plan_id||spec.planVersion!==item.plan_version))throw Error('DJ清单身份已变更');await changeQueue({action:'play',item_id:item.id},q.revision);if(current!==token)throw Error('播放选择已变化');if(install(spec,{queueId:item.id})){transport.play();emit();}}
 let stepping=false;
 async function stepQueue(direction,automatic=false){if(active?.offline){if(!automatic)notify('离线收听请从已下载材料中明确选择下一项。');return;}if(stepping||automatic&&(transport?.status().sleepExpired||transport?.status().deadline&&Date.now()>=transport.status().deadline))return;stepping=true;const current=token;
  try{const q=await readQueue();if(current!==token||!queueId||(automatic&&(!q.autoplay||q.current_item_id!==queueId)))return;const index=q.items.findIndex(i=>i.id===queueId);if(index<0)return;const available=[];for(let i=index+direction;i>=0&&i<q.items.length;i+=direction){if(q.items[i].available)available.push(q.items[i]);}
   // Bounded by this queue snapshot. Failures never create a plan or call AI.
   for(const item of available){try{if(current!==token)return;await playQueue(item,q);return;}catch(error){notify('跳过不可播放条目：'+error.message);if(/其他窗口|登录|选择已变化/.test(error.message))return;}}
   notify(available.length?'本次后续条目均无法播放。':'没有下一项可播放。');
  }catch(error){notify(error.message);}finally{stepping=false;}
 }
 function anchor(){if(!active)return null;let ids=[],position=audio.currentTime;const item=player?.position().item;
  if(item?.type==='narration'){ids=item.segmentIds||[];const first=active.segments.find(s=>s.id===ids[0]);if(!first)return null;position=first.start;}
  else if(item?.type==='evidence'){ids=item.segmentIds||[];}else if(active.mode==='excerpt'){ids=active.segmentIds||[];}else{const seg=active.segments.find(s=>s.start<=position&&s.end>position);if(seg)ids=[seg.id];}
  return {sourceType:active.sourceType,sourceId:active.sourceId,title:active.title,csrf:active.csrf,anchor:{snapshot_id:active.snapshot,version:active.snapshotVersion,position,segment_ids:ids,mode:active.mode,plan_id:active.planId,plan_version:active.planVersion,audio_sha256:active.audioSHA}};
 }
 let noteCapture=null;
 function captureNote(frozen){const capture=frozen?.anchor?structuredClone(frozen):anchor();if(!capture?.anchor.snapshot_id){notify('当前播放没有可确认的来源快照，请到来源页面写无位置笔记。');return;}noteCapture=structuredClone(capture);byId('listening-note-anchor').textContent='记录 '+capture.title+' · 原音 '+fmt(capture.anchor.position)+(player?.position().item?.type==='narration'?'（AI解说参考原音）':'');byId('listening-note-form').hidden=false;byId('listening-note-content').focus();saveNoteDraft();}
 function saveNoteDraft(){try{sessionStorage.setItem('cwp-playing-note',JSON.stringify({capture:noteCapture,text:byId('listening-note-content').value}));}catch(_){byId('listening-note-feedback').textContent='本地草稿保存失败，请复制文字。';}}
 byId('listening-note').onclick=captureNote;byId('listening-note-content').oninput=saveNoteDraft;byId('listening-note-cancel').onclick=()=>{saveNoteDraft();byId('listening-note-form').hidden=true;};
 byId('listening-note-form').onsubmit=async event=>{event.preventDefault();saveNoteDraft();if(!noteCapture)return;const originalCapture=noteCapture,frozen=structuredClone(noteCapture),text=byId('listening-note-content').value,button=event.target.querySelector('[type=submit]');button.disabled=true;
  try{const data=new URLSearchParams({_csrf:active?.csrf||frozen.csrf,source_type:frozen.sourceType,source_id:frozen.sourceId,kind:'owner_reflection',content:text,references_json:JSON.stringify(frozen.anchor.segment_ids),anchor_json:JSON.stringify(frozen.anchor)});const response=await fetch('/api/owner-notes',{method:'POST',headers:{Accept:'application/json'},body:data});if(!response.ok)throw Error(await response.text());byId('listening-note-feedback').textContent='已保存到 '+frozen.title;if(byId('listening-note-content').value!==text||noteCapture!==originalCapture){saveNoteDraft();return;}sessionStorage.removeItem('cwp-playing-note');byId('listening-note-content').value='';byId('listening-note-form').hidden=true;noteCapture=null;
  }catch(error){byId('listening-note-feedback').textContent='未保存，草稿保留。'+error.message;}finally{button.disabled=false;}};
 function stop(clear=true){++token;transport?.pause();transport?.destroy();player?.stop();audio.pause();narration.pause();audio.removeAttribute('src');narration.removeAttribute('src');audio.load();narration.load();active=null;transport=player=null;loop=null;resume=null;prepared=null;suppressSave=false;queueId='';noticeAction=null;if(clear)localStorage.removeItem(sessionKey);emit();}
 audio.addEventListener('loadedmetadata',()=>{ready=true;if(active?.mode==='excerpt')seek(audio.currentTime);emit();});audio.addEventListener('timeupdate',()=>{if(active?.mode==='excerpt'){const sleep=transport?.status();if(sleep?.sleepExpired||sleep?.deadline&&Date.now()>=sleep.deadline){transport?.tick();remember();emit();return;}if(loop&&audio.currentTime>=loop.end){seek(loop.start);}else if(audio.currentTime>=active.endSeconds&&!audio.paused){seek(active.endSeconds);transport?.pause();ended();}}else if(loop&&audio.currentTime>=loop.end)seek(loop.start);remember();emit();});narration.addEventListener('timeupdate',emit);
 ['play','pause','seeked'].forEach(event=>{audio.addEventListener(event,()=>{if(event==='play')suppressSave=false;if(!suppressSave){transport?.changed();if(event!=='play')transport?.save();}remember();emit();});narration.addEventListener(event,emit);});
 audio.addEventListener('ended',()=>{if(!player)ended();});audio.addEventListener('error',()=>{notify('原音播放失败，当前会话已暂停。');if(active&&!player)stepQueue(1,true);});
 byId('listening-toggle').onclick=()=>{transport?.toggle();emit();};byId('listening-back').onclick=()=>transport?.skip(-15);byId('listening-forward').onclick=()=>transport?.skip(15);byId('listening-sleep').onchange=event=>transport?.setSleep(Number(event.target.value));byId('listening-prev').onclick=()=>stepQueue(-1);byId('listening-next').onclick=()=>stepQueue(1);byId('listening-stop').onclick=()=>{const id=queueId;stop();if(id)readQueue().then(q=>changeQueue({action:'stop'},q.revision)).catch(error=>notify(error.message));};
 root.CWPListening={state,same,specFrom,install,act,anchor,captureNote,stop,notify,readQueue,changeQueue,playQueue,urlFor,startOffline,
  clearPrivate:()=>{stop();noteCapture=null;for(const id of ['listening-title','listening-note-anchor','listening-note-feedback','listening-feedback'])byId(id).textContent='';byId('listening-note-content').value='';byId('listening-note-form').hidden=true;},
  setLoop:(start,end)=>{if(!active||!['original','excerpt'].includes(active.mode)||!Number.isFinite(start)||!Number.isFinite(end)||start<0||end<=start||(active.mode==='excerpt'&&(start<active.startSeconds||end>active.endSeconds))||(Number.isFinite(audio.duration)&&end>audio.duration))return false;loop={start,end};remember();emit();return true;},clearLoop:()=>{loop=null;remember();emit();},
  subscribe:fn=>{subscribers.add(fn);fn(state());return ()=>subscribers.delete(fn);}};
 async function restore(){let saved;try{saved=JSON.parse(localStorage.getItem(sessionKey)||'null');}catch(_){}const initial=token;
  if(!saved)return;
  if(saved.offline){try{const grant=await root.CWPOfflineStorage.get('meta','grant');if(grant?.namespace!==saved.offline.namespace||grant.epoch!==saved.offline.epoch)throw Error('离线授权已变化');await startOffline({file:saved.offline.file,grant,excerpt:saved.excerptId?{excerpt_id:saved.excerptId}:undefined,paused:true});restorePosition({item_offset_seconds:saved.position});notify('已恢复离线原音，保持暂停；点击播放继续。');}catch(error){notify('离线会话未恢复：'+error.message);}return;}
  try{const spec=await loadSpec(saved);if(token!==initial)return;if(saved.audioSHA!==spec.audioSHA||!spec.audioSHA||saved.snapshot!==spec.snapshot){localStorage.removeItem(sessionKey);notify('上次音频或依据版本无法确认，请在来源页面重新选择。');return;}
   install(spec,{queueId:saved.queueId,restoring:true,localRevision:saved.revision});if(spec.mode==='original'||spec.mode==='excerpt'){restorePosition({item_offset_seconds:saved.position});if(saved.loop)root.CWPListening.setLoop(saved.loop.start,saved.loop.end);}else if(saved.itemPosition){restorePosition({item_position:saved.itemPosition,highlight_id:saved.highlightId||'',item_offset_seconds:saved.position});}remember();notify('已恢复上次播放会话，保持暂停；点击播放继续。');
  }catch(error){if(token===initial)notify('上次会话未恢复：'+error.message);}
 }
 // Restore is explicit about identity and remains paused. There is never startup autoplay.
 root.CWPListening.restored=restore();
 byId('listening-loop-set').onclick=()=>{if(!root.CWPListening.setLoop(Number(byId('listening-loop-start').value),Number(byId('listening-loop-end').value)))notify('循环需要当前原音、有效起止位置且终点晚于起点。');};byId('listening-loop-clear').onclick=()=>root.CWPListening.clearLoop();
 let validating=false;
 async function validate(){if(!active||validating)return;if(active.offline){try{const grant=await root.CWPOfflineStorage.get('meta','grant');if(grant?.namespace!==active.offline.namespace||grant.epoch!==active.offline.epoch)throw Error('离线授权已变化');await root.CWPOfflinePacks.readable(active.offline.file,grant);}catch(error){stop();notify('离线播放已停止：'+error.message);}return;}validating=true;const current=token,spec=active;
  try{const query=new URLSearchParams({source_type:spec.sourceType,source_id:spec.sourceId,mode:spec.mode,plan_id:spec.planId,plan_version:String(spec.planVersion),audio_sha256:spec.audioSHA,excerpt_id:spec.excerptId||''});const response=await fetch('/api/listening-session?'+query,{cache:'no-store'});if(response.status===401||response.redirected){root.CWPNavigation?.expire();return;}if(!response.ok)return;const result=await response.json();if(current!==token)return;if(!result.available){stop();notify('收听已停止：'+result.reason);document.getElementById('navigation-feedback').hidden=false;document.getElementById('navigation-feedback').textContent='来源不可用，播放会话已清除：'+result.reason;}
  }catch(_){}finally{validating=false;}
 }
 root.addEventListener('private-reset',()=>{if(active?.offline)stop();});root.addEventListener('offline-revoked',()=>{if(active?.offline)stop();});
 root.setInterval(validate,15000);root.addEventListener('pageshow',validate);document.addEventListener('visibilitychange',()=>{if(!document.hidden)validate();});
 try{const draft=JSON.parse(sessionStorage.getItem('cwp-playing-note')||'null');if(draft?.capture){noteCapture=draft.capture;byId('listening-note-content').value=draft.text||'';byId('listening-note-anchor').textContent='保留的当前播放笔记：'+noteCapture.title;byId('listening-note-form').hidden=false;}}catch(_){}
})(window);
