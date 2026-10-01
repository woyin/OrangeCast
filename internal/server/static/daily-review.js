window.CWPViews.define(function(view,scope){'use strict';view.querySelectorAll('.daily-review-form').forEach(form=>{
 const key='cwp-daily-review-draft:'+form.elements.session.value+':'+form.elements.item.value,feedback=form.querySelector('.review-feedback');
 try{const draft=JSON.parse(sessionStorage.getItem(key)||'null');if(draft){for(const name of ['answer','assessment','expected_revision','request_key']){if(draft[name]!==undefined)form.elements[name].value=draft[name];}feedback.textContent='已恢复未保存解释；旧版本需核对后提交。';}}catch(_){}
 function save(){try{const draft={};for(const name of ['answer','assessment','expected_revision','request_key'])draft[name]=form.elements[name].value;sessionStorage.setItem(key,JSON.stringify(draft));}catch(_){feedback.textContent='本地草稿未保存，请复制解释后再试。';}}
 scope.on(form,'input',save);window.CWPForms.bind(form,scope,{feedback,before:save,prepare:(data,event)=>data.set('action',event.submitter?.value||'answer'),success:()=>sessionStorage.removeItem(key)});
});
 view.querySelectorAll('#daily-review-view form[method="post"]:not(.daily-review-form)').forEach(form=>{
  const feedback=document.createElement('p');feedback.className='review-feedback';feedback.setAttribute('aria-live','polite');form.append(feedback);
  window.CWPForms.bind(form,scope,{feedback});
 });
});
