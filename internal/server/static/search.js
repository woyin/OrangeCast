window.CWPViews.define(function(view,scope){'use strict';
 const root=view.querySelector('#semantic-search');if(!root)return;
 const form=root.querySelector('#semantic-query-form'),retry=root.querySelector('#semantic-retry-form');if(!form)return;
 const status=root.querySelector('#semantic-search-status'),link=root.querySelector('#semantic-use-results'),jobLink=root.querySelector('#semantic-job-link');
 const confirmation=retry.querySelector('#semantic-unknown-confirm'),checkbox=confirmation.querySelector('input');
 let jobID=root.dataset.jobId||'',lookup=false,busy=false,pendingQuery=null,pendingRetry=null;
 function resultsLink(){const url=new URL(location.href);url.searchParams.set('q',form.elements.query.value);url.searchParams.set('semantic','1');url.searchParams.set('embedding_config',form.elements.config_id.value);url.searchParams.delete('page');link.href=url.pathname+url.search;}
 resultsLink();
 function render(state){
  if(scope.signal.aborted)return;jobID=state.job_id;lookup=false;
  status.textContent=state.query_ready?'当前查询已准备；点击链接使用本地语义结果。':state.error||(state.status==='failed'?'任务尚未完成，请核对后明确恢复。':state.status==='succeeded'?'查询缓存已失效；重新准备前继续使用FTS。':'正在后台准备；本页结果保持可用。');
  link.hidden=!state.query_ready;jobLink.href='/automation/'+encodeURIComponent(jobID);jobLink.hidden=false;
  form.hidden=!state.query_ready&&state.status!=='succeeded';
  retry.hidden=state.status!=='failed';retry.elements.job_id.value=jobID;if(!pendingRetry)retry.elements.expected_revision.value=state.control_revision;
  confirmation.hidden=state.known_response||!state.remote_started;checkbox.required=!confirmation.hidden;
  if(confirmation.hidden&&!pendingRetry)checkbox.checked=false;
  retry.querySelector('button').textContent=state.known_response?'复用已保存响应':state.remote_started?'核对后重试，可能再次计费':'明确重试未外发任务';
 }
 function clearPending(target){if(target===form)pendingQuery=null;else pendingRetry=null;target.elements.request_key.value=crypto.randomUUID();}
 function bind(target,command){window.CWPForms.bind(target,scope,{navigate:false,headers:{'X-CSRF-Token':target.elements._csrf.value},json:data=>{if(target===form){pendingQuery=pendingQuery||command(data);return pendingQuery;}pendingRetry=pendingRetry||command(data);return pendingRetry;},success:state=>{clearPending(target);render(state);},error:state=>{if(state.status===409)clearPending(target);},unknown:()=>{lookup=true;}});}
 bind(form,data=>({action:'query',config_id:data.get('config_id'),query:data.get('query'),request_key:data.get('request_key')}));
 bind(retry,data=>({action:'retry',job_id:data.get('job_id'),expected_revision:Number(data.get('expected_revision')),request_key:data.get('request_key'),allow_unknown:data.get('allow_unknown')==='1'}));
 async function poll(){
  if(busy||document.hidden||(!jobID&&!lookup))return;busy=true;
  try{const url=new URL('/api/knowledge-semantic',location.origin);if(lookup&&(pendingQuery||pendingRetry)){const pending=pendingRetry||pendingQuery;url.searchParams.set('request_key',pending.request_key);url.searchParams.set('request_action',pendingRetry?'retry':'query');}else if(jobID)url.searchParams.set('job_id',jobID);else{url.searchParams.set('config_id',form.elements.config_id.value);url.searchParams.set('query',form.elements.query.value);}
   const response=await scope.fetch(url.pathname+url.search,{cache:'no-store',headers:{Accept:'application/json'}});if(!response.ok)return;const state=await response.json();if(scope.signal.aborted)return;if(lookup&&(pendingQuery||pendingRetry)){const target=pendingRetry?retry:form;clearPending(target);target.querySelector('[aria-live]').textContent='任务已确认。';}render(state);
  }catch(_){if(!scope.signal.aborted)status.textContent='状态暂未确认；输入、请求身份和本地搜索结果保留。';}finally{busy=false;}
 }
 scope.interval(poll,3000);if(jobID)poll();
});
