window.CWPViews.define(function(view,scope){
 'use strict';
 const owner=window.CWPListening,spec=owner.specFrom(view),panel=view.querySelector('#listening-queue');let revision=panel?Number(panel.dataset.revision):null,queue=null;
 const feedback=panel?.querySelector('#queue-feedback');
 async function refresh(){if(!panel)return;try{const q=await owner.readQueue();if(scope.signal.aborted)return;queue=q;revision=q.revision;render(q);}catch(error){if(feedback&&!scope.signal.aborted)feedback.textContent=error.message;}}
 function render(q){const list=panel.querySelector('#queue-items');list.replaceChildren();panel.querySelector('#queue-autoplay').checked=q.autoplay;
  if(!q.items.length){const li=document.createElement('li');li.textContent='队列为空，在节目或DJ页面添加稍后听。';list.append(li);return;}
  q.items.forEach(item=>{const li=document.createElement('li');li.dataset.queueId=item.id;const link=document.createElement('a');link.href=owner.urlFor({sourceType:item.source_type,sourceId:item.source_id,mode:item.mode,planId:item.plan_id,planVersion:item.plan_version});link.textContent=item.title;li.append(link,document.createTextNode(' · '+(item.mode==='dj'?'DJ精听':'原音')+(q.current_item_id===item.id?' · 当前':'')));
   if(item.reason){const reason=document.createElement('p');reason.className='meta';reason.textContent=item.reason;li.append(reason);}
   for(const [action,label] of [['play','立即播放'],['up','上移'],['down','下移'],['move_last','移到末尾'],['remove','移除']]){const button=document.createElement('button');button.type='button';button.dataset.queueAction=action;button.textContent=label;button.disabled=action==='play'&&!item.available;li.append(button);}list.append(li);});
 }
 scope.on(panel,'click',async event=>{const button=event.target.closest('[data-queue-action]');if(!button||!queue)return;const action=button.dataset.queueAction,id=button.closest('[data-queue-id]')?.dataset.queueId;button.disabled=true;
  try{if(action==='play'){await owner.playQueue(queue.items.find(i=>i.id===id),queue);await refresh();return;}
   let change={action,item_id:id||''};if(action==='up'||action==='down'){const ids=queue.items.map(i=>i.id),index=ids.indexOf(id),next=index+(action==='up'?-1:1);if(next<0||next>=ids.length)return;[ids[index],ids[next]]=[ids[next],ids[index]];change={action:'reorder',order:ids};}
   const q=await owner.changeQueue(change,revision);queue=q;revision=q.revision;render(q);feedback.textContent='队列已保存';if((action==='clear'||action==='remove'&&owner.state().queueId===id)&&owner.state().queueId)owner.stop();
  }catch(error){feedback.textContent=error.message;await refresh();}finally{button.disabled=false;}
 });
 scope.on(panel?.querySelector('#queue-autoplay'),'change',async event=>{try{const q=await owner.changeQueue({action:'autoplay',autoplay:event.target.checked},revision);queue=q;revision=q.revision;render(q);feedback.textContent=q.autoplay?'已开启结束后自动连播':'自动连播已关闭';}catch(error){feedback.textContent=error.message;await refresh();}});
 view.querySelectorAll('[data-queue-add]').forEach(button=>scope.on(button,'click',async()=>{if(!spec){button.textContent='当前没有播放来源';return;}button.disabled=true;try{const q=await owner.readQueue();await owner.changeQueue({action:'add',source_type:spec.sourceType,source_id:spec.sourceId,mode:button.dataset.queueAdd,plan_id:spec.planId,plan_version:spec.planVersion},q.revision);button.textContent='已加入稍后听';}catch(error){button.textContent=error.message;}finally{button.disabled=false;}}));
 if(panel){refresh();scope.interval(()=>{if(!document.hidden)refresh();},15000);}
});
