// Each view owns its listeners and requests; the listening session belongs to the shell.
(function(root){
 'use strict';
 const mounts=[];
 let cleanup=()=>{};
 function define(mount){mounts.push(mount);}
 function unmount(){cleanup();cleanup=()=>{};}
 function mount(view){
  unmount();const controller=new AbortController(),tasks=[];
  const scope={signal:controller.signal,on:(node,event,fn,options={})=>node?.addEventListener(event,fn,{...options,signal:controller.signal}),
   cleanup:fn=>tasks.push(fn),fetch:(url,options={})=>root.fetch(url,{...options,signal:controller.signal}),
   interval:(fn,ms)=>{const id=root.setInterval(fn,ms);tasks.push(()=>root.clearInterval(id));return id;}};
  cleanup=()=>{controller.abort();tasks.reverse().forEach(fn=>fn());};
  try{mounts.forEach(fn=>fn(view,scope));}catch(error){unmount();throw error;}
  view.querySelectorAll('[data-utc]').forEach(node=>{if(root.cwpLocalTime){node.textContent=root.cwpLocalTime(node.dataset.utc);node.title=node.dataset.utc+' UTC';}});
 }
 root.CWPViews={define,mount,unmount};
})(window);
