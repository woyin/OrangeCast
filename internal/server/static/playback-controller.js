// Shared transport/persistence; adapters retain their own original/DJ state.
(function (root) {
  'use strict';
  var rates = [0.75, 1, 1.25, 1.5, 1.75, 2];
  function create(options) {
    var a = options.adapter, revision = 0, loaded = false, dirty = false;
    var busy = false, conflict = false, stopped = false, deadline = 0, saved = null;
    var notify = options.notify || function () {}, fetcher = options.fetch || root.fetch.bind(root);
    var now = options.now || Date.now;
    var pendingKey = 'cwp-pending-progress:' + options.sourceType + ':' + options.sourceId + ':' + options.mode;
    function persist(snap) { try { root.localStorage.setItem(pendingKey, JSON.stringify(snap)); } catch (_) {} }
    function clearPending() { try { root.localStorage.removeItem(pendingKey); } catch (_) {} }
    function storedRate() { try { return Number(root.localStorage.getItem('cwp-playback-rate')) || 1; } catch (_) { return 1; } }
    function setRate(rate) { if (rates.indexOf(rate) < 0) return; a.setRate(rate); try { root.localStorage.setItem('cwp-playback-rate', String(rate)); } catch (_) {} if (options.onRate) options.onRate(rate); dirty = true; }
    setRate(storedRate()); dirty = false;
    function play() { return Promise.resolve().then(function () { return a.play(); }).catch(function () { notify('浏览器未开始播放，请再次点播放。'); }); }
    function pause() { a.pause(); save(); }
    function toggle() { if (a.paused()) play(); else pause(); }
    function seek(position) { a.seek(position); dirty = true; save(); }
    function skip(delta) { seek(a.time() + delta); }
    async function load() {
      try {
        var response = await fetcher('/api/listening-progress?source_type=' + encodeURIComponent(options.sourceType) + '&source_id=' + encodeURIComponent(options.sourceId) + '&mode=' + options.mode);
        if (!response.ok) throw new Error('read');
        saved = await response.json(); revision = saved.revision || 0;
        if (saved.speed) setRate(saved.speed);
        if (saved.id && options.onResume) options.onResume(saved, function () { a.restore(saved); dirty = true; play(); });
        loaded = true;
        try {
          var pending = JSON.parse(root.localStorage.getItem(pendingKey) || 'null');
          if (pending && pending.mode === options.mode) { notify('本地有未保存的位置，可选择继续并保存。', function () { a.restore(pending);dirty=true;if(pending.expected_revision!==revision){conflict=true;notify('进度已被其他页面更新，请明确保存本次位置。',resolveConflict);}else{play();save();} }); }
        } catch (_) {}
      } catch (_) { notify('无法读取续听进度，收听仍可继续。'); }
    }
    async function save() {
      if (!loaded || !dirty || busy || conflict || stopped) return;
      var snap = a.snapshot(); if (!snap) return;
      snap.source_type = options.sourceType; snap.source_id = options.sourceId; snap.mode = options.mode;
      snap.expected_revision = revision; snap.seq = now(); snap.speed = a.getRate();
      persist(snap); busy = true; dirty = false;
      try {
        var response = await fetcher('/api/listening-progress', { method:'POST', headers:{'Content-Type':'application/json', 'X-CSRF-Token':options.csrf}, body:JSON.stringify(snap), keepalive:true });
        if (response.status === 409) { conflict = true; dirty = true; notify('另一处已更新进度；继续本次播放需明确保存。', resolveConflict); return; }
        if (!response.ok) throw new Error('save');
        var result = await response.json(); revision = result.revision; clearPending();
      } catch (_) { dirty = true; notify('进度暂未保存；恢复联网后重试。'); }
      finally { busy = false; }
    }
    async function resolveConflict() {
      try {
        var response = await fetcher('/api/listening-progress?source_type=' + encodeURIComponent(options.sourceType) + '&source_id=' + encodeURIComponent(options.sourceId) + '&mode=' + options.mode);
        if (!response.ok) throw new Error('read');
        var result = await response.json(); revision = result.revision || 0; conflict = false; dirty = true; await save();
      } catch (_) { notify('未能取得新版本，请联网后重试。', resolveConflict); }
    }
    function setSleep(minutes) { deadline = minutes ? now() + minutes * 60000 : 0; notify(deadline ? minutes + '分钟后暂停' : '睡眠定时已关闭'); }
    function tick() { if (deadline && now() >= deadline) { deadline = 0; pause(); notify('睡眠定时已暂停播放'); } if (!a.paused()) { dirty = true; save(); } }
    var timer = root.setInterval(tick, 5000);
    function changed() { dirty = true; }
    function hidden() { tick(); if (root.document.hidden) save(); }
    if (root.document) root.document.addEventListener('visibilitychange', hidden);
    function online() { if (!loaded) load().then(save); else save(); }
    root.addEventListener('pagehide', save); root.addEventListener('online', online);
    if (root.navigator && 'mediaSession' in root.navigator) {
      var handlers = { play:play, pause:pause, seekbackward:function (d) { skip(-(d.seekOffset || 15)); }, seekforward:function (d) { skip(d.seekOffset || 15); }, seekto:function (d) { seek(d.seekTime); } };
      if (a.next) handlers.nexttrack = a.next; if (a.prev) handlers.previoustrack = a.prev;
      Object.keys(handlers).forEach(function (name) { try { root.navigator.mediaSession.setActionHandler(name, handlers[name]); } catch (_) {} });
    }
    function key(e) {
      if (e.target && (/^(INPUT|TEXTAREA|SELECT|BUTTON|A)$/.test(e.target.tagName) || e.target.isContentEditable)) return;
      if (e.code === 'Space') { e.preventDefault(); toggle(); }
      if (e.code === 'ArrowRight') { e.preventDefault(); skip(15); }
      if (e.code === 'ArrowLeft') { e.preventDefault(); skip(-15); }
    }
    if (root.document) root.document.addEventListener('keydown', key);
    load();
    return {play:play,pause:pause,toggle:toggle,seek:seek,skip:skip,setRate:setRate,setSleep:setSleep,save:save,changed:changed,tick:tick,
      destroy:function () { stopped=true;root.clearInterval(timer);root.removeEventListener('pagehide',save);root.removeEventListener('online',online); if(root.document){root.document.removeEventListener('keydown',key);root.document.removeEventListener('visibilitychange',hidden);} },
      status:function () { return {revision:revision,loaded:loaded,dirty:dirty,conflict:conflict,deadline:deadline}; }};
  }
  root.CWPPlayback = Object.freeze({create:create,rates:rates});
})(typeof window !== 'undefined' ? window : globalThis);
