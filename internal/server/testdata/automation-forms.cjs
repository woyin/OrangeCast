const fs=require('fs'),vm=require('vm'),assert=require('assert');
(async()=>{
 for(const ok of [true,false]){
 const listeners={},feedback={textContent:''},fields=[{name:'action',value:'pause'},{name:'reason',value:'我的原理由'},{name:'expected_revision',value:'1'},{name:'request_key',value:'stable'}];let endpoint,redirect,interval,hidden=false;
 const form={action:fields[0],getAttribute:n=>n==='action'?'/automation/action':null,querySelector:()=>feedback,querySelectorAll:()=>fields};
 const pollFeedback={textContent:''},status={textContent:''},block={textContent:''},row={dataset:{runId:'job'},querySelector:s=>s==='[data-run-status]'?status:block};
 const root={querySelectorAll:s=>s==='.automation-form'?[form]:[row],querySelector:()=>pollFeedback};
 const view={querySelector:()=>root};let gets=0;
 const scope={signal:new AbortController().signal,on:(n,event,fn)=>listeners[event]=fn,interval:fn=>interval=fn,fetch:async(url,opts)=>{if(opts.method==='POST'){endpoint=url;return{ok,json:async()=>({href:'/automation'}),text:async()=>'旧修订冲突'}};gets++;return{ok:true,json:async()=>[{ID:'job',Status:'running',NextAction:'已保存',BlockReason:'等待'}]}}};
 const context={document:{get hidden(){return hidden}},location:{search:'?status=queued'},FormData:class{constructor(){this.fields=new Map(fields.map(f=>[f.name,f.value]))}set(n,v){this.fields.set(n,v)}},window:{CWPViews:{define:fn=>fn(view,scope)},CWPNavigation:{visit:url=>redirect=url}}};
 vm.runInNewContext(fs.readFileSync('static/automation.js','utf8'),context);
 await listeners.submit({preventDefault(){},submitter:{value:'pause'}});assert.equal(endpoint,'/automation/action');assert.equal(fields[1].value,'我的原理由');assert(fields.every(f=>!f.disabled));if(ok)assert.equal(redirect,'/automation');else{assert.equal(redirect,undefined);assert.equal(feedback.textContent,'旧修订冲突')}
 hidden=true;await interval();assert.equal(gets,0);hidden=false;await interval();assert.equal(gets,1);assert.equal(status.textContent,'running · 已保存');assert.equal(fields[1].value,'我的原理由');
 }
 console.log('automation named action, conflict reason, visible bounded polling passed');
})().catch(e=>{console.error(e);process.exit(1)});

// Daily start has a named hidden action, the exact pattern that caused a 404.
(async()=>{
 const listeners={},form={action:{value:'start'},querySelectorAll:()=>[],append(){},getAttribute:()=>'/review/daily/action'};let endpoint;
 const feedback={setAttribute(){}};
 const scope={on:(node,event,fn)=>listeners[event]=fn,fetch:async url=>{endpoint=url;return{ok:true,json:async()=>({href:'/review/daily'})}}};
 const context={document:{createElement:()=>feedback},FormData:class{},window:{CWPViews:{define:fn=>fn({querySelectorAll:s=>s==='.daily-review-form'?[]:[form]},scope)},CWPNavigation:{visit(){}}}};
 vm.runInNewContext(fs.readFileSync('static/form-actions.js','utf8'),context);vm.runInNewContext(fs.readFileSync('static/daily-review.js','utf8'),context);
 await listeners.submit({preventDefault(){},submitter:{}});assert.equal(endpoint,'/review/daily/action');
})().catch(e=>{console.error(e);process.exit(1)});
