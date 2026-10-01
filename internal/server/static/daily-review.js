window.CWPViews.define(function(view,scope){'use strict';view.querySelectorAll('.daily-review-form').forEach(form=>{
 const key='cwp-daily-review-draft:'+form.elements.session.value+':'+form.elements.item.value,feedback=form.querySelector('.review-feedback');
 try{const draft=JSON.parse(sessionStorage.getItem(key)||'null');if(draft){for(const name of ['answer','assessment','expected_revision','request_key']){if(draft[name]!==undefined)form.elements[name].value=draft[name];}feedback.textContent='已恢复未保存解释；旧版本需核对后提交。';}}catch(_){}
 function save(){try{const draft={};for(const name of ['answer','assessment','expected_revision','request_key'])draft[name]=form.elements[name].value;sessionStorage.setItem(key,JSON.stringify(draft));}catch(_){feedback.textContent='本地草稿未保存，请复制解释后再试。';}}
 scope.on(form,'input',save);scope.on(form,'submit',async event=>{event.preventDefault();save();const data=new FormData(form);data.set('action',event.submitter?.value||'answer');const controls=form.querySelectorAll('button,textarea,select');controls.forEach(e=>e.disabled=true);try{const response=await scope.fetch(form.getAttribute('action'),{method:'POST',body:data,headers:{Accept:'application/json'}});if(!response.ok){feedback.textContent=await response.text();return;}const result=await response.json();sessionStorage.removeItem(key);window.CWPNavigation.visit(result.href);}catch(_){feedback.textContent='请求未确认，解释草稿仍保留；重试复用同一请求身份。';}finally{controls.forEach(e=>e.disabled=false);}});
});
 view.querySelectorAll('#daily-review-view form[method="post"]:not(.daily-review-form)').forEach(form=>{
  const feedback=document.createElement('p');feedback.className='review-feedback';feedback.setAttribute('aria-live','polite');form.append(feedback);
  scope.on(form,'submit',async event=>{event.preventDefault();const data=new FormData(form);if(event.submitter?.name==='action')data.set('action',event.submitter.value);const controls=form.querySelectorAll('button,input,select,textarea');controls.forEach(e=>e.disabled=true);
   try{const response=await scope.fetch(form.getAttribute('action'),{method:'POST',body:data,headers:{Accept:'application/json'}});if(!response.ok){feedback.textContent=await response.text();return;}const result=await response.json();window.CWPNavigation.visit(result.href);}catch(_){feedback.textContent='请求未确认，表单保持原值；重新提交会核对同一请求和修订。';}finally{controls.forEach(e=>e.disabled=false);}
  });
 });
});
