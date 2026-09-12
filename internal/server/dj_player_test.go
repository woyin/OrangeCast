package server

import (
	"net/http"
	"net/http/httptest"
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
