#!/usr/bin/env python3
"""Agent-browser E2E against the opt-in Go router and fresh scratch SQLite.

Start: CWP_OFFLINE_GO_BROWSER=1 CWP_OFFLINE_GO_STOP=/tmp/cwp-offline-go-stop
       go test ./internal/server -run '^TestOfflineGoBrowserHarness$' -v -count=1 -timeout=30m
Run: python3 scripts/offline-go-browser-acceptance.py [base-url] [evidence-dir]
Stop the harness by creating CWP_OFFLINE_GO_STOP after this driver exits.
Synthetic desktop evidence does not prove physical-mobile or real-user quality.
"""
import json,pathlib,subprocess,sys,time,uuid,urllib.request,select
base=sys.argv[1] if len(sys.argv)>1 else 'http://127.0.0.1:18777'
evidence=pathlib.Path(sys.argv[2] if len(sys.argv)>2 else '/tmp/cwp-offline-go-browser-evidence');evidence.mkdir(parents=True,exist_ok=True)
session='offline-go-'+uuid.uuid4().hex[:8]
def browser(*args,script=None):
 r=subprocess.run(['agent-browser','--session',session,*args],input=script,text=True,capture_output=True,timeout=40)
 if r.returncode:raise AssertionError(r.stderr or r.stdout)
 return r.stdout.strip()
def js(s):return json.loads(browser('eval','--stdin',script=s))
def check(v,label):
 if not v:raise AssertionError(label)
 print('PASS '+label,flush=True)
def wait_js(s):
 deadline=time.monotonic()+15
 while time.monotonic()<deadline:
  if js(s):return
  time.sleep(.1)
 print('WAIT DEBUG '+str(js("(async()=>({status:document.getElementById('status')?.textContent,ops:await CWPOfflineStorage.all('outbox'),grant:await CWPOfflineStorage.get('meta','grant'),click:String(document.getElementById('sync')?.onclick),requests:window.__requests,syncCalls:window.__syncCalls}))()")),flush=True)
 raise AssertionError('async browser condition timed out: '+s)
def browser_sw_blocker():
 # Use the actual Chrome CDP Network settings; keep this session attached so
 # the browser-level block remains active during native page navigation.
 endpoint=browser('get','cdp-url')
 code=r"""
const endpoint=process.argv[1],origin=process.argv[2],ws=new WebSocket(endpoint),pending=new Map();let serial=0;
function call(method,params={},sessionId){return new Promise((resolve,reject)=>{const id=++serial;pending.set(id,{resolve,reject});ws.send(JSON.stringify({id,method,params,...(sessionId?{sessionId}:{})}));});}
const networkResponses=[];ws.addEventListener('message',e=>{const value=JSON.parse(e.data);if(value.method==='Network.responseReceived')networkResponses.push({url:value.params.response.url,fromServiceWorker:!!value.params.response.fromServiceWorker,status:value.params.response.status});if(value.id&&pending.has(value.id)){const p=pending.get(value.id);pending.delete(value.id);value.error?p.reject(new Error(JSON.stringify(value.error))):p.resolve(value.result);}});
ws.addEventListener('open',async()=>{try{
 const targets=await call('Target.getTargets'),page=targets.targetInfos.find(t=>t.type==='page'&&t.url.startsWith(origin));if(!page)throw Error('own page missing');
 const {sessionId}=await call('Target.attachToTarget',{targetId:page.targetId,flatten:true});
 await call('Network.enable',{},sessionId);await call('Network.setBypassServiceWorker',{bypass:true},sessionId);await call('Network.setBlockedURLs',{urls:[origin+'/sw.js']},sessionId);
 console.log(JSON.stringify({ready:true,settings:['Network.setBypassServiceWorker','Network.setBlockedURLs'],cleared:'none'}));
 const readline=await import('node:readline');const input=readline.createInterface({input:process.stdin});input.once('line',async()=>{await call('Network.setBlockedURLs',{urls:[]},sessionId);await call('Network.setBypassServiceWorker',{bypass:false},sessionId);console.log(JSON.stringify({responses:networkResponses}));ws.close();input.close();});
 }catch(e){console.error(e.message);process.exitCode=1;ws.close();}});
"""
 process=subprocess.Popen(['node','-e',code,endpoint,base],stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True)
 if not select.select([process.stdout],[],[],15)[0]:process.terminate();raise AssertionError('CDP settings setup timed out')
 ready=process.stdout.readline()
 if not ready:raise AssertionError(process.stderr.read())
 check(json.loads(ready)['ready'],'actual Chrome CDP service-worker bypass and worker-script block enabled')
 return process
def stats():return js("fetch('/__offline_stats').then(r=>r.json())")
try:
 deadline=time.monotonic()+30
 while True:
  try:
   with urllib.request.urlopen(base+'/__offline_stats',timeout=1) as r:r.read()
   break
  except OSError:
   if time.monotonic()>deadline:raise
   time.sleep(.1)
 baseline=json.load(urllib.request.urlopen(base+'/__offline_stats'))
 check(baseline['offline_operation_receipts']==0 and baseline['owner_notes']==0,'fresh isolated Go database')
 browser('open',base+'/login');browser('snapshot','-i')
 browser('find','label','邮箱','fill','offline-go@example.com');browser('find','label','密码','fill','password123');browser('find','role','button','click','--name','登录')
 browser('open',base+'/offline');browser('wait','--fn',"navigator.serviceWorker.controller!==null")
 check(js("document.getElementById('private').hidden"),'actual authenticated device defaults disabled')
 browser('click','#enable');browser('wait','--fn',"document.querySelector('#catalog-sources input')!==null")
 browser('snapshot','-i');browser('check','#catalog-sources input');browser('check','#catalog-excerpts label:nth-of-type(1) input');browser('check','#catalog-excerpts label:nth-of-type(2) input');browser('click','#download')
 browser('wait','--fn',"document.querySelector('#files button')!==null")
 check(js("CWPOfflineStorage.all('files').then(rows=>rows.some(r=>r.state==='ready'&&r.blob))"),'actual Go manifest bytes stored ready in IndexedDB')
 result=js("(async()=>{const rows=await CWPOfflineStorage.all('files'),f=rows.find(x=>x.file?.kind==='audio').file;const r=await fetch('/offline-audio?file_id='+f.id,{headers:{Range:'bytes=44-53'}});return {id:f.id,status:r.status,bytes:(await r.arrayBuffer()).byteLength};})()")
 check(result['status']==206 and result['bytes']==10,'service worker range over actual local WAV')
 browser('click','#files button');browser('wait','--fn',"document.getElementById('audio').currentTime>0")
 played=js("(()=>{const a=document.getElementById('audio');a.currentTime=2.5;a.pause();return {duration:a.duration,position:a.currentTime};})()")
 check(abs(played['duration']-8)<.1 and abs(played['position']-2.5)<.1,'actual WAV playback and seek')
 browser('set','offline','on');browser('open',base+'/offline');browser('wait','--fn',"document.querySelector('#files button')!==null");browser('click','#files button');browser('wait','--fn',"document.getElementById('audio').currentTime>0")
 browser('eval',"document.getElementById('audio').pause();true")
 browser('fill','#text','Go浏览器断网个人笔记');browser('click','#save-note');browser('fill','#text','Go浏览器断网独立整理');browser('click','#save-reflection');browser('scrollintoview','#save-progress');browser('snapshot','-i');browser('click','#save-progress')
 wait_js("CWPOfflineStorage.all('outbox').then(r=>r.length===3)")
 browser('screenshot',str(evidence/'offline-pending.png'))
 check(js("CWPOfflineStorage.all('outbox').then(r=>r.every(x=>x.state==='pending'))"),'actual offline reload and durable note/draft/progress outbox')
 browser('set','offline','off');browser('open',base+'/offline');browser('wait','--fn',"document.getElementById('status').textContent.includes('在线授权已核对')")
 # Test-only document-scoped browser capability faults, not mocked API data.
 for fault in ['unsupported-sw','denied-sw','failed-idb']:
  browser('open',base+'/__offline_capability?case='+fault)
  wait_js("document.getElementById('status').textContent.includes('在线站点仍可使用')")
  check(js("window.__offlineCapabilityCase") == fault and js("document.getElementById('private').hidden"),'actual browser '+fault+' offers online fallback without private UI')
  cache=js("(async()=>{const urls=[];for(const name of await caches.keys()){const c=await caches.open(name);for(const r of await c.keys())urls.push(new URL(r.url).pathname)}return urls})()")
  check(all(u.startswith('/static/') for u in cache),'capability failure retains only whitelisted static cache')
  browser('screenshot',str(evidence/(fault+'.png')))
  browser('open',base+'/dashboard')
  check('offline-go@example.com' in browser('get','text','body'),'real authenticated SSR works after '+fault)
  browser('click','a[href="/offline"]');wait_js("document.getElementById('status')?.textContent.includes('在线授权已核对')")
  check(js("CWPOfflineStorage.all('outbox').then(r=>r.length===3&&r.every(x=>x.state==='pending'))"),'existing durable drafts survive '+fault+' unchanged')
 blocker=browser_sw_blocker()
 try:
  bypassed=js("(async()=>{const g=await CWPOfflineStorage.get('meta','grant'),f=g.files.find(f=>f.kind==='audio');const r=await fetch('/offline-audio?file_id='+f.id);return {status:r.status,support:!!navigator.serviceWorker,rows:(await CWPOfflineStorage.all('outbox')).length}})()")
  check(bypassed=={'status':404,'support':True,'rows':3},'Chrome browser-level SW bypass reaches Go404 rather than ready audio blob, preserving API support and drafts')
  browser('open',base+'/dashboard');check('offline-go@example.com' in browser('get','text','body'),'real SSR works while Chrome bypasses service worker')
  browser('screenshot',str(evidence/'chrome-worker-bypassed.png'))
 finally:
  blocker.stdin.write('restore\n');blocker.stdin.flush();blocker.wait(timeout=10)
 report=json.loads(blocker.stdout.readline());(evidence/'chrome-network-settings.json').write_text(json.dumps(report,indent=2))
 check(any('/dashboard' in r['url'] and r['status']==200 and not r['fromServiceWorker'] for r in report['responses']),'actual Chrome network event proves private SSR response bypassed service worker')
 browser('click','a[href="/offline"]');wait_js("document.getElementById('status')?.textContent.includes('在线授权已核对')")
 check(js("CWPOfflineStorage.all('outbox').then(r=>r.length===3&&r.every(x=>x.state==='pending'))"),'Chrome worker-setting change preserves all existing IndexedDB drafts')
 cache=js("(async()=>{const urls=[];for(const name of await caches.keys()){const c=await caches.open(name);for(const r of await c.keys())urls.push(new URL(r.url).pathname)}return urls})()")
 check(all(u.startswith('/static/') for u in cache),'Chrome worker bypass never caches private SSR or API responses')

 # Drop real acknowledgements after commit, including automatic browser retries; explicitly restore before replay.
 check(js("fetch('/__offline_control',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({action:'lose_ack'})}).then(r=>r.status===200)"),'real Go acknowledgement drop armed after commit')
 js("(()=>{window.__pointerTrace=[];for(const k of ['pointerdown','pointerup','click'])document.addEventListener(k,e=>window.__pointerTrace.push({kind:k,id:e.target.id,x:e.clientX,y:e.clientY}),true);return true})()")
 browser('scrollintoview','#sync');browser('snapshot','-i');print('SYNC HIT '+str(js("(()=>{const r=document.getElementById('sync').getBoundingClientRect();return {x:r.x,y:r.y,w:r.width,h:r.height,hit:document.elementFromPoint(r.x+r.width/2,r.y+r.height/2)?.id}})()")),flush=True);browser('find','role','button','click','--name','明确同步待同步操作');print('POINTER '+str(js("window.__pointerTrace")),flush=True);wait_js("fetch('/__offline_stats').then(r=>r.json()).then(s=>s.lost_acks>=1&&s.offline_operation_receipts===3)");wait_js("!CWPOfflineOperations.busy()&&document.getElementById('status').textContent.includes('未保存文字')")
 check(True,'native sync button committed then real HTTP connection closed before acknowledgement')
 first=stats();check(first['owner_notes']==1 and first['offline_organizing_drafts']==1 and first['offline_operation_receipts']==3,'server committed each offline operation exactly once despite lost ack')
 check(js("fetch('/__offline_control',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({action:'restore_ack'})}).then(r=>r.status===200)"),'restore acknowledgements before explicit replay')
 browser('scrollintoview','#sync');browser('snapshot','-i');print('SYNC HIT '+str(js("(()=>{const r=document.getElementById('sync').getBoundingClientRect();return {x:r.x,y:r.y,w:r.width,h:r.height,hit:document.elementFromPoint(r.x+r.width/2,r.y+r.height/2)?.id}})()")),flush=True);browser('find','role','button','click','--name','明确同步待同步操作');print('POINTER '+str(js("window.__pointerTrace")),flush=True);wait_js("CWPOfflineStorage.all('outbox').then(r=>r.length===0)")
 second=stats();check(second['owner_notes']==1 and second['offline_organizing_drafts']==1 and second['offline_operation_receipts']==3,'same UUID/hash replay deduplicates real DB and clears durable outbox')
 browser('open',base+'/dashboard');browser('wait','--fn',"!!window.CWPOfflineDownloads")
 js("(async()=>{const g=await CWPOfflineStorage.get('meta','grant'),f=g.files.find(f=>f.kind==='audio');await CWPOfflineDownloads.startDownloaded(f.id,{excerptId:g.excerpts[0].excerpt_id,paused:true});return true})()")
 browser('click','#listening-toggle');wait_js("CWPListening.state().time>0")
 js("(async()=>{const s=CWPListening.state();s.audio.currentTime=.75;await s.transport.pause();return true})()")
 wait_js("CWPOfflineStorage.all('outbox').then(r=>r.some(x=>x.kind==='excerpt_progress'))")
 js("(()=>{localStorage.setItem('offline-nav-trace','[]');for(const k of ['beforeunload','pagehide'])window.addEventListener(k,e=>{const s=CWPListening.state();const rows=JSON.parse(localStorage.getItem('offline-nav-trace'));rows.push({event:k,paused:s.paused,time:s.time,mode:s.spec?.mode,transport:s.transport?.status()});localStorage.setItem('offline-nav-trace',JSON.stringify(rows))});return true})()")
 browser('snapshot','-i');browser('click','a[href="/offline"]');wait_js("document.getElementById('status')?.textContent.includes('在线授权已核对')")
 print('ROOT NAV '+str(js("JSON.parse(localStorage.getItem('offline-nav-trace'))")),flush=True)
 browser('scrollintoview','#sync');browser('click','#sync');wait_js("CWPOfflineStorage.all('outbox').then(r=>r.length===0)")
 part=stats();check(part['learning_excerpt_progress']==1 and part['original_progress']['revision']==second['original_progress']['revision'],'root downloaded excerpt playback sync leaves original progress revision unchanged')
 shell_id=js("CWPOfflineStorage.get('meta','grant').then(g=>g.excerpts.find(e=>e.start_seconds===1).excerpt_id)")
 browser('snapshot','-i');browser('click','button[data-excerpt-id="'+shell_id+'"]');wait_js("document.getElementById('audio').currentTime>=1&&!document.getElementById('audio').paused")
 js("document.getElementById('audio').currentTime=.1;true");wait_js("document.getElementById('audio').currentTime>=1")
 js("document.getElementById('audio').currentTime=6;true");wait_js("document.getElementById('audio').currentTime===2&&document.getElementById('audio').paused")
 check(True,'native offline shell frozen 1–2s interval clamps both seek bounds and pauses at end')
 js("document.getElementById('audio').currentTime=1.5;true");wait_js("CWPOfflineStorage.get('meta','grant').then(async g=>{const s=await CWPOfflineStorage.get('meta',g.namespace+':offline-shell-session');return s?.excerpt_id==="+json.dumps(shell_id)+"&&s.position===1.5})")
 browser('reload');wait_js("document.getElementById('audio').readyState>0&&document.getElementById('audio').paused&&document.getElementById('audio').currentTime===1.5")
 check(True,'offline shell reload restores frozen interval and exact position paused')
 wait_js("document.getElementById('status').textContent.includes('在线授权已核对')")
 browser('scrollintoview','#save-progress');browser('snapshot','-i');browser('click','#save-progress');wait_js("CWPOfflineStorage.all('outbox').then(r=>r.length===1&&r[0].kind==='excerpt_progress')")
 browser('scrollintoview','#sync');browser('click','#sync');wait_js("CWPOfflineStorage.all('outbox').then(r=>r.length===0)")
 shell=stats();check(shell['learning_excerpt_progress']==2 and shell['shell_excerpt_progress']['item_offset_seconds']==1.5 and shell['original_progress']['revision']==1 and shell['excerpt_progress']['revision']==part['excerpt_progress']['revision'],'shell second interval has independent real DB progress without overwriting first interval or original')
 browser('screenshot',str(evidence/'shell-excerpt.png'))
 browser('click','#files button');browser('wait','--fn',"document.getElementById('audio').currentTime>0");browser('eval',"document.getElementById('audio').pause();true")
 browser('fill','#text','来源变更时必须保留的待同步文字');browser('click','#save-reflection');wait_js("CWPOfflineStorage.all('outbox').then(r=>r.length===1)")
 check(js("fetch('/__offline_control',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({action:'change_source'})}).then(r=>r.status===200)"),'actual source revision changed')
 browser('open',base+'/offline');browser('wait','--fn',"!document.getElementById('locked-drafts').hidden")
 locked=js("(async()=>({private:document.getElementById('private').hidden,text:document.getElementById('locked-text').value,states:(await CWPOfflineStorage.all('outbox')).map(r=>r.state)}))()")
 print('LOCKED '+json.dumps(locked,ensure_ascii=False),flush=True)
 check(locked['private'] and '必须保留' in locked['text'] and locked['states']==['locked'],'real Go409 locks old source bytes and preserves copyable pending text')
 browser('screenshot',str(evidence/'source-change-locked.png'))
 browser('click','#clear');browser('wait','--fn',"document.getElementById('status').textContent.includes('已清理')")
 check(js("(async()=>((await CWPOfflineStorage.all('outbox')).length===0&&(await CWPOfflineStorage.all('files')).length===0&&!await CWPOfflineStorage.get('meta','grant')&&!await CWPOfflineStorage.get('meta','lockedGrant')))()"),'explicit exit clears real device private DB')
 final=stats();(evidence/'database-evidence.json').write_text(json.dumps({'after_replay':second,'after_excerpt_and_source_change':final},ensure_ascii=False,indent=2))
 print('DB '+json.dumps(final,ensure_ascii=False));print('SCREENSHOTS '+str(evidence));print('BROWSER '+js('navigator.userAgent'))
finally:browser('set','offline','off');browser('close')
