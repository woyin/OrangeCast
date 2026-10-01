const fs=require('fs'),vm=require('vm'),assert=require('node:assert/strict');
function fixture(){
 const controls=[{name:'action',value:'save',disabled:false},{name:'content',value:'我的输入',disabled:false},{name:'locked',value:'excluded',disabled:true},{name:'request_key',value:'same-key',disabled:false}];
 const feedback={textContent:'',dataset:{}},form={action:controls[0],getAttribute:()=>'/actual/endpoint',querySelector:()=>feedback,querySelectorAll:()=>controls};
 let calls=0,body,resolve,endpoint,cleared=0,navigated=[];
 const context={FormData:class extends Map{constructor(){super(controls.filter(c=>!c.disabled).map(c=>[c.name,c.value]));}},CWPPrivate:{clear:()=>cleared++},CWPNavigation:{visit:async href=>navigated.push(href)}};
 context.window=context;vm.runInNewContext(fs.readFileSync('static/form-actions.js','utf8'),context);
 const abort=new AbortController(),scope={signal:abort.signal,fetch:async(url,options)=>{calls++;endpoint=url;body=options.body;return await new Promise(r=>resolve=r);}};
 return {context,scope,form,controls,feedback,abort,get calls(){return calls;},get body(){return body;},get endpoint(){return endpoint;},get cleared(){return cleared;},navigated,respond:r=>resolve(r)};
}
(async()=>{
 let f=fixture(),saved=0;const p=f.context.CWPForms.submit(f.form,f.scope,{event:{preventDefault(){},submitter:{name:'action',value:'confirm',disabled:false}},success:()=>saved++});
 assert.equal(f.body.get('action'),'confirm');assert.equal(f.body.get('content'),'我的输入');assert(!f.body.has('locked'));assert.equal(f.endpoint,'/actual/endpoint');
 assert.equal((await f.context.CWPForms.submit(f.form,f.scope)).state,'busy');assert.equal(f.calls,1);
 f.respond({ok:true,json:async()=>({href:'/saved'})});assert.equal((await p).state,'saved');assert.equal(saved,1);assert.equal(f.navigated[0],'/saved');assert.equal(f.controls[2].disabled,true);assert.equal(f.controls[0].disabled,false);
 for(const status of [409,401,422]){
  f=fixture();const run=f.context.CWPForms.submit(f.form,f.scope);f.respond({ok:false,status,text:async()=>status===422?'validation message':JSON.stringify({code:status===409?'scope_changed':'unauthorized',message:'保留文字'})});
  assert.equal((await run).state,'rejected');assert.equal(f.controls[1].value,'我的输入');assert.equal(f.controls[3].value,'same-key');assert.equal(f.feedback.textContent,status===422?'validation message':'保留文字');
  assert.equal(f.cleared,status===401?1:0);assert.equal(f.navigated.length,status===401?1:0);
 }
 f=fixture();let run=f.context.CWPForms.submit(f.form,f.scope);f.abort.abort();f.respond({ok:true,json:async()=>({href:'/late'})});assert.equal((await run).state,'unmounted');assert.equal(f.navigated.length,0);assert.equal(f.controls[2].disabled,true);
 f=fixture();f.scope.fetch=async()=>{throw new TypeError('offline');};assert.equal((await f.context.CWPForms.submit(f.form,f.scope)).state,'unknown');assert.equal(f.feedback.dataset.errorCode,'remote_unknown');assert.equal(f.controls[3].value,'same-key');
 f=fixture();assert.equal((await f.context.CWPForms.submit(f.form,f.scope,{confirm:()=>false})).state,'cancelled');assert.equal(f.calls,0);
 console.log('common form: snapshot, disabled restoration, duplicate suppression, CAS, 401, unknown result and unmount passed');
})().catch(error=>{console.error(error);process.exit(1)});
