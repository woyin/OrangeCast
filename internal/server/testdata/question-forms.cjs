const fs=require('fs'),vm=require('vm'),assert=require('node:assert/strict');
(async()=>{
 for(const ok of [true,false]){
  const handlers={},feedback={textContent:''},fields=[{name:'action',value:'edit',type:'hidden'},{name:'question_id',value:'q'},{name:'expected_revision',value:'2'},{name:'body',value:'当前编辑'},{name:'goal',value:'目标'},{name:'_csrf',value:'fresh-token'}];fields.namedItem=n=>fields.find(f=>f.name===n);
  const form={action:fields[0],elements:fields,getAttribute:n=>n==='action'?'/questions/action':null,querySelector:()=>feedback,hasAttribute:()=>false,append(){},addEventListener:(name,fn)=>handlers[name]=fn};
  const values=new Map([['cwp-question-draft:q:edit:2',JSON.stringify({body:'同版本草稿',_csrf:'old-secret',expected_revision:'1'})],['cwp-question-draft:q:edit:1',JSON.stringify({body:'旧版本草稿'})]]);
  const storage={get length(){return values.size},key:i=>Array.from(values.keys())[i],getItem:k=>values.get(k)||null,setItem:(k,v)=>values.set(k,v),removeItem:k=>values.delete(k)};
  let endpoint,navigated;const context={AbortController,sessionStorage:storage,document:{createElement:()=>({append(){},textContent:'',value:''})},fetch:async url=>{endpoint=url;return {ok,json:async()=>({href:'/questions/q'}),text:async()=> 'conflict'}},URLSearchParams,FormData:class extends Map{constructor(){super(fields.map(f=>[f.name,f.value]))}},CWPNavigation:{visit:async href=>navigated=href}};context.window=context;
  const view={querySelector:()=>null,querySelectorAll:selector=>selector==='form.question-form'?[form]:[]};
  vm.runInNewContext(fs.readFileSync('static/view-lifecycle.js','utf8'),context);vm.runInNewContext(fs.readFileSync('static/form-actions.js','utf8'),context);vm.runInNewContext(fs.readFileSync('static/learning-questions.js','utf8'),context);context.CWPViews.mount(view);
  assert.equal(fields.namedItem('body').value,'同版本草稿');assert.equal(fields.namedItem('_csrf').value,'fresh-token');assert.equal(fields.namedItem('expected_revision').value,'2');
  const button={disabled:false,name:'',value:''};await handlers.submit({preventDefault(){},submitter:button});assert.equal(endpoint,'/questions/action');assert.equal(button.disabled,false);
  if(ok){assert.equal(navigated,'/questions/q');assert(!values.has('cwp-question-draft:q:edit:2'));assert(values.has('cwp-question-draft:q:edit:1'))}else{assert.equal(feedback.textContent,'conflict');assert.equal(fields.namedItem('body').value,'同版本草稿');assert(values.has('cwp-question-draft:q:edit:2'))}
 }
 console.log('question named action endpoint, revision draft isolation, CSRF freshness and conflict retention passed');
})().catch(e=>{console.error(e);process.exit(1)});
