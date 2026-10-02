const fs=require('fs'),vm=require('vm'),assert=require('node:assert/strict');
for(const previous of ['', '既有笔记草稿']){
 const fields=['action','question_id','expected_revision','content','source','references_json','anchor_json'].map(name=>({name,value:({action:'note',question_id:'q',expected_revision:'4',content:previous,source:'document:old',references_json:'["old"]',anchor_json:'{"snapshot_id":"old"}'})[name]}));fields.namedItem=name=>fields.find(f=>f.name===name);
 const feedback={textContent:''},form={elements:fields,querySelector:()=>feedback,hasAttribute:()=>false,append(){}};
 const values=new Map([['cwp-question-study-adopt:q',JSON.stringify({content:'[AI 辅助问答草稿]\n解释\n冻结依据：\n/evidence/a\n/evidence/b',session:'session'})]]);
 const storage={get length(){return values.size},key:i=>Array.from(values.keys())[i],getItem:k=>values.get(k)||null,setItem:(k,v)=>values.set(k,v),removeItem:k=>values.delete(k)};
 let mount,calls=0;
 const context={sessionStorage:storage,window:{CWPViews:{define:fn=>mount=fn},CWPForms:{bind:()=>{}}},document:{createElement:()=>({append(){}})},fetch:()=>{calls++;throw Error('adoption must not save');}};
 vm.runInNewContext(fs.readFileSync('static/learning-questions.js','utf8'),context);
 mount({querySelector:()=>null,querySelectorAll:s=>s==='form.question-form'?[form]:[]},{on:()=>{}});
 assert.equal(calls,0);
 if(previous){assert.equal(fields.namedItem('content').value,previous);assert.ok(values.has('cwp-question-study-adopt:q'));}
 else{assert.match(fields.namedItem('content').value,/AI 辅助/);assert.equal(fields.namedItem('source').value,'');assert.equal(fields.namedItem('references_json').value,'[]');assert.equal(fields.namedItem('anchor_json').value,'{"no_position":true}');assert.ok(values.has('cwp-question-draft:q:note:4'));assert.ok(!values.has('cwp-question-study-adopt:q'));}
}
console.log('AI draft adoption preserves old text, requires source choice and never saves or fabricates citations');
