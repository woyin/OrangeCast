// Conservative GET navigation. SSR remains authoritative; fetched scripts are never executed.
(function(root){
 'use strict';
 const allowed=/^\/(?:dashboard|automation(?:\/[^/]+)?|voice-notes|podcasts(?:\/[^/]+)?|sources\/(?:episode|upload)\/[^/]+(?:\/dj)?|search|notes(?:\/[^/]+\/history)?|questions(?:\/[^/]+)?|knowledge-updates(?:\/[^/]+)?|knowledge-articles(?:\/[^/]+(?:\/revisions\/[^/]+)?)?|review(?:\/daily)?|listening-queue|documents(?:\/[^/]+)?|uploads)$/;
 let seq=0,pending=null,expired=false;
 const feedback=document.getElementById('navigation-feedback');
 const privateClear=()=>root.CWPPrivate.clear();
 function expire(){if(expired)return;expired=true;++seq;pending?.abort();root.CWPViews.unmount();root.CWPListening.stop();privateClear();document.getElementById('page-view').replaceChildren();feedback.hidden=false;feedback.textContent='登录已失效，收听已停止。';root.location.assign('/login');}
 function rememberScroll(){history.replaceState({...history.state,cwp:true,scroll:[root.scrollX,root.scrollY]},'',location.href);}
 function fail(url,error){feedback.hidden=false;feedback.replaceChildren();const text=document.createElement('span');text.textContent='页面未切换，当前收听继续。'+error.message+' ';const retry=document.createElement('button');retry.type='button';retry.textContent='重试';retry.onclick=()=>visit(url);const full=document.createElement('a');full.href=url;full.dataset.fullNavigation='1';full.textContent='完整打开（会离开当前播放器）';feedback.append(text,retry,full);}
 async function visit(value,options={}){
  const url=new URL(value,location.href);if(url.origin!==location.origin||!allowed.test(url.pathname)){root.location.assign(url.href);return false;}
  if(expired)return false;
  const mine=++seq;pending?.abort();const controller=new AbortController();pending=controller;feedback.hidden=false;feedback.textContent='加载中…';
  try{const response=await root.fetch(url.href,{signal:controller.signal,cache:'no-store',headers:{'X-CWP-View':'1'}});
   if(mine!==seq)return false;
   if(response.status===401||response.status===403||response.redirected&&/\/(login|register)$/.test(new URL(response.url).pathname)){expire();return false;}
   if(!response.ok)throw Error('请求失败（'+response.status+'）');
   if(response.redirected&&new URL(response.url).href!==url.href)throw Error('页面地址已改变，请完整打开。');
   if(!response.headers.get('Content-Type')?.includes('text/html'))throw Error('该地址不是页面。');
   const parsed=new DOMParser().parseFromString(await response.text(),'text/html'),main=parsed.querySelector('#page-view');
   if(mine!==seq)return false;if(!main)throw Error('页面缺少内容区域。');
   // All whitelisted behaviours are preloaded and mounted explicitly, never by fetched script tags.
   if(main.querySelector('script')||Array.from(main.querySelectorAll('*')).some(node=>Array.from(node.attributes).some(attr=>/^on/i.test(attr.name))))throw Error('该页面尚未支持连续收听导航，请完整打开。');
   if(!options.pop)rememberScroll();root.CWPViews.unmount();const old=document.getElementById('page-view');old.replaceWith(document.importNode(main,true));document.title=parsed.title;
   if(!options.pop)history.pushState({cwp:true,scroll:[0,0]},'',url.href);
   root.CWPViews.mount(document.getElementById('page-view'));feedback.hidden=true;
   const focus=document.getElementById('page-view');focus.focus({preventScroll:true});
   if(options.pop&&options.scroll)root.scrollTo(...options.scroll);else if(url.hash){try{document.querySelector(url.hash)?.scrollIntoView();}catch(_){root.scrollTo(0,0);}}else root.scrollTo(0,0);
   return true;
  }catch(error){if(mine===seq&&error.name!=='AbortError')fail(url.href,error);return false;}finally{if(mine===seq)pending=null;}
 }
 root.CWPNavigation={visit,expire,privateClear,allowed};
 if(/^\/(login|register|logout)$/.test(location.pathname)){root.CWPListening.stop();privateClear();return;}
 history.scrollRestoration='manual';history.replaceState({cwp:true,scroll:[root.scrollX,root.scrollY]},'',location.href);
 document.addEventListener('click',event=>{const link=event.target.closest?.('a[href]');if(!link)return;const url=new URL(link.href,location.href);
  if(url.origin===location.origin&&url.pathname==='/logout'){root.CWPListening.stop();privateClear();return;}
  if(event.defaultPrevented||event.button!==0||event.metaKey||event.ctrlKey||event.altKey||event.shiftKey||link.target&&link.target!=='_self'||link.hasAttribute('download')||link.dataset.fullNavigation)return;
  if(url.origin!==location.origin||!allowed.test(url.pathname)||url.pathname===location.pathname&&url.search===location.search&&url.hash)return;
  event.preventDefault();visit(url.href);
 });
 root.addEventListener('popstate',event=>{if(!allowed.test(location.pathname)){root.location.reload();return;}visit(location.href,{pop:true,scroll:event.state?.scroll||[0,0]});});
 root.addEventListener('pageshow',event=>{if(event.persisted)root.CWPListening.readQueue().catch(()=>{});});
 root.CWPViews.mount(document.getElementById('page-view'));
})(window);
