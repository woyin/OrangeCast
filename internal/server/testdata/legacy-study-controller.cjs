const assert=require('node:assert/strict'),fs=require('node:fs'),vm=require('node:vm');
const code=fs.readFileSync('static/player.js','utf8');
const controller=code.slice(code.indexOf('// ---- StudyChat'),code.lastIndexOf('\n});'));
class Node {constructor(){this.value='';this.innerHTML='';this.children=[];this.listeners={};this.disabled=false;}querySelector(q){return this.fields?.[q]||null;}insertAdjacentHTML(_,s){this.innerHTML+=s;}append(n){this.children.push(n);}async fire(e){await this.listeners[e]?.({preventDefault(){}});}}
class FD {constructor(form){this.fields={_csrf:'token',question:form.fields['[name=question]'].value,session_id:form.fields['[name=question]'].value};}set(k,v){this.fields[k]=String(v);}}
async function fixture(options={}){
 const form=new Node(),thread=new Node(),feedback=new Node(),session=new Node(),question=new Node(),button=new Node();form.fields={'[name=question]':question,'[type=submit]':button};
 const sourceId=options.sourceId||'ep',pendingKey='cwp-study-command:episode:'+sourceId,storage=options.storage||new Map(),calls=[],intervals=[];let id=0;
 const context={spec:{sourceType:'episode',sourceId},view:{querySelector:q=>({'#study-chat-form':form,'#study-chat-thread':thread,'#study-chat-feedback':feedback,'#study-chat-session-id':session}[q])},
 scope:{signal:{aborted:false},fetch:async(url,opt={})=>{calls.push({url,opt});if(options.handler)return options.handler(url,opt,storage);return {ok:true,status:202,json:async()=>({state:'queued',session_id:'session',turn_id:'turn',revision:2})};},interval:fn=>intervals.push(fn)},
 on:(node,event,fn)=>{if(node)node.listeners[event]=fn;},escapeHtml:s=>String(s),crypto:{randomUUID:()=>`request-${++id}`},document:{createElement:()=>new Node()},FormData:FD,
 sessionStorage:{setItem:(key,value)=>{if(options.storageError)throw Error('denied');storage.set(key,value);},getItem:key=>storage.get(key)||null,removeItem:key=>storage.delete(key)}};
 vm.runInNewContext(controller,context);await new Promise(resolve=>setImmediate(resolve));
 return {form,thread,feedback,session,question,button,calls,storage,intervals,context,pendingKey};
}
(async()=>{
 const f=await fixture({handler:async(url,opt,storage)=>{
  if(opt.method==='POST'){assert.ok(storage.has('cwp-study-command:episode:ep'),'UUID persisted before POST');assert.equal(opt.body.fields.revision,'1');return {ok:true,status:202,json:async()=>({state:'queued',session_id:'session',turn_id:'turn',revision:2})};}
  if(url.includes('/history'))return {ok:true,status:200,json:async()=>({revision:3,messages:[{role:'user',content:'通胀'},{role:'assistant',content:'checked',reference_segment_ids:['seg-0001']}]})};
  return {ok:true,status:200,json:async()=>({state:'accepted',session_id:'session',turn_id:'turn',revision:3,generated:true,answer:'checked'})};
 }});
 f.question.value='通胀';await f.form.fire('submit');assert.equal(f.calls.filter(c=>c.opt.method==='POST').length,1);assert.ok(!f.thread.innerHTML.includes('checked'),'202 is not an accepted answer');await f.intervals[0]();await new Promise(resolve=>setImmediate(resolve));assert.ok(f.thread.innerHTML.includes('checked'));assert.ok(!f.storage.has(f.pendingKey));assert.equal(f.button.disabled,false);
 const storage=new Map([[f.pendingKey,JSON.stringify({request_key:'original-uuid',question:'lost reply',session_id:'',revision:1})]]);
 const restored=await fixture({storage,handler:async(url,opt)=>{assert.notEqual(opt.method,'POST','mount never resends lost response');return {ok:true,status:200,json:async()=>({state:'unknown',result_unknown:true,retry:true,turn_id:'turn',session_id:'s',revision:2,job_id:'j',job_revision:2})};}});
 assert.ok(restored.feedback.innerHTML.includes('再次计费'));assert.equal(restored.feedback.children[0].textContent,'明确发起新的付费尝试');assert.ok(restored.storage.has(f.pendingKey));
 const denied=await fixture({storageError:true});denied.question.value='draft';await denied.form.fire('submit');assert.equal(denied.calls.length,0);assert.ok(denied.feedback.innerHTML.includes('本次没有提交'));
 const lost=await fixture({handler:async(url,opt)=>{if(opt.method==='POST')throw Error('response lost');return {ok:true,status:200,json:async()=>({state:'queued',session_id:'s',turn_id:'t',revision:2})};}});lost.question.value='lost';await lost.form.fire('submit');assert.equal(lost.calls.filter(c=>c.opt.method==='POST').length,1);assert.ok(lost.calls.some(c=>c.url.includes('request_key=request-1')));assert.ok(lost.storage.has(f.pendingKey));

 const working=await fixture();working.question.value='submitted question';await working.question.fire('input');await working.form.fire('submit');
 const draftKey='cwp-study-draft:episode:ep';assert.ok(!working.storage.has(draftKey),'submitted input retires its editable draft');
 working.question.value='下一问本地草稿';await working.question.fire('input');assert.equal(JSON.parse(working.storage.get(draftKey)).text,'下一问本地草稿');working.context.scope.signal.aborted=true;
 const returned=await fixture({storage:working.storage,handler:async(url,opt)=>{
  assert.notEqual(opt.method,'POST','returning to source only polls existing command');
  if(url.includes('/history'))return {ok:true,status:200,json:async()=>({revision:3,messages:[{role:'user',content:'submitted question'},{role:'assistant',content:'checked answer'}]})};
  return {ok:true,status:200,json:async()=>({state:'accepted',session_id:'session',turn_id:'turn',revision:3})};
 }});
 assert.equal(returned.question.value,'下一问本地草稿','pending recovery and terminal history preserve current draft');assert.ok(!returned.storage.has(returned.pendingKey));assert.ok(returned.storage.has(draftKey));assert.equal(working.calls.filter(c=>c.opt.method==='POST').length,1);
 const other=await fixture({storage:returned.storage,sourceId:'foreign-source'});assert.equal(other.question.value,'','other sources never restore this draft');assert.equal(other.calls.length,0);
 const privateStorage={get length(){return returned.storage.size;},key:i=>Array.from(returned.storage.keys())[i],getItem:k=>returned.storage.get(k),setItem:(k,v)=>returned.storage.set(k,v),removeItem:k=>returned.storage.delete(k)};
 const logout={sessionStorage:privateStorage,localStorage:{length:0,setItem(){},key(){},removeItem(){}},location:{pathname:'/logout'},addEventListener(){}};
 vm.runInNewContext(fs.readFileSync('static/private-state.js','utf8'),{window:logout});assert.ok(!returned.storage.has(draftKey),'actual logout clear removes editable private draft');
 const loggedIn=await fixture({storage:returned.storage});assert.equal(loggedIn.question.value,'','a new login cannot inherit the cleared draft');assert.equal(loggedIn.calls.length,0);
 console.log('legacy StudyChat identity, private generation, restart, unknown, draft isolation and logout: passed');

})().catch(err=>{console.error(err);process.exit(1);});
