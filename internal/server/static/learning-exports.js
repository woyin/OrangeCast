window.CWPViews.define(function(view,scope){
 'use strict';
 const root=view.querySelector('#learning-exports-view');if(!root)return;
 root.querySelectorAll('.learning-export-command').forEach(form=>window.CWPForms.bind(form,scope,{encoding:'urlencoded'}));
 root.querySelectorAll('[data-export-download]').forEach(link=>{
  let downloading=false;
  scope.on(link,'click',async event=>{
   event.preventDefault();if(downloading||scope.signal.aborted)return;downloading=true;
   const feedback=link.closest('[data-export-id]').querySelector('[data-export-download-feedback]');
   feedback.textContent='正在下载…';
   try{
    const response=await scope.fetch(link.getAttribute('href'),{headers:{Accept:'application/zip'},cache:'no-store'});
    if(scope.signal.aborted)return;
    if(response.status===401||response.redirected&&/\/(login|register)$/.test(new URL(response.url).pathname)){
     window.CWPPrivate?.clear();if(window.CWPNavigation?.expire)window.CWPNavigation.expire();else await window.CWPNavigation?.visit('/login');return;
    }
    if(!response.ok){const message=await response.text();if(!scope.signal.aborted)feedback.textContent=message||'文件暂不可下载，请重新预览。';return;}
    if(!response.headers.get('Content-Type')?.includes('application/zip')||!response.headers.get('Content-Disposition')?.includes('attachment')){feedback.textContent='下载响应无效，请重新预览。';return;}
    const maximum=(100<<20)+(1<<20),length=Number(response.headers.get('Content-Length')||0);
    if(length>maximum){feedback.textContent='导出文件超出下载限制，请重新预览。';return;}
    const blob=await response.blob();if(scope.signal.aborted)return;
    if(blob.size>maximum){feedback.textContent='导出文件超出下载限制，请重新预览。';return;}
    const url=URL.createObjectURL(blob);let anchor;
    try{anchor=document.createElement('a');anchor.href=url;anchor.download='learning-export.zip';anchor.hidden=true;document.body.append(anchor);anchor.click();}
    finally{anchor?.remove();URL.revokeObjectURL(url);}
    feedback.textContent='下载已开始；当前收听继续。';
   }catch(_){if(!scope.signal.aborted)feedback.textContent='下载暂未完成，当前范围和输入保留。';}
   finally{downloading=false;}
  });
 });
 const feedback=root.querySelector('#learning-export-poll');let busy=false;
 scope.interval(async()=>{
  if(busy||document.hidden)return;busy=true;
  try{
   for(const row of root.querySelectorAll('[data-export-id]')){
    const response=await scope.fetch('/learning-exports/'+encodeURIComponent(row.dataset.exportId)+'/status',{headers:{Accept:'application/json'},cache:'no-store'});
    if(scope.signal.aborted)return;
    if(response.status===401){window.CWPPrivate?.clear();await window.CWPNavigation?.visit('/login');return;}
    if(!response.ok){feedback.textContent='状态暂未更新；选项保留。';continue;}
    const value=await response.json();if(scope.signal.aborted)return;
    row.querySelector('[data-export-state]').textContent=value.status;
    row.querySelector('[data-export-download]').hidden=value.status!=='ready';
    feedback.textContent='后台状态已更新；范围及选项保持不变。';
   }
  }catch(_){if(!scope.signal.aborted)feedback.textContent='状态暂未更新；选项保留。';}finally{busy=false;}
 },5000);
});
