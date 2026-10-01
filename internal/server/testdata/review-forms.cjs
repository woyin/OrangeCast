const fs=require('fs'),vm=require('vm'),assert=require('node:assert/strict');
(async()=>{
 for(const daily of [true,false])for(const ok of [true,false]){
  const handlers={},feedback={textContent:'',dataset:{}},values=new Map(),fields=['session','item','answer','assessment','expected_revision','request_key'].map(name=>({name,value:{session:'s',item:'i',answer:'我的解释',assessment:'unsure',expected_revision:'3',request_key:'original-request'}[name],disabled:false}));
  for(const f of fields)fields[f.name]=f;
  const form={elements:fields,getAttribute:()=>daily?'/review/daily/action':'/review/action',querySelector:()=>feedback,querySelectorAll:()=>fields};let posted,navigated=0;
  const storage={getItem:k=>values.get(k),setItem:(k,v)=>values.set(k,v),removeItem:k=>values.delete(k)};
  const scope={signal:new AbortController().signal,on:(node,event,fn)=>handlers[event]=fn,fetch:async(url,options)=>{posted=options.body;return{ok,status:409,json:async()=>({href:'/review'}),text:async()=>'旧回答冲突'};}};
  const view={querySelectorAll:s=>s===(daily?'.daily-review-form':'.review-form')?[form]:[]};
  const context={sessionStorage:storage,FormData:class extends Map{constructor(){super(fields.filter(f=>!f.disabled).map(f=>[f.name,f.value]));}},window:{CWPViews:{define:fn=>fn(view,scope)},CWPNavigation:{visit:async()=>navigated++}}};
  vm.runInNewContext(fs.readFileSync('static/form-actions.js','utf8'),context);vm.runInNewContext(fs.readFileSync(daily?'static/daily-review.js':'static/review.js','utf8'),context);
  await handlers.submit({preventDefault(){},submitter:{name:'action',value:'reveal',disabled:false}});
  assert.equal(posted.get('action'),'reveal');assert.equal(posted.get('answer'),'我的解释');assert.equal(posted.get('expected_revision'),'3');assert.equal(posted.get('request_key'),'original-request');
  if(ok){assert.equal(navigated,1);if(!daily){assert.equal(fields.expected_revision.value,'4');assert.equal(JSON.parse([...values.values()][0]).answer,'我的解释');}}else{assert.equal(navigated,0);assert.equal(fields.expected_revision.value,'3');assert.equal(feedback.textContent,'旧回答冲突');assert.equal(JSON.parse([...values.values()][0]).answer,'我的解释');}
 }
 console.log('daily and weekly reveal preserve answer identity and CAS drafts');
})().catch(error=>{console.error(error);process.exit(1)});
