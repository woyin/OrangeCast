const assert = require('node:assert/strict');
let time=0,paused=true,rate=1,clock=10000,serverRevision=0, requests=[],conflict=false,offline=false;
const events={};
globalThis.addEventListener=(n,f)=>{events[n]=f;};globalThis.removeEventListener=()=>{};
globalThis.document={hidden:false,addEventListener:(n,f)=>{events[n]=f;},removeEventListener:()=>{}};
Object.defineProperty(globalThis,'navigator',{value:{mediaSession:{setActionHandler:(n,f)=>{events[n]=f;}}},configurable:true});
globalThis.localStorage={getItem:key=>key==='cwp-playback-rate'?'1.75':null,setItem:()=>{}};
globalThis.setInterval=()=>1;globalThis.clearInterval=()=>{};
const flush=async()=>{for(let i=0;i<10;i++)await Promise.resolve();};
const fetcher=async(url,opts)=>{
 if(offline)throw Error('offline');
 if(!opts)return {ok:true,json:async()=>({id:'p',revision:serverRevision,speed:1.25,item_offset_seconds:38})};
 const body=JSON.parse(opts.body);requests.push(body);
 if(conflict||body.expected_revision!==serverRevision)return {ok:false,status:409};
 return {ok:true,json:async()=>({revision:++serverRevision})};
};
(async()=>{
 let message='',resolve;
 const c=CWPPlayback.create({sourceType:'episode',sourceId:'s',mode:'original',csrf:'token',fetch:fetcher,now:()=>clock,
 adapter:{play:async()=>{paused=false;},pause:()=>{paused=true;},paused:()=>paused,time:()=>time,seek:n=>{time=Math.max(0,Math.min(100,n));},setRate:n=>{rate=n;},getRate:()=>rate,snapshot:()=>({item_offset_seconds:time}),restore:p=>{time=p.item_offset_seconds;}},
 notify:(m,action)=>{message=m;resolve=action;},onResume:(p,resume)=>{events.resume=resume;}});
 await flush();assert.equal(c.status().loaded,true);assert.equal(time,0);assert.equal(rate,1.25);assert.equal(paused,true);
 events.resume();await flush();assert.equal(time,38);assert.equal(paused,false);
 c.skip(-15);await flush();assert.equal(time,23);assert.equal(serverRevision,1);assert.equal(requests[0].mode,'original');
 c.skip(-100);await flush();assert.equal(time,0);c.skip(200);await flush();assert.equal(time,100);
 conflict=true;c.seek(40);await flush();assert.equal(c.status().conflict,true);const count=requests.length;c.tick();await flush();assert.equal(requests.length,count);
 conflict=false;serverRevision=9;await resolve();await flush();assert.equal(serverRevision,10);assert.equal(c.status().conflict,false);
 offline=true;c.seek(44);await flush();assert.equal(c.status().dirty,true);assert.equal(time,44);offline=false;events.online();await flush();assert.equal(c.status().dirty,false);
 c.setSleep(15);clock+=15*60000;c.tick();await flush();assert.equal(paused,true);assert.equal(c.status().deadline,0);
 events.play();await flush();assert.equal(paused,false);events.seekbackward({});await flush();assert.equal(time,29);
 c.setRate(7);assert.equal(rate,1.25);c.setRate(1.75);assert.equal(rate,1.75);
 events.keydown({target:{tagName:'TEXTAREA'},code:'Space',preventDefault:()=>{throw Error('typed shortcut');}});
 events.keydown({target:{tagName:'DIV'},code:'Space',preventDefault:()=>{}});assert.equal(paused,true);c.destroy();
 process.stdout.write('playback behavior passed');
})().catch(err=>{process.stderr.write(String(err.stack));process.exitCode=1;});
