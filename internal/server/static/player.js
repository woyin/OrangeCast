// 播放器全三层联动（第 5 题）：
// A 层：点转录句/章节/金句 → 跳转播放
// B 层：播放进度 → 高亮 + 自动滚动当前转录句
// C 层：章节/金句卡片与播放器双向联动
(function () {
  const audio = document.getElementById('audio-player');
  if (!audio) return;

  const playBtn = document.getElementById('play-btn');
  const seekBar = document.getElementById('seek-bar');
  const currentTimeEl = document.getElementById('current-time');
  const durationEl = document.getElementById('duration');

  const segs = Array.from(document.querySelectorAll('.transcript .seg'));
  const chapters = Array.from(document.querySelectorAll('.chapter'));
  const quotes = Array.from(document.querySelectorAll('.quote'));

  function fmt(sec) {
    if (!isFinite(sec)) return '0:00';
    const t = Math.floor(sec), h = Math.floor(t / 3600), m = Math.floor((t % 3600) / 60), s = t % 60;
    return h > 0 ? `${h}:${String(m).padStart(2,'0')}:${String(s).padStart(2,'0')}` : `${m}:${String(s).padStart(2,'0')}`;
  }

  // 深链跳转（Roadmap Phase 5）：?t=秒 或 #seg-XXXX 命中后跳转播放
  (function deepLink() {
    const params = new URLSearchParams(window.location.search);
    const t = parseFloat(params.get('t'));
    if (isFinite(t) && t >= 0 && t <= (audio.duration || Infinity)) {
      audio.currentTime = t;
      audio.play();
      return;
    }
    const hash = window.location.hash;
    if (hash.startsWith('#seg-')) {
      const want = hash.slice(1);
      const el = segs.find(sg => sg.dataset.id === want);
      if (el) jumpTo(el);
    }
  })();

  // 播放/暂停
  if (playBtn) {
    playBtn.addEventListener('click', () => {
      if (audio.paused) audio.play(); else audio.pause();
    });
  }
  audio.addEventListener('play', () => { if (playBtn) playBtn.textContent = '⏸'; });
  audio.addEventListener('pause', () => { if (playBtn) playBtn.textContent = '▶'; });

  // 进度条同步
  audio.addEventListener('loadedmetadata', () => {
    if (durationEl) durationEl.textContent = fmt(audio.duration);
    if (seekBar) seekBar.max = audio.duration;
  });
  audio.addEventListener('timeupdate', () => {
    if (currentTimeEl) currentTimeEl.textContent = fmt(audio.currentTime);
    if (seekBar) seekBar.value = audio.currentTime;
    highlightCurrentSeg();
  });
  if (seekBar) {
    seekBar.addEventListener('input', () => { audio.currentTime = parseFloat(seekBar.value); });
  }

  // A 层：点击跳转 —— 任何带 data-start 的元素点击后跳转
  function jumpTo(el) {
    const start = parseFloat(el.dataset.start);
    if (isFinite(start)) {
      audio.currentTime = start;
      audio.play();
    }
  }
  segs.forEach(s => s.addEventListener('click', () => jumpTo(s)));
  chapters.forEach(c => c.addEventListener('click', () => jumpTo(c)));
  quotes.forEach(q => q.addEventListener('click', () => jumpTo(q)));

  // B 层：根据当前播放时间高亮对应转录句并滚动
  let activeIdx = -1;
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
    if (idx === idx && idx !== activeIdx) { // idx 变化才更新（NaN 检查：idx!==idx 为 NaN）
      if (activeIdx >= 0) segs[activeIdx].classList.remove('active');
      activeIdx = idx;
      if (idx >= 0) {
        segs[idx].classList.add('active');
        // 自动滚动到当前句（仅在播放时，避免打断用户浏览）
        if (!audio.paused) segs[idx].scrollIntoView({ behavior: 'smooth', block: 'center' });
      }
    }
  }

  // EvidenceQA 表单（证据问答，ADR-0018；fetch /api/evidence-qa，无需刷新）
  const qaForm = document.getElementById('evidence-qa-form');
  const qaAnswer = document.getElementById('evidence-qa-answer');
  if (qaForm) {
    qaForm.addEventListener('submit', async (e) => {
      e.preventDefault();
      const q = qaForm.querySelector('[name=question]').value;
      qaAnswer.innerHTML = '<p>思考中…</p>';
      const fd = new FormData(qaForm);
      // source 标识从 URL 推断：/sources/{type}/{id}
      const parts = window.location.pathname.split('/');
      fd.append('source_type', parts[2]);
      fd.append('source_id', parts[3]);
      try {
        const resp = await fetch('/api/evidence-qa', { method: 'POST', body: fd });
        const data = await resp.json();
        if (data.error) {
          qaAnswer.innerHTML = `<p class="error">${data.error}</p>`;
          return;
        }
        // 渲染答案 + 引用列表（点击引用跳转播放器，复用 jumpTo 逻辑）
        let html = `<p>${data.answer || '无回答'}</p>`;
        if (Array.isArray(data.sources) && data.sources.length) {
          html += '<ul class="qa-sources">';
          data.sources.forEach((s, i) => {
            const ts = fmt(s.start);
            const snippet = (s.content || '').slice(0, 80);
            html += `<li class="qa-source" data-start="${s.start}" data-end="${s.end}"><span class="ts">[${ts}]</span> ${snippet}…</li>`;
          });
          html += '</ul>';
        }
        qaAnswer.innerHTML = html;
        // 绑定引用点击 → 跳转播放器
        qaAnswer.querySelectorAll('.qa-source').forEach(el => {
          el.addEventListener('click', () => jumpTo(el));
        });
      } catch (err) {
        qaAnswer.innerHTML = '<p class="error">请求失败</p>';
      }
    });
  }

// ---- Paraphrase 复述讲解（GeneratedDerivative，ADR-0018）----
  const paraphrasePanel = document.getElementById('paraphrase-panel');
  const paraphraseForm = document.getElementById('paraphrase-form');
  const paraphraseOutput = document.getElementById('paraphrase-output');
  const paraphraseSegInput = document.getElementById('paraphrase-segment-ids');

  // 每个 Segment 的"重讲"按钮：填入该 segment_id 并展开面板
  document.querySelectorAll('.seg-paraphrase-btn').forEach((btn) => {
    btn.addEventListener('click', (e) => {
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

  const paraphraseClose = document.getElementById('paraphrase-close');
  if (paraphraseClose) {
    paraphraseClose.addEventListener('click', () => {
      if (paraphrasePanel) paraphrasePanel.hidden = true;
    });
  }

  if (paraphraseForm) {
    paraphraseForm.addEventListener('submit', async (e) => {
      e.preventDefault();
      if (!paraphraseSegInput.value) {
        paraphraseOutput.innerHTML = '<p class="error">请先在转录稿中选择至少一个片段。</p>';
        return;
      }
      paraphraseOutput.innerHTML = '<p>重新讲解中…</p>';
      const fd = new FormData(paraphraseForm);
      const parts = window.location.pathname.split('/');
      fd.append('source_type', parts[2]);
      fd.append('source_id', parts[3]);
      try {
        const resp = await fetch('/api/paraphrase', { method: 'POST', body: fd });
        const data = await resp.json();
        if (data.error) {
          paraphraseOutput.innerHTML = '<p class="error">' + data.error + '</p>';
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

  function escapeHtml(s) {
    return String(s).replace(/[&<>"']/g, (c) => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
  }


// ---- StudyChat 学习对话（GeneratedDerivative，ADR-0018 R3）----
  const scForm = document.getElementById('study-chat-form');
  const scThread = document.getElementById('study-chat-thread');
  const scFeedback = document.getElementById('study-chat-feedback');
  const scSessionInput = document.getElementById('study-chat-session-id');

  function escapeHtml(s) {
    return String(s).replace(/[&<>"']/g, (c) => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
  }
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
    scForm.addEventListener('submit', async (e) => {
      e.preventDefault();
      const q = scForm.querySelector('[name=question]').value;
      if (!q.trim()) return;
      // 即时显示用户问题
      if (scThread) scThread.insertAdjacentHTML('beforeend', renderSCMessage('user', q, []));
      scForm.querySelector('[name=question]').value = '';
      if (scFeedback) scFeedback.innerHTML = '<p>思考中…</p>';
      const fd = new FormData(scForm);
      const parts = window.location.pathname.split('/');
      fd.append('source_type', parts[2]);
      fd.append('source_id', parts[3]);
      try {
        const resp = await fetch('/api/study-chat', { method: 'POST', body: fd });
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

})();
