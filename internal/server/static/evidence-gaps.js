window.CWPViews.define(function(view,scope){
 'use strict';
 const root=view.querySelector('#evidence-gaps-view');if(!root)return;
 root.querySelectorAll('.evidence-gap-form').forEach(form=>{
  const action=form.elements.namedItem('action').value,gap=form.elements.namedItem('gap_id')?.value||'new';
  const key='cwp-evidence-gap-draft:'+root.dataset.question+':'+gap+':'+action;
  const names=['explanation','kind','query','comment','state','semantic','config_id'];
  const fields=names.map(name=>[name,form.elements.namedItem(name)]).filter(pair=>pair[1]);
  const feedback=form.querySelector('.gap-feedback');
  try{const saved=JSON.parse(sessionStorage.getItem(key)||'null');if(saved)fields.forEach(([name,field])=>{if(typeof saved[name]==='string')field.value=saved[name];});}catch(_){if(feedback)feedback.textContent='私人草稿无法恢复，请核对文字。';}
  const save=()=>{try{sessionStorage.setItem(key,JSON.stringify(Object.fromEntries(fields.map(([name,field])=>[name,field.value]))));}catch(_){if(feedback)feedback.textContent='私人草稿无法保存，请复制文字。';}};
  fields.forEach(([,field])=>scope.on(field,'input',save));
  window.CWPForms.bind(form,scope,{encoding:'urlencoded',feedback,before:save,error:()=>{if(!scope.signal?.aborted&&feedback?.isConnected){feedback.tabIndex=-1;feedback.focus();}},success:()=>sessionStorage.removeItem(key)});
 });
});
