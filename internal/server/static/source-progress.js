// 单集处理进度：轮询只读接口，更新阶段状态与“下一步”，完成后自动刷新页面。
// 普通轮询不触发处理、不入队、不调用模型；播放中的来源不会被强制刷新。
(function (root) {
  'use strict';
  if (!root.CWPViews) return;

  root.CWPViews.define(function (view, scope) {
    const panel = view.querySelector('[data-source-progress]');
    if (!panel) return;
    const sourceType = panel.dataset.sourceType;
    const sourceId = panel.dataset.sourceId;
    if (!sourceType || !sourceId) return;

    const live = view.querySelector('#source-live-status');
    const rows = new Map();
    panel.querySelectorAll('[data-stage]').forEach(function (row) { rows.set(row.dataset.stage, row); });
    let stopped = false;
    let reloading = false;

    function setRow(stage) {
      const row = rows.get(stage.stage);
      if (!row) return;
      const badge = row.querySelector('[data-stage-status]');
      row.dataset.status = stage.status;
      if (badge) {
        badge.textContent = stage.status_label || stage.status;
        badge.className = 'stage-badge ' + stage.status;
      }
      const detail = row.querySelector('[data-stage-detail]');
      if (detail) detail.textContent = stage.detail || '';
    }

    function paint(data) {
      (data.stages || []).forEach(setRow);
      if (live) {
        const parts = [];
        if (data.next) parts.push(data.next);
        if (data.failed && data.error) parts.push(data.error);
        live.textContent = parts.join(' · ');
        live.dataset.state = data.failed ? 'failed' : data.complete ? 'done' : 'running';
      }
      panel.dataset.status = data.status || '';
      if (data.complete && !reloading) {
        reloading = true;
        const active = root.CWPListening && root.CWPListening.state().spec;
        const playingThis = active && active.sourceType === sourceType && active.sourceId === sourceId;
        if (playingThis) {
          if (live) live.textContent = '处理完成。正在播放本节，刷新后查看新内容。';
        } else {
          location.reload();
        }
        return true;
      }
      return data.complete || data.failed;
    }

    async function tick() {
      if (stopped) return;
      try {
        const response = await scope.fetch('/api/source-progress?source_type=' + encodeURIComponent(sourceType) + '&source_id=' + encodeURIComponent(sourceId), { cache: 'no-store' });
        if (!response.ok) return;
        const data = await response.json();
        const done = paint(data);
        if (done) stopped = true;
      } catch (error) {
        if (scope.signal.aborted) return;
        if (live) live.textContent = '暂时无法获取进度，稍后自动重试。';
      }
    }

    if (panel.dataset.status === 'processed') return; // 已完成：不需要轮询
    scope.interval(tick, 3000);
    tick();
    scope.cleanup(function () { stopped = true; });
  });
})(window);
