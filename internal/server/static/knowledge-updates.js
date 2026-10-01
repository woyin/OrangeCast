window.CWPViews.define(function(view,scope){
 'use strict';
 view.querySelectorAll('form.update-form').forEach(form=>{
  scope.on(form,'submit',async event=>{
   event.preventDefault();const feedback=form.querySelector('.update-feedback'),button=event.submitter||form.querySelector('button'),body=new URLSearchParams(new FormData(form));
   if(event.submitter?.name)body.set(event.submitter.name,event.submitter.value);if(button)button.disabled=true;
   try{const response=await scope.fetch(form.getAttribute('action'),{method:'POST',headers:{Accept:'application/json'},body});if(!response.ok)throw Error(await response.text());const result=await response.json();if(!scope.signal.aborted)await window.CWPNavigation.visit(result.href);}
   catch(error){if(!scope.signal.aborted)feedback.textContent=error.message||'处理结果未确认，请刷新核对。';}
   finally{if(button&&!scope.signal.aborted)button.disabled=false;}
  });
 });
});
