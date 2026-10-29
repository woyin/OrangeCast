// 侧栏导航：按当前路径标记所在分组与当前页（含客户端导航后的路径变化），
// 默认展开当前分组；窄屏下用顶部按钮开关抽屉；并把底部播放坞高度同步给布局。
(function (root) {
  'use strict';
  const menus = Array.from(document.querySelectorAll('.nav-menu'));
  const primary = Array.from(document.querySelectorAll('.nav-primary'));
  const toggle = document.querySelector('.nav-toggle');

  function setDrawer(open) {
    document.body.classList.toggle('nav-open', open);
    if (toggle) toggle.setAttribute('aria-expanded', String(open));
  }

  function matches(link) {
    const here = location.pathname;
    const path = new URL(link.href, location.origin).pathname;
    return { path: path, hit: path === here || (path !== '/' && here.indexOf(path + '/') === 0) };
  }

  function mark() {
    primary.forEach(function (link) {
      if (matches(link).hit) link.setAttribute('aria-current', 'page');
      else link.removeAttribute('aria-current');
    });
    menus.forEach(function (menu) {
      let hit = null;
      menu.querySelectorAll('.nav-menu-panel a').forEach(function (link) {
        const m = matches(link);
        if (m.hit && (!hit || m.path.length > hit.path.length)) hit = { link: link, path: m.path };
        link.removeAttribute('aria-current');
      });
      if (hit) {
        menu.classList.add('current');
        menu.open = true;
        hit.link.setAttribute('aria-current', 'page');
      } else {
        menu.classList.remove('current');
      }
    });
  }

  if (toggle) toggle.addEventListener('click', function () { setDrawer(!document.body.classList.contains('nav-open')); });
  document.addEventListener('click', function (event) {
    const target = event.target;
    if (!target.closest) return;
    if (target.closest('[data-nav-close]') || target.closest('.sidebar a')) setDrawer(false);
  });
  document.addEventListener('keydown', function (event) { if (event.key === 'Escape') setDrawer(false); });
  mark();

  // 客户端导航只替换 #page-view，路径会变；观察替换后重新标记。
  const main = document.getElementById('page-view');
  if (main && main.parentNode && 'MutationObserver' in root) {
    new root.MutationObserver(mark).observe(main.parentNode, { childList: true });
  }
  root.addEventListener('popstate', mark);

  // 播放坞固定在底部：把它的实际高度写入 --dock-h，主区据此留出空隙。
  const dock = document.getElementById('listening-shell');
  if (dock && 'ResizeObserver' in root) {
    const sync = function () {
      document.documentElement.style.setProperty('--dock-h', dock.hidden ? '0px' : dock.offsetHeight + 'px');
    };
    new root.ResizeObserver(sync).observe(dock);
    new root.MutationObserver(sync).observe(dock, { attributes: true, attributeFilter: ['hidden'] });
    sync();
  }
})(window);
