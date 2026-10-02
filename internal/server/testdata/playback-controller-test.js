const assert = require('node:assert/strict');
let time=0,paused=true,rate=1,clock=10000,serverRevision=0, requests=[],conflict=false,offline=false;
const events={};
globalThis.addEventListener=(n,f)=>{events[n]=f;};globalThis.removeEventListener=()=>{};
globalThis.document={hidden:false,addEventListener:(n,f)=>{events[n]=f;},removeEventListener:()=>{}};
Object.defineProperty(globalThis,'navigator',{value:{mediaSession:{setActionHandler:(n,f)=>{events[n]=f;}}},configurable:true});
const localValues=new Map([['cwp-playback-rate','1.75']]);
globalThis.localStorage={getItem:key=>localValues.get(key)||null,setItem:(key,value)=>localValues.set(key,value),removeItem:key=>localValues.delete(key)};
globalThis.setInterval=()=>1;globalThis.clearInterval=()=>{};
const flush=async()=>{for(let i=0;i<10;i++)await Promise.resolve();};
const fetcher=async(url,opts)=>{
 if(offline)throw Error('offline');
 if(!opts||opts.method!=='POST')return {ok:true,json:async()=>({id:'p',revision:serverRevision,speed:1.25,item_offset_seconds:38})};
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
 c.tick();await flush();assert.equal(requests.length,0,'opening a paused page must not overwrite saved progress');assert.equal(c.status().dirty,false);
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
 assert.equal(events.play,null,'destroy removes MediaSession action');
 // A newer seek is durable while the first request is outstanding. Its old response cannot erase it.
 let slowTime=0,pending=[],slowCalls=[];
 const slow=CWPPlayback.create({sourceType:'episode',sourceId:'slow',mode:'original',audioSHA:'sha',csrf:'token',now:()=>++clock,
  fetch:async(url,opts)=>{if(opts?.method!=='POST')return {ok:true,json:async()=>({revision:0})};slowCalls.push(JSON.parse(opts.body));return new Promise(resolve=>pending.push(resolve));},
  adapter:{play:async()=>{},pause:()=>{},paused:()=>true,time:()=>slowTime,seek:value=>{slowTime=value;},setRate:()=>{},getRate:()=>1,snapshot:()=>({item_offset_seconds:slowTime}),restore:()=>{}}});
 await flush();slow.seek(11);await flush();slow.seek(22);await flush();
 const key='cwp-pending-progress:episode:slow:original:sha:';
 assert.equal(JSON.parse(localValues.get(key)).item_offset_seconds,22);assert.equal(slowCalls.length,1);
 pending.shift()({ok:true,json:async()=>({revision:1})});await flush();
 assert.equal(JSON.parse(localValues.get(key)).item_offset_seconds,22);assert.equal(slowCalls.length,2);assert.equal(slowCalls[1].expected_revision,1);
 pending.shift()({ok:true,json:async()=>({revision:2})});await flush();assert.equal(localValues.has(key),false);slow.destroy();
 let deliverRead,lateResume=false,lateRate=false;
 const late=CWPPlayback.create({sourceType:'episode',sourceId:'late',mode:'original',fetch:()=>new Promise(resolve=>{deliverRead=resolve;}),
  adapter:{play:()=>{},pause:()=>{},paused:()=>true,time:()=>0,seek:()=>{},setRate:()=>{lateRate=true;},getRate:()=>1,snapshot:()=>null,restore:()=>{}},onResume:()=>{lateResume=true;}});
 lateRate=false;late.destroy();deliverRead({ok:true,json:async()=>({id:'late',revision:2,speed:2})});await flush();assert.equal(lateResume,false);assert.equal(lateRate,false);assert.equal(late.status().loaded,false);
 let resumeServer,conflictPosts=0,localTime=5;
 const refreshed=CWPPlayback.create({sourceType:'episode',sourceId:'refresh',mode:'original',audioSHA:'sha',localRevision:3,
  fetch:async(url,opts)=>{if(opts?.method==='POST'){conflictPosts++;assert.equal(JSON.parse(opts.body).item_offset_seconds,88);return{ok:true,json:async()=>({revision:5})};}return{ok:true,json:async()=>({id:'p',revision:4,item_offset_seconds:88,audio_sha256:'sha'})};},
  adapter:{play:async()=>{},pause:()=>{},paused:()=>true,time:()=>localTime,seek:value=>{localTime=value;},setRate:()=>{},getRate:()=>1,snapshot:()=>({item_offset_seconds:localTime}),restore:saved=>{localTime=saved.item_offset_seconds;}},onResume:(saved,run)=>{resumeServer=run;}});
 await flush();assert.equal(refreshed.status().conflict,true);refreshed.seek(6);refreshed.tick();await flush();assert.equal(conflictPosts,0);
 resumeServer();await flush();assert.equal(localTime,88);await refreshed.save();assert.equal(conflictPosts,1);refreshed.destroy();
 const pendingAck={source_type:'episode',source_id:'ack',mode:'original',audio_sha256:'sha',item_offset_seconds:88,expected_revision:3,seq:77,speed:1};
 const ackKey='cwp-pending-progress:episode:ack:original:sha:';localValues.set(ackKey,JSON.stringify(pendingAck));
 const ack=CWPPlayback.create({sourceType:'episode',sourceId:'ack',mode:'original',audioSHA:'sha',localRevision:3,
  fetch:async()=>({ok:true,json:async()=>({...pendingAck,revision:4,plan_id:'',plan_version:0,item_position:0,highlight_id:''})}),
  adapter:{play:()=>{},pause:()=>{},paused:()=>true,time:()=>88,seek:()=>{},setRate:()=>{},getRate:()=>1,snapshot:()=>null,restore:()=>{}}});
 await flush();assert.equal(ack.status().conflict,false,'its own acknowledged pagehide write is not another-window conflict');assert.equal(localValues.has(ackKey),false);ack.destroy();
 let inheritedPaused=false;const deadline=clock+60000;
 const inherited=CWPPlayback.create({sourceType:'episode',sourceId:'sleep-next',mode:'original',sleepDeadline:deadline,now:()=>clock,
  fetch:async()=>({ok:true,json:async()=>({})}),adapter:{play:()=>{inheritedPaused=false;},pause:()=>{inheritedPaused=true;},paused:()=>inheritedPaused,time:()=>0,seek:()=>{},setRate:()=>{},getRate:()=>1,snapshot:()=>null,restore:()=>{}}});
 await flush();assert.equal(inherited.status().deadline,deadline);clock=deadline;await inherited.play();assert.equal(inheritedPaused,true);assert.equal(inherited.status().sleepExpired,true);assert.equal(inherited.status().deadline,0);inherited.destroy();
 const endState={spec:{mode:'original'},audio:{ended:true,error:null,currentTime:100,duration:100},transport:{status:()=>({})},player:null,loop:null};
 assert.equal(CWPPlayback.canOfferReflection(endState,1000),true);
 for(const value of [{...endState,spec:{mode:'dj'}},{...endState,spec:{mode:'original',excerptId:'excerpt'}},{...endState,player:{}},{...endState,loop:{start:1,end:2}},{...endState,audio:{...endState.audio,error:{code:4}}},{...endState,audio:{...endState.audio,ended:false}},{...endState,audio:{...endState.audio,currentTime:10}},{...endState,audio:{...endState.audio,duration:NaN}},{...endState,transport:{status:()=>({sleepExpired:true})}},{...endState,transport:{status:()=>({deadline:999})}}])assert.equal(CWPPlayback.canOfferReflection(value,1000),false,'partial/error/sleep end cannot prompt');
 let excerptRead='',excerptBody,resumes=0,excerptPosition=35;
 const excerpt=CWPPlayback.create({sourceType:'episode',sourceId:'same-source',mode:'excerpt',excerptId:'interval-a',snapshotId:'snapshot',audioSHA:'hash',
 fetch:async(url,opts)=>{if(opts?.method==='POST'){excerptBody=JSON.parse(opts.body);return {ok:true,json:async()=>({revision:1})};}excerptRead=url;return {ok:true,json:async()=>({id:'p',revision:0,audio_sha256:'hash',excerpt_id:'interval-b',snapshot_id:'snapshot'})};},
 adapter:{play:()=>{},pause:()=>{},paused:()=>true,time:()=>excerptPosition,seek:n=>{excerptPosition=n;},setRate:()=>{},getRate:()=>1,snapshot:()=>({item_offset_seconds:excerptPosition}),restore:()=>{}},onResume:()=>{resumes++;}});
 await flush();assert.match(excerptRead,/mode=excerpt&excerpt_id=interval-a/);assert.equal(resumes,0,'another interval never restores this interval');
 excerpt.seek(39);await flush();assert.equal(excerptBody.excerpt_id,'interval-a');assert.equal(excerptBody.snapshot_id,'snapshot');assert.equal(excerptBody.item_offset_seconds,39,'position remains absolute audio time');excerpt.destroy();
 assert.equal(CWPPlayback.canOfferReflection({...endState,spec:{mode:'excerpt',excerptId:'interval-a'}},1000),false);
 process.stdout.write('playback behavior passed');
})().catch(err=>{process.stderr.write(String(err.stack));process.exitCode=1;});
