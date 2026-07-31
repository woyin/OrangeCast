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

  // Q&A 表单（fetch /api/qa，无需刷新）
  const qaForm = document.getElementById('qa-form');
  const qaAnswer = document.getElementById('qa-answer');
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
        const resp = await fetch('/api/qa', { method: 'POST', body: fd });
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
})();
