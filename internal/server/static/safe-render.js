(function (root) {
  'use strict';
  function escapeHTML(value) {
    return String(value == null ? '' : value).replace(/[&<>"']/g, function (c) {
      return { '&':'&amp;', '<':'&lt;', '>':'&gt;', '"':'&#34;', "'":'&#39;' }[c];
    });
  }
  // Formatting is deliberately limited. Links are generated separately from
  // verified source identities; raw HTML and Markdown links remain literal.
  function markdown(value) {
    var result = '', inList = false;
    function closeList() { if (inList) { result += '</ul>'; inList = false; } }
    String(value == null ? '' : value).split('\n').forEach(function (line) {
      var text = line.trim(), match;
      if (!text) { closeList(); return; }
      if ((match = /^(#{1,3}) /.exec(text))) {
        closeList(); var level = match[1].length;
        result += '<h' + level + '>' + escapeHTML(text.slice(level + 1).trim()) + '</h' + level + '>';
      } else if (text.startsWith('- ')) {
        if (!inList) { result += '<ul>'; inList = true; }
        result += '<li>' + escapeHTML(text.slice(2).trim()) + '</li>';
      } else { closeList(); result += '<p>' + escapeHTML(text) + '</p>'; }
    });
    closeList(); return result;
  }
  function link(label, href) {
    var url;
    try { url = new URL(href, root.location ? root.location.origin : 'http://localhost'); } catch (_) { return null; }
    if (url.protocol !== 'https:' && url.protocol !== 'http:') return null;
    var node = document.createElement('a');
    node.textContent = String(label);
    node.href = url.href;
    node.rel = 'noopener';
    return node;
  }
  root.CWPSafe = Object.freeze({ escapeHTML: escapeHTML, markdown: markdown, link: link });
})(typeof window !== 'undefined' ? window : globalThis);
