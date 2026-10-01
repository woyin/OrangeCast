package server

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestViewLifecycleCleansListenersTimersAndRequests(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node unavailable")
	}
	script, err := os.ReadFile("static/view-lifecycle.js")
	if err != nil {
		t.Fatal(err)
	}
	harness := []byte(`
const assert=require('node:assert/strict');globalThis.window=globalThis;
const timers=new Set();let nextTimer=0,aborted=0,cleaned=0,fired=0;
globalThis.setInterval=()=>{const id=++nextTimer;timers.add(id);return id;};globalThis.clearInterval=id=>timers.delete(id);
globalThis.fetch=(url,options)=>{options.signal.addEventListener('abort',()=>aborted++);return new Promise(()=>{});};
`)
	behavior := []byte(`
const target=new EventTarget(),view={querySelectorAll:()=>[]};
CWPViews.define((view,scope)=>{scope.on(target,'change',()=>fired++);scope.interval(()=>{},5000);scope.cleanup(()=>cleaned++);scope.fetch('/delayed');});
for(let i=0;i<10;i++){CWPViews.mount(view);target.dispatchEvent(new Event('change'));assert.equal(fired,i+1);assert.equal(timers.size,1);}
CWPViews.unmount();target.dispatchEvent(new Event('change'));assert.equal(fired,10);assert.equal(timers.size,0);assert.equal(aborted,10);assert.equal(cleaned,10);
CWPViews.define(()=>{throw Error('mount failure');});assert.throws(()=>CWPViews.mount(view),/mount failure/);assert.equal(timers.size,0);assert.equal(aborted,11);
`)
	file := filepath.Join(t.TempDir(), "view-test.js")
	contents := append(harness, script...)
	contents = append(contents, behavior...)
	if err = os.WriteFile(file, contents, 0600); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command(node, file).CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, output)
	}
}

func TestLogoutClearsPersistentListeningDraftDOMAndStorage(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node unavailable")
	}
	harness := `const assert=require('node:assert/strict');globalThis.window=globalThis;
const nodes=new Map();class Element extends EventTarget{constructor(){super();this.value='';this.textContent='';this.hidden=false;this.paused=true;this.currentTime=0;this.duration=120;this.playbackRate=1;this.dataset={};}append(){}appendChild(){}removeAttribute(){}load(){}pause(){this.paused=true;}play(){this.paused=false;return Promise.resolve();}focus(){}querySelector(){return new Element();}}
globalThis.document={createElement:()=>new Element(),getElementById:id=>{if(!nodes.has(id))nodes.set(id,new Element());return nodes.get(id);},querySelector:()=>null,addEventListener:()=>{},removeEventListener:()=>{}};
const values=new Map();const storage={get length(){return values.size},key:i=>Array.from(values.keys())[i],getItem:k=>values.get(k)||null,setItem:(k,v)=>values.set(k,v),removeItem:k=>values.delete(k)};globalThis.localStorage=storage;globalThis.sessionStorage=storage;
globalThis.location={pathname:'/dashboard'};globalThis.fetch=async()=>({ok:true,json:async()=>({})});globalThis.setInterval=()=>1;globalThis.clearInterval=()=>{};globalThis.addEventListener=()=>{};globalThis.removeEventListener=()=>{};Object.defineProperty(globalThis,'navigator',{value:{mediaSession:{setActionHandler:()=>{}}},configurable:true});
`
	for _, name := range []string{"private-state.js", "playback-controller.js", "listening-session.js"} {
		source, err := os.ReadFile(filepath.Join("static", name))
		if err != nil {
			t.Fatal(err)
		}
		harness += "\n" + string(source)
	}
	harness += `
(async()=>{CWPListening.install({title:'私有播放标题',sourceType:'episode',sourceId:'s',mode:'original',planId:'',planVersion:0,audioSHA:'sha',audioURL:'/private-audio',csrf:'test',snapshot:'snapshot',snapshotVersion:1,segments:[],items:[]});
nodes.get('listening-note-content').value='未保存的私有笔记';CWPListening.captureNote();assert.equal(nodes.get('listening-note-form').hidden,false);assert(values.size>0);
CWPPrivate.clear();await Promise.resolve();await Promise.resolve();assert.equal(CWPListening.state().spec,null);assert.equal(nodes.get('listening-title').textContent,'');assert.equal(nodes.get('listening-note-content').value,'');assert.equal(nodes.get('listening-note-form').hidden,true);assert.equal(values.size,0);assert.equal(navigator.mediaSession.metadata,null);
})().catch(error=>{console.error(error);process.exitCode=1});`
	file := filepath.Join(t.TempDir(), "private-state-test.js")
	if err = os.WriteFile(file, []byte(harness), 0600); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command(node, file).CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, output)
	}
}
