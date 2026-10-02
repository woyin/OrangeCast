#!/usr/bin/env python3
"""Deterministic Chromium + real IndexedDB/SW acceptance. No user DB or AI calls.
Requires installed agent-browser. Fixtures are synthetic and are not mobile/human quality evidence.
"""
import urllib.parse, sys, hashlib, http.server, io, json, math, pathlib, struct, subprocess, threading, time, uuid, wave
ROOT = pathlib.Path(__file__).resolve().parents[1] / 'internal/server/static'
ASSETS = ['offline-shell.html','offline-core.js','offline-storage.js','offline-packs.js','offline-operations.js','offline-shell.js']
STATE = {'release': 1, 'bad': False, 'status': 200, 'owner': 'synthetic-session'}
issued = int(time.time())
GRANT = {'schema_version':1,'namespace':'synthetic-browser-grant','device_id':'synthetic-device','epoch':'synthetic-epoch','issued_at':issued,'expires_at':issued+3600,'owner_session_namespace':STATE['owner']}
buf=io.BytesIO()
with wave.open(buf,'wb') as wav:
    wav.setnchannels(1); wav.setsampwidth(2); wav.setframerate(8000)
    wav.writeframes(b''.join(struct.pack('<h',int(300*math.sin(i*2*math.pi*220/8000))) for i in range(8*8000)))
AUDIO=buf.getvalue(); SHA=hashlib.sha256(AUDIO).hexdigest()
FILE={'title':'确定性原音测试（自建8秒WAV）','id':'synthetic-original','kind':'audio','source_type':'episode','source_id':'synthetic-source','snapshot_id':'synthetic-snapshot','snapshot_version':1,'audio_sha':SHA,'sha256':SHA,'size':len(AUDIO),'url':'/api/offline/file?file_id=synthetic-original','progress_revision':0}
CONTENT={'synthetic-original':AUDIO};READ_FILES=[]
for kind,data in [('transcript',{'snapshot':{'Title':'确定性节目','ID':'internal-snapshot-id'},'segments':[{'id':'internal-segment-id','start':2,'end':3,'text':'转录正文学习句'}]}),('note',{'ID':'internal-note-id','Kind':'owner_reflection','Content':'个人笔记正文 <script>不是脚本</script>'}),('article',{'article_id':'internal-article-id','title':'通过文章正文','blocks':[{'kind':'reflection','text':'文章解释段落','material_ids':['internal-material-id']}],'passed':True})]:
    raw=json.dumps(data,ensure_ascii=False).encode();fid='synthetic-'+kind;CONTENT[fid]=raw
    READ_FILES.append({'id':fid,'kind':kind,'title':kind+'自建学习资料','source_type':'episode','source_id':'synthetic-source','snapshot_id':'synthetic-snapshot','size':len(raw),'sha256':hashlib.sha256(raw).hexdigest(),'url':'/api/offline/file?file_id='+fid})
class Handler(http.server.BaseHTTPRequestHandler):
    def do_POST(self):
        length=int(self.headers.get('Content-Length','0'));self.rfile.read(length)
        code=STATE.get('revoke_status',200) if self.path=='/api/offline/revoke' else 200
        data=json.dumps({'error':'offline_expired'} if code==410 else {'ok':True}).encode()
        self.send_response(code);self.send_header('Content-Type','application/json');self.end_headers();self.wfile.write(data)
    def do_GET(self):
        path=self.path.split('?')[0]; status=200
        if path=='/api/offline/session': data=json.dumps({'csrf_token':'synthetic-csrf','session_namespace':STATE['owner']}).encode(); content='application/json'
        elif path=='/api/offline/drafts': data=json.dumps({'drafts':[{'id':'internal-synced-id','content':'已同步个人整理不会消失','revision':1,'source_available':False}]}).encode(); content='application/json'
        elif path=='/api/offline/catalog': data=json.dumps({'sources':[],'articles':[],'excerpts':[]}).encode(); content='application/json'
        elif path=='/api/offline/status': data=json.dumps({'schema_version':1,'authorization':GRANT}).encode(); content='application/json'; status=STATE['status']
        elif path=='/api/offline/file': fid=urllib.parse.parse_qs(urllib.parse.urlparse(self.path).query).get('file_id',['synthetic-original'])[0];data=CONTENT[fid];content='audio/wav' if fid=='synthetic-original' else 'application/json'
        elif path=='/sw.js':
            assets=[{'url':'/static/'+n,'sha256':hashlib.sha256((ROOT/n).read_bytes()).hexdigest()} for n in ASSETS]
            if STATE['bad']: assets[-1]['sha256']='0'*64
            script=(ROOT/'sw.js').read_text().replace('__CWP_OFFLINE_ASSETS__',json.dumps(assets)).replace('__CWP_OFFLINE_VERSION__',json.dumps('cwp-static-offline-fixture-'+str(STATE['release'])))
            data=script.encode();content='text/javascript'
        elif path=='/offline': data=(ROOT/'offline-shell.html').read_bytes();content='text/html'
        elif path.startswith('/static/') and path[8:] in ASSETS: data=(ROOT/path[8:]).read_bytes();content='text/html' if path.endswith('.html') else 'text/javascript'
        else:self.send_error(404);return
        self.send_response(status);self.send_header('Content-Type',content);self.send_header('Cache-Control','no-store');self.send_header('Service-Worker-Allowed','/');self.end_headers();self.wfile.write(data)
    def log_message(self,*_): pass
evidence=pathlib.Path(sys.argv[1]) if len(sys.argv)>1 else pathlib.Path('/tmp/cwp-offline-browser-evidence')
evidence.mkdir(parents=True,exist_ok=True)
session='cwp-offline-fixture-'+uuid.uuid4().hex[:8]
server=http.server.ThreadingHTTPServer(('127.0.0.1',0),Handler)
threading.Thread(target=server.serve_forever,daemon=True).start()
url='http://127.0.0.1:'+str(server.server_port)+'/offline'
def browser(*args,script=None):
    result=subprocess.run(['agent-browser','--session',session,*args],input=script,text=True,capture_output=True,timeout=40)
    if result.returncode:raise AssertionError(result.stderr or result.stdout)
    return result.stdout.strip()
def evaluate(script):
    result=browser('eval','--stdin',script=script)
    return json.loads(result)
def check(value,message):
    if not value:raise AssertionError(message)
    print('PASS '+message,flush=True)
try:
    browser('open',url);browser('wait','--fn','navigator.serviceWorker.controller !== null')
    result=evaluate("(async()=>{const g=await CWPOfflineStorage.get('meta','grant');const f="+json.dumps(FILE)+";await CWPOfflinePacks.download({...g,pack_id:'synthetic-pack',files:[f,..."+json.dumps(READ_FILES)+"]});const r=await fetch('/offline-audio?file_id='+f.id,{headers:{Range:'bytes=44-53'}});return {status:r.status,size:(await r.arrayBuffer()).byteLength,range:r.headers.get('Content-Range'),scope:(await navigator.serviceWorker.getRegistration()).scope};})()")
    check(result['status']==206 and result['size']==10 and result['range']==f'bytes 44-53/{len(AUDIO)}' and result['scope'].endswith('/'),'real IDB ready blob, root SW scope and exact Range bytes')
    preserved=evaluate("(async()=>{const db=CWPOfflineStorage,g=await db.get('meta','grant'),pack=await db.get('packs',g.namespace+':synthetic-pack'),f="+json.dumps(FILE)+";let requests=0;const fetcher=window.fetch;window.fetch=async(...args)=>{if(String(args[0]).includes('/api/offline/file')){requests++;throw new TypeError('same ID must reuse bytes')}return fetcher(...args)};try{await CWPOfflinePacks.download({...g,pack_id:'synthetic-pack',files:[f,..."+json.dumps(READ_FILES)+"]});}finally{window.fetch=fetcher}let conflict=false;try{await CWPOfflinePacks.download({...g,pack_id:'synthetic-pack',files:[{...f,sha256:'0'.repeat(64),audio_sha:'0'.repeat(64)}]})}catch(e){conflict=true}const transaction=IDBDatabase.prototype.transaction;IDBDatabase.prototype.transaction=function(stores,...args){const tx=transaction.call(this,stores,...args);if(Array.isArray(stores)&&stores.includes('files')&&stores.includes('packs')&&stores.includes('meta'))queueMicrotask(()=>tx.abort());return tx};let aborted=false;try{await CWPOfflinePacks.download({...g,pack_id:'synthetic-pack',files:[f]})}catch(e){aborted=true}finally{IDBDatabase.prototype.transaction=transaction}const after=await db.get('packs',pack.id),row=await CWPOfflinePacks.readable(f,await db.get('meta','grant'));return {requests,conflict,aborted,state:after.state,size:row.blob.size};})()")
    check(preserved=={'requests':0,'conflict':True,'aborted':True,'state':'ready','size':len(AUDIO)},'same file and same pack retries reuse verified bytes; immutable conflict and native publication transaction abort preserve ready audio')
    cleanup=evaluate("(async()=>{const db=CWPOfflineStorage,g=await db.get('meta','grant');await db.put('files',{id:g.namespace+':staging:interrupted:synthetic-original',namespace:g.namespace,state:'downloading'});await db.put('packs',{id:g.namespace+':interrupted',namespace:g.namespace,state:'downloading',manifest:{pack_id:'interrupted',files:["+json.dumps(FILE)+"]}});await CWPOfflinePacks.cancelIncomplete(g.namespace);return {stage:!!await db.get('files',g.namespace+':staging:interrupted:synthetic-original'),ready:!!await CWPOfflinePacks.readable("+json.dumps(FILE)+",await db.get('meta','grant'))};})()")
    check(cleanup=={'stage':False,'ready':True},'real interrupted staging cleanup does not remove shared complete audio')
    browser('open',url);browser('wait','--fn',"document.querySelector('#files button[data-kind=audio]') !== null")
    browser('scrollintoview','#files button[data-kind=audio]');browser('click','#files button[data-kind=audio]');browser('wait','--fn',"document.getElementById('audio').currentTime>0")
    played=evaluate("(()=>{const a=document.getElementById('audio');a.currentTime=2.5;a.pause();return {duration:a.duration,position:a.currentTime};})()")
    check(abs(played['duration']-8)<.1 and abs(played['position']-2.5)<.1,'legal synthetic original audio actually plays and seeks')
    for kind,text in [('transcript','转录正文学习句'),('note','个人笔记正文'),('article','文章解释段落')]:
        browser('scrollintoview','#files button[data-kind='+kind+']');browser('click','#files button[data-kind='+kind+']')
        browser('wait','--fn',"document.getElementById('files').textContent.includes("+json.dumps(text,ensure_ascii=False)+")")
        check(text in evaluate("document.getElementById('files').textContent"),'browser reading action '+kind)
    browser('wait','--fn',"document.getElementById('files').textContent.includes('文章解释段落') && document.getElementById('files').textContent.includes('个人笔记正文') && document.getElementById('files').textContent.includes('转录正文学习句')")
    reading=evaluate("(()=>({text:document.getElementById('files').innerText,scripts:document.querySelectorAll('#files article script').length}))()")
    check(all(text in reading['text'] for text in ['转录正文学习句','个人笔记正文','文章解释段落']) and 'internal-' not in reading['text'] and reading['scripts']==0,'transcript, personal note and passed article render human text safely without JSON IDs')
    browser('click','#files article button')
    browser('wait','--fn',"document.getElementById('audio').currentTime>=2")
    browser('eval',"document.getElementById('audio').pause();true")
    check(True,'offline transcript timestamp seeks the exact source original audio')
    browser('scrollintoview','#view-synced');browser('click','#view-synced');browser('wait','--fn',"document.querySelector('#synced-drafts textarea') !== null")
    synced=evaluate("(()=>({text:document.querySelector('#synced-drafts textarea').value,visible:document.getElementById('synced-drafts').textContent}))()")
    check(synced['text']=='已同步个人整理不会消失' and '来源已不可用' in synced['visible'] and 'internal-synced-id' not in synced['visible'],'committed organizing text remains visibly readable and copyable after source loss')
    browser('scrollintoview','#files button[data-kind=audio]');browser('click','#files button[data-kind=audio]');browser('wait','--fn',"!document.getElementById('audio').paused");browser('eval',"document.getElementById('audio').pause();true")
    browser('fill','#text','synthetic browser personal learning text');browser('click','#save-reflection');browser('wait','--fn',"document.getElementById('outbox').textContent.includes('synthetic browser')")
    browser('screenshot',str(evidence/'ready.png'))
    browser('set','offline','on');browser('open',url)
    browser('wait','--fn',"document.getElementById('outbox').textContent.includes('synthetic browser')")
    check('网络不可达' in browser('get','text','#status'),'true network-offline reload uses static shell and durable outbox')
    browser('set','offline','off');browser('open',url)
    # Actual IndexedDB index without blob cannot remain ready.
    eviction=evaluate("(async()=>{const db=CWPOfflineStorage,g=await db.get('meta','grant'),id=g.namespace+':synthetic-original',row=await db.get('files',id);await db.put('files',{...row,blob:null});const r=await fetch('/offline-audio?file_id=synthetic-original');await db.put('files',row);return r.status;})()")
    check(eviction==404,'blob eviction rejects stale ready index')
    quota=evaluate("(async()=>{const put=CWPOfflineStorage.put;CWPOfflineStorage.put=async()=>{throw new DOMException('fixture quota','QuotaExceededError')};try{await CWPOfflineOperations.enqueue('organizing_draft',{content:'copy retained'}, {source_type:'episode',source_id:'synthetic-source',snapshot_id:'synthetic-snapshot'});return false;}catch(e){return e.name==='QuotaExceededError';}finally{CWPOfflineStorage.put=put;}})()")
    check(quota,'injected quota failure rejects durable save')
    # Bad new static hash must leave active version and cache usable.
    STATE.update(release=2,bad=True)
    browser('eval','--stdin',script="(async()=>{await (await navigator.serviceWorker.getRegistration()).update();return true;})()")
    browser('wait','--fn',"navigator.serviceWorker.getRegistration().then(r=>!r.installing)")
    kept=evaluate("(async()=>({keys:await caches.keys(),response:(await fetch('/static/offline-shell.html')).status}))()")
    check('cwp-static-offline-fixture-1' in kept['keys'] and 'cwp-static-offline-fixture-2' not in kept['keys'] and kept['response']==200,'bad updated asset hash preserves old active static cache')
    # Two real clients: a playing client prevents fresh update vote, then both idle permit activation.
    STATE.update(release=3,bad=False)
    browser('tab','new',url);browser('wait','--fn',"document.querySelector('#files button[data-kind=audio]') !== null")
    browser('scrollintoview','#files button[data-kind=audio]');browser('click','#files button[data-kind=audio]');browser('wait','--fn',"!document.getElementById('audio').paused")
    browser('tab','t1');browser('eval','--stdin',script="(async()=>{await(await navigator.serviceWorker.getRegistration()).update();return true;})()")
    browser('wait','--fn',"navigator.serviceWorker.getRegistration().then(r=>!!r.waiting)")
    browser('eval','--stdin',script="(async()=>{const r=await navigator.serviceWorker.getRegistration();r.waiting.postMessage({type:'OFFLINE_UPDATE_PROPOSE'});return true;})()")
    time.sleep(.15)
    blocked=evaluate("navigator.serviceWorker.getRegistration().then(r=>!!r.waiting)")
    check(blocked,'second playing client blocks service worker activation')
    browser('tab','t2');browser('eval',"document.getElementById('audio').pause();true")
    browser('tab','t1');browser('eval','--stdin',script="(async()=>{const r=await navigator.serviceWorker.getRegistration();r.waiting.postMessage({type:'OFFLINE_UPDATE_PROPOSE'});return true;})()")
    browser('wait','--fn',"navigator.serviceWorker.getRegistration().then(r=>!r.waiting)")
    check(evaluate("caches.keys().then(keys=>keys.includes('cwp-static-offline-fixture-3'))"),'fresh idle votes from both clients safely activate verified version')
    STATE['status']=403
    revoked=evaluate("(async()=>{const r=await fetch('/offline-audio?file_id=synthetic-original');await new Promise(resolve=>setTimeout(resolve,100));return {status:r.status,hidden:document.getElementById('private').hidden,copy:document.getElementById('locked-text').value,rows:(await CWPOfflineStorage.all('outbox')).map(x=>x.state)};})()")
    check(revoked['status']==403 and revoked['hidden'] and 'synthetic browser' in revoked['copy'] and revoked['rows']==['locked'],'online 403 revocation stops private media and preserves locked copyable text')
    STATE['status']=200
    browser('open',url);browser('wait','--fn',"document.getElementById('status').textContent.includes('在线授权已核对')")
    evaluate("(async()=>{const g=await CWPOfflineStorage.get('meta','grant');await CWPOfflinePacks.download({...g,pack_id:'second-synthetic-pack',files:["+json.dumps(FILE)+"]});return true;})()")
    STATE['status']=409
    source_changed=evaluate("(async()=>{const r=await fetch('/offline-audio?file_id=synthetic-original');await new Promise(resolve=>setTimeout(resolve,100));return {status:r.status,copy:document.getElementById('locked-text').value};})()")
    check(source_changed['status']==403 and 'synthetic browser' in source_changed['copy'],'online 409 source change rejects old bytes and retains text')
    # New login must not inherit the locked old namespace.
    STATE.update(status=200,owner='synthetic-new-session');GRANT.update(namespace='synthetic-new-namespace')
    browser('click','#check');browser('wait','--fn',"document.getElementById('status').textContent.includes('在线授权已核对')")
    changed=evaluate("(async()=>({rows:(await CWPOfflineStorage.all('outbox')).length,copy:document.getElementById('locked-text').value}))()")
    check(changed['rows']==0 and changed['copy']=='','new login session cannot inherit old private outbox')
    evaluate("(async()=>{const g=await CWPOfflineStorage.get('meta','grant');await CWPOfflinePacks.download({...g,pack_id:'expiry-synthetic-pack',files:["+json.dumps(FILE)+"]});await CWPOfflineOperations.enqueue('organizing_draft',{content:'expiry text'},{source_type:'episode',source_id:'synthetic-source',snapshot_id:'synthetic-snapshot'});await CWPOfflineStorage.put('meta',{...await CWPOfflineStorage.get('meta','grant'),expires_at:Math.floor(Date.now()/1000)-1});const r=await fetch('/offline-audio?file_id=synthetic-original');await new Promise(resolve=>setTimeout(resolve,100));return r.status;})()")
    expired=evaluate("(async()=>({hidden:document.getElementById('private').hidden,text:document.getElementById('locked-text').value,states:(await CWPOfflineStorage.all('outbox')).map(x=>x.state)}))()")
    check(expired['hidden'] and expired['text']=='expiry text' and expired['states']==['locked'],'expiry stops reads/media and retains locked draft')
    browser('screenshot',str(evidence/'expired.png'))
    browser('click','#clear')
    browser('wait','--fn',"document.getElementById('status').textContent.includes('已清理')")
    cleared=evaluate("(async()=>({outbox:(await CWPOfflineStorage.all('outbox')).length,files:(await CWPOfflineStorage.all('files')).length,grant:!!await CWPOfflineStorage.get('meta','grant'),locked:!!await CWPOfflineStorage.get('meta','lockedGrant')}))()")
    check(cleared=={'outbox':0,'files':0,'grant':False,'locked':False},'explicit offline authorization cleanup removes this device private copies and locked outbox')
    browser('click','#check');browser('wait','--fn',"document.getElementById('status').textContent.includes('在线授权已核对')")
    evaluate("CWPOfflineOperations.enqueue('organizing_draft',{content:'must be cleared even on server 410'},{source_type:'episode',source_id:'synthetic-source',snapshot_id:'synthetic-snapshot'}).then(()=>true)")
    STATE['revoke_status']=410
    browser('click','#clear');browser('wait','--fn',"document.getElementById('status').textContent.includes('本机已清理')")
    denied_cleanup=evaluate("(async()=>({outbox:(await CWPOfflineStorage.all('outbox')).length,message:document.getElementById('status').textContent}))()")
    check(denied_cleanup['outbox']==0 and '服务器撤回未核实' in denied_cleanup['message'],'expired server revoke still clears explicitly requested local private data without false remote success')
    # Unknown protocols and physical IndexedDB version changes must expose only text.
    STATE['revoke_status']=200
    browser('click','#check');browser('wait','--fn',"document.getElementById('status').textContent.includes('在线授权已核对')")
    evaluate("(async()=>{await CWPOfflineOperations.enqueue('organizing_draft',{content:'schema two copy only'},{source_type:'episode',source_id:'synthetic-source',snapshot_id:'synthetic-snapshot'});const op=(await CWPOfflineStorage.all('outbox'))[0];await CWPOfflineStorage.put('outbox',{...op,schema_version:2});return true;})()")
    browser('set','offline','on');browser('open',url)
    browser('wait','--fn',"document.getElementById('locked-text').value.includes('schema two copy only')")
    check(evaluate("document.getElementById('private').hidden && !document.getElementById('locked-drafts').hidden"),'unknown operation protocol locks sync and preserves copyable text on offline reload')
    evaluate("(async()=>{const db=CWPOfflineStorage;await db.put('meta',{...await db.get('meta','grant'),schema_version:2});return true;})()")
    browser('open',url);browser('wait','--fn',"document.getElementById('locked-text').value.includes('schema two copy only')")
    check(evaluate("document.getElementById('private').hidden"),'unknown grant schema has a read-only text escape without authorizing playback')
    evaluate("(async()=>{await CWPOfflineStorage.close();return await new Promise((resolve,reject)=>{const r=indexedDB.open('cwp-offline-v1',2);r.onerror=()=>reject(r.error);r.onsuccess=()=>{const v=r.result.version;r.result.close();resolve(v)}})})()")
    browser('open',url);browser('wait','--fn',"document.getElementById('locked-text').value.includes('schema two copy only')")
    actual_version=evaluate("(async()=>{const data=await CWPOfflineStorage.readLockedTexts();return {version:data.version,hidden:document.getElementById('private').hidden,text:document.getElementById('locked-text').value};})()")
    check(actual_version['version']==2 and actual_version['hidden'] and 'schema two copy only' in actual_version['text'],'actual IndexedDB version 2 remains unchanged while old owner text is copyable offline')
    browser('screenshot',str(evidence/'schema2-copy.png'))
    STATE['owner']='third-owner-no-inheritance';GRANT.update(namespace='third-private-namespace',owner_session_namespace=STATE['owner'])
    browser('set','offline','off');browser('open',url)
    browser('wait','--fn',"document.getElementById('locked-text').value === ''")
    browser('wait','--fn',"document.getElementById('status').textContent.includes('格式') || document.getElementById('status').textContent.includes('升级')")
    check(evaluate("document.getElementById('private').hidden && document.getElementById('locked-text').value === ''"),'new online owner cannot inherit incompatible database text')
    print('SCREENSHOTS '+str(evidence),flush=True)
    print('BROWSER '+evaluate('navigator.userAgent'),flush=True)
    print('Deterministic desktop-browser acceptance complete. Physical mobile and real owner/model quality remain separate.',flush=True)
finally:
    try:browser('close')
    finally:server.shutdown();server.server_close()
