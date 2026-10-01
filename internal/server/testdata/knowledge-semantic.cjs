const fs=require('fs'),vm=require('vm'),assert=require('node:assert/strict');
function fixture(){
 const elements=values=>Object.fromEntries(Object.entries(values).map(([name,value])=>[name,{name,value,disabled:false}]));
 const form=values=>{const node={elements:elements(values),hidden:false,feedback:{textContent:'',dataset:{}},button:{disabled:false},listeners:{}};node.getAttribute=()=>'/api/knowledge-semantic';node.querySelector=selector=>selector==='button'?node.button:node.feedback;node.querySelectorAll=()=>Object.values(node.elements).concat(node.button);return node;};
 const query=form({_csrf:'csrf',config_id:'config',query:'如何应用',request_key:'query-key'}),retry=form({_csrf:'csrf',job_id:'',expected_revision:'',request_key:'retry-key',allow_unknown:'1'});retry.hidden=true;
 const checkbox=retry.elements.allow_unknown;checkbox.type='checkbox';checkbox.checked=false;
 const confirmation={hidden:true,querySelector:()=>checkbox};const oldRetrySelector=retry.querySelector;retry.querySelector=selector=>selector==='#semantic-unknown-confirm'?confirmation:oldRetrySelector(selector);
 const status={textContent:''},link={hidden:true},jobLink={hidden:true};const nodes={'#semantic-query-form':query,'#semantic-retry-form':retry,'#semantic-search-status':status,'#semantic-use-results':link,'#semantic-job-link':jobLink};
 const root={dataset:{jobId:''},querySelector:selector=>nodes[selector]},view={querySelector:selector=>selector==='#semantic-search'?root:null};
 const abort=new AbortController(),intervals=[],requests=[];let responder=async()=>{throw Error('offline')},uuid=0;
 const context={URL,location:{href:'http://local/search?q=%E5%A6%82%E4%BD%95%E5%BA%94%E7%94%A8&kind=owner_reflection',origin:'http://local'},document:{hidden:false},crypto:{randomUUID:()=>`new-${++uuid}`},FormData:class extends Map{constructor(form){super(Object.values(form.elements).filter(v=>!v.disabled&&(v.type!=='checkbox'||v.checked)).map(v=>[v.name,v.value]));}},CWPViews:{define:fn=>context.mount=fn}};context.window=context;
 const scope={signal:abort.signal,on:(node,event,fn)=>node.listeners[event]=fn,interval:fn=>intervals.push(fn),fetch:async(url,options={})=>{requests.push({url,options});return responder(url,options);}};
 vm.runInNewContext(fs.readFileSync('static/form-actions.js','utf8'),context);vm.runInNewContext(fs.readFileSync('static/search.js','utf8'),context);context.mount(view,scope);
 return {query,retry,confirmation,checkbox,status,link,jobLink,abort,requests,poll:()=>intervals[0](),respond:fn=>responder=fn,submit:form=>form.listeners.submit({preventDefault(){},submitter:form.button})};
}
const reply=state=>({ok:true,json:async()=>state});
(async()=>{
 const f=fixture();await f.poll();assert.equal(f.requests.length,0,'opening page must not prepare query');
 assert.equal((await f.submit(f.query)).state,'unknown');const queryBody=f.requests[0].options.body;assert.equal(JSON.parse(queryBody).query,'如何应用');assert.equal(f.query.elements.request_key.value,'query-key');
 f.respond(async()=>reply({job_id:'query-job',status:'queued',control_revision:2,query_ready:false}));await f.poll();assert(f.requests[1].url.includes('request_action=query'));assert(f.requests[1].url.includes('request_key=query-key'));assert.equal(f.query.hidden,true);assert.equal(f.jobLink.href,'/automation/query-job');
 f.respond(async()=>reply({job_id:'query-job',status:'failed',control_revision:6,query_ready:false,remote_started:true,known_response:false,error:'远端结果未知'}));await f.poll();assert.equal(f.retry.hidden,false);assert.equal(f.confirmation.hidden,false);assert.equal(f.checkbox.required,true);
 f.checkbox.checked=true;f.respond(async()=>{throw Error('lost retry acknowledgement')});assert.equal((await f.submit(f.retry)).state,'unknown');const retryBody=f.requests.at(-1).options.body;assert.equal(JSON.parse(retryBody).expected_revision,6);
 f.respond(async()=>({ok:false,status:404}));await f.poll();assert(f.requests.at(-1).url.includes('request_action=retry'));assert.equal(f.retry.elements.expected_revision.value,6);
 f.retry.elements.expected_revision.value=99;f.respond(async()=>reply({job_id:'retry-job',status:'queued',control_revision:1,query_ready:false}));await f.submit(f.retry);assert.equal(f.requests.at(-1).options.body,retryBody,'unconfirmed retry command must remain byte-for-byte stable');assert.equal(f.jobLink.href,'/automation/retry-job');
 f.respond(async()=>reply({job_id:'retry-job',status:'succeeded',control_revision:2,query_ready:true}));await f.poll();assert.equal(f.link.hidden,false);assert(f.link.href.includes('semantic=1'));assert(f.link.href.includes('kind=owner_reflection'));assert.equal(new URL(f.link.href,'http://local').searchParams.get('q'),'如何应用');
 let resolve;f.respond(()=>new Promise(r=>resolve=r));const late=f.poll();const text=f.status.textContent;f.abort.abort();resolve(reply({job_id:'late-job',status:'failed',error:'late',query_ready:false}));await late;assert.equal(f.status.textContent,text);assert.equal(f.jobLink.href,'/automation/retry-job');
 console.log('semantic controller: explicit prepare, readonly lookup, unknown identity, normalized link and unmount passed');
})().catch(error=>{console.error(error);process.exit(1)});
