const fs=require('fs'),vm=require('vm'),assert=require('assert');
const settle=()=>new Promise(resolve=>setImmediate(resolve));
async function scenario(local){
 const nodes={},requests=[],draft={id:'draft',sessionId:'session',createdAt:'2026-10-01',text:'恢复后保留我的解释',capture:{title:'来源A',sourceType:'episode',sourceId:'A',anchor:{snapshot_id:'snap',version:1,position:12,mode:'original'}},blob:new Blob(['audio'])};
 const node=id=>nodes[id]||(nodes[id]={hidden:true,value:'',textContent:'',children:[],replaceChildren(...items){this.children=items},append(item){this.children.push(item)},dataset:{}});
 const panel=node('voice-recorder'),list=node('voice-local-list');let click;
 const database={transaction(){const tx={objectStore(){return {getAll(){const req={};setImmediate(()=>{req.result=[draft];req.onsuccess?.();tx.oncomplete?.()});return req},delete(){},put(){}}}};return tx},close(){}};
 const indexedDB={open(){const req={};setImmediate(()=>{req.result=database;req.onsuccess?.()});return req}};
 const content={querySelector:()=>list},document={getElementById:node,createElement:()=>({}),querySelector:()=>null};
 const root={CWPListening:{captureNote(capture){root.deniedCapture=capture},notify(){}},indexedDB,addEventListener(){},CWPViews:{define(fn){fn({querySelector:()=>content},{on(_,event,fn){click=fn}})}}};
 const context={window:root,document,indexedDB,AbortController,structuredClone,Blob,Option:function(t,v){this.text=t;this.value=v;this.dataset={}},fetch:async(url,opts)=>{requests.push({url,method:opts.method||'GET'});return {ok:true,json:async()=>url.endsWith('/session')?{session_id:'session',questions:[]}:{id:'draft',text:draft.text,source_type:'episode',source_id:'A',anchor_json:JSON.stringify(draft.capture.anchor),revision:1,state:'uploaded'}}},setTimeout,clearTimeout,setInterval,clearInterval};
 vm.runInNewContext(fs.readFileSync('static/voice-notes.js','utf8'),context);
 const target={closest(selector){if(local&&selector.includes('="local"'))return {};if(!local&&selector==='[data-voice-open]')return {dataset:{voiceOpen:'draft'}};return null}};
 click({target});for(let i=0;i<5;i++)await settle();
 if(local){assert.equal(list.children.length,1);await list.children[0].onclick()}
 for(let i=0;i<5;i++)await settle();
 await root.CWPVoice.start({capture:draft.capture,reflectionId:'reflection'});assert.equal(root.deniedCapture.sourceId,'A','unsupported recording retains reflection capture');
 assert.equal(panel.hidden,false,'restored draft editor must open');assert.equal(node('voice-text').value,draft.text);assert.equal(node('voice-text').disabled,false);assert(node('voice-anchor').textContent.includes('0:12'));assert(requests.every(r=>r.method==='GET'),'restoring must not upload or trigger ASR');
}
(async()=>{await scenario(true);await scenario(false);console.log('local/server voice restoration opens editor without upload or ASR')})().catch(e=>{console.error(e);process.exit(1)});
