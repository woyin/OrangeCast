package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/woyin/orangecast/internal/models"
)

// TestDJPlayerStaticServed D05：提取出的状态机经 /static/ 提供，
// 且包含事件驱动与会话令牌关键构造，不再依赖墙钟定时器判断区间播完。
func TestDJPlayerStaticServed(t *testing.T) {
	srv := newTestServer(t)
	session := claimOwnerAndLogin(t, srv, "djstatic@example.com", "password123")
	rec := doWithCookie(srv, session, http.MethodGet, "/static/dj-player.js")
	if rec.Code != http.StatusOK {
		t.Fatalf("dj-player.js 应可经 /static/ 获取: %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"createDJPlayer", // 工厂接口
		"STATES",         // 显式状态机
		"ontimeupdate",   // 媒体时间事件驱动区间
		"onseeked",       // seek 完成后再播放
		"onwaiting",      // 缓冲状态
		"token++",        // 会话令牌
		"stale(t)",       // 令牌过期检查（无幽灵续播）
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("dj-player.js 缺少 %q", want)
		}
	}
	// 区间播完不得由墙钟 setTimeout 驱动（文件内不允许出现 setTimeout）。
	if strings.Contains(body, "setTimeout") {
		t.Fatal("状态机不得使用墙钟定时器判断区间播完")
	}
}

// TestDJPageUsesExtractedPlayer D05：dj 页面引用提取出的播放器脚本，
// 内联的旧控制逻辑（runAutoplay/playEvidence/setTimeout）不再存在。
func TestDJPageUsesExtractedPlayer(t *testing.T) {
	srv := newTestServer(t)
	session := claimOwnerAndLogin(t, srv, "djplayer@example.com", "password123")
	podcast, err := srv.store.CreatePodcast(t.Context(), "https://feed.example.com/djpage.xml", "DJ播客", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.store.MergeEpisodes(t.Context(), podcast.ID, []models.Episode{{GUID: "dj-page-1", Title: "DJ单集", AudioURL: "https://cdn.example.com/d.mp3"}}); err != nil {
		t.Fatal(err)
	}
	eps, err := srv.store.ListEpisodes(t.Context(), podcast.ID)
	if err != nil || len(eps) != 1 {
		t.Fatalf("单集 setup: %v", err)
	}
	seedHighlightAndNarration(t, srv, eps[0].ID) // 卡片+高光版本（DJ 页渲染前提）
	rec := doWithCookie(srv, session, http.MethodGet, "/sources/episode/"+eps[0].ID+"/dj")
	if rec.Code != http.StatusOK {
		t.Fatalf("DJ 页应 200: %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `/static/dj-player.js`) {
		t.Fatal("DJ 页应引用提取出的 dj-player.js")
	}
	for _, legacy := range []string{"runAutoplay", "endTimer = setTimeout"} {
		if strings.Contains(body, legacy) {
			t.Fatalf("旧内联逻辑 %q 不应保留在页面中", legacy)
		}
	}
	// 新接口按钮与状态容器存在
	for _, want := range []string{"dj-pause", "dj-next", "dj-stop", "dj-status"} {
		if !strings.Contains(body, want) {
			t.Fatalf("DJ 页缺少 %q 控件", want)
		}
	}
}

// TestDJPageTransportControls_D06 完整传输控件：上一段/下一段/倍速/继续原节目/
// 进度显示；键盘可达（button 元素 + keydown 绑定）；倍速写回 playbackRate。
func TestDJPageTransportControls_D06(t *testing.T) {
	srv := newTestServer(t)
	session := claimOwnerAndLogin(t, srv, "djtransport@example.com", "password123")
	podcast, err := srv.store.CreatePodcast(t.Context(), "https://feed.example.com/djd06.xml", "DJ播客D06", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.store.MergeEpisodes(t.Context(), podcast.ID, []models.Episode{{GUID: "dj-d06", Title: "DJ单集D06", AudioURL: "https://cdn.example.com/d.mp3"}}); err != nil {
		t.Fatal(err)
	}
	eps, err := srv.store.ListEpisodes(t.Context(), podcast.ID)
	if err != nil || len(eps) != 1 {
		t.Fatalf("单集 setup: %v", err)
	}
	seedHighlightAndNarration(t, srv, eps[0].ID)
	rec := doWithCookie(srv, session, http.MethodGet, "/sources/episode/"+eps[0].ID+"/dj")
	if rec.Code != http.StatusOK {
		t.Fatalf("DJ 页应 200: %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"dj-prev", "dj-next", "dj-pause", // 上一段/下一段/暂停
		"dj-rate",                        // 速度选择
		"dj-full",                        // 继续听原节目
		"dj-progress",                    // 总进度显示
		"aria-label=\"播放速度\"",            // 控件标签
		"/static/playback-controller.js", // 共用键盘控制
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("DJ 页缺少 %q", want)
		}
	}
	playerSrc, err := os.ReadFile("static/dj-player.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(playerSrc)
	for _, want := range []string{"prev: function", "setRate: function", "playFullProgram: function", "playbackRate"} {
		if !strings.Contains(js, want) {
			t.Fatalf("dj-player.js 缺少 %q", want)
		}
	}
	// app.css 触控目标样式
	css, err := os.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(css), "min-height: 44px") {
		t.Fatal("app.css 应包含触控目标最小尺寸")
	}
}

// TestDJHarnessFixtureServedFromTestDataOnly D05：浏览器夹具仅存在于 testdata，
// 由测试路由注入真实播放器源码后提供；生产路由不包含夹具。
func TestDJHarnessFixtureServedFromTestDataOnly(t *testing.T) {
	playerSrc, err := os.ReadFile("static/dj-player.js")
	if err != nil {
		t.Fatalf("读取 dj-player.js: %v", err)
	}
	harness, err := os.ReadFile("testdata/dj-harness.html")
	if err != nil {
		t.Fatalf("读取夹具: %v", err)
	}
	page := strings.Replace(string(harness), "<!--DJ_PLAYER_SOURCE-->", "<script>"+string(playerSrc)+"</script>", 1)

	// 测试专用路由（生产 routes 不注册夹具）。
	mux := http.NewServeMux()
	mux.HandleFunc("/harness", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(page))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/harness")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	buf := make([]byte, len(page))
	n := 0
	for {
		m, rerr := resp.Body.Read(buf[n:])
		n += m
		if rerr != nil || n >= len(buf) {
			break
		}
	}
	served := string(buf[:n])
	for _, want := range []string{
		"createDJPlayer",             // 真实播放器源码已注入
		"FakeAudio",                  // 媒体替身
		"bufferingDoesNotConsume",    // 场景：缓冲不消耗片段时间
		"pauseDoesNotSkip",           // 场景：暂停不跳下一段
		"stopKillsGhostContinuation", // 场景：停止后无幽灵续播
		"fastSwitchNoDoubleAudio",    // 场景：快速切换不双声叠播
		"dj-harness-result",          // 结果容器
	} {
		if !strings.Contains(served, want) {
			t.Fatalf("夹具缺少 %q", want)
		}
	}
	// 生产路由不含夹具：主路由器上不存在 /harness。
	srv := newTestServer(t)
	session := claimOwnerAndLogin(t, srv, "djharness@example.com", "password123")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/harness", nil)
	req.AddCookie(session)
	srv.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("生产路由不应提供夹具: %d", rec.Code)
	}
	_ = session
}

// TestDJHarnessScenariosViaNode 若环境提供 node，则用 DOM 桩实际执行夹具场景，
// 验证状态机契约（缓冲不消耗时间/暂停不跳段/停止无幽灵续播/快速切换互斥）。
// 无 node 的 CI 环境自动跳过（人工浏览器执行同一夹具文件）。
func TestDJHarnessScenariosViaNode(t *testing.T) {
	nodePath, err := exec.LookPath("node")
	if err != nil {
		t.Skip("环境无 node，跳过（夹具可人工在浏览器执行）")
	}
	playerSrc, err := os.ReadFile("static/dj-player.js")
	if err != nil {
		t.Fatal(err)
	}
	harness, err := os.ReadFile("testdata/dj-harness.html")
	if err != nil {
		t.Fatal(err)
	}
	page := strings.Replace(string(harness), "<!--DJ_PLAYER_SOURCE-->", "<script>"+string(playerSrc)+"</script>", 1)

	// 抽取全部内联 <script> 体（FakeAudio 桩 + 播放器 + 场景）。
	var scripts []string
	for {
		start := strings.Index(page, "<script>")
		if start < 0 {
			break
		}
		end := strings.Index(page[start:], "</script>")
		if end < 0 {
			break
		}
		scripts = append(scripts, page[start+len("<script>"):start+end])
		page = page[:start] + page[start+end+len("</script>"):]
	}
	if len(scripts) < 3 {
		t.Fatalf("夹具应包含桩/播放器/场景三段脚本: %d", len(scripts))
	}

	runner := strings.Join([]string{
		"global.window = global;",
		"global.document = { getElementById: function () { return { textContent: '' }; }, querySelectorAll: function () { return []; }, title: '' };",
		strings.Join(scripts, "\n"),
		"(async function(){ for (var i=0;i<scenarios.length;i++){ await scenarios[i](); await wait(5); }",
		"  console.log('PASS ' + passCount + '/' + totalCount);",
		"  if (passCount !== totalCount) process.exit(1); })();",
	}, "\n")
	tmp := filepath.Join(t.TempDir(), "dj-harness-run.js")
	if err := os.WriteFile(tmp, []byte(runner), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(nodePath, tmp).CombinedOutput()
	if err != nil {
		t.Fatalf("夹具场景执行失败: %v\n%s", err, out)
	}
	// 夹具自带 run 循环与外层执行器各跑一遍：只断言全部通过（PASS a/b 且 a==b，
	// 由执行器在不等时以非零退出保证）。
	if !strings.Contains(string(out), "PASS") {
		t.Fatalf("夹具应输出 PASS 统计: %s", out)
	}
}

// TestDJBriefCaptureButtons D08：DJ 页提供收藏/理解按钮（复用 /api/pin 与
// /api/owner-notes，不建独立 DJ 笔记体系）；个人理解以 OwnerReflection 身份保存，
// 不会变成原文证据；重复收藏不产生重复行（TogglePin 按 segment_ids 幂等）。
func TestDJBriefCaptureButtons(t *testing.T) {
	srv := newTestServer(t)
	session := claimOwnerAndLogin(t, srv, "djcapture@example.com", "password123")
	podcast, err := srv.store.CreatePodcast(t.Context(), "https://feed.example.com/djcap.xml", "DJ收藏播客", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.store.MergeEpisodes(t.Context(), podcast.ID, []models.Episode{{GUID: "djcap-1", Title: "收藏单集", AudioURL: "https://a.mp3"}}); err != nil {
		t.Fatal(err)
	}
	eps, _ := srv.store.ListEpisodes(t.Context(), podcast.ID)
	seedHighlightAndNarration(t, srv, eps[0].ID)

	rec := doWithCookie(srv, session, http.MethodGet, "/sources/episode/"+eps[0].ID+"/dj")
	if rec.Code != http.StatusOK {
		t.Fatalf("DJ 页应 200: %d", rec.Code)
	}
	body := rec.Body.String() + doWithCookie(srv, session, http.MethodGet, "/static/dj-view.js").Body.String() + doWithCookie(srv, session, http.MethodGet, "/static/listening-session.js").Body.String()
	for _, want := range []string{"dj-pin", "dj-note", "/api/pin", "/api/owner-notes", "owner_reflection", "data-segments"} {
		if !strings.Contains(body, want) {
			t.Fatalf("DJ 页缺少 %q", want)
		}
	}
	// 记录理解 → OwnerReflection 落库，出现在单集学习页（个人身份，非原文证据）。
	rec0 := doWithCookie(srv, session, http.MethodGet, "/dashboard")
	csrf := ""
	for _, c := range rec0.Result().Cookies() {
		if c.Name == "cwp_csrf" {
			csrf = c.Value
		}
	}
	form := url.Values{
		"_csrf":           {csrf},
		"source_type":     {"episode"},
		"source_id":       {eps[0].ID},
		"kind":            {"owner_reflection"},
		"content":         {"我的理解：这段在讲样本选择偏差"},
		"references_json": {`["seg-0001"]`},
	}
	req := httptest.NewRequest(http.MethodPost, "/api/owner-notes", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-CSRF-Token", csrf)
	req.AddCookie(session)
	req.AddCookie(&http.Cookie{Name: "cwp_csrf", Value: csrf})
	rec = httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("理解应保存 303: %d %s", rec.Code, rec.Body.String())
	}
	page := doWithCookie(srv, session, http.MethodGet, "/sources/episode/"+eps[0].ID)
	pageBody := page.Body.String()
	if !strings.Contains(pageBody, "我的理解：这段在讲样本选择偏差") || !strings.Contains(pageBody, "owner_reflection") {
		t.Fatalf("单集页应显示个人理解（OwnerReflection 身份）")
	}
	// 收藏幂等：同 segment_ids 两次 TogglePin → 1 行再 0 行（不重复）。
	if _, err := srv.store.TogglePin(t.Context(), models.SourceEpisode, eps[0].ID, `["seg-0001"]`, 0, 5, "DJ 精听"); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = srv.store.DB.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM pins WHERE source_id=? AND segment_ids='["seg-0001"]'`, eps[0].ID).Scan(&n)
	if n != 1 {
		t.Fatalf("首次收藏应有且仅有 1 行: %d", n)
	}
	if _, err := srv.store.TogglePin(t.Context(), models.SourceEpisode, eps[0].ID, `["seg-0001"]`, 0, 5, "DJ 精听"); err != nil {
		t.Fatal(err)
	}
	_ = srv.store.DB.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM pins WHERE source_id=? AND segment_ids='["seg-0001"]'`, eps[0].ID).Scan(&n)
	if n != 0 {
		t.Fatalf("取消收藏应删除行: %d", n)
	}
}
