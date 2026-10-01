// Shared transport/persistence; adapters retain their own original/DJ state.
(function (root) {
  'use strict';
  var rates = [0.75, 1, 1.25, 1.5, 1.75, 2];
  function create(options) {
    var a = options.adapter, revision = 0, loaded = false, dirty = false;
    var busy = false, conflict = false, stopped = false, deadline = options.sleepDeadline || 0, saved = null, sleepExpired=false;
    var notify = options.notify || function () {}, fetcher = options.fetch || root.fetch.bind(root);
    var now = options.now || Date.now;
    var lastSeq=0;
    var mediaActions = [], abort = typeof root.AbortController === 'function' ? new root.AbortController() : null;
    var pendingKey = 'cwp-pending-progress:' + options.sourceType + ':' + options.sourceId + ':' + options.mode + ':' + (options.audioSHA || '') + ':' + (options.planId || '');
    function persist(snap) { try { root.localStorage.setItem(pendingKey, JSON.stringify(snap)); } catch (_) { notify('本地位置保存失败，请保留页面或记下时间。'); } }
    function clearPending(snap) { try { if(root.localStorage.getItem(pendingKey) === JSON.stringify(snap))root.localStorage.removeItem(pendingKey); } catch (_) {} }
    function storedRate() { try { return Number(root.localStorage.getItem('cwp-playback-rate')) || 1; } catch (_) { return 1; } }
    function setRate(rate) { if (rates.indexOf(rate) < 0) return; a.setRate(rate); try { root.localStorage.setItem('cwp-playback-rate', String(rate)); } catch (_) {} if (options.onRate) options.onRate(rate); dirty = true; }
    setRate(storedRate()); dirty = false;
    function play() { if(deadline&&now()>=deadline){tick();return Promise.resolve();}sleepExpired=false;return Promise.resolve().then(function () { if (!stopped) return a.play(); }).catch(function () { notify('浏览器未开始播放，请再次点播放。'); }); }
    function pause() { a.pause(); save(); }
    function toggle() { if (a.paused()) play(); else pause(); }
    function seek(position) { a.seek(position); dirty = true; save(); }
    function skip(delta) { seek(a.time() + delta); }
    async function load() {
      try {
        var response = await fetcher('/api/listening-progress?source_type=' + encodeURIComponent(options.sourceType) + '&source_id=' + encodeURIComponent(options.sourceId) + '&mode=' + options.mode, abort ? {signal:abort.signal} : undefined);
        if (!response.ok) throw new Error('read');
        saved = await response.json(); if(stopped)return; revision = saved.revision || 0;
        if (saved.speed && !dirty) { setRate(saved.speed);dirty=false; }
        if (saved.id && identityMatches(saved) && options.onResume) options.onResume(saved, function () { if(stopped)return;a.restore(saved);conflict=false;dirty=true;play(); });
        loaded = true;
        var previousPending=null;try{previousPending=JSON.parse(root.localStorage.getItem(pendingKey)||'null');}catch(_){}
        var acknowledged=previousPending&&sameSaved(previousPending,saved);if(acknowledged)clearPending(previousPending);
        if(typeof options.localRevision==='number'&&options.localRevision!==revision&&!acknowledged){conflict=true;notify('进度已被其他页面更新；恢复的位置保持暂停，请明确保存或使用服务端续听位置。',resolveConflict);}
        try {
          var pending = JSON.parse(root.localStorage.getItem(pendingKey) || 'null');
          if (pending && pending.mode === options.mode && identityMatches(pending)) { notify('本地有未保存的位置，可选择继续并保存。', function () { a.restore(pending);dirty=true;if(pending.expected_revision!==revision){conflict=true;notify('进度已被其他页面更新，请明确保存本次位置。',resolveConflict);}else{play();save();} }); }
        } catch (_) {}
      } catch (_) { if(stopped)return;notify('无法读取续听进度，收听仍可继续。'); }
    }
    function sameSaved(pending,saved){return ['source_type','source_id','mode','audio_sha256','plan_id','plan_version','item_position','highlight_id','item_offset_seconds','seq','speed'].every(function(key){return (pending[key]===undefined?(['plan_version','item_position'].includes(key)?0:''):pending[key])===(saved[key]===undefined?(['plan_version','item_position'].includes(key)?0:''):saved[key]);});}
    function identityMatches(saved) {
      if ((saved.audio_sha256 || '') !== (options.audioSHA || '')) { notify('原音指纹已变化，旧位置无法自动恢复。');return false; }
      if (options.mode === 'dj' && options.planId && (saved.plan_id !== options.planId || saved.plan_version !== options.planVersion)) { notify('DJ清单已变化，旧位置无法恢复。');return false; }
      return true;
    }
    async function save() {
      if (!dirty || stopped) return;
      var snap = a.snapshot(); if (!snap) return;
      snap.source_type = options.sourceType; snap.source_id = options.sourceId; snap.mode = options.mode;
      snap.audio_sha256 = options.audioSHA || '';
      snap.expected_revision = revision; snap.seq = lastSeq=Math.max(now(),lastSeq+1); snap.speed = a.getRate();
      persist(snap); if (!loaded || busy || conflict) return; busy = true; dirty = false; var accepted=false;
      try {
        var response = await fetcher('/api/listening-progress', { method:'POST', headers:{'Content-Type':'application/json', 'X-CSRF-Token':options.csrf}, body:JSON.stringify(snap), keepalive:true });
        if (response.status === 409) { conflict = true; dirty = true; notify('另一处已更新进度；继续本次播放需明确保存。', resolveConflict); return; }
        if (!response.ok) throw new Error('save');
        var result = await response.json(); revision = result.revision; accepted=true; clearPending(snap);if(options.onSaved&&!stopped)options.onSaved(result);
      } catch (_) { dirty = true; notify('进度暂未保存；恢复联网后重试。'); }
      finally { busy = false;if(accepted&&dirty&&!stopped)save(); }
    }
    async function resolveConflict() {
      try {
        var response = await fetcher('/api/listening-progress?source_type=' + encodeURIComponent(options.sourceType) + '&source_id=' + encodeURIComponent(options.sourceId) + '&mode=' + options.mode, abort ? {signal:abort.signal} : undefined);
        if (!response.ok) throw new Error('read');
        var result = await response.json(); if(stopped)return; revision = result.revision || 0; conflict = false; dirty = true; await save();
      } catch (_) { notify('未能取得新版本，请联网后重试。', resolveConflict); }
    }
    function setSleep(minutes) { sleepExpired=false;deadline = minutes ? now() + minutes * 60000 : 0; notify(deadline ? minutes + '分钟后暂停' : '睡眠定时已关闭'); }
    function tick() { if (deadline && now() >= deadline) { deadline = 0;sleepExpired=true;pause(); notify('睡眠定时已暂停播放'); } if (!a.paused()) dirty = true; save(); }
    var timer = root.setInterval(tick, 5000);
    function changed() { dirty = true; }
    function hidden() { tick(); if (root.document.hidden) save(); }
    if (root.document) root.document.addEventListener('visibilitychange', hidden);
    function online() { if (!loaded) load().then(save); else save(); }
    root.addEventListener('pagehide', save); root.addEventListener('online', online);
    if (root.navigator && 'mediaSession' in root.navigator) {
      if(root.MediaMetadata){try{root.navigator.mediaSession.metadata=new root.MediaMetadata({title:options.title||'CloudWisePod',artist:options.mode==='dj'?'DJ 精听':'原音收听',album:'CloudWisePod'});}catch(_){}}
      var handlers = { play:play, pause:pause, seekbackward:function (d) { skip(-(d.seekOffset || 15)); }, seekforward:function (d) { skip(d.seekOffset || 15); }, seekto:function (d) { seek(d.seekTime); } };
      if (a.next) handlers.nexttrack = a.next; if (a.prev) handlers.previoustrack = a.prev;
      mediaActions = Object.keys(handlers);
      mediaActions.forEach(function (name) { try { root.navigator.mediaSession.setActionHandler(name, handlers[name]); } catch (_) {} });
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
      destroy:function () { stopped=true;if(abort)abort.abort();root.clearInterval(timer);mediaActions.forEach(function(name){try{root.navigator.mediaSession.setActionHandler(name,null);}catch(_){}});try{root.navigator.mediaSession.metadata=null;root.navigator.mediaSession.playbackState='none';}catch(_){}root.removeEventListener('pagehide',save);root.removeEventListener('online',online); if(root.document){root.document.removeEventListener('keydown',key);root.document.removeEventListener('visibilitychange',hidden);} },
      status:function () { return {revision:revision,loaded:loaded,dirty:dirty,conflict:conflict,deadline:deadline,sleepExpired:sleepExpired}; }};
  }
  // Only a whole original recording's natural end can offer reflection.
  // Queue/DJ transitions, loops, excerpts, failures and expired sleep are excluded.
  function canOfferReflection(state,now=Date.now()) {
    var spec=state?.spec,a=state?.audio,sleep=state?.transport?.status()||{};
    return !!spec&&spec.mode==='original'&&!state.player&&!state.loop&&!spec.excerptId&&
      a?.ended===true&&!a.error&&Number.isFinite(a.duration)&&a.duration>0&&
      Number.isFinite(a.currentTime)&&a.currentTime>=a.duration-0.05&&
      !sleep.sleepExpired&&(!sleep.deadline||sleep.deadline>now);
  }
  root.CWPPlayback = Object.freeze({create:create,rates:rates,canOfferReflection});
})(typeof window !== 'undefined' ? window : globalThis);
