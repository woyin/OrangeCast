const fs=require('fs'),vm=require('vm'),assert=require('assert'),{webcrypto}=require('crypto');
const settle=()=>new Promise(resolve=>setImmediate(resolve));
async function scenario(restore=false){
 const nodes={},requests=[],storage=new Map(),listeners={};let active={sourceType:'episode',sourceId:'A',title:'正在播放A',csrf:'never-persist',anchor:{snapshot_id:'snap-A',version:1,mode:'original',position:12,segment_ids:['seg-A']}};
 const node=id=>nodes[id]||(nodes[id]={hidden:true,value:'',textContent:'',disabled:false,dataset:{},children:[],events:{},addEventListener(e,fn){this.events[e]=fn},getAttribute(name){return name==='action'?'/api/listening-reflections':null},querySelector(){return node('reflection-feedback')},querySelectorAll(){return ['remember','uncertain','apply'].map(f=>node('reflection-'+f)).concat([node('reflection-save'),node('reflection-sync'),node('reflection-question')])},replaceChildren(...items){this.children=items},append(item){this.children.push(item)},focus(){this.focused=true},get selectedOptions(){return this.children.filter(o=>o.value===this.value)}});
 const localStorage={getItem:k=>storage.get(k)||null,setItem:(k,v)=>storage.set(k,v),removeItem:k=>storage.delete(k)};
 if(restore)storage.set('cwp-reflection-draft-v1',JSON.stringify({sessionId:'session',current:{id:'restored',capture:active,answers:{remember:'刷新前的理解'},revision:1,question_id:'',question_revision:0,createdAt:Date.now()}}));
 let revision=1,failSave=true,serverAnswers={};
 const fetch=async(url,opts={})=>{requests.push({url,opts});if(!opts.method)return {ok:true,json:async()=>({session_id:'session',csrf:'csrf',questions:[{ID:'q',Body:'关联问题',Revision:3}]})};
  const cmd=JSON.parse(opts.body);if(cmd.action==='save'&&failSave){failSave=false;throw Error('ack lost')}
  if(cmd.action==='edit'){revision++;serverAnswers=cmd.answers}if(cmd.action==='voice_adopt'){revision++;serverAnswers={...serverAnswers,[cmd.field]:'明确采用的语音文字'}}return {ok:true,json:async()=>({id:cmd.id,revision,state:cmd.action==='save'?'saved':cmd.action==='cancel'?'cancelled':'draft',saved_note_id:'one-note',answers:serverAnswers}),text:async()=>''};
 };
 const root={addEventListener:(e,fn)=>listeners[e]=fn,CWPViews:{define(){}},CWPVoice:{async start(options){root.voiceCapture=options}},CWPListening:{anchor:()=>structuredClone(active),notify(value){root.notice=value}}};
 const context={window:root,document:{getElementById:node},localStorage,fetch,AbortController,structuredClone,crypto:webcrypto,Date,Number,JSON,Object,Option:function(text,value){this.text=text;this.value=value;this.dataset={}},setTimeout,clearTimeout,URLSearchParams,FormData:class {constructor(){this.values=new Map()}set(k,v){this.values.set(k,v)}},console};
 vm.runInNewContext(fs.readFileSync('static/form-actions.js','utf8'),context);
 vm.runInNewContext(fs.readFileSync('static/listening-reflections.js','utf8'),context);
 for(let i=0;i<5;i++)await settle();
 if(restore){assert.equal(node('reflection-remember').value,'刷新前的理解');assert(requests.every(r=>!r.opts.method));root.CWPReflections.clearPrivate();assert.equal(node('reflection-remember').value,'');assert(!storage.has('cwp-reflection-draft-v1'));return}
 await root.CWPReflections.start();assert(node('reflection-anchor').textContent.includes('正在播放A'));assert.equal(node('reflection-remember').focused,true);
 active={...active,sourceId:'B',title:'浏览B'};
 node('reflection-question').value='q';node('reflection-question').events.change();
 node('reflection-remember').value='我自己的解释';await node('reflection-sync').onclick();for(let i=0;i<5;i++)await settle();
 assert.equal(node('reflection-question').disabled,true);const start=JSON.parse(requests.find(r=>r.opts.method).opts.body);assert.equal(start.capture.sourceId,'A');assert.equal(start.question_revision,3);assert(!('csrf' in start.capture));
 await node('reflection-voice-start').onclick();assert.equal(root.voiceCapture.capture.sourceId,'A');
 const voice={id:'voice',revision:1};await root.CWPReflections.adoptVoice(voice,'uncertain',root.voiceCapture.reflectionId);assert.equal(node('reflection-uncertain').value,'明确采用的语音文字');voice.asr_text='迟到的ASR';assert.equal(node('reflection-uncertain').value,'明确采用的语音文字');assert.equal(node('reflection-remember').value,'我自己的解释');
 const event={preventDefault(){}};await node('reflection-form').events.submit(event);
 assert.equal(node('reflection-editor').hidden,false,'lost response keeps editor');assert.equal(node('reflection-remember').value,'我自己的解释');assert.equal(node('reflection-remember').disabled,true,'unknown formal save locks payload');
 await node('reflection-form').events.submit(event);
 const saves=requests.filter(r=>r.opts.method&&JSON.parse(r.opts.body).action==='save').map(r=>JSON.parse(r.opts.body));assert.equal(saves.length,2);assert.equal(saves[0].request_key,saves[1].request_key);assert.deepEqual(saves[0],saves[1]);assert.equal(node('reflection-editor').hidden,true);assert(!storage.has('cwp-reflection-draft-v1'));
 await root.CWPReflections.start({sourceType:'episode',sourceId:'none',anchor:{no_position:true}});assert.equal(node('reflection-editor').hidden,true,'no position cannot become zero');
}
(async()=>{await scenario();await scenario(true);console.log('frozen playing source, question revision, lost-ack replay, private restore and logout verified')})().catch(e=>{console.error(e);process.exit(1)});
