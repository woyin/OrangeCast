window.CWPViews.define(function(view,scope){
 'use strict';const root=view.querySelector('#evidence-view');if(!root)return;
 const audio=root.querySelector('#frozen-audio'),query=new URLSearchParams(location.search);
 if(audio){
  const seek=()=>{const t=Number(query.get('t'));if(Number.isFinite(t)&&t>=0)audio.currentTime=Math.min(t,audio.duration||t);};
  if(audio.readyState>=1)seek();else scope.on(audio,'loadedmetadata',seek,{once:true});
  root.querySelectorAll('.frozen-seek').forEach(button=>scope.on(button,'click',()=>{audio.currentTime=Number(button.dataset.time);audio.play().catch(()=>{});}));
  scope.cleanup(()=>audio.pause());
 }
 const position=query.get('position');if(position){const target=Array.from(root.querySelectorAll('[id]')).find(n=>n.id==='position-'+position);target?.scrollIntoView();}
});
