window.CWPViews.define(function(view,scope){
 'use strict';
 const pl=view.querySelector('#playlist');if(!pl)return;
 const owner=window.CWPListening,spec=owner.specFrom(view);
 view.querySelectorAll('#dj-evidence,#dj-narration').forEach(media=>{media.pause();media.removeAttribute('src');media.remove();});
 const el=id=>view.querySelector('#'+id),on=scope.on;
 const names={idle:'待机',loading:'加载中…',playing:'播放中',paused:'已暂停',buffering:'缓冲中…',ended:'连播结束',error:'出错，跳过该段'};
 function action(fn){owner.act(spec,state=>{if(state.player)fn(state);});}
 on(el('dj-autoplay'),'click',()=>action(s=>{s.player.load(spec.items);s.transport.play();}));
 on(el('dj-pause'),'click',()=>{if(owner.same(owner.state().spec,spec))owner.state().transport.pause();});
 on(el('dj-prev'),'click',()=>action(s=>s.player.prev()));on(el('dj-next'),'click',()=>action(s=>s.player.next()));
 on(el('dj-stop'),'click',()=>{if(owner.same(owner.state().spec,spec)){owner.stop();}});
 on(el('dj-rate'),'change',()=>action(s=>s.transport.setRate(Number(el('dj-rate').value))));
 on(el('dj-full'),'click',()=>{const current=owner.state();const position=owner.same(current.spec,spec)?current.time:0;const original={...spec,mode:'original',planId:'',planVersion:0,items:[]};owner.act(original,s=>{s.transport.seek(position);s.transport.play();});});
 view.querySelectorAll('.dj-play,.dj-narration-play').forEach(button=>on(button,'click',()=>{const node=button.closest('.dj-item'),kind=button.classList.contains('dj-play')?'evidence':'narration';const items=spec.items.filter(item=>item.idx===node.dataset.idx&&item.type===kind);action(s=>s.player.playItems(items));}));
 const controls=el('dj-autoplay')?.parentNode;
 if(controls){const sleep=document.createElement('select');sleep.setAttribute('aria-label','睡眠定时');[0,15,30,60].forEach(n=>{const opt=document.createElement('option');opt.value=n;opt.textContent=n?n+'分钟后暂停':'睡眠定时关闭';sleep.append(opt);});on(sleep,'change',()=>action(s=>s.transport.setSleep(Number(sleep.value))));controls.append(sleep);
  [-15,15].forEach(n=>{const button=document.createElement('button');button.type='button';button.textContent=(n>0?'+':'')+n+'秒';on(button,'click',()=>action(s=>s.transport.skip(n)));controls.append(button);});
  const resume=document.createElement('button');resume.type='button';resume.id='dj-resume';resume.textContent='继续听（上次位置）';resume.hidden=true;on(resume,'click',()=>{const s=owner.state();if(owner.same(s.spec,spec))s.resume?.run();});controls.append(resume);
 }
 let currentItem=null;
 scope.cleanup(owner.subscribe(state=>{const owns=owner.same(state.spec,spec),status=owns?state.player?.state():'idle';currentItem=owns?state.player?.position().item:null;
  el('dj-status').textContent=owns?(names[status]||'待机'):(state.spec?'正在听 '+state.spec.title+'，点击播放切换到本清单。':'待机');
  for(const id of ['dj-pause','dj-prev','dj-next','dj-stop'])el(id).disabled=!owns||status==='idle';
  el('dj-autoplay').disabled=owns&&['playing','loading','buffering'].includes(status);
  el('dj-pin').disabled=!currentItem?.segmentIds?.length;el('dj-note').disabled=!currentItem?.segmentIds?.length;
  if(owns)el('dj-rate').value=String(state.audio.playbackRate);
  const resume=el('dj-resume');if(resume)resume.hidden=!owns||!state.resume;
  view.querySelectorAll('.dj-item').forEach(node=>{node.style.outline=currentItem&&node.dataset.idx===currentItem.idx?'2px solid var(--accent,#333)':'';});
  el('dj-progress').textContent=currentItem?'进度：'+(state.player.position().index+1)+' / '+spec.items.length+' 段 · '+(currentItem.type==='narration'?'AI 解说':'原音区间'):'';
 }));
 on(el('dj-note'),'click',()=>{if(owner.same(owner.state().spec,spec))owner.captureNote();});
 on(el('dj-pin'),'click',async()=>{if(!owner.same(owner.state().spec,spec)||!currentItem?.segmentIds?.length)return;const ids=currentItem.segmentIds.slice(),feedback=el('dj-action-feedback');const data=new URLSearchParams({_csrf:spec.csrf,source_type:spec.sourceType,source_id:spec.sourceId,segment_ids:JSON.stringify(ids)});
  try{const response=await scope.fetch('/api/pin',{method:'POST',body:data});feedback.textContent=response.ok?'已收藏':'收藏失败（'+response.status+'）';}catch(error){if(!scope.signal.aborted)feedback.textContent='收藏失败，稍后重试。';}
 });
});
