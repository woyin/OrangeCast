// The recorder belongs to the root shell; view changes cannot rebind its frozen source.
(function(root){
 'use strict';
 const panel=document.getElementById('voice-recorder');if(!panel)return;
 const byId=id=>document.getElementById(id),text=byId('voice-text'),status=byId('voice-status');
 const dbName='cwp-voice-drafts-v1',maxBytes=20*1024*1024,maxLocalBytes=50*1024*1024,maxLocalDrafts=10;
 let session=null,db=null,dbPromise=null,serial=Promise.resolve(),current=null,recorder=null,stream=null,timer=null,chunks=[],recordBytes=0,startedAt=0,afterStop=false,waiting=false,epoch=0,busy=false,permissionSeq=0,pendingCapture=null;
 const requests=new Set();
 function say(value){status.textContent=value;}
 function fmt(value){value=Math.floor(value||0);return Math.floor(value/60)+':'+String(value%60).padStart(2,'0');}
 async function request(url,options={}){
  const controller=new AbortController();requests.add(controller);
  try{const response=await fetch(url,{...options,signal:controller.signal,cache:'no-store'});if(response.status===401||response.redirected){root.CWPNavigation?.expire();throw Error('登录已失效');}if(!response.ok)throw Error(await response.text());return response.json();}finally{requests.delete(controller);}
 }
 async function ensureSession(){if(session)return session;const token=epoch;const value=await request('/api/voice-notes/session');if(token!==epoch)throw Error('登录会话已变化');session=value;const select=byId('voice-question');select.replaceChildren(new Option('不关联',''));for(const q of value.questions||[]){const option=new Option(q.Body||q.body,q.ID||q.id);option.dataset.revision=q.Revision||q.revision;select.append(option);}return session;}
 function openDB(){if(dbPromise)return dbPromise;const token=epoch;dbPromise=new Promise((resolve,reject)=>{if(!root.indexedDB){reject(Error('浏览器未提供本地录音存储，请复制文字后显式上传'));return;}const req=indexedDB.open(dbName,1);req.onupgradeneeded=()=>req.result.createObjectStore('drafts',{keyPath:'id'});req.onerror=()=>reject(req.error);req.onsuccess=()=>{if(token!==epoch){req.result.close();reject(Error('会话已变化'));return;}db=req.result;db.onversionchange=()=>{db.close();db=null;dbPromise=null;};resolve(db);};req.onblocked=()=>say('本地录音存储被其他窗口占用，尚未保存。');});return dbPromise;}
 async function localList(){const auth=await ensureSession(),database=await openDB();return new Promise((resolve,reject)=>{const tx=database.transaction('drafts','readwrite'),store=tx.objectStore('drafts'),req=store.getAll();let values=[];req.onsuccess=()=>{values=req.result.filter(d=>d.sessionId===auth.session_id);for(const d of req.result){if(d.sessionId!==auth.session_id)store.delete(d.id);}};tx.oncomplete=()=>resolve(values);tx.onerror=()=>reject(tx.error);});}
 async function persist(){if(!current)return;current.text=text.value;const draft=structuredClone(current),token=epoch;
  const operation=async()=>{const auth=await ensureSession();if(token!==epoch)throw Error('会话已变化');draft.sessionId=auth.session_id;const database=await openDB();return new Promise((resolve,reject)=>{const tx=database.transaction('drafts','readwrite'),store=tx.objectStore('drafts'),req=store.getAll();let reason='';req.onsuccess=()=>{if(token!==epoch){reason='会话已变化';tx.abort();return;}const others=req.result.filter(d=>d.id!==draft.id&&d.sessionId===auth.session_id),size=others.reduce((n,d)=>n+(d.blob?.size||0),0)+(draft.blob?.size||0);if(others.length>=maxLocalDrafts||size>maxLocalBytes){reason='本地草稿已达10条／50MB，未保存本次变更。请先删除旧草稿，或复制文字并上传。';tx.abort();return;}for(const d of req.result){if(d.sessionId!==auth.session_id)store.delete(d.id);}store.put(draft);};tx.oncomplete=resolve;tx.onerror=()=>reject(Error(reason||tx.error?.message||'本地保存失败'));tx.onabort=()=>reject(Error(reason||'本地保存失败'));});};
  const result=serial.then(operation,operation);serial=result.catch(()=>{});await result;
 }
 async function removeLocal(id){await serial;const database=await openDB();await new Promise((resolve,reject)=>{const tx=database.transaction('drafts','readwrite');tx.objectStore('drafts').delete(id);tx.oncomplete=resolve;tx.onerror=()=>reject(tx.error);});}
 function render(){
  const capture=waiting?pendingCapture:current?.capture,a=capture?.anchor;
  byId('voice-anchor').textContent=capture?(capture.title||'原节目')+' · 冻结原音 '+fmt(a?.position)+' · '+(a?.mode==='dj'?'DJ精听':'原音')+'（位置不会跟随播放改变）':'';
  if(current)text.value=current.text||'';
  text.disabled=!current;byId('voice-asr-text').textContent=current?.server?.asr_text||'';byId('voice-asr-result').hidden=!current?.server?.asr_text;
  byId('voice-stop-draft').textContent=waiting&&!recorder?'取消权限等待':'停止并留草稿';
  byId('voice-stop-draft').disabled=!(waiting||recorder?.state==='recording');byId('voice-stop-asr').disabled=recorder?.state!=='recording';
  byId('voice-transcribe').textContent=current?.server?.state==='failed'?'显式重试转写（可能再次计费）':'上传并转写';
  byId('voice-transcribe').disabled=!current||busy||waiting||!!recorder||!current.blob&&!current.server?.audio_available;
  byId('voice-save').disabled=!current||busy||waiting||!!recorder||current.server?.state==='queued'||current.server?.state==='transcribing';
  byId('voice-refresh').disabled=!current?.server||busy||waiting;byId('voice-adopt').disabled=!current?.server?.asr_text||busy||waiting||!!recorder;byId('voice-delete').disabled=!current||busy||waiting||!!recorder;
 }
 async function start(){
  // Freeze before any session fetch or microphone permission wait.
  const frozen=root.CWPListening?.anchor();if(!frozen?.anchor.snapshot_id){root.CWPListening?.captureNote();say('当前播放没有可确认的位置，请使用文字笔记。');return;}
  if(waiting||recorder||busy){say('已有录音或权限请求，请先停止或取消。');return;}
  const capture=structuredClone(frozen);delete capture.csrf;
  if(!root.isSecureContext||!navigator.mediaDevices?.getUserMedia||!root.MediaRecorder){root.CWPListening?.captureNote(capture);root.CWPListening?.notify('此环境不支持安全录音，可记录文字笔记。');return;}
  const mime=['audio/webm;codecs=opus','audio/ogg;codecs=opus','audio/mp4','audio/webm','audio/ogg'].find(type=>MediaRecorder.isTypeSupported(type));if(!mime){root.CWPListening?.captureNote(capture);root.CWPListening?.notify('没有兼容的录音格式，可记录文字笔记。');return;}
  const token=epoch,permission=++permissionSeq;waiting=true;pendingCapture=capture;panel.hidden=false;render();say('播放位置已冻结，等待麦克风权限。节目暂不暂停。');
  try{await ensureSession();if(token!==epoch||permission!==permissionSeq)return;if(current){try{await persist();}catch(error){say(error.message);return;}}
   if(permission!==permissionSeq)return;const acquired=await navigator.mediaDevices.getUserMedia({audio:true});if(token!==epoch||permission!==permissionSeq||!waiting){acquired.getTracks().forEach(track=>track.stop());return;}stream=acquired;
   current={id:crypto.randomUUID(),capture,text:'',createdAt:new Date().toISOString(),sessionId:session.session_id,blob:null};chunks=[];recordBytes=0;afterStop=false;text.value='';
   recorder=new MediaRecorder(acquired,{mimeType:mime});
   recorder.onstart=()=>{if(token!==epoch||permission!==permissionSeq)return;root.CWPListening?.state().transport?.pause();startedAt=performance.now();say('录音中：0:00／5:00，0MB／20MB；节目已暂停。');render();timer=setInterval(()=>{const elapsed=(performance.now()-startedAt)/1000;say('录音中：'+fmt(elapsed)+'/5:00，'+(recordBytes/1048576).toFixed(1)+'MB/20MB');if(elapsed>=299)stop(false);},500);};
   recorder.ondataavailable=event=>{if(token!==epoch||!event.data.size)return;chunks.push(event.data);recordBytes+=event.data.size;current.blob=new Blob(chunks,{type:recorder.mimeType});current.interrupted=true;persist().catch(error=>say('录音仍在内存，本地保存失败：'+error.message));if(recordBytes>=maxBytes)stop(false);};
   recorder.onerror=event=>{afterStop=false;say('录音失败，已收到的部分保留为草稿：'+(event.error?.message||'设备错误'));stop(false);};
   recorder.onstop=async()=>{clearInterval(timer);timer=null;acquired.getTracks().forEach(track=>track.stop());stream=null;recorder=null;if(token!==epoch)return;current.blob=new Blob(chunks,{type:mime});current.interrupted=false;chunks=[];render();try{await persist();say('已停止，录音与文字已存本地草稿；节目保持暂停。');}catch(error){say('已停止；本地未保存，请显式上传或复制文字。'+error.message);}if(afterStop){afterStop=false;transcribe();}};
   recorder.start(1000);
  }catch(error){if(token!==epoch||permission!==permissionSeq)return;stream?.getTracks().forEach(track=>track.stop());stream=null;recorder=null;root.CWPListening?.notify('无法录音：'+error.message+'；可以记录文字笔记。');root.CWPListening?.captureNote(capture);say('未开始录音，原播放状态保留。'+error.message);if(!current)panel.hidden=true;}finally{if(permission===permissionSeq){waiting=false;pendingCapture=null;render();}}
 }
 function stop(transcribeAfter){if(waiting&&!recorder){permissionSeq++;waiting=false;pendingCapture=null;render();if(!current)panel.hidden=true;say('已取消权限等待；不会在权限迟到时开始录音。');return;}if(!recorder||recorder.state==='inactive')return;afterStop=transcribeAfter;recorder.stop();}
 async function post(action,fields={}){const auth=await ensureSession();return request('/api/voice-notes/'+encodeURIComponent(current.id),{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':auth.csrf},body:JSON.stringify({action,expected_revision:current.server?.revision,...fields})});}
 async function syncText(){if(!current.server)return;if(current.server.text!==text.value){current.server=await post('edit',{text:text.value});current.text=text.value;}}
 async function upload(){if(current.server)return;if(!current.blob||current.blob.size<=0||current.blob.size>maxBytes)throw Error('没有可上传录音，或录音已超过20MB。草稿仍保留。');const auth=await ensureSession(),body=new FormData();body.set('audio',current.blob,'recording');body.set('draft_id',current.id);body.set('capture',JSON.stringify(current.capture));body.set('text',text.value);current.server=await request('/api/voice-notes/upload',{method:'POST',headers:{'X-CSRF-Token':auth.csrf},body});}
 async function perform(fn,lockEditor=false){if(busy||recorder||waiting||!current)return;busy=true;render();if(lockEditor)text.disabled=true;try{await fn();}catch(error){say(error.message+' 草稿仍保留。');}finally{busy=false;render();}}
 async function transcribe(){return perform(async()=>{say('正在上传，尚未启动转写…');await upload();await syncText();current.server=await post(current.server.state==='failed'?'retry':'transcribe');await persist().catch(error=>say(error.message));say(current.server.state==='transcribed'?'已有转写结果，请核对后采用。':'已明确启动转写；可只读刷新进度。你的文字不会被覆盖。');});}
 async function refresh(){return perform(async()=>{current.server=await request('/api/voice-notes/'+encodeURIComponent(current.id));current.text=text.value;await persist();byId('voice-asr-text').textContent=current.server.asr_text;byId('voice-asr-result').hidden=!current.server.asr_text;say('服务器状态：'+current.server.state+(current.server.asr_base_revision&&current.server.asr_base_revision!==current.server.revision?'；转写基于较早的草稿版本，请核对后采用。':'')+(current.server.error?'；'+current.server.error:''));});}
 async function openServer(id){if(busy||recorder||waiting){say('请先停止录音或等待当前操作结束。');return;}try{if(current)await persist();await ensureSession();const draft=await request('/api/voice-notes/'+encodeURIComponent(id)),local=(await localList()).find(d=>d.id===id);let anchor;try{anchor=JSON.parse(draft.anchor_json);}catch(_){anchor={};}current=local||{id,capture:{sourceType:draft.source_type,sourceId:draft.source_id,title:'原节目',anchor},text:draft.text,createdAt:draft.created_at,blob:null};current.server=draft;panel.hidden=false;render();say('已读取草稿，没有上传或启动转写；原音位置保持冻结。');}catch(error){say(error.message);}}
 async function showLocal(container){try{const list=(await localList()).sort((a,b)=>b.createdAt.localeCompare(a.createdAt));container.replaceChildren();for(const d of list){const button=document.createElement('button');button.type='button';button.textContent=(d.capture?.title||'原节目')+' · '+d.createdAt+(d.interrupted?'（中断录音，请检查可读性）':'');button.onclick=async()=>{if(recorder||waiting||busy)return;try{if(current)await persist();current=d;panel.hidden=false;render();say('仅恢复本地草稿；未上传、未调用AI。');}catch(error){say(error.message);}};container.append(button);}if(!list.length)container.textContent='当前会话没有本地草稿。';}catch(error){container.textContent='读取本地草稿失败：'+error.message;}}
 byId('voice-start').onclick=start;byId('voice-stop-draft').onclick=()=>stop(false);byId('voice-stop-asr').onclick=()=>stop(true);byId('voice-transcribe').onclick=transcribe;byId('voice-refresh').onclick=refresh;
 text.oninput=()=>{if(!current)return;current.text=text.value;persist().then(()=>{if(!recorder&&!busy)say('文字已保留在本地；尚未保存为笔记。');}).catch(error=>say('本地保存失败，请复制文字：'+error.message));};
 byId('voice-resume-play').onclick=()=>{if(recorder||waiting){say('请先停止录音再播放节目。');return;}root.CWPListening?.state().transport?.play();};
 byId('voice-hide').onclick=()=>{if(recorder||waiting){stop(false);return;}if(current)persist().catch(error=>say(error.message));panel.hidden=true;};
 byId('voice-adopt').onclick=()=>perform(async()=>{await syncText();current.server=await post('adopt',{job_id:current.server.job_id});current.text=current.server.text;text.value=current.text;await persist();say('已明确采用转写，仍需编辑核对并保存为笔记。');},true);
 byId('voice-save').onclick=()=>perform(async()=>{await upload();await syncText();const option=byId('voice-question').selectedOptions[0],result=await post('save',{question_id:option.value,question_revision:Number(option.dataset.revision)||0,keep_audio:byId('voice-keep-audio').checked});if(!result.saved)throw Error('未保存');await removeLocal(current.id);say('已保存为个人理解笔记。'+(byId('voice-keep-audio').checked?'录音已私有保留。':'录音已安排删除。'));current=null;text.value='';byId('voice-asr-result').hidden=true;},true);
 byId('voice-delete').onclick=()=>perform(async()=>{if(current.server)await post('delete');await removeLocal(current.id);current=null;text.value='';panel.hidden=true;say('已删除草稿，录音已安排清理；已发生的远端调用与用量记录仍保留。');},true);
 function clearPrivate(){epoch++;permissionSeq++;pendingCapture=null;waiting=false;afterStop=false;clearInterval(timer);timer=null;stream?.getTracks().forEach(track=>track.stop());if(recorder?.state==='recording')recorder.stop();recorder=null;stream=null;chunks=[];recordBytes=0;current=null;session=null;busy=false;requests.forEach(c=>c.abort());db?.close();db=null;dbPromise=null;panel.hidden=true;text.value='';status.textContent='';byId('voice-asr-text').textContent='';byId('voice-anchor').textContent='';byId('voice-asr-result').hidden=true;byId('voice-question').replaceChildren(new Option('不关联',''));try{const req=indexedDB.deleteDatabase(dbName);req.onblocked=()=>{};}catch(_){} }
 render();
 root.CWPVoice={start,clearPrivate};
 // Logout in another tab immediately stops microphone access and clears memory.
 root.addEventListener('storage',event=>{if(event.key==='cwp-private-reset')clearPrivate();});
 root.addEventListener('pagehide',()=>{if(recorder?.state==='recording')stop(false);});
 root.CWPViews.define((view,scope)=>{const content=view.querySelector('#voice-notes-view');if(!content)return;scope.on(content,'click',event=>{const startButton=event.target.closest('[data-voice-action="start"]'),local=event.target.closest('[data-voice-action="local"]'),open=event.target.closest('[data-voice-open]');if(startButton)start();if(local)showLocal(content.querySelector('#voice-local-list'));if(open)openServer(open.dataset.voiceOpen);});});
})(window);
