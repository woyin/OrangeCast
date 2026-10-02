const assert=require('node:assert/strict'),fs=require('node:fs'),vm=require('node:vm'),path=require('node:path');
let mount,binding;const memory=new Map(),events=new Map(),feedback={textContent:'',isConnected:true,focus(){this.focused=true;}};
const fields={action:{value:'create'},kind:{value:'practice'},explanation:{value:''},expected_revision:{value:'7'}};
const form={elements:{namedItem:n=>fields[n]},querySelector:()=>feedback};
const root={dataset:{question:'question'},querySelectorAll:()=>[form]};
const key='cwp-evidence-gap-draft:question:new:create';memory.set(key,JSON.stringify({kind:'counterexample',explanation:'我尚未保存的反例问题'}));
const window={CWPViews:{define:fn=>mount=fn},CWPForms:{bind:(f,s,o)=>binding=o}};
vm.runInNewContext(fs.readFileSync(path.join(__dirname,'../static/evidence-gaps.js'),'utf8'),{window,sessionStorage:{getItem:k=>memory.get(k),setItem:(k,v)=>memory.set(k,v),removeItem:k=>memory.delete(k)},Object,JSON});
const scope={on:(n,e,fn)=>events.set(n,fn)};mount({querySelector:()=>root},scope);
assert.equal(fields.explanation.value,'我尚未保存的反例问题');assert.equal(fields.kind.value,'counterexample');assert.equal(fields.expected_revision.value,'7');
fields.explanation.value='跨页和冲突保留的文字';events.get(fields.explanation)();binding.before();assert.match(memory.get(key),/跨页和冲突保留/);
// No rejection hook clears or rewrites the draft; shared CWPForms covers actual
// 409, network ambiguity, control restoration and disposal independently.
binding.error({status:409});assert.equal(feedback.focused,true);assert.equal(feedback.tabIndex,-1);assert.match(memory.get(key),/跨页和冲突保留/);assert.equal(binding.encoding,'urlencoded');mount({querySelector:()=>root},scope);assert.equal(fields.explanation.value,'跨页和冲突保留的文字');binding.success();assert.equal(memory.has(key),false);
console.log('Evidence gap drafts preserve user fields and parent CAS across remount, conflict and success');
