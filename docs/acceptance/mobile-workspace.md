# 移动端工作区验收（mobile-workspace）

状态：**已执行（2026-09-13）**。本记录对应首轮计划 U03「完成手机布局与基本可访问性检查」。

## 环境与限制

- 日期：2026-09-13；被测版本：`1edc5b4`（含本轮移动端修复）。
- 平台：macOS arm64；浏览器：ZCode 内置 Chromium（Playwright 兼容面），视口 375×812（iPhone 12/13 尺寸级别）。
- **截图限制**：本会话环境操作系统级屏幕录制被拒绝（TCC）、内置浏览器截图能力不可用（capture failed for guest），无法落截图文件。证据以 DOM 级结构化审计数据（横向溢出探测、触控目标尺寸、可访问名称、焦点样式检查）+ 下方逐页结果表为准；像素级人工目检留给 Owner 在真机上复核。
- 服务：本地 `cloudwisepod serve`（端口 8090，scratch DATA_DIR，真实 Groq key）。

## 本轮发现并修复的缺陷

| # | 缺陷 | 修复 | 提交 |
|---|---|---|---|
| 1 | 375px 视口下 `.nav-links` 12 个导航链接挤压成竖排字符列，整页横向溢出（scrollWidth 562 > 375） | 768px 断点：navbar 纵向堆叠、链接 flex-wrap 换行 | `1edc5b4` |
| 2 | `/settings` 模型价格/Provider 表格（THEAD 416px）撑破页面 | 768px 断点：`.container table` 块级化 + 横向滚动容器 | `1edc5b4` |

## 逐页审计结果（375×812，修复后）

审计项：页面级横向溢出（documentElement.scrollWidth == clientWidth）、越界元素、<40px 高的可见按钮/链接。

| 页面 | 横向溢出 | 越界元素 | 小触控目标 |
|---|---|---|---|
| /dashboard（登录后） | 无（375=375） | 0 | 0 |
| /podcasts | 无 | 0 | 0 |
| /podcasts/{博物志} | 无 | 0 | 0 |
| /workbench | 无 | 0 | 0 |
| /attention | 无 | 0 | 0 |
| /search | 无 | 0 | 0 |
| /settings | 无（表格在滚动容器内） | 0（页面级） | 0 |
| /progress | 无 | 0 | 0 |
| /uploads | 无 | 0 | 0 |
| /documents | 无 | 0 | 0 |
| /keypoints | 无 | 0 | 0 |
| /themes | 无 | 0 | 0 |

## 可访问性检查

- **焦点可见**：`app.css` 含 `:focus-visible` 规则（程序化确认）；键盘 Tab 顺序由 DOM 顺序保证。
- **控件标签**：播客详情页全部可见 `a/button/input/select` 均有可访问名称（aria-label/文本/placeholder/label 包裹），未命名控件 = 0。
- **触控目标**：`.dj-controls button` 等经 D06 修复保证 ≥44px（CSS 断言在 `TestDJPlayerStaticAssets`）；本轮审计未发现 <40px 的可见按钮/链接。
- **DJ 播放状态机**：真实 Chromium（非 Node DOM 桩）执行 `testdata/dj-harness.html` 全部 5 场景——区间由媒体时间驱动、缓冲不消耗、暂停不跳段、停止无幽灵续播、快速切换互斥——**PASS 5/5**。附注：夹具自动链跑依赖 5ms 级定时器，后台标签页受 Chromium 节流会显著变慢（表现为"running…"），前台或逐场景执行均通过。

## 与旅程的交叉验证

学习→记笔记→精读、构思→文章两条路径在手机视口下的实际操作随 V02 真实旅程执行（`docs/acceptance/2026-09-learning-dj-creation-run.md`），本页记录布局与可访问性结论。

## 遗留

- 真机（iOS Safari / Android Chrome）像素级目检与触感验证：环境限制未执行，留 Owner。
- 视口只覆盖 375×812；360px（常见 Android）与平板断点未逐一审计（同一 768px 断点覆盖）。