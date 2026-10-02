window.CWPViews.define(function(view,scope){
 'use strict';
 const root=view.querySelector('#question-study-view');if(!root)return;
 const form=root.querySelector('#question-study-ask'),input=form?.elements.namedItem('input'),keyInput=form?.elements.namedItem('request_key');
 const key='cwp-question-study-draft:'+root.dataset.session;
 if(form){
  try{const saved=JSON.parse(sessionStorage.getItem(key)||'null');if(saved){input.value=saved.input;keyInput.value=saved.requestKey;}}catch(_){}
  const save=()=>{try{sessionStorage.setItem(key,JSON.stringify({input:input.value,requestKey:keyInput.value}));}catch(_){form.querySelector('[aria-live]').textContent='草稿保存失败，请复制文字。';}};
  scope.on(input,'input',save);
  window.CWPForms.bind(form,scope,{encoding:'urlencoded',before:save,success:()=>sessionStorage.removeItem(key)});
 }
 root.querySelectorAll('.question-study-form').forEach(item=>{if(item!==form)window.CWPForms.bind(item,scope,{encoding:'urlencoded'});});
 root.querySelectorAll('.question-study-claim').forEach(claim=>{
  const draft=()=>{const refs=Array.from(claim.querySelectorAll('a')).map(a=>a.textContent+' '+a.href);return '[AI 辅助问答草稿 · '+claim.dataset.kind+'；未经你确认]\n'+claim.dataset.text+(refs.length?'\n\n冻结依据：\n'+refs.join('\n'):'');};
  scope.on(claim.querySelector('[data-study-copy]'),'click',async()=>{const feedback=claim.querySelector('[aria-live]');try{await navigator.clipboard.writeText(draft());if(!scope.signal.aborted)feedback.textContent='已复制，尚未保存为笔记。';}catch(_){if(!scope.signal.aborted)feedback.textContent='复制失败，请手动选择正文。';}});
  scope.on(claim.querySelector('[data-study-understanding]'),'click',async()=>{try{sessionStorage.setItem('cwp-question-study-adopt:'+root.dataset.question,JSON.stringify({content:draft(),session:root.dataset.session}));await window.CWPNavigation?.visit('/questions/'+encodeURIComponent(root.dataset.question)+'/understandings');}catch(_){claim.querySelector('[aria-live]').textContent='理解草稿采用失败，请复制文字。';}});
  scope.on(claim.querySelector('[data-study-adopt]'),'click',async()=>{const feedback=claim.querySelector('[aria-live]');try{sessionStorage.setItem('cwp-question-study-adopt:'+root.dataset.question,JSON.stringify({content:draft(),session:root.dataset.session}));await window.CWPNavigation?.visit('/questions/'+encodeURIComponent(root.dataset.question)+'#question-note');}catch(_){feedback.textContent='草稿采用失败，请复制文字。';}});
 });
 let busy=false;const feedback=root.querySelector('#question-study-poll');
 if(feedback)scope.interval(async()=>{
  if(busy||document.hidden)return;busy=true;
  try{
   const response=await scope.fetch(location.pathname+'?session='+encodeURIComponent(root.dataset.session),{headers:{Accept:'application/json'},cache:'no-store'});
   if(scope.signal.aborted)return;
   if(response.status===401){window.CWPPrivate?.clear();await window.CWPNavigation?.visit('/login');return;}
   if(!response.ok){feedback.textContent='状态暂未更新，输入保留。';return;}
   const value=await response.json();if(scope.signal.aborted)return;
   for(const turn of value.turns){const row=Array.from(root.querySelectorAll('[data-turn-id]')).find(n=>n.dataset.turnId===turn.ID);if(row)row.querySelector('[data-turn-state]').textContent=turn.State;}
   feedback.textContent='状态已更新；请明确刷新查看新回答，下一问草稿保持。';
  }catch(_){if(!scope.signal.aborted)feedback.textContent='状态暂未更新，输入保留。';}finally{busy=false;}
 },5000);
});
