const fs=require('fs'),vm=require('vm'),assert=require('assert');
(async()=>{for(const ok of [true,false]){const listeners={},feedback={textContent:''},fields=[{name:'theme',value:'learning'},{name:'source',value:'episode:actual'}];let endpoint,href;
 const form={getAttribute:()=>'/knowledge-articles/generate',querySelector:()=>feedback,querySelectorAll:()=>fields};
 const view={querySelector:()=>null,querySelectorAll:s=>s==='.knowledge-generation-form'?[form]:[]};
 const scope={on:(node,event,fn)=>listeners[event]=fn,fetch:async url=>{endpoint=url;return{ok,json:async()=>({redirect:'/knowledge-articles/id'}),text:async()=>'材料不足'}}};
 const context={FormData:class{},window:{CWPViews:{define:fn=>fn(view,scope)},CWPNavigation:{visit:v=>href=v}}};
 vm.runInNewContext(fs.readFileSync('static/knowledge-articles.js','utf8'),context);await listeners.submit({preventDefault(){}});assert.equal(endpoint,'/knowledge-articles/generate');assert(fields.every(f=>!f.disabled));assert.equal(fields[0].value,'learning');if(ok)assert.equal(href,'/knowledge-articles/id');else{assert.equal(href,undefined);assert.equal(feedback.textContent,'材料不足')}
 }console.log('generation JSON navigation and scope preservation passed');})().catch(e=>{console.error(e);process.exit(1)});
