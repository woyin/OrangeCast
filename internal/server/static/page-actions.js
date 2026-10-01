window.CWPViews.define(function(view,scope){view.querySelectorAll('form[data-confirm]').forEach(form=>scope.on(form,'submit',event=>{if(!window.confirm(form.dataset.confirm))event.preventDefault();}));});
window.CWPViews.define(function(view,scope){
if(!view.querySelector('[data-view="podcast_detail"]'))return;

  // ---- 单集"处理"：AJAX，不刷新页面 ----
  view.querySelectorAll('.process-one').forEach(function(btn) {
    scope.on(btn,'click', async function() {
      const sid = btn.dataset.sourceId;
      const stype = btn.dataset.sourceType;
      const csrf = btn.dataset.csrf;
      const fd = new FormData();
      fd.append('_csrf', csrf);
      fd.append('source_type', stype);
      fd.append('source_id', sid);
      btn.disabled = true;
      btn.textContent = '入队中…';
      try {
        // handleProcess 返回 redirect，fetch 默认跟随；入队成功后 redirect 到详情页——
        // 但我们不想要跳转，所以用 redirect:'manual' 然后只看是否 ok
        const resp = await scope.fetch('/api/process', { method: 'POST', body: fd, redirect: 'manual' });
        if (resp.status === 0 || resp.status === 303) {
          // 入队成功（303 redirect 被拦截）：更新行 UI
          btn.textContent = '✓ 已入队';
          btn.classList.add('btn-secondary');
          const statusEl = view.querySelector('.status-text[data-source-id="' + sid + '"]');
          if (statusEl) statusEl.textContent = 'queued（等待处理）';
          const checkbox = view.querySelector('.batch-check[value="' + sid + '"]');
          if (checkbox) { checkbox.remove(); }
        } else {
          btn.textContent = '处理';
          btn.disabled = false;
          alert('入队失败');
        }
      } catch(e) {
        if(scope.signal.aborted)return;
        btn.textContent = '处理';
        btn.disabled = false;
        alert('请求失败');
      }
    });
  });

  // ---- 批量工具栏 ----
  const checks = view.querySelectorAll('.batch-check');
  const toolbar = view.querySelector('#' + 'batch-toolbar');
  const countEl = view.querySelector('#' + 'batch-count');
  const selectAllBtn = view.querySelector('#' + 'select-all-btn');
  const clearAllBtn = view.querySelector('#' + 'clear-all-btn');
  const submit = view.querySelector('#' + 'batch-submit');
  if (checks.length === 0) return;

  function update() {
    const n = Array.from(checks).filter(c => c.checked).length;
    if (n > 0) {
      toolbar.style.display = 'flex';
      countEl.textContent = '已选 ' + n + ' 集';
      submit.disabled = false;
    } else {
      toolbar.style.display = 'none';
      submit.disabled = true;
    }
  }
  checks.forEach(c => scope.on(c,'change', update));
  scope.on(selectAllBtn,'click', function() {
    checks.forEach(c => c.checked = true);
    update();
  });
  scope.on(clearAllBtn,'click', function() {
    checks.forEach(c => c.checked = false);
    update();
  });
  update();

});

window.CWPViews.define(function(view,scope){
if(!view.querySelector('[data-view="progress"]'))return;

  const esc=window.CWPSafe.escapeHTML;
  function jobTypeLabel(t) {
    if (t === 'episode_digest') return '单集精读生成';
    if (t === 'transcribe') return '音频转录';
    if (t === 'analyze') return '知识卡片生成';
    return esc(t);
  }
  async function refresh() {
    if(document.hidden)return;
    try {
      const resp = await scope.fetch('/api/progress');
      const data = await resp.json();
      let html = '';
      if (data.active) {
        const a = data.active;
        html += '<div class="status-bar processing"><span class="spinner"></span><div style="flex:1"><strong>正在处理：</strong><a href="/sources/' + encodeURIComponent(a.source_type) + '/' + encodeURIComponent(a.source_id) + '">' + esc(a.title) + '</a> <span class="meta">（' + esc(a.stage) + ' · ' + jobTypeLabel(a.job_type) + '）</span></div></div>';
      } else {
        html += '<div class="status-bar pending">当前没有正在处理的任务。</div>';
      }
      if (data.queued && data.queued.length > 0) {
        html += '<h2>排队中（' + data.queued.length + '）</h2><div id="queued-list">';
        data.queued.forEach(function(j) {
          html += '<div class="list-item"><div class="ep-info" style="flex:1"><h3><a href="/sources/' + encodeURIComponent(j.source_type) + '/' + encodeURIComponent(j.source_id) + '">' + esc(j.title) + '</a></h3><p class="meta">' + esc(j.stage) + ' · ' + jobTypeLabel(j.job_type) + '</p></div></div>';
        });
        html += '</div>';
      } else {
        html += '<p class="empty">没有排队中的任务。</p>';
      }
      if (data.recent && data.recent.length > 0) {
        html += '<h2 style="margin-top:1.5rem">最近完成</h2><div id="recent-list">';
        data.recent.forEach(function(j) {
          const icon = j.status === 'succeeded' ? '✅' : '❌';
          html += '<div class="list-item"><div class="ep-info" style="flex:1"><h3><a href="/sources/' + encodeURIComponent(j.source_type) + '/' + encodeURIComponent(j.source_id) + '">' + esc(j.title) + '</a></h3><p class="meta">' + icon + (j.status === 'succeeded' ? ' 成功' : ' 失败') + ' · ' + jobTypeLabel(j.job_type) + ' · ' + (window.cwpLocalTime ? window.cwpLocalTime(j.updated_at) : j.updated_at) + '</p></div></div>';
        });
        html += '</div>';
      }
      view.querySelector('#' + 'progress-content').innerHTML = html;
    } catch(e) {}
  }
  scope.interval(refresh, 5000);

});
