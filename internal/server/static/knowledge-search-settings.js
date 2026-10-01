window.CWPViews.define(function(view,scope){'use strict';
 const form=view.querySelector('#embedding-preflight-form');if(!form)return;
 const link=view.querySelector('#embedding-preflight-job');let pending=null,lookup=false,busy=false;
 function confirmed(state){pending=null;lookup=false;form.elements.request_key.value=crypto.randomUUID();link.href='/automation/'+encodeURIComponent(state.job_id);link.hidden=false;form.querySelector('[aria-live]').textContent='预检任务已确认；请查看任务结果，索引不会自动开启。';}
 window.CWPForms.bind(form,scope,{navigate:false,headers:{'X-CSRF-Token':form.elements._csrf.value},json:data=>{pending=pending||{action:'preflight',request_key:data.get('request_key')};return pending;},success:confirmed,unknown:()=>{lookup=true;},error:state=>{if(state.status===409){pending=null;lookup=false;form.elements.request_key.value=crypto.randomUUID();}}});
 scope.interval(async function(){if(!pending||!lookup||busy||document.hidden)return;busy=true;try{const response=await scope.fetch('/api/knowledge-semantic?request_action=preflight&request_key='+encodeURIComponent(pending.request_key),{cache:'no-store'});if(response.ok){const state=await response.json();if(!scope.signal.aborted)confirmed(state);}}catch(_){}finally{busy=false;}},3000);
});
