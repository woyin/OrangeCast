window.CWPViews.define(function(view,scope){
 'use strict';
 const state=view.querySelector('#' + 'article-state');if(!state)return;
 const drafts=window.CWPArticleDrafts;
 const mode=view.querySelector('#' + 'article-mode');scope.on(mode,'change',()=>view.querySelectorAll('.article-evidence').forEach(el=>{el.open=mode.value==='evidence';}));
 view.querySelectorAll('.knowledge-form').forEach(form=>{
  const purpose=form.dataset.draftPurpose||form.elements.namedItem('action')?.value||'process';
  const parent={article:state.dataset.id,revision:form.elements.namedItem('expected_revision')?.value||'',hash:form.dataset.parentHash||'',purpose};
  const draftKey=drafts.prefix+parent.article+':'+purpose;
  const legacyKey='cwp-article-draft:'+parent.article+':'+(purpose==='process'?'revise':purpose);
  let draft=null;
  const currentKey=draftKey+':current:'+parent.revision+':'+parent.hash;
  let currentDraft=null,recoveryPending=false;
  const feedback=form.querySelector('.knowledge-feedback');
  const save=()=>{try{if(recoveryPending){currentDraft=drafts.capture(form,parent,currentDraft);localStorage.setItem(currentKey,JSON.stringify(currentDraft));}else{draft=drafts.capture(form,parent,draft);localStorage.setItem(draftKey,JSON.stringify(draft));}}catch(_){feedback.textContent='本地草稿保存失败，请先复制文字。';}};
  try{draft=JSON.parse(localStorage.getItem(draftKey)||sessionStorage.getItem(legacyKey)||'null');}catch(_){}
  const result=drafts.restore(form,draft,parent,false);
  recoveryPending=result.conflict||result.legacy;
  if(recoveryPending){try{currentDraft=JSON.parse(localStorage.getItem(currentKey)||'null');drafts.restore(form,currentDraft,parent,false);}catch(_){}}
  if(result.conflict||result.legacy||result.missing.length){
   const recovery=document.createElement('details');recovery.className='panel';recovery.open=true;
   const summary=document.createElement('summary');summary.textContent=result.legacy?'发现旧格式草稿，请复制后比对编辑':'草稿的父稿或材料已变化，请比对后处理';recovery.appendChild(summary);
   const old=document.createElement('textarea');old.rows=6;old.readOnly=true;old.setAttribute('aria-label','保留的旧草稿');
   old.value=Array.isArray(draft)?draft.map(v=>v.value||'').join('\n'):Object.entries(draft?.fields||{}).map(([key,v])=>key+'\n'+(v.checked!==undefined?(v.checked?'已选择':'未选择'):Array.isArray(v.value)?v.value.join(', '):v.value)).join('\n\n');recovery.appendChild(old);
   const hint=document.createElement('p');hint.textContent=result.missing.length?'部分材料/字段已不在当前表单中，旧选择仍保留在草稿。':'当前正文与旧输入同时保留；不会自动提交。';recovery.appendChild(hint);
   if(!result.legacy){const merge=document.createElement('button');merge.type='button';merge.textContent='明确合并仍可匹配的字段';scope.on(merge,'click',()=>{drafts.restore(form,draft,parent,true);draft=null;recoveryPending=false;save();feedback.textContent='已合并可匹配字段，请检查后保存。';});recovery.appendChild(merge);}
   const discard=document.createElement('button');discard.type='button';discard.textContent='明确丢弃这份旧草稿';scope.on(discard,'click',()=>{localStorage.removeItem(currentKey);if(!recoveryPending){localStorage.removeItem(draftKey);sessionStorage.removeItem(legacyKey);}draft=null;recoveryPending=false;recovery.remove();save();});recovery.appendChild(discard);
   form.before(recovery);
  }
  scope.on(form,'input',()=>{
   // Keep an incompatible old draft intact until an explicit merge/discard.
   save();if(recoveryPending)feedback.textContent='旧草稿与当前输入分别保留，请比对后处理。';
  });
  scope.on(form,'submit',async event=>{
   event.preventDefault();save();
   const data=new FormData(form);if(event.submitter?.name)data.set(event.submitter.name,event.submitter.value);
   feedback.textContent='处理中…';const button=event.submitter;if(button)button.disabled=true;
   try{const response=await scope.fetch(form.getAttribute('action'),{method:'POST',body:data,headers:{Accept:'application/json'}});if(!response.ok){feedback.textContent=await response.text();return;}
    const value=await response.json();localStorage.removeItem(currentKey);if(!recoveryPending){localStorage.removeItem(draftKey);sessionStorage.removeItem(legacyKey);}draft=null;
    if(data.get('action')==='feedback'){feedback.textContent='反馈已记录，偏好未自动改变。';}else{window.CWPNavigation.visit(value.redirect);}
   }catch(_){feedback.textContent='请求未完成，输入仍保留。';}finally{if(button)button.disabled=false;}
  });
 });
 const active=['discover','select','write','review','revise','review_final'];
 const timer=scope.interval(async()=>{if(document.hidden)return;try{const response=await scope.fetch('/knowledge-articles/'+encodeURIComponent(state.dataset.id)+'?state=1');if(!response.ok)return;const value=await response.json();
  if(!active.includes(value.Status)){clearInterval(timer);const output=view.querySelector('#' + 'article-update');output.textContent='后台状态：'+value.Status+'。';const link=document.createElement('a');link.href='/knowledge-articles/'+encodeURIComponent(value.ID);link.textContent='查看最新结果';output.appendChild(link);}
 }catch(_){}},5000);

});
