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
    on(scForm,'submit', async (e) => {
      e.preventDefault();
      const q = scForm.querySelector('[name=question]').value;
      if (!q.trim()) return;
      // 即时显示用户问题
      if (scThread) scThread.insertAdjacentHTML('beforeend', renderSCMessage('user', q, []));
      const fd = new FormData(scForm);
      scForm.querySelector('[name=question]').value = '';
      if (scFeedback) scFeedback.innerHTML = '<p>思考中…</p>';
      fd.append('source_type', spec.sourceType);
      fd.append('source_id', spec.sourceId);
      try {
        const resp = await scope.fetch('/api/study-chat', { method: 'POST', body: fd });
        const data = await resp.json();
        if (data.error) {
          if (scFeedback) scFeedback.innerHTML = '<p class="error">' + escapeHtml(data.error) + '</p>';
          return;
        }
        if (scSessionInput && data.session_id) scSessionInput.value = data.session_id;
        if (data.generated && data.answer) {
          if (scThread) scThread.insertAdjacentHTML('beforeend', renderSCMessage('assistant', data.answer, data.references || []));
          if (scFeedback) scFeedback.innerHTML = '';
        } else {
          // 硬约束触发（超出本集范围 / ReferenceCheck 拒绝）
          if (scFeedback) scFeedback.innerHTML = '<p class="scope-feedback">' + escapeHtml(data.scope_feedback || '该问题超出本集范围，未生成回答。') + '</p>';
        }
      } catch (err) {
        if (scFeedback) scFeedback.innerHTML = '<p class="error">请求失败</p>';
      }
    });
  }

});
