const fs=require('fs'),vm=require('vm'),assert=require('assert');
(async()=>{
 for(const ok of [true,false]){
  const listeners={},feedback={textContent:''},fields=[{name:'action',value:'save',type:'hidden'},{name:'title',value:'编辑内容'},{name:'expected_revision',value:'1'}];fields.namedItem=n=>fields.find(f=>f.name===n);
  const form={dataset:{draftPurpose:'save',parentHash:'hash1'},action:fields[0],elements:fields,getAttribute:n=>n==='action'?'/knowledge-articles/action':null,querySelector:()=>feedback,addEventListener:(name,fn)=>listeners[name]=fn};
  const state={dataset:{id:'article'}};let endpoint,redirect;const drafts=new Map();
  const context={document:{getElementById:id=>id==='article-state'?state:null,querySelectorAll:()=>[form]},sessionStorage:{getItem:k=>drafts.get(k),setItem:(k,v)=>drafts.set(k,v),removeItem:k=>drafts.delete(k)},location:{assign:v=>redirect=v},fetch:async url=>{endpoint=url;return{ok,json:async()=>({redirect:'/knowledge-articles/article?revision=2'}),text:async()=> 'conflict'}},setInterval:()=>1,clearInterval:()=>{},FormData:class{constructor(){this.fields=new Map(fields.map(f=>[f.name,f.value]))}get(n){return this.fields.get(n)}set(n,v){this.fields.set(n,v)}}};
  context.window=context;context.addEventListener=()=>{};context.localStorage=context.sessionStorage;
  vm.runInNewContext(fs.readFileSync('static/article-drafts.js','utf8'),context);
  vm.runInNewContext(fs.readFileSync('static/knowledge-articles.js','utf8'),context);
  const button={disabled:false,name:'',value:''};await listeners.submit({preventDefault(){},submitter:button});assert.equal(endpoint,'/knowledge-articles/action');assert.equal(button.disabled,false);
  if(ok){assert.equal(redirect,'/knowledge-articles/article?revision=2');assert.equal(drafts.size,0)}else{assert.equal(feedback.textContent,'conflict');assert.equal(fields[1].value,'编辑内容');assert.equal(drafts.size,1)}
 }
 console.log('named action field, successful navigation and conflict draft preservation passed');
})().catch(e=>{console.error(e);process.exit(1)});
