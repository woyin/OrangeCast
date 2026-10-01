window.CWPViews.define(function(view,scope){
 'use strict';
 const field=(form,name)=>form.elements.namedItem(name),note=view.querySelector('#question-note');
 scope.on(view.querySelector('#question-note-playing'),'click',()=>{
  const frozen=window.CWPListening?.anchor();if(!frozen?.anchor.snapshot_id){note.querySelector('.question-feedback').textContent='当前播放没有可确认的来源快照，请选择来源写无位置笔记。';return;}
  const value=frozen.sourceType+':'+frozen.sourceId,select=field(note,'source');let option=Array.from(select.options).find(o=>o.value===value);
  if(!option){option=document.createElement('option');option.value=value;option.textContent=frozen.title;select.append(option);}select.value=value;
  field(note,'references_json').value=JSON.stringify(frozen.anchor.segment_ids);field(note,'anchor_json').value=JSON.stringify(frozen.anchor);
  view.querySelector('#question-note-anchor').textContent='已冻结 '+frozen.title+' · 原音 '+frozen.anchor.position.toFixed(1)+'秒';field(note,'content').focus();note.dispatchEvent(new Event('input',{bubbles:true}));
 });
 scope.on(note&&field(note,'source'),'change',()=>{field(note,'references_json').value='[]';field(note,'anchor_json').value=JSON.stringify({no_position:true});view.querySelector('#question-note-anchor').textContent='该来源的无位置笔记';note.dispatchEvent(new Event('input',{bubbles:true}));});
 view.querySelectorAll('form.question-form').forEach(form=>{
  const action=field(form,'action')?.value,id=field(form,'question_id')?.value||'create',revision=field(form,'expected_revision')?.value||'0',draftable=['create','edit','note'].includes(action),key='cwp-question-draft:'+id+':'+action+':'+revision;
  const names=['body','goal','theme_id','target_date','content','source','references_json','anchor_json'];
  function save(){if(!draftable)return;const values={};for(const name of names){const el=field(form,name);if(el)values[name]=el.value;}try{sessionStorage.setItem(key,JSON.stringify(values));}catch(_){form.querySelector('.question-feedback').textContent='草稿保存失败，请复制文字。';}}
  if(draftable){try{const raw=sessionStorage.getItem(key);if(raw){const values=JSON.parse(raw);for(const name of names){const el=field(form,name);if(el&&typeof values[name]==='string'){if(el.tagName==='SELECT'&&!Array.from(el.options).some(o=>o.value===values[name]))continue;el.value=values[name];}}form.querySelector('.question-feedback').textContent='已恢复本问题版本的草稿。';}
   for(let i=0;i<sessionStorage.length;i++){const old=sessionStorage.key(i);if(old!==key&&old.startsWith('cwp-question-draft:'+id+':'+action+':')){const details=document.createElement('details'),summary=document.createElement('summary'),text=document.createElement('textarea');summary.textContent='旧问题版本的草稿（可复制，未自动覆盖当前版本）';text.readOnly=true;text.value=sessionStorage.getItem(old);details.append(summary,text);form.append(details);}}
  }catch(_){}scope.on(form,'input',save);scope.on(form,'change',save);}
  scope.on(form,'submit',async event=>{event.preventDefault();if(form.hasAttribute('data-question-delete')&&!confirm('删除这个学习问题及组织关系？底层来源、笔记和文章会保留。'))return;save();
   const feedback=form.querySelector('.question-feedback'),button=event.submitter||form.querySelector('button[type=submit]'),body=new URLSearchParams(new FormData(form));if(event.submitter?.name)body.set(event.submitter.name,event.submitter.value);if(button)button.disabled=true;
   try{const response=await scope.fetch(form.getAttribute('action'),{method:'POST',headers:{Accept:'application/json'},body});if(!response.ok)throw Error(await response.text());const result=await response.json();if(scope.signal.aborted)return;if(draftable)sessionStorage.removeItem(key);await window.CWPNavigation.visit(result.href);
   }catch(error){if(!scope.signal.aborted)feedback.textContent=error.message||'保存结果未确认，请刷新核对后再操作。';}finally{if(button&&!scope.signal.aborted)button.disabled=false;}
  });
 });
});
