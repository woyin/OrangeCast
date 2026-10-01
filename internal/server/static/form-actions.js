// Form state belongs to the view; an HTTP wait never owns the background task.
(function(root){
 'use strict';
 const pending=new WeakMap();
 function active(scope){return !scope.signal?.aborted;}
 function errorText(value){return typeof value==='string'?value:(value?.message||value?.error||'操作未完成，请核对原记录。');}
 async function submit(form,scope,options={}){
  const event=options.event;event?.preventDefault();
  if(pending.has(form)||!active(scope))return {state:'busy'};
  if(options.confirm&&!options.confirm(event))return {state:'cancelled'};
  const feedback=options.feedback||form.querySelector('[aria-live]');
  let data;
  // Capture enabled fields and the submitter before changing any disabled state.
  try{options.before?.(event);data=new FormData(form);if(event?.submitter?.name)data.set(event.submitter.name,event.submitter.value);options.prepare?.(data,event);}
  catch(error){if(feedback)feedback.textContent=error.message||'无法读取表单，请复制文字后核对。';return {state:'invalid'};}
  const controls=Array.from(form.querySelectorAll?.('button,input,select,textarea')||[]);
  if(event?.submitter&&!controls.includes(event.submitter))controls.push(event.submitter);
  const disabled=controls.map(control=>Boolean(control.disabled));
  controls.forEach(control=>control.disabled=true);
  pending.set(form,true);
  if(feedback)feedback.textContent=options.pendingText||'处理中…';
  try{
   const response=await scope.fetch(form.getAttribute('action'),{method:'POST',headers:{Accept:'application/json'},body:data});
   if(!active(scope))return {state:'unmounted'};
   if(!response.ok){
    const raw=await response.text();let value;try{value=JSON.parse(raw);}catch(_){value=raw;}
    if(!active(scope))return {state:'unmounted'};
    const code=value?.code||({401:'unauthorized',409:'conflict'}[response.status])||'rejected';
    if(feedback){feedback.textContent=errorText(value);feedback.dataset&&(feedback.dataset.errorCode=code);}
    if(response.status===401){root.CWPPrivate?.clear();options.unauthorized?.();await root.CWPNavigation?.visit('/login');}
    options.error?.({status:response.status,code,message:errorText(value)},data);
    return {state:'rejected',status:response.status,code};
   }
   const value=await response.json();if(!active(scope))return {state:'unmounted'};
   await options.success?.(value,data);
   if(active(scope)&&options.navigate!==false){const href=value.href||value.redirect;if(href)await root.CWPNavigation?.visit(href);}
   return {state:'saved',value};
  }catch(error){
   if(!active(scope))return {state:'unmounted'};
   if(feedback){feedback.textContent=options.unknownText||'请求结果尚未确认，输入和请求身份保留；核对原记录后再明确操作。';feedback.dataset&&(feedback.dataset.errorCode='remote_unknown');}
   options.unknown?.(error,data);
   return {state:'unknown'};
  }finally{pending.delete(form);controls.forEach((control,i)=>control.disabled=disabled[i]);}
 }
 function bind(form,scope,options={}){scope.on(form,'submit',event=>submit(form,scope,{...options,event}));}
 root.CWPForms={submit,bind,isPending:form=>pending.has(form)};
})(window);
