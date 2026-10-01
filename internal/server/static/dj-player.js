// dj-player.js DJ 播放状态机（D05 / ADR-0024 §3）。
//
// 从 dj.html 内联逻辑提取的可测试播放控制模块：
//   - Interface：load(items) / play() / pause() / next() / stop()，
//     另有 playItems(items) 用于手动单段试听。
//   - 原音区间由媒体事件驱动：seeked（seek 完成）→ timeupdate/currentTime ≥ 区间末
//     → pause；ended/error/waiting/playing 事件分别处理播完、失败、缓冲与恢复。
//     不使用墙钟定时器判断播完——缓冲不消耗片段时间。
//   - 会话令牌（session token）：load/play/pause/next/stop 与手动切换都会使令牌
//     变化；未完成的异步续播在恢复执行时发现令牌过期即静默退出——
//     停止后无幽灵续播，快速切换不双声叠播。
//   - 显式状态机：idle | loading | playing | paused | buffering | ended | error，
//     通过 onState 回调对外报告；条目切换通过 onItem 回调报告。
//
// 两类音轨（evidence 原音 / narration 解说）互斥：同一时刻只允许一个 <audio> 处于
// 播放状态；切换条目类型时先硬停另一路。
(function (global) {
  'use strict';

  var STATES = {
    IDLE: 'idle',
    LOADING: 'loading',
    PLAYING: 'playing',
    PAUSED: 'paused',
    BUFFERING: 'buffering',
    ENDED: 'ended',
    ERROR: 'error'
  };

  // 区间末尾容差（秒）：currentTime >= end - EPSILON 即认为播完区间。
  var EPSILON = 0.25;

  function createDJPlayer(opts) {
    opts = opts || {};
    var evidence = opts.evidence;
    var narration = opts.narration;
    var onState = typeof opts.onState === 'function' ? opts.onState : function () {};
    var onItem = typeof opts.onItem === 'function' ? opts.onItem : function () {};

    var state = STATES.IDLE;
    var token = 0; // 会话令牌
    var queue = [];
    var idx = 0;
    var currentItem = null;
    var skipRequested = false;
    var pauseRequested = false;
    var resumeAt = null; // 暂停时的原音位置（恢复用，不回到条目开头）
    var rate = 1;        // 播放速度（D06）：应用于原音与解说

    function setState(s, detail) {
      state = s;
      onState(s, detail, currentItem);
    }

    function stale(t) { return t !== token; }

    function activeMedia(item) {
      return item && item.type === 'narration' ? narration : evidence;
    }

    function detachMedia(el) {
      el.onended = null;
      el.ontimeupdate = null;
      el.onwaiting = null;
      el.onplaying = null;
      el.onerror = null;
      el.onpause = null;
      el.onseeked = null;
    }

    function hardStopMedia() {
      try { evidence.pause(); } catch (e) { /* 已停止 */ }
      try { narration.pause(); } catch (e) { /* 已停止 */ }
      detachMedia(evidence);
      detachMedia(narration);
    }

    // runItem 执行单个条目。结果：'done'（区间播完/跳过）| 'failed'（播放失败）
    // | 'paused'（用户暂停）| 'aborted'（会话过期，静默退出）。
    function runItem(item, t) {
      return new Promise(function (resolve) {
        var finished = false;
        var isNarr = item.type === 'narration';
        var media = isNarr ? narration : evidence;

        function finish(outcome) {
          if (finished) return;
          finished = true;
          detachMedia(media);
          resolve(outcome);
        }

        media.onended = function () { finish('done'); };
        media.onpause = function () {
          if (finished) return;
          if (skipRequested) { skipRequested = false; finish('done'); return; }
          if (pauseRequested) { pauseRequested = false; finish('paused'); return; }
          if (stale(t)) { finish('aborted'); return; }
        };
        if (!isNarr) {
          media.ontimeupdate = function () {
            if (finished) return;
            if (rangeEnd !== null &&
                typeof media.currentTime === 'number' &&
                media.currentTime >= rangeEnd - EPSILON) {
              try { media.pause(); } catch (e) { /* 已停止 */ }
              finish('done'); // 媒体时间到达区间末尾：不以墙钟判断
            }
          };
        }
        media.onwaiting = function () {
          if (!finished && !stale(t)) setState(STATES.BUFFERING, item);
        };
        media.onplaying = function () {
          if (!finished && !stale(t)) setState(STATES.PLAYING, item);
        };
        media.onerror = function () {
          if (!finished) { setState(STATES.ERROR, item); finish('failed'); }
        };

        function begin() {
          if (finished || stale(t)) { finish('aborted'); return; }
          try { media.playbackRate = rate; } catch (e) { /* 替身或实现不支持 */ }
          var p;
          try {
            p = media.play();
          } catch (e) {
            setState(STATES.ERROR, item);
            finish('failed');
            return;
          }
          if (p && typeof p.then === 'function') {
            p.then(function () {
              if (!finished && !stale(t)) setState(STATES.PLAYING, item);
            }).catch(function (error) {
              if(error && error.name === 'NotAllowedError'){pauseRequested=false;if(!isNarr)resumeAt=media.currentTime;setState(STATES.PAUSED,error);finish('paused');return;}
              // 播放被拒绝（自动播放策略等）：跳过该条目，不阻塞后续。
              if (!finished) { setState(STATES.ERROR, item); finish('failed'); }
            });
          }
        }

        if (isNarr) {
          media.src = item.url || '';
          if (!item.url) { finish('done'); return; }
          begin();
          return;
        }
        // D06：end 为 null（继续听原节目）时不设区间末尾——播放到集尾自然 ended。
        var rangeEnd = item.end === null || item.end === undefined ? null : item.end;
        var target = resumeAt !== null ? resumeAt : item.start;
        var onSeeked = function () {
          media.onseeked = null;
          begin(); // seek 完成后再播放（seek 尚未完成时不启动）
        };
        media.onseeked = onSeeked;
        media.currentTime = target;
        // 已位于目标位置（如恢复播放）时 seeked 可能不触发：直接启动。
        if (typeof media.currentTime === 'number' &&
            Math.abs(media.currentTime - target) < 1e-6) {
          media.onseeked = null;
          begin();
        }
      });
    }

    function runQueueFrom(t) {
      setState(STATES.LOADING);
      function step() {
        if (stale(t)) return; // 会话过期：不推动旧队列（无幽灵续播）
        if (idx >= queue.length) {
          currentItem = null;
          setState(STATES.ENDED);
          return;
        }
        currentItem = queue[idx];
        onItem(currentItem, idx);
        runItem(currentItem, t).then(function (outcome) {
          if (stale(t)) return;
          if (outcome === 'paused') { setState(STATES.PAUSED); return; }
          if (outcome === 'failed') { setState(STATES.ERROR, currentItem); }
          idx++;
          step();
        });
      }
      step();
    }

    return {
      STATES: STATES,
      load: function (items) {
        token++;
        hardStopMedia();
        queue = (items || []).slice();
        idx = 0;
        currentItem = null;
        resumeAt = null;
        setState(STATES.IDLE);
      },
      play: function () {
        if (state === STATES.PLAYING || state === STATES.BUFFERING || state === STATES.LOADING) return;
        var t = ++token;
        pauseRequested = false;
        skipRequested = false;
        runQueueFrom(t);
      },
      pause: function () {
        if (state !== STATES.PLAYING && state !== STATES.BUFFERING && state !== STATES.LOADING) return;
        pauseRequested = true;
        skipRequested = false;
        if (currentItem && currentItem.type !== 'narration' &&
            typeof evidence.currentTime === 'number') {
          resumeAt = evidence.currentTime; // 暂停位置恢复，不回到条目开头
        }
        token++; // 当前会话以 paused 结束，防止未完成 Promise 推进队列
        try { evidence.pause(); } catch (e) { /* 已停止 */ }
        try { narration.pause(); } catch (e) { /* 已停止 */ }
        setState(STATES.PAUSED);
      },
      next: function () {
        var media = activeMedia(currentItem);
        skipRequested = true;
        pauseRequested = false;
        resumeAt = null;
        try { media.pause(); } catch (e) { /* 未在播放：状态机按需推进 */ }
        if (state !== STATES.PLAYING && state !== STATES.BUFFERING && state !== STATES.LOADING) {
          // 未在播放时直接推进并保持暂停语义。
          if (idx < queue.length) idx++;
          setState(STATES.PAUSED);
        }
      },
      stop: function () {
        token++;
        skipRequested = false;
        pauseRequested = false;
        resumeAt = null;
        hardStopMedia();
        idx = 0;
        currentItem = null;
        setState(STATES.IDLE);
      },
      prev: function () {
        // 上一段（D06）：回到上一条目开头。
        if (idx > 0) {
          idx--;
        }
        resumeAt = null;
        var t = ++token;
        hardStopMedia();
        pauseRequested = false;
        skipRequested = false;
        runQueueFrom(t);
      },
      setRate: function (r) {
        if (typeof r !== 'number' || !(r >= 0.5 && r <= 3)) return;
        rate = r;
        try { evidence.playbackRate = r; } catch (e) { /* 未就绪 */ }
        try { narration.playbackRate = r; } catch (e) { /* 未就绪 */ }
      },
      getRate: function () { return rate; },
      playFullProgram: function () {
        // “继续听原节目”（D06）：从当前原音位置播放到集尾（rangeEnd=null，
        // timeupdate 不再截停）；仍受同一状态机与会话令牌治理。
        token++;
        hardStopMedia();
        pauseRequested = false;
        skipRequested = false;
        var start = (typeof evidence.currentTime === 'number' && evidence.currentTime > 0) ? evidence.currentTime : 0;
        resumeAt = null;
        queue = [{ type: 'evidence', start: start, end: null, idx: 'full', full: true }];
        idx = 0;
        var t = token;
        runQueueFrom(t);
      },
      playItems: function (items) {
        // 手动试听：取消旧会话，只播放给定条目序列。
        token++;
        hardStopMedia();
        resumeAt = null;
        queue = (items || []).slice();
        idx = 0;
        var t = token;
        runQueueFrom(t);
      },
      state: function () { return state; },
      position: function () { return { index: idx, item: currentItem }; }
    };
  }

  global.DJPlayer = { create: createDJPlayer, STATES: STATES };
})(window);
