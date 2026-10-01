window.CWPViews.define(function(view,scope){
 'use strict';
 const panel=view.querySelector('#' + 'owner-notes');if(!panel)return;
 const form=view.querySelector('#quick-note-form');
 const preview=view.querySelector('#' + 'note-anchor-preview');let selected=[];
 function field(name){return form.elements.namedItem(name);}
 function capture(ids,position,text,kind){
  selected=ids;field('kind').value=kind;
  field('citations_json').value=JSON.stringify(kind==='source_note'?ids:[]);
  field('references_json').value=JSON.stringify(kind==='owner_reflection'?ids:[]);
  field('anchor_json').value=JSON.stringify({snapshot_id:panel.dataset.snapshot||'',version:Number(panel.dataset.version)||0,position:position,segment_ids:ids});
  preview.textContent=(panel.dataset.sourceType==='document'?'段落 ':'音频位置 ')+position+(text?' · '+text:'');
  field('content').focus();saveDraft(form);
 }
 view.querySelectorAll('.note-segment').forEach(btn=>scope.on(btn,'click',()=>{
  const id=btn.dataset.segmentId,node=view.querySelector('[data-id="'+CSS.escape(id)+'"]')||view.querySelector('#' + id);
  capture([id],Number(btn.dataset.position)||0,node?.textContent||'已选原文','source_note');
 }));
 scope.on(view.querySelector('#note-now'),'click',()=>{
  const state=window.CWPListening.state();
  if(!state.spec||state.spec.sourceType!==panel.dataset.sourceType||state.spec.sourceId!==panel.dataset.sourceId){
   selected=[];field('kind').value='owner_reflection';field('citations_json').value='[]';field('references_json').value='[]';field('anchor_json').value=JSON.stringify({no_position:true});
   preview.textContent='记录当前浏览来源的无音频位置笔记。';field('content').focus();saveDraft(form);return;
  }
  const frozen=window.CWPListening.anchor();if(!frozen?.anchor.snapshot_id){preview.textContent='没有可确认的播放锚点，可以记录无位置笔记。';return;}
  selected=frozen.anchor.segment_ids;field('kind').value='owner_reflection';field('citations_json').value='[]';field('references_json').value=JSON.stringify(selected);field('anchor_json').value=JSON.stringify(frozen.anchor);preview.textContent='记录本来源原音位置 '+frozen.anchor.position;field('content').focus();saveDraft(form);
 });
 scope.on(view.querySelector('#note-playing'),'click',()=>window.CWPListening.captureNote());
 scope.cleanup(window.CWPListening.subscribe(state=>{
  const other=state.spec&&(state.spec.sourceType!==panel.dataset.sourceType||state.spec.sourceId!==panel.dataset.sourceId);
  const playing=view.querySelector('#note-playing');playing.hidden=!other;playing.textContent=other?'记录当前播放 '+state.spec.title:'记录当前播放';
  view.querySelector('#note-now').textContent=other||!state.spec?'记录本页来源（无音频位置）':'记录本来源当前播放位置';
 }));
 scope.on(view.querySelector('#note-selection'),'click',()=>{
  const selection=window.getSelection();if(!selection||selection.isCollapsed){preview.textContent='请先选中一段原文。';return;}
  const range=selection.getRangeAt(0),nodes=Array.from(view.querySelectorAll('.seg,[data-note-segment]')).filter(node=>range.intersectsNode(node));
  if(!nodes.length){preview.textContent='请选择转录稿或文档正文。';return;}
  capture(nodes.map(n=>n.dataset.id||n.dataset.noteSegment),Number(nodes[0].dataset.start||nodes[0].dataset.position)||0,selection.toString().slice(0,500),'source_note');
 });
 scope.on(field('kind'),'change',()=>{field('citations_json').value=JSON.stringify(field('kind').value==='source_note'?selected:[]);field('references_json').value=JSON.stringify(field('kind').value==='owner_reflection'?selected:[]);});
 function key(f){return 'cwp-note-draft:'+panel.dataset.sourceType+':'+panel.dataset.sourceId+':'+(f.elements.namedItem('note_id')?.value||'new');}
 function saveDraft(f){if(!f.elements.namedItem('content'))return;const data={};for(const name of ['content','kind','citations_json','references_json','anchor_json','expected_revision']){const el=f.elements.namedItem(name);if(el)data[name]=el.value;}try{sessionStorage.setItem(key(f),JSON.stringify(data));}catch(_){} }
 view.querySelectorAll('.note-form').forEach(f=>{
  try{const draft=JSON.parse(sessionStorage.getItem(key(f))||'null');if(draft){for(const [name,value]of Object.entries(draft)){const el=f.elements.namedItem(name);if(el)el.value=value;}if(f===form){selected=JSON.parse((field('kind').value==='source_note'?field('citations_json').value:field('references_json').value)||'[]');preview.textContent='已恢复未保存草稿及原位置。';}}}catch(_){}
  scope.on(f,'input',()=>saveDraft(f));
  scope.on(f,'submit',async e=>{
   e.preventDefault();const feedback=f.querySelector('.note-feedback'),button=f.querySelector('[type=submit]');saveDraft(f);button.disabled=true;feedback.textContent='保存中…';
   try{
    const response=await scope.fetch(f.getAttribute('action'),{method:'POST',body:new FormData(f),headers:{Accept:'application/json'}});
    if(!response.ok){feedback.textContent=await response.text();return;}
    const data=await response.json();feedback.textContent='已保存';try{sessionStorage.removeItem(key(f));}catch(_){}
    if(f.elements.namedItem('action')?.value==='delete'){f.closest('article').remove();return;}
    if(data.note&&f.elements.namedItem('expected_revision')){f.closest('article').querySelectorAll('[name=expected_revision]').forEach(el=>el.value=data.note.Revision);f.closest('article').querySelector('p').textContent=data.note.Content;const badge=f.closest('article').querySelector('[data-note-revision]');if(badge)badge.textContent='v'+data.note.Revision;}
    if(f===form){const article=document.createElement('article'),p=document.createElement('p');p.textContent=(data.note.Kind==='source_note'?'来源笔记：':'我的理解：')+data.note.Content;article.id='note-'+data.note.ID;article.appendChild(p);const history=document.createElement('a');history.href='/notes/'+encodeURIComponent(data.note.ID)+'/history';history.textContent='修订历史与记录位置';article.appendChild(history);view.querySelector('#' + 'note-list').prepend(article);field('content').value='';}
   }catch(_){feedback.textContent='未保存，草稿和位置仍保留。';}finally{button.disabled=false;}
  });
 });
});
