(function(){
 'use strict';
 document.addEventListener('click',event=>{
  const link=event.target.closest?.('a[data-source-page]');if(!link)return;
  const selects=Array.from(document.querySelectorAll('select[name="source"]'));
  const select=selects.find(el=>el.closest('form')?.getAttribute('action')==='/knowledge-articles/generate')||selects[0];
  if(!select)return;
  const url=new URL(link.href,location.href);url.searchParams.set('source',select.value);
  url.searchParams.delete('source_type');url.searchParams.delete('source_id');
  // The selected identity is carried in the URL and reinserted server-side even outside this page.
  link.href=url.pathname+url.search;
 });
})();
