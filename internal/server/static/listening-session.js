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
 function same(a,b){return !!a&&!!b&&a.sourceType===b.sourceType&&a.sourceId===b.sourceId&&a.mode===b.mode&&a.planId===b.planId&&a.planVersion===b.planVersion&&a.audioSHA===b.audioSHA;}
 function state(){return {spec:active,audio,narration,transport,player,queueId,ready,loop,resume,notice,noticeAction,paused:audio.paused&&narration.paused,time:audio.currentTime,duration:audio.duration};}
 function remember(){if(!active)return;try{localStorage.setItem(sessionKey,JSON.stringify({sourceType:active.sourceType,sourceId:active.sourceId,mode:active.mode,planId:active.planId,planVersion:active.planVersion,audioSHA:active.audioSHA,snapshot:active.snapshot,queueId,loop,position:audio.currentTime,itemPosition:player?.position().item?.type==='evidence'?player.position().item.position:prepared?.item_position,highlightId:player?.position().item?.type==='evidence'?player.position().item.highlightId:prepared?.highlight_id,revision:transport?.status().revision||0}));}catch(_){}}
 function emit(){shell.hidden=!active;try{if(navigator.mediaSession)navigator.mediaSession.playbackState=active?(audio.paused&&narration.paused?'paused':'playing'):'none';}catch(_){}if(active){byId('listening-title').textContent=active.title+' · '+(active.mode==='dj'?'DJ精听':'原音');byId('listening-title').href=urlFor(active);byId('listening-position').textContent=' '+fmt(audio.currentTime);byId('listening-toggle').textContent=audio.paused&&narration.paused?'播放':'暂停';if(loop){byId('listening-loop-start').value=loop.start;byId('listening-loop-end').value=loop.end;}const deadline=transport?.status().deadline||0;byId('listening-sleep').value=deadline?String([15,30,60].find(n=>n>=Math.ceil((deadline-Date.now())/60000))||60):'0';}
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
 function seek(value){const current=token;const apply=()=>{if(!active||current!==token)return;const item=player?.position().item;let min=0,max=Number.isFinite(audio.duration)?audio.duration:Number(value);if(item?.type==='evidence'){min=item.start;max=item.end===null?max:item.end;}
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
  transport=root.CWPPlayback.create({title:spec.title,sourceType:spec.sourceType,sourceId:spec.sourceId,mode:spec.mode,csrf:spec.csrf,audioSHA:spec.audioSHA,planId:spec.planId,planVersion:spec.planVersion,localRevision:options.localRevision,sleepDeadline:previousSleep,
   adapter:{play:()=>player?player.play():audio.play(),pause:()=>{if(player)player.pause();audio.pause();narration.pause();},paused:()=>audio.paused&&narration.paused,time:()=>audio.currentTime,seek,
    setRate:value=>{audio.playbackRate=narration.playbackRate=value;if(player)player.setRate(value);emit();},getRate:()=>audio.playbackRate,
    next:()=>stepQueue(1),prev:()=>stepQueue(-1),
    snapshot:()=>{if(!ready)return null;const item=player?.position().item;if(player&&item?.type==='narration')return null;if(player&&!item&&!prepared)return null;return {item_offset_seconds:audio.currentTime,...(player?{plan_id:spec.planId,plan_version:spec.planVersion,item_position:item?.position??prepared.item_position,highlight_id:item?.highlightId||prepared?.highlight_id||''}:{})};},
    restore:restorePosition},
   notify:(text,action)=>{if(current===token)notify(text,action);},onSaved:()=>{if(current===token)remember();},onRate:()=>{if(current===token)emit();},
   onResume:(saved,fn)=>{if(current!==token)return;const run=()=>{resume=null;noticeAction=null;fn();emit();};resume={saved,run};notify('已找到上次位置；继续听需要明确点击。',run);}});
  remember();emit();return true;
 }
 function act(spec,fn){if(!install(spec))return;fn(state());emit();}
 let lastNaturalEnd=-1;
 function ended(){
  if(loop&&active?.mode==='original'){seek(loop.start);transport?.play();return;}
  if(root.CWPPlayback.canOfferReflection(state())&&lastNaturalEnd!==token){lastNaturalEnd=token;const captured=anchor();if(captured)root.dispatchEvent(new CustomEvent('cwp-listening-ended',{detail:{capture:captured,playbackId:token}}));}
  loop=null;remember();emit();stepQueue(1,true);
 }
 async function readQueue(){const response=await fetch('/api/listening-queue',{cache:'no-store'});if(!response.ok){if(response.status===401||response.redirected)root.CWPNavigation?.expire();throw Error('无法读取队列');}return response.json();}
 async function changeQueue(change,revision){const response=await fetch('/api/listening-queue',{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':active?.csrf||document.querySelector('[name=_csrf]')?.value||document.querySelector('#listening-queue')?.dataset.csrf||''},body:JSON.stringify({...change,expected_revision:revision})});if(!response.ok){if(response.status===409)throw Error('队列已被其他窗口修改，请重载后操作。');throw Error(await response.text());}return response.json();}
 async function loadSpec(identity,signal){const response=await fetch(urlFor({sourceType:identity.sourceType||identity.source_type,sourceId:identity.sourceId||identity.source_id,mode:identity.mode,planId:identity.planId||identity.plan_id,planVersion:identity.planVersion||identity.plan_version}),{signal,cache:'no-store'});if(response.redirected||response.status===401){root.CWPNavigation?.expire();throw Error('登录已失效');}if(!response.ok)throw Error('来源或清单不可用');const view=new DOMParser().parseFromString(await response.text(),'text/html').querySelector('#page-view');const spec=view&&specFrom(view);if(!spec)throw Error('没有可播放的音频');return spec;}
 async function playQueue(item,q){const current=token;const spec=await loadSpec(item);if(current!==token)throw Error("播放选择已变化");if(item.audio_sha256&&spec.audioSHA!==item.audio_sha256)throw Error('原音身份已变更，无法播放旧队列项');if(item.mode==='dj'&&(spec.planId!==item.plan_id||spec.planVersion!==item.plan_version))throw Error('DJ清单身份已变更');await changeQueue({action:'play',item_id:item.id},q.revision);if(current!==token)throw Error('播放选择已变化');if(install(spec,{queueId:item.id})){transport.play();emit();}}
 let stepping=false;
 async function stepQueue(direction,automatic=false){if(stepping||automatic&&(transport?.status().sleepExpired||transport?.status().deadline&&Date.now()>=transport.status().deadline))return;stepping=true;const current=token;
  try{const q=await readQueue();if(current!==token||!queueId||(automatic&&(!q.autoplay||q.current_item_id!==queueId)))return;const index=q.items.findIndex(i=>i.id===queueId);if(index<0)return;const available=[];for(let i=index+direction;i>=0&&i<q.items.length;i+=direction){if(q.items[i].available)available.push(q.items[i]);}
   // Bounded by this queue snapshot. Failures never create a plan or call AI.
   for(const item of available){try{if(current!==token)return;await playQueue(item,q);return;}catch(error){notify('跳过不可播放条目：'+error.message);if(/其他窗口|登录|选择已变化/.test(error.message))return;}}
   notify(available.length?'本次后续条目均无法播放。':'没有下一项可播放。');
  }catch(error){notify(error.message);}finally{stepping=false;}
 }
 function anchor(){if(!active)return null;let ids=[],position=audio.currentTime;const item=player?.position().item;
  if(item?.type==='narration'){ids=item.segmentIds||[];const first=active.segments.find(s=>s.id===ids[0]);if(!first)return null;position=first.start;}
  else if(item?.type==='evidence'){ids=item.segmentIds||[];}else{const seg=active.segments.find(s=>s.start<=position&&s.end>position);if(seg)ids=[seg.id];}
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
 audio.addEventListener('loadedmetadata',()=>{ready=true;emit();});audio.addEventListener('timeupdate',()=>{if(loop&&audio.currentTime>=loop.end)seek(loop.start);remember();emit();});narration.addEventListener('timeupdate',emit);
 ['play','pause','seeked'].forEach(event=>{audio.addEventListener(event,()=>{if(event==='play')suppressSave=false;if(!suppressSave){transport?.changed();if(event!=='play')transport?.save();}remember();emit();});narration.addEventListener(event,emit);});
 audio.addEventListener('ended',()=>{if(!player)ended();});audio.addEventListener('error',()=>{notify('原音播放失败，当前会话已暂停。');if(active&&!player)stepQueue(1,true);});
 byId('listening-toggle').onclick=()=>{transport?.toggle();emit();};byId('listening-back').onclick=()=>transport?.skip(-15);byId('listening-forward').onclick=()=>transport?.skip(15);byId('listening-sleep').onchange=event=>transport?.setSleep(Number(event.target.value));byId('listening-prev').onclick=()=>stepQueue(-1);byId('listening-next').onclick=()=>stepQueue(1);byId('listening-stop').onclick=()=>{const id=queueId;stop();if(id)readQueue().then(q=>changeQueue({action:'stop'},q.revision)).catch(error=>notify(error.message));};
 root.CWPListening={state,same,specFrom,install,act,anchor,captureNote,stop,notify,readQueue,changeQueue,playQueue,urlFor,
  clearPrivate:()=>{stop();noteCapture=null;for(const id of ['listening-title','listening-note-anchor','listening-note-feedback','listening-feedback'])byId(id).textContent='';byId('listening-note-content').value='';byId('listening-note-form').hidden=true;},
  setLoop:(start,end)=>{if(!active||active.mode!=='original'||!Number.isFinite(start)||!Number.isFinite(end)||start<0||end<=start||(Number.isFinite(audio.duration)&&end>audio.duration))return false;loop={start,end};remember();emit();return true;},clearLoop:()=>{loop=null;remember();emit();},
  subscribe:fn=>{subscribers.add(fn);fn(state());return ()=>subscribers.delete(fn);}};
 async function restore(){let saved;try{saved=JSON.parse(localStorage.getItem(sessionKey)||'null');}catch(_){}const initial=token;
  if(!saved)return;
  try{const spec=await loadSpec(saved);if(token!==initial)return;if(saved.audioSHA!==spec.audioSHA||!spec.audioSHA||saved.snapshot!==spec.snapshot){localStorage.removeItem(sessionKey);notify('上次音频或依据版本无法确认，请在来源页面重新选择。');return;}
   install(spec,{queueId:saved.queueId,restoring:true,localRevision:saved.revision});if(spec.mode==='original'){restorePosition({item_offset_seconds:saved.position});if(saved.loop)root.CWPListening.setLoop(saved.loop.start,saved.loop.end);}else if(saved.itemPosition){restorePosition({item_position:saved.itemPosition,highlight_id:saved.highlightId||'',item_offset_seconds:saved.position});}remember();notify('已恢复上次播放会话，保持暂停；点击播放继续。');
  }catch(error){if(token===initial)notify('上次会话未恢复：'+error.message);}
 }
 // Restore is explicit about identity and remains paused. There is never startup autoplay.
 root.CWPListening.restored=restore();
 byId('listening-loop-set').onclick=()=>{if(!root.CWPListening.setLoop(Number(byId('listening-loop-start').value),Number(byId('listening-loop-end').value)))notify('循环需要当前原音、有效起止位置且终点晚于起点。');};byId('listening-loop-clear').onclick=()=>root.CWPListening.clearLoop();
 let validating=false;
 async function validate(){if(!active||validating)return;validating=true;const current=token,spec=active;
  try{const query=new URLSearchParams({source_type:spec.sourceType,source_id:spec.sourceId,mode:spec.mode,plan_id:spec.planId,plan_version:String(spec.planVersion),audio_sha256:spec.audioSHA});const response=await fetch('/api/listening-session?'+query,{cache:'no-store'});if(response.status===401||response.redirected){root.CWPNavigation?.expire();return;}if(!response.ok)return;const result=await response.json();if(current!==token)return;if(!result.available){stop();notify('收听已停止：'+result.reason);document.getElementById('navigation-feedback').hidden=false;document.getElementById('navigation-feedback').textContent='来源不可用，播放会话已清除：'+result.reason;}
  }catch(_){}finally{validating=false;}
 }
 root.setInterval(validate,15000);root.addEventListener('pageshow',validate);document.addEventListener('visibilitychange',()=>{if(!document.hidden)validate();});
 try{const draft=JSON.parse(sessionStorage.getItem('cwp-playing-note')||'null');if(draft?.capture){noteCapture=draft.capture;byId('listening-note-content').value=draft.text||'';byId('listening-note-anchor').textContent='保留的当前播放笔记：'+noteCapture.title;byId('listening-note-form').hidden=false;}}catch(_){}
})(window);
