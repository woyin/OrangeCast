(function(){
 'use strict';
 const state=document.getElementById('article-state');if(!state)return;
 const mode=document.getElementById('article-mode');mode?.addEventListener('change',()=>document.querySelectorAll('.article-evidence').forEach(el=>{el.open=mode.value==='evidence';}));
 document.querySelectorAll('.knowledge-form').forEach(form=>{
  const draftKey='cwp-article-draft:'+state.dataset.id+':'+(form.elements.namedItem('action')?.value||'revise');
  const save=()=>{const fields=[];Array.from(form.elements).forEach((el,i)=>{if(el.name&&el.name!=='_csrf')fields.push({i,value:el.value,checked:el.checked});});try{sessionStorage.setItem(draftKey,JSON.stringify(fields));}catch(_){} };
  try{const draft=JSON.parse(sessionStorage.getItem(draftKey)||'null');draft?.forEach(field=>{const el=form.elements[field.i];if(el&&el.name!=='_csrf'){el.value=field.value;if(el.type==='checkbox')el.checked=field.checked;}});}catch(_){}
  form.addEventListener('input',save);
  form.addEventListener('submit',async event=>{
   event.preventDefault();save();const data=new FormData(form);if(event.submitter?.name)data.set(event.submitter.name,event.submitter.value);
   const feedback=form.querySelector('.knowledge-feedback');feedback.textContent='处理中…';event.submitter.disabled=true;
   try{const response=await fetch(form.action,{method:'POST',body:data,headers:{Accept:'application/json'}});if(!response.ok){feedback.textContent=await response.text();return;}
    const result=await response.json();sessionStorage.removeItem(draftKey);if(data.get('action')==='feedback'){feedback.textContent='反馈已记录，偏好未自动改变。';}else{location.assign(result.redirect);}
   }catch(_){feedback.textContent='请求未完成，输入仍保留。';}finally{event.submitter.disabled=false;}
  });
 });
 const active=['discover','write','review','revise','review_final'];let original;
 const timer=setInterval(async()=>{try{const response=await fetch('/knowledge-articles/'+encodeURIComponent(state.dataset.id)+'?state=1');if(!response.ok)return;const value=await response.json();if(!original)original=value.Status;
  if(!active.includes(value.Status)){clearInterval(timer);const output=document.getElementById('article-update');output.textContent='后台状态：'+value.Status+'。';const link=document.createElement('a');link.href='/knowledge-articles/'+encodeURIComponent(value.ID);link.textContent='查看最新结果';output.appendChild(link);}
 }catch(_){}},5000);
})();
