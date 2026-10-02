// 播放器全三层联动（第 5 题）：
// A 层：点转录句/章节/金句 → 跳转播放
// B 层：播放进度 → 高亮 + 自动滚动当前转录句
// C 层：章节/金句卡片与播放器双向联动
window.CWPViews.define(function(view,scope) {
  const unavailable=view.querySelector('[data-listening-unavailable]');if(unavailable){const active=window.CWPListening.state().spec;if(active?.sourceType===unavailable.dataset.sourceType&&active?.sourceId===unavailable.dataset.sourceId){window.CWPListening.stop();window.CWPListening.notify(unavailable.textContent);}}
  const declared=view.querySelector('#source-audio');if(!declared)return;
  const spec=window.CWPListening.specFrom(view),audio=window.CWPListening.state().audio;
  declared.pause();declared.removeAttribute('src');declared.remove();
  const on=(node,event,fn,options)=>scope.on(node,event,fn,options);
  const activate=fn=>window.CWPListening.act(spec,fn);
  const transport={toggle:()=>activate(s=>s.transport.toggle()),play:()=>activate(s=>s.transport.play()),seek:t=>activate(s=>s.transport.seek(t)),
   skip:t=>activate(s=>s.transport.skip(t)),setRate:v=>activate(s=>s.transport.setRate(v)),setSleep:v=>activate(s=>s.transport.setSleep(v))};
  const playBtn = view.querySelector('#' + 'play-btn');
  const seekBar = view.querySelector('#' + 'seek-bar');
  const currentTimeEl = view.querySelector('#' + 'current-time');
  const durationEl = view.querySelector('#' + 'duration');

  const segs = Array.from(view.querySelectorAll('.transcript .seg'));
  const chapters = Array.from(view.querySelectorAll('.chapter'));
  const quotes = Array.from(view.querySelectorAll('.quote'));

  function fmt(sec) {
    if (!isFinite(sec)) return '0:00';
    const t = Math.floor(sec), h = Math.floor(t / 3600), m = Math.floor((t % 3600) / 60), s = t % 60;
    return h > 0 ? `${h}:${String(m).padStart(2,'0')}:${String(s).padStart(2,'0')}` : `${m}:${String(s).padStart(2,'0')}`;
  }

  const feedback = view.querySelector('#playback-feedback');
  const rate = view.querySelector('#playback-rate');
  const deepTime = Number(new URLSearchParams(location.search).get('t'));
  const hasDeepTime = new URLSearchParams(location.search).has('t') && Number.isFinite(deepTime) && deepTime >= 0;
  const hashSeg = segs.find(el => '#' + el.dataset.id === location.hash);
  const deepPosition = hasDeepTime ? deepTime : hashSeg ? Number(hashSeg.dataset.start) : null;
  if(playBtn)on(playBtn,'click',()=>transport.toggle());
  if(rate)on(rate,'change',()=>transport.setRate(Number(rate.value)));
  on(view.querySelector('#playback-back'),'click',()=>transport.skip(-15));
  on(view.querySelector('#playback-forward'),'click',()=>transport.skip(15));
  on(view.querySelector('#playback-sleep'),'change',e=>transport.setSleep(Number(e.target.value)));
  // A deep link prepares this source only when it owns the active session; it does not replace another source.
  window.CWPListening.restored.then(()=>{if(scope.signal.aborted)return;if(!window.CWPListening.state().spec)window.CWPListening.install(spec);if(deepPosition!==null&&window.CWPListening.same(window.CWPListening.state().spec,spec))transport.seek(deepPosition);});
  const follow = view.querySelector('#' + 'playback-follow');
  on(view.querySelector('#' + 'transcript'),'wheel',()=>{if(follow)follow.checked=false;},{passive:true});
  on(view.querySelector('#' + 'transcript'),'touchmove',()=>{if(follow)follow.checked=false;},{passive:true});
  let activeIdx = -1;
  scope.cleanup(window.CWPListening.subscribe(state=>{
   const owns=window.CWPListening.same(state.spec,spec);
   if(playBtn)playBtn.textContent=owns&&!state.paused?'⏸':'▶';
   if(feedback)feedback.textContent=owns?state.notice:(state.spec?'正在听 '+state.spec.title+'，点播放切换到本节目。':'点击播放开始');
   if(currentTimeEl)currentTimeEl.textContent=fmt(owns?state.time:0);
   if(durationEl)durationEl.textContent=fmt(owns?state.duration:0);
   if(seekBar){seekBar.max=owns&&Number.isFinite(state.duration)?state.duration:100;seekBar.value=owns?state.time:0;}
   if(rate&&owns)rate.value=String(audio.playbackRate);
   const resume=view.querySelector('#playback-resume');if(resume){resume.hidden=!owns||!state.resume;resume.textContent='从 '+fmt(state.resume?.saved.item_offset_seconds)+' 继续';}
   if(owns)highlightCurrentSeg();
  }));
  on(view.querySelector('#playback-resume'),'click',()=>{const state=window.CWPListening.state();if(window.CWPListening.same(state.spec,spec))state.resume?.run();});
  on(seekBar,'input',()=>transport.seek(parseFloat(seekBar.value)));
  // A 层：点击跳转 —— 任何带 data-start 的元素点击后跳转
  function jumpTo(el) {
    const start = parseFloat(el.dataset.start);
    if (isFinite(start)) {
      transport.seek(start);
      transport.play();
    }
  }
  segs.forEach(s => on(s,'click', () => jumpTo(s)));
  chapters.forEach(c => on(c,'click', event => {event.preventDefault();jumpTo(c);}));
  quotes.forEach(q => on(q,'click', () => jumpTo(q)));

  // B 层：根据当前播放时间高亮对应转录句并滚动
  function highlightCurrentSeg() {
    const t = audio.currentTime;
    // 找到当前时间所在的 segment（start <= t < end）
    let idx = -1;
    for (let i = 0; i < segs.length; i++) {
      const start = parseFloat(segs[i].dataset.start);
      const end = parseFloat(segs[i].dataset.end);
      if (t >= start && t < end) { idx = i; break; }
      if (t >= start && i === segs.length - 1) { idx = i; }
    }
    if (idx !== activeIdx) { // idx 变化才更新（NaN 检查：idx!==idx 为 NaN）
      if (activeIdx >= 0) segs[activeIdx].classList.remove('active');
      activeIdx = idx;
      if (idx >= 0) {
        segs[idx].classList.add('active');
        // 自动滚动到当前句（仅在播放时，避免打断用户浏览）
        if (!audio.paused && (!follow || follow.checked)) segs[idx].scrollIntoView({ behavior: 'smooth', block: 'center' });
      }
    }
  }

  // EvidenceQA 表单（证据问答，ADR-0018；fetch /api/evidence-qa，无需刷新）
  const qaForm = view.querySelector('#' + 'evidence-qa-form');
  const qaAnswer = view.querySelector('#' + 'evidence-qa-answer');
  if (qaForm) {
    on(qaForm,'submit', async (e) => {
      e.preventDefault();
      const q = qaForm.querySelector('[name=question]').value;
      qaAnswer.innerHTML = '<p>思考中…</p>';
      const fd = new FormData(qaForm);
      // source 标识从 URL 推断：/sources/{type}/{id}
      fd.append('source_type', spec.sourceType);
      fd.append('source_id', spec.sourceId);
      try {
        const resp = await scope.fetch('/api/evidence-qa', { method: 'POST', body: fd });
        const data = await resp.json();
        if (data.error) {
          qaAnswer.innerHTML = `<p class="error">${escapeHtml(data.error)}</p>`;
          return;
        }
        // 渲染答案 + 引用列表（点击引用跳转播放器，复用 jumpTo 逻辑）
        let html = `<p>${escapeHtml(data.answer || '无回答')}</p>`;
        if (Array.isArray(data.sources) && data.sources.length) {
          html += '<ul class="qa-sources">';
          data.sources.forEach((s, i) => {
            const ts = fmt(s.start);
            const snippet = (s.content || '').slice(0, 80);
            html += `<li class="qa-source" data-start="${Number.isFinite(Number(s.start)) ? Number(s.start) : 0}" data-end="${Number.isFinite(Number(s.end)) ? Number(s.end) : 0}"><span class="ts">[${ts}]</span> ${escapeHtml(snippet)}…</li>`;
          });
          html += '</ul>';
        }
        qaAnswer.innerHTML = html;
        // 绑定引用点击 → 跳转播放器
        qaAnswer.querySelectorAll('.qa-source').forEach(el => {
          on(el,'click', () => jumpTo(el));
        });
      } catch (err) {
        qaAnswer.innerHTML = '<p class="error">请求失败</p>';
      }
    });
  }

// ---- Paraphrase 复述讲解（GeneratedDerivative，ADR-0018）----
  const paraphrasePanel = view.querySelector('#' + 'paraphrase-panel');
  const paraphraseForm = view.querySelector('#' + 'paraphrase-form');
  const paraphraseOutput = view.querySelector('#' + 'paraphrase-output');
  const paraphraseSegInput = view.querySelector('#' + 'paraphrase-segment-ids');

  // 每个 Segment 的"重讲"按钮：填入该 segment_id 并展开面板
  view.querySelectorAll('.seg-paraphrase-btn').forEach((btn) => {
    on(btn,'click', (e) => {
      e.preventDefault();
      if (!paraphrasePanel) return;
      paraphraseSegInput.value = JSON.stringify([btn.dataset.segId]);
      paraphrasePanel.hidden = false;
      if (paraphraseForm.querySelector('[name=question]')) {
        paraphraseForm.querySelector('[name=question]').value = '';
      }
      paraphraseOutput.innerHTML = '';
      paraphrasePanel.scrollIntoView({ behavior: 'smooth', block: 'center' });
    });
  });

  const paraphraseClose = view.querySelector('#' + 'paraphrase-close');
  if (paraphraseClose) {
    on(paraphraseClose,'click', () => {
      if (paraphrasePanel) paraphrasePanel.hidden = true;
    });
  }

  if (paraphraseForm) {
    on(paraphraseForm,'submit', async (e) => {
      e.preventDefault();
      if (!paraphraseSegInput.value) {
        paraphraseOutput.innerHTML = '<p class="error">请先在转录稿中选择至少一个片段。</p>';
        return;
      }
      paraphraseOutput.innerHTML = '<p>重新讲解中…</p>';
      const fd = new FormData(paraphraseForm);
      fd.append('source_type', spec.sourceType);
      fd.append('source_id', spec.sourceId);
      try {
        const resp = await scope.fetch('/api/paraphrase', { method: 'POST', body: fd });
        const data = await resp.json();
        if (data.error) {
          paraphraseOutput.innerHTML = '<p class="error">' + escapeHtml(data.error) + '</p>';
          return;
        }
        // 渲染讲解（明确标注 AI 生成·非原文），并列出参考片段（点击跳播放器）
        let html = '<div class="ai-generated-block">';
        html += '<p class="ai-tag">AI 讲解·非原文</p>';
        html += '<p>' + escapeHtml(data.text || '（空）') + '</p>';
        if (Array.isArray(data.references) && data.references.length) {
          html += '<p class="ref-note">参考片段（点击跳转播放器）：</p><ul class="qa-sources">';
          data.references.forEach((segId) => {
            html += '<li class="qa-source" data-seg-id="' + escapeHtml(segId) + '">' + escapeHtml(segId) + '</li>';
          });
          html += '</ul>';
        }
        html += '</div>';
        paraphraseOutput.innerHTML = html;
      } catch (err) {
        paraphraseOutput.innerHTML = '<p class="error">请求失败</p>';
      }
    });
  }

  function escapeHtml(s) { return window.CWPSafe.escapeHTML(s); }


// ---- StudyChat 学习对话（GeneratedDerivative，ADR-0018 R3）----
  const scForm = view.querySelector('#' + 'study-chat-form');
  const scThread = view.querySelector('#' + 'study-chat-thread');
  const scFeedback = view.querySelector('#' + 'study-chat-feedback');
  const scSessionInput = view.querySelector('#' + 'study-chat-session-id');


  function renderSCMessage(role, content, refs) {
    const cls = role === 'user' ? 'sc-msg sc-user' : 'sc-msg sc-assistant';
    let html = '<div class="' + cls + '">';
    if (role === 'assistant') {
      html += '<p class="ai-tag">AI 讲解·非原文</p>';
    }
    html += '<p>' + escapeHtml(content) + '</p>';
    if (Array.isArray(refs) && refs.length) {
      html += '<p class="ref-note">参考片段：</p><ul class="qa-sources">';
      refs.forEach((segId) => {
        html += '<li class="qa-source" data-seg-id="' + escapeHtml(segId) + '">' + escapeHtml(segId) + '</li>';
      });
      html += '</ul>';
    }
    html += '</div>';
    return html;
  }

  if (scForm) {
    const pendingKey = 'cwp-study-command:' + spec.sourceType + ':' + spec.sourceId;
    const sessionKey = pendingKey + ':session';
    const draftKey = 'cwp-study-draft:' + spec.sourceType + ':' + spec.sourceId;
    const questionInput = scForm.querySelector('[name=question]');
    // Submitted command identity and the next editable question have separate
    // lifetimes. Polling/completion may retire the command but never its draft.
    function saveDraft() {
      try { if(questionInput.value)sessionStorage.setItem(draftKey,JSON.stringify({version:1,text:questionInput.value}));else sessionStorage.removeItem(draftKey); }
      catch(err){feedback('下一问草稿无法保存在本机，请先复制文字。','error');}
    }
    on(questionInput,'input',saveDraft);
    let pending = null, revision = 1, polling = false, current = null;
    function feedback(text, cls = '') { if(scFeedback) scFeedback.innerHTML='<p class="'+cls+'">'+escapeHtml(text)+'</p>'; }
    function busy(value) { const button=scForm.querySelector('[type=submit]');if(button)button.disabled=value; }
    function savePending(value) { sessionStorage.setItem(pendingKey,JSON.stringify(value));pending=value; }
    async function history(id) {
      if (!id) return;
      const response=await scope.fetch('/api/study-chat/history?session_id='+encodeURIComponent(id),{cache:'no-store'});
      if(response.status===404){sessionStorage.removeItem(sessionKey);if(scSessionInput)scSessionInput.value='';revision=1;return;}
      if(!response.ok) return;
      const data=await response.json();revision=data.revision||revision;
      if(scThread)scThread.innerHTML=(data.messages||[]).map(m=>renderSCMessage(m.role,m.content,m.reference_segment_ids)).join('');
    }
    async function show(data) {
      current=data;revision=data.revision||revision;
      if(scSessionInput&&data.session_id)scSessionInput.value=data.session_id;
      if(data.session_id)sessionStorage.setItem(sessionKey,JSON.stringify({id:data.session_id,revision}));
      if (['accepted','insufficient'].includes(data.state)) {
        if(data.session_id)await history(data.session_id);
        sessionStorage.removeItem(pendingKey);pending=null;busy(false);
        feedback(data.scope_feedback||'回答已通过检查并保存。');return;
      }
      if(data.result_unknown||data.state==='blocked'||data.job_status==='failed') {
        busy(true);feedback(data.result_unknown?'远端结果未知，没有自动重发。新的付费尝试可能再次计费。':'任务已阻断。可恢复已保存响应，或在尚未调用时重新尝试。');
        if(data.retry&&scFeedback){const button=document.createElement('button');button.type='button';button.textContent=data.result_unknown?'明确发起新的付费尝试':'恢复任务';on(button,'click',()=>retry(data));scFeedback.append(button);}return;
      }
      feedback(data.state==='checking'||data.state==='response_saved'?'回答已保存，正在独立检查…':'任务已保存，等待处理…');busy(true);
    }
    async function poll() {
      if(!pending||polling||scope.signal.aborted)return;polling=true;
      try{
        const query=pending.turn_id?'turn_id='+encodeURIComponent(pending.turn_id):'request_key='+encodeURIComponent(pending.request_key);
        const response=await scope.fetch('/api/study-chat/status?'+query,{cache:'no-store'});
        if(response.status===404){
          if(pending.turn_id){sessionStorage.removeItem(pendingKey);pending=null;busy(false);feedback('原会话或任务已清理，没有重新生成。');return;}
          feedback('尚未查到原命令；可用原身份再次提交，服务器会去重。');
          if(scFeedback){const button=document.createElement('button');button.type='button';button.textContent='核对并提交原命令';on(button,'click',submitPending);scFeedback.append(button);
            const discard=document.createElement('button');discard.type='button';discard.textContent='放弃这条未查到的本地命令';on(discard,'click',()=>{if(!questionInput.value){questionInput.value=pending.question||'';saveDraft();}sessionStorage.removeItem(pendingKey);pending=null;busy(false);feedback('问题已恢复到输入框，尚未发起新任务。');});scFeedback.append(discard);}
          return;
        }
        const data=await response.json();if(!response.ok){feedback(data.error||'任务核对失败','error');return;}
        if(data.turn_id&&!pending.turn_id)savePending({...pending,turn_id:data.turn_id});
        await show(data);
      }catch(err){if(!scope.signal.aborted)feedback('网络不可用，已保留原命令身份。恢复连接后只查询，不自动重发。','error');}
      finally{polling=false;}
    }
    async function submitPending() {
      if(!pending)return;busy(true);
      const fd=new FormData(scForm);
      for(const name of ['question','session_id','request_key','revision'])fd.set(name,String(pending[name]||''));
      fd.set('source_type',spec.sourceType);fd.set('source_id',spec.sourceId);
      try{
        const response=await scope.fetch('/api/study-chat',{method:'POST',body:fd});const data=await response.json();
        if(!response.ok){feedback(data.error||'命令冲突，请核对原任务','error');if(response.status===409)await poll();return;}
        savePending({...pending,turn_id:data.turn_id});await show(data);
      }catch(err){if(!scope.signal.aborted){feedback('提交响应丢失；已保留原身份，正在查询任务。','error');await poll();}}
    }
    async function retry(data) {
      const key=crypto.randomUUID();
      // A new paid attempt is only sent from this explicit control. Persist its
      // receipt key before POST so a lost acknowledgement can be queried.
      savePending({...pending,request_key:'retry:'+key,turn_id:data.turn_id});
      const fd=new FormData(scForm);fd.set('request_key',key);fd.set('job_id',data.job_id);fd.set('job_revision',data.job_revision);fd.set('allow_unknown',data.result_unknown?'1':'0');
      try{const response=await scope.fetch('/api/study-chat/retry',{method:'POST',body:fd});const result=await response.json();if(!response.ok){feedback(result.error||'恢复失败','error');return;}await show(result);}catch(err){if(!scope.signal.aborted){feedback('恢复响应丢失，继续核对原任务。','error');await poll();}}
    }
    try {
      const saved=JSON.parse(sessionStorage.getItem(sessionKey)||'null');if(saved&&scSessionInput){scSessionInput.value=saved.id;revision=saved.revision;}
      pending=JSON.parse(sessionStorage.getItem(pendingKey)||'null');
      const draft=JSON.parse(sessionStorage.getItem(draftKey)||'null');
      if(draft?.version===1&&typeof draft.text==='string')questionInput.value=draft.text;
    } catch(err) {feedback('无法读取本地任务身份，请核对任务列表。','error');}
    if(scSessionInput?.value)history(scSessionInput.value).catch(()=>{});
    if(pending){busy(true);poll();}
    scope.interval(()=>{if(pending&&!['unknown','blocked'].includes(current?.state))poll();},2000);
    on(scForm,'submit',async(e)=>{
      e.preventDefault();if(pending){await poll();return;}
      const q=scForm.querySelector('[name=question]').value.trim();if(!q)return;
      try {savePending({request_key:crypto.randomUUID(),question:q,session_id:scSessionInput?.value||'',revision});}
      catch(err){feedback('无法持久保存请求身份，本次没有提交；请复制问题后重试。','error');return;}
      if(scThread)scThread.insertAdjacentHTML('beforeend',renderSCMessage('user',q,[]));
      questionInput.value='';saveDraft();feedback('正在提交持久任务…');await submitPending();
    });
  }

});
