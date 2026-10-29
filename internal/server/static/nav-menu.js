// 顶部导航的分组菜单：同一时间只展开一组，点击外部或按 Esc 收起，
// 并按当前路径标记所在分组与当前页（含客户端导航后的路径变化）。
(function (root) {
  'use strict';
  const menus = Array.from(document.querySelectorAll('.nav-menu'));
  if (!menus.length) return;

  function closeAll(except) {
    menus.forEach(function (menu) { if (menu !== except) menu.open = false; });
  }

  function mark() {
    const here = location.pathname;
    menus.forEach(function (menu) {
      let hit = null;
      menu.querySelectorAll('.nav-menu-panel a').forEach(function (link) {
        const path = new URL(link.href, location.origin).pathname;
        const matches = path === here || (path !== '/' && here.indexOf(path + '/') === 0);
        if (matches && (!hit || path.length > hit.path.length)) hit = { link: link, path: path };
        link.removeAttribute('aria-current');
      });
      if (hit) {
        menu.classList.add('current');
        hit.link.setAttribute('aria-current', 'page');
      } else {
        menu.classList.remove('current');
      }
    });
  }

  menus.forEach(function (menu) {
    menu.addEventListener('toggle', function () { if (menu.open) closeAll(menu); });
    // 点击面板内的链接后立即收起，避免客户端导航后旧菜单还开着。
    menu.querySelectorAll('.nav-menu-panel a').forEach(function (link) {
      link.addEventListener('click', function () { menu.open = false; });
    });
  });

  document.addEventListener('click', function (event) {
    if (!event.target.closest || !event.target.closest('.nav-menu')) closeAll(null);
  });
  document.addEventListener('keydown', function (event) { if (event.key === 'Escape') closeAll(null); });
  mark();

  // 客户端导航只替换 #page-view，路径会变；观察替换后重新标记。
  const main = document.getElementById('page-view');
  if (main && main.parentNode && 'MutationObserver' in root) {
    new root.MutationObserver(mark).observe(main.parentNode, { childList: true });
  }
  root.addEventListener('popstate', mark);
})(window);
