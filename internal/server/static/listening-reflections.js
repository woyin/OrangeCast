// This editor is owned by the root shell. Page changes cannot change its capture.
(function(root){
 'use strict';
 const panel=document.getElementById('reflection-editor');if(!panel)return;
 const byId=id=>document.getElementById(id),form=byId('reflection-form'),feedback=byId('reflection-feedback'),select=byId('reflection-question');
 const fields=['remember','uncertain','apply'],key='cwp-reflection-draft-v1';
 let current=null,session=null,epoch=0,timer=null,serial=Promise.resolve(),busy=false;
 const requests=new Set();let lifetime=new AbortController();
 const scope={signal:lifetime.signal,async fetch(url,options={}){const controller=new AbortController();requests.add(controller);try{return await fetch(url,{...options,signal:controller.signal,cache:'no-store'});}finally{requests.delete(controller);}}};
 function say(value){feedback.textContent=value;}
 function answers(){return Object.fromEntries(fields.map(f=>[f,byId('reflection-'+f).value]));}
 function storeLocal(){if(!current||!session)return;current.answers=answers();try{localStorage.setItem(key,JSON.stringify({sessionId:session.session_id,current}));}catch(error){say('本地保存失败，请复制文字：'+error.message);}}
 function render(fill=false){
  panel.hidden=!current;if(!current)return;
  const c=current.capture,a=c.anchor;byId('reflection-anchor').textContent=(c.title||'原节目')+' · '+(a.mode==='dj'?'DJ映射原音':'原音')+' '+Number(a.position).toFixed(1)+'秒（已固定）';
  if(fill)for(const f of fields)byId('reflection-'+f).value=current.answers?.[f]||'';
  select.value=current.question_id||'';select.disabled=busy||current.revision>0||!!current.pending;
  for(const f of fields)byId('reflection-'+f).disabled=busy||['save','cancel'].includes(current.pending?.action);
  byId('reflection-save').textContent=current.pending?.action==='save'?'核对并重试原保存请求':'保存为一条个人理解笔记';
  byId('reflection-sync').disabled=busy||['save','cancel'].includes(current.pending?.action);byId('reflection-save').disabled=busy||current.pending?.action==='cancel';
 }
 async function ensureSession(){if(session)return session;const token=epoch;
  const response=await scope.fetch('/api/voice-notes/session');if(response.status===401||response.redirected){root.CWPNavigation?.expire();throw Error('登录已失效');}if(!response.ok)throw Error('暂时无法验证登录会话');
  const value=await response.json();if(token!==epoch)throw Error('会话已变化');session=value;
  select.replaceChildren(new Option('不关联',''));for(const q of value.questions||[]){const option=new Option(q.Body||q.body,q.ID||q.id);option.dataset.revision=q.Revision||q.revision;select.append(option);}return value;
 }
 async function command(cmd){const auth=await ensureSession(),token=epoch;
  const response=await scope.fetch('/api/listening-reflections',{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':auth.csrf},body:JSON.stringify(cmd)});
  if(token!==epoch)throw Error('会话已变化');if(response.status===401||response.redirected){root.CWPNavigation?.expire();throw Error('登录已失效');}
  if(!response.ok)throw Error((await response.text())+' 草稿文字仍保留。');return response.json();
 }
 async function ensureStarted(){if(current.revision>0)return;
  const id=current.id,token=epoch,cmd={action:'start',id,capture:current.capture,question_id:current.question_id||'',question_revision:current.question_revision||0};
  current.pending=cmd;storeLocal();const result=await command(cmd);if(token!==epoch||current?.id!==id)return;
  current.revision=result.revision;current.pending=null;storeLocal();render();
 }
 async function synchronize(){if(!current||['save','cancel'].includes(current.pending?.action))return;await ensureStarted();if(!current)return;
  const id=current.id,token=epoch;
  const cmd=current.pending||{action:'edit',id,request_key:crypto.randomUUID(),expected_revision:current.revision,answers:answers()};
  current.pending=cmd;storeLocal();const result=await command(cmd);if(token!==epoch||current?.id!==id)return;
  if(result.state!=='draft')throw Error('原草稿已失效，请复制文字后重新整理。');
  current.revision=result.revision;current.pending=null;storeLocal();render();say('私人草稿已同步；尚未成为笔记。');
 }
 function enqueueSync(){const run=async()=>{if(!current||busy)return;await synchronize();};const result=serial.then(run,run);serial=result.catch(error=>say('同步未确认，可明确重试；文字与请求身份保留。'+error.message));return result;}
 async function start(captured){const frozen=captured||root.CWPListening?.anchor();if(!frozen?.anchor?.snapshot_id||frozen.anchor.no_position){root.CWPListening?.notify('没有可确认的播放位置，请使用普通文字笔记。');return;}
  if(current){panel.hidden=false;say('已有整理草稿，请先保存或丢弃，再开始新的整理。');return;}
  const capture=structuredClone(frozen);delete capture.csrf;const token=epoch;
  try{await ensureSession();if(token!==epoch||current)return;current={id:crypto.randomUUID(),capture,answers:{},revision:0,question_id:'',question_revision:0,createdAt:Date.now()};render(true);storeLocal();byId('reflection-remember').focus();say('已固定实际播放位置。输入会保留草稿，不会调用AI。');}catch(error){say(error.message);root.CWPListening?.notify(error.message);}
 }
 async function open(id){if(current){panel.hidden=false;say(current.id===id?'已打开原本地草稿，保留尚未同步的文字。':'已有本地整理草稿，请先保存或丢弃。');return;}
  const token=epoch;try{await ensureSession();const response=await scope.fetch('/api/listening-reflections?id='+encodeURIComponent(id));if(!response.ok)throw Error('无法读取整理草稿');const result=await response.json();if(token!==epoch)return;
   if(result.state!=='draft')throw Error('这个整理已保存或已失效');current={...result,createdAt:Date.parse(result.created_at+'Z')};render(true);storeLocal();say('已只读恢复服务器草稿；没有调用AI。');
  }catch(error){say(error.message);root.CWPListening?.notify(error.message);}
 }
 form.addEventListener('submit',async event=>{event.preventDefault();if(!current||busy)return;clearTimeout(timer);await serial;if(!current||busy)return;
  busy=true;render();const id=current.id,token=epoch;
  try{
   if(!current.pending||current.pending.action!=='save'){await synchronize();if(!current||token!==epoch)return;current.pending={action:'save',id,request_key:crypto.randomUUID(),expected_revision:current.revision,answers:answers()};storeLocal();}
   const cmd=structuredClone(current.pending),auth=await ensureSession();
   await root.CWPForms.submit(form,{signal:lifetime.signal,fetch:scope.fetch},{event,feedback,navigate:false,headers:{'X-CSRF-Token':auth.csrf},json:()=>cmd,
    success(result){if(token!==epoch||current?.id!==id)return;current=null;localStorage.removeItem(key);for(const f of fields)byId('reflection-'+f).value='';panel.hidden=true;root.CWPListening?.notify('已保存一条个人理解笔记，可在整理记录中核对。');},
    error(error){if(error.status===409)say('修订或关联问题已改变；文字和原请求保留，请复制并核对服务器草稿。');},
    unknownText:'保存结果未确认；文字和原请求身份保留。再次保存会核对同一个请求。'
   });
  }catch(error){if(token===epoch)say(error.message);}finally{if(token===epoch){busy=false;render();}}
 });
 for(const f of fields)byId('reflection-'+f).addEventListener('input',()=>{storeLocal();clearTimeout(timer);timer=setTimeout(()=>enqueueSync().catch(()=>{}),750);});
 select.addEventListener('change',()=>{if(!current||current.revision>0||current.pending)return;const o=select.selectedOptions[0];current.question_id=o.value;current.question_revision=Number(o.dataset.revision)||0;storeLocal();});
 byId('reflection-sync').onclick=()=>enqueueSync().catch(()=>{});
 byId('reflection-hide').onclick=()=>{storeLocal();panel.hidden=true;};
 byId('reflection-cancel').onclick=async()=>{if(!current||busy||current.pending?.action==='save')return;busy=true;clearTimeout(timer);const token=epoch,id=current.id;await serial;
  try{if(token!==epoch||current?.id!==id)return;if(current.revision>0||current.pending){await ensureStarted();if(token!==epoch||current?.id!==id)return;const cmd=current.pending?.action==='cancel'?current.pending:{action:'cancel',id,request_key:crypto.randomUUID(),expected_revision:current.revision,answers:{}};current.pending=cmd;storeLocal();await command(cmd);}if(token!==epoch)return;current=null;localStorage.removeItem(key);for(const f of fields)byId('reflection-'+f).value='';panel.hidden=true;}
  catch(error){say(error.message);}finally{if(token===epoch){busy=false;render();}}
 };
 function clearPrivate(){epoch++;lifetime.abort();lifetime=new AbortController();clearTimeout(timer);requests.forEach(c=>c.abort());current=null;session=null;busy=false;panel.hidden=true;feedback.textContent='';byId('reflection-anchor').textContent='';for(const f of fields)byId('reflection-'+f).value='';select.replaceChildren(new Option('不关联',''));try{localStorage.removeItem(key);}catch(_){} }
 byId('reflection-start').onclick=()=>start();
 async function adoptVoice(voice,field,target){
  if(!current||busy||current.pending?.action==='save'||(target&&target!==current.id)||(voice.reflection_id&&voice.reflection_id!==current.id))throw Error('请先打开录音所属的原整理草稿');
  if(!fields.includes(field))throw Error('整理栏无效');clearTimeout(timer);await serial;if(!current||busy)throw Error('已有操作进行中');busy=true;render();const token=epoch;
  try{await synchronize();if(token!==epoch||!current)return;const cmd={action:'voice_adopt',id:current.id,request_key:crypto.randomUUID(),expected_revision:current.revision,voice_id:voice.id,voice_revision:voice.revision,field};
   current.pending=cmd;storeLocal();const result=await command(cmd);if(token!==epoch)return;current={...current,...result,pending:null};voice.reflection_id=current.id;render(true);storeLocal();say('已采用语音草稿到所选栏；其他栏保持原文字。');
  }finally{if(token===epoch){busy=false;render();}}
 }
 byId('reflection-voice-start').onclick=async()=>{if(!current||busy)return;clearTimeout(timer);await serial;if(!current||busy)return;busy=true;render();const token=epoch;
  try{await synchronize();if(token!==epoch||!current)return;await root.CWPVoice.start({capture:structuredClone(current.capture),reflectionId:current.id});}catch(error){say(error.message);}finally{if(token===epoch){busy=false;render();}}
 };
 root.CWPReflections={start,open,adoptVoice,clearPrivate};
 root.addEventListener('storage',event=>{if(event.key==='cwp-private-reset')clearPrivate();});
 root.CWPViews.define((view,viewScope)=>{viewScope.on(view,'click',event=>{const startButton=event.target.closest('[data-reflection-start]'),openButton=event.target.closest('[data-reflection-open]');if(startButton)start();if(openButton)open(openButton.dataset.reflectionOpen);});});
 // Restore only after verifying the current session. Reading cannot start a paid job.
 (async()=>{let value;try{value=JSON.parse(localStorage.getItem(key));}catch(_){}if(!value)return;const token=epoch;
  try{const auth=await ensureSession();if(token!==epoch)return;if(value.sessionId!==auth.session_id||Date.now()-value.current.createdAt>7*86400000){localStorage.removeItem(key);return;}current=value.current;render(true);say('已恢复原播放位置和文字；没有自动上传或调用AI。');}catch(error){say(error.message);}
 })();
})(window);
