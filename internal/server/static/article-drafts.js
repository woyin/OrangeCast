(function(root){
 'use strict';
 const prefix='cwp-article-draft:v2:';
 const own=(obj,key)=>Object.prototype.hasOwnProperty.call(obj,key);
 function editable(el){return !!el.name&&!['hidden','submit','button','file','reset'].includes(el.type)&&el.name!=='_csrf'&&!el.disabled;}
 function identity(el){
  if(el.dataset?.draftKey)return el.dataset.draftKey;
  return el.name+(['checkbox','radio'].includes(el.type)?':'+el.value:'');
 }
 function controls(form){
  const entries=Array.from(form.elements).filter(editable);const counts=new Map();
  entries.forEach(el=>{const key=identity(el);counts.set(key,(counts.get(key)||0)+1);});
  // Unlabelled repeated text controls are ambiguous; never fall back to an index.
  return entries.filter(el=>counts.get(identity(el))===1);
 }
 function sameParent(a,b){return !!a&&!!b&&a.article===b.article&&a.revision===b.revision&&a.hash===b.hash&&a.purpose===b.purpose;}
 function capture(form,parent,previous){
  const fields=Object.create(null);
  if(previous?.version===2&&sameParent(previous.parent,parent))Object.assign(fields,previous.fields);
  controls(form).forEach(el=>{
   const value=el.type==='select-multiple'?Array.from(el.options).filter(o=>o.selected).map(o=>o.value):el.value;
   fields[identity(el)]={type:el.type,value,checked:['checkbox','radio'].includes(el.type)?!!el.checked:undefined};
  });
  return {version:2,parent,fields};
 }
 function restore(form,draft,parent,allowMerge){
  const result={matched:0,missing:[],conflict:false,legacy:false};
  if(!draft)return result;
  if(draft.version!==2||!draft.parent||!draft.fields||typeof draft.fields!=='object'){result.legacy=true;return result;}
  result.conflict=!sameParent(draft.parent,parent);
  if(result.conflict&&!allowMerge)return result;
  const found=new Set();
  controls(form).forEach(el=>{
   const key=identity(el);if(!own(draft.fields,key))return;
   const field=draft.fields[key];if(!field||field.type!==el.type)return;
   if(['checkbox','radio'].includes(el.type))el.checked=!!field.checked;
   else if(el.type==='select-multiple'){
    if(!Array.isArray(field.value))return;
    const values=new Set(field.value);Array.from(el.options).forEach(o=>{o.selected=values.has(o.value);values.delete(o.value);});
    values.forEach(v=>result.missing.push(key+':'+v));
   }else{
    if(typeof field.value!=='string')return;
    if(el.type==='select-one'&&!Array.from(el.options).some(o=>o.value===field.value))return;
    el.value=field.value;
   }
   found.add(key);result.matched++;
  });
  Object.keys(draft.fields).forEach(key=>{if(!found.has(key))result.missing.push(key);});
  return result;
 }
 function clearPrivate(){
  for(const storage of [root.localStorage,root.sessionStorage]){
   try{for(let i=storage.length-1;i>=0;i--){const key=storage.key(i);if(key?.startsWith('cwp-article-draft:'))storage.removeItem(key);}}catch(_){}
  }
 }
 root.CWPArticleDrafts={prefix,identity,controls,sameParent,capture,restore,clearPrivate};
 if(root.document?.addEventListener)root.document.addEventListener('click',event=>{
  const link=event.target.closest?.('a[href]');
  if(link&&new URL(link.href,root.location.href).pathname==='/logout')clearPrivate();
 });
})(typeof window==='undefined'?globalThis:window);
