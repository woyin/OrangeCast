window.CWPViews.define(function(view,scope){
 'use strict';
 const root=view.querySelector('#quality-cases-view');if(!root)return;
 root.querySelectorAll('.quality-form').forEach(form=>{
  const field=name=>form.elements.namedItem(name);
  const action=field('action')?.value||'';
  const identity=[action,field('case_id')?.value||'',field('article_id')?.value||'',field('revision')?.value||'',field('paragraph_index')?.value||''].join(':');
  const draftKey='cwp-quality-case-draft:'+identity+':'+(form.dataset?.draftSuffix||'');
  const names=['feedback_id','version','expected','classification','evidence','category','comment','request_key'];
  const feedback=form.querySelector('.quality-status');
  try{const saved=JSON.parse(sessionStorage.getItem(draftKey)||'null');if(saved)names.forEach(name=>{const input=field(name);if(input&&typeof saved[name]==='string'){if(input.tagName==='SELECT'&&saved[name]&&!Array.from(input.options).some(option=>option.value===saved[name]))input.add(new Option('此前选择的反馈（当前列表未包含）',saved[name]));input.value=saved[name];}});}catch(_){feedback.textContent='私人草稿无法恢复，请核对输入。';}
  const save=()=>{const values={};names.forEach(name=>{const input=field(name);if(input)values[name]=input.value;});try{sessionStorage.setItem(draftKey,JSON.stringify(values));}catch(_){feedback.textContent='私人草稿无法保存，请复制文字。';}};
  names.forEach(name=>{const input=field(name);if(input)scope.on(input,'input',save);});
  window.CWPForms.bind(form,scope,{encoding:'urlencoded',navigate:false,feedback,
   before:()=>{const request=field('request_key');if(request&&!request.value)request.value=crypto.randomUUID();save();},
   success:value=>{
    sessionStorage.removeItem(draftKey);const result=value.result;
    feedback.textContent='已保存。'+(typeof result==='string'?'反馈 ID：'+result:result?.ID?'案例 ID：'+result.ID:'');
    if(action==='feedback'&&typeof result==='string') {const accept=root.querySelector('[data-quality-accept]');if(accept){const select=accept.elements.namedItem('feedback_id');if(!Array.from(select.options).some(option=>option.value===result))select.add(new Option('刚记录的原版本反馈',result));select.value=result;accept.elements.namedItem('version').value='0';}}
    if(action==='accept'&&result?.Version){field('version').value=String(result.Version);field('request_key').value=crypto.randomUUID();}
   },error:state=>{if(state.status===409)feedback.textContent='原版本冲突，输入和请求身份已保留。请核对原版本后再操作。';},
   unknownText:'请求结果尚未确认，输入和请求身份已保留；核对原记录后再明确重试。'
  });
 });
});
