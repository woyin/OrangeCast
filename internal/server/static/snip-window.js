// Snip 窗口：以当前播放位置所在的转录句为中心，向前取约 20 秒、向后取约 10 秒的相邻句子
// （各最多 4 句），返回连续的 segment id。片段边界因此总落在整句上，而不是随手切的时间点。
(function (root) {
  'use strict';
  var BEFORE = 20, AFTER = 10, MAX_SIDE = 4;

  function snipWindow(segments, time) {
    if (!Array.isArray(segments) || !segments.length) return [];
    var i = -1, k;
    for (k = 0; k < segments.length; k++) {
      if (segments[k].start <= time && segments[k].end > time) { i = k; break; }
    }
    if (i < 0) {
      // 落在两句之间的停顿里：取刚结束的那一句。
      for (k = segments.length - 1; k >= 0; k--) { if (segments[k].end <= time) { i = k; break; } }
    }
    if (i < 0) return [];
    var a = i, b = i;
    while (a > 0 && i - a < MAX_SIDE && segments[i].start - segments[a].start < BEFORE) a--;
    while (b < segments.length - 1 && b - i < MAX_SIDE && segments[b].end - segments[i].end < AFTER) b++;
    return segments.slice(a, b + 1).map(function (s) { return s.id; });
  }

  var api = { window: snipWindow };
  if (typeof module !== 'undefined' && module.exports) module.exports = api;
  root.CWPSnip = api;
})(typeof window !== 'undefined' ? window : globalThis);
