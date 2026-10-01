window.CWPViews.define(function(view,scope){'use strict';view.querySelectorAll('.review-form').forEach(form=>{
 const key='cwp-review-draft:'+form.elements.item.value,feedback=form.querySelector('.review-feedback');
 try{const draft=JSON.parse(sessionStorage.getItem(key)||'null');if(draft){for(const name of ['answer','assessment','expected_revision'])form.elements[name].value=draft[name];feedback.textContent='已恢复未保存解释。';}}catch(_){}
 function save(){try{sessionStorage.setItem(key,JSON.stringify({answer:form.elements.answer.value,assessment:form.elements.assessment.value,expected_revision:form.elements.expected_revision.value}));}catch(_){}}
 scope.on(form,'input',save);window.CWPForms.bind(form,scope,{feedback,before:save,prepare:(data,event)=>data.set('action',event.submitter?.value||'answer'),success:(_,data)=>{
  if(data.get('action')==='reveal'){form.elements.expected_revision.value=String(Number(data.get('expected_revision'))+1);save();}else{sessionStorage.removeItem(key);}
 }});
});});
