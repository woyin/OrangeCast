// 设置页：用表单里的 key/URL 调 /settings/models 拉取模型列表，填充 datalist；
// 模型输入框随各自 Provider 选择切换候选列表，仍可手动输入。
window.CWPViews.define(function(view,scope){'use strict';
 const form=view.querySelector('.settings-form');if(!form||!form.querySelector('[data-fetch-models]'))return;
 const csrf=form.elements._csrf.value,provider=name=>form.elements[name];
 function listFor(task){
  const select=provider(task+'_provider');let value=select?select.value:'';
  if(!value)value=provider('analysis_provider').value;
  return 'models-'+(value==='openai'?'openai':'groq');
 }
 function bindLists(){form.querySelectorAll('[data-model-input]').forEach(input=>{input.setAttribute('list',listFor(input.name.replace(/_model$/,'')));});}
 form.querySelectorAll('select[name$="_provider"]').forEach(select=>scope.on(select,'change',bindLists));
 bindLists();
 async function fetchModels(name,quiet){
  const status=form.querySelector('[data-models-status="'+name+'"]'),button=form.querySelector('[data-fetch-models="'+name+'"]'),list=form.querySelector('#models-'+name);
  button.disabled=true;status.textContent=' 获取中…';
  try{
   const response=await scope.fetch('/settings/models',{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':csrf},body:JSON.stringify({provider:name,api_key:provider(name+'_api_key').value,base_url:provider(name+'_base_url').value})});
   const data=await response.json();
   if(!response.ok)throw new Error(data.error||('HTTP '+response.status));
   list.replaceChildren(...data.models.map(id=>{const option=document.createElement('option');option.value=id;return option;}));
   status.textContent=' 已获取 '+data.models.length+' 个模型，点击模型输入框选择。';
  }catch(error){
   if(scope.signal.aborted)return;
   status.textContent=quiet?'':' 获取失败：'+error.message;
  }finally{button.disabled=false;}
 }
 form.querySelectorAll('[data-fetch-models]').forEach(button=>scope.on(button,'click',()=>fetchModels(button.dataset.fetchModels,false)));
 form.querySelectorAll('[data-provider-row]').forEach(row=>{if(row.dataset.ready)fetchModels(row.dataset.providerRow,true);});
});
