package server

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/woyin/orangecast/internal/config"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/queue"
	"github.com/woyin/orangecast/internal/rss"
	"github.com/woyin/orangecast/internal/store"
)

// newTestServer 构造一个完整装配的 server，用临时 SQLite + 假 API key。
func newTestServer(t *testing.T) *Server {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(dir + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	cfg := &config.Config{
		Port: "0", DBPath: dir + "/test.db",
		SessionSecret: "test-secret-abcdef", TempDir: dir,
		GroqAPIKey: "fake-groq-key", OpenAIAPIKey: "fake-openai-key",
		PublicURL: "http://localhost",
		DataDir:   dir, EvidenceDir: dir + "/evidence", BackupDir: dir + "/backups",
	}
	selector := provider.NewSelector(cfg.GroqAPIKey, cfg.OpenAIAPIKey)
	worker := queue.NewWorker(s, selector, cfg.TempDir, dir+"/evidence", dir+"/narrations")
	refresher := rss.NewRefresher(s)
	srv, err := New(cfg, s, worker, refresher, selector)
	if err != nil {
		t.Fatal(err)
	}
	return srv
}

// claimOwnerAndLogin 认领 Owner 并返回带 session + CSRF cookie 的 jar。
// 先 GET /register 获取 CSRF cookie，再 POST 认领。
func claimOwnerAndLogin(t *testing.T, srv *Server, email, pw string) *http.Cookie {
	t.Helper()
	router := srv.Router()

	// GET /register 拿 CSRF cookie
	req := httptest.NewRequest(http.MethodGet, "/register", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	csrf := ""
	for _, c := range rec.Result().Cookies() {
		if c.Name == "cwp_csrf" {
			csrf = c.Value
		}
	}
	if csrf == "" {
		t.Fatal("GET /register 未设置 CSRF cookie")
	}

	// POST /register 认领
	req2 := httptest.NewRequest(http.MethodPost, "/register",
		strings.NewReader("_csrf="+csrf+"&email="+email+"&password="+pw))
	req2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req2.AddCookie(&http.Cookie{Name: "cwp_csrf", Value: csrf})
	rec2 := httptest.NewRecorder()
	router.ServeHTTP(rec2, req2)
	for _, c := range rec2.Result().Cookies() {
		if c.Name == "cwp_session" {
			return c
		}
	}
	t.Fatal("认领后未拿到 session cookie")
	return nil
}

func doWithCookie(srv *Server, cookie *http.Cookie, method, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	return rec
}

func TestUnauth_RedirectsToLogin(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Errorf("未登录应 303，实际 %d", rec.Code)
	}
	if !strings.HasPrefix(rec.Header().Get("Location"), "/login") {
		t.Errorf("应重定向到 /login，实际 %s", rec.Header().Get("Location"))
	}
}

func TestAuth_ClaimThenAccessDashboard(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "test@example.com", "password123")
	rec := doWithCookie(srv, cookie, http.MethodGet, "/dashboard")
	if rec.Code != http.StatusOK {
		t.Errorf("登录后 dashboard 应 200，实际 %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "仪表盘") {
		t.Error("dashboard 应含'仪表盘'")
	}
}

func TestRegister_FirstVisitRendersUsableCSRFToken(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/register", nil)
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)

	var csrf string
	for _, c := range rec.Result().Cookies() {
		if c.Name == "cwp_csrf" {
			csrf = c.Value
		}
	}
	if csrf == "" {
		t.Fatal("首次 GET /register 未设置 CSRF cookie")
	}
	if !strings.Contains(rec.Body.String(), `name="_csrf" value="`+csrf+`"`) {
		t.Fatal("首次 GET /register 必须渲染与 CSRF cookie 相同的 token")
	}

	post := httptest.NewRequest(http.MethodPost, "/register",
		strings.NewReader("_csrf="+csrf+"&email=first@example.com&password=password123"))
	post.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	post.AddCookie(&http.Cookie{Name: "cwp_csrf", Value: csrf})
	postRec := httptest.NewRecorder()
	srv.Router().ServeHTTP(postRec, post)
	if postRec.Code != http.StatusSeeOther {
		t.Errorf("首次认领无需刷新应成功并重定向，实际 %d", postRec.Code)
	}
}

func TestRegister_SecondClaimRejected(t *testing.T) {
	srv := newTestServer(t)
	claimOwnerAndLogin(t, srv, "a@example.com", "password123")

	// 实例已认领：GET /register 应重定向到 /login
	rec := doWithCookie(srv, nil, http.MethodGet, "/register")
	if rec.Code != http.StatusSeeOther || !strings.HasPrefix(rec.Header().Get("Location"), "/login") {
		t.Errorf("已认领后 GET /register 应重定向到 /login，实际 %d %s", rec.Code, rec.Header().Get("Location"))
	}

	// 直接 POST 第二次认领也应被拒绝（新 CSRF 会话）
	req := httptest.NewRequest(http.MethodGet, "/register", nil)
	rec0 := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec0, req)
	csrf := ""
	for _, c := range rec0.Result().Cookies() {
		if c.Name == "cwp_csrf" {
			csrf = c.Value
		}
	}
	req2 := httptest.NewRequest(http.MethodPost, "/register",
		strings.NewReader("_csrf="+csrf+"&email=b@example.com&password=password123"))
	req2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req2.AddCookie(&http.Cookie{Name: "cwp_csrf", Value: csrf})
	rec2 := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec2, req2)
	// 应被拒绝（403 CSRF 或 302 登录）。无论哪种，都不能创建第二个 Owner。
	n, _ := store.CountUsers(req2.Context(), srv.store.DB)
	if n != 1 {
		t.Errorf("第二次认领后用户数应为 1，实际 %d", n)
	}
}

func TestCSRF_RejectsForgedPOST(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "c@example.com", "password123")

	// 无 CSRF token 的 POST → 403（跨站写请求失败）
	req := httptest.NewRequest(http.MethodPost, "/settings", strings.NewReader("transcription_model=x"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("无 CSRF token 的 POST 应 403，实际 %d", rec.Code)
	}
}

func TestSettings_DefaultGroq(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "s@example.com", "password123")
	rec := doWithCookie(srv, cookie, http.MethodGet, "/settings")
	if rec.Code != http.StatusOK {
		t.Fatalf("settings 应 200，实际 %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Groq") {
		t.Error("settings 应显示 Groq 默认 Provider")
	}
	// 不应再有 provider 切换表单
	if strings.Contains(rec.Body.String(), "active_provider") {
		t.Error("settings 不应再有全局 provider 切换")
	}
}

func TestSourceDetail_NotFound(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "n@example.com", "password123")
	rec := doWithCookie(srv, cookie, http.MethodGet, "/sources/episode/nonexistent")
	if rec.Code != http.StatusNotFound {
		t.Errorf("不存在的 source 应 404，实际 %d", rec.Code)
	}
}

func TestLogout_ClearsSession(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "lo@example.com", "password123")
	rec := doWithCookie(srv, cookie, http.MethodGet, "/dashboard")
	if rec.Code != http.StatusOK {
		t.Fatal("登录后应能访问")
	}
	rec2 := doWithCookie(srv, cookie, http.MethodGet, "/logout")
	if rec2.Code != http.StatusSeeOther {
		t.Fatalf("登出应重定向，实际 %d", rec2.Code)
	}
	rec3 := doWithCookie(srv, cookie, http.MethodGet, "/dashboard")
	if rec3.Code != http.StatusSeeOther {
		t.Errorf("登出后 dashboard 应重定向到登录，实际 %d", rec3.Code)
	}
}

func TestAPINotAuth_Returns401(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/qa", nil)
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("/api/ 未登录应 401，实际 %d", rec.Code)
	}
}

func TestRegister_WeakPasswordRejected(t *testing.T) {
	srv := newTestServer(t)
	// 先取 CSRF
	req0 := httptest.NewRequest(http.MethodGet, "/register", nil)
	rec0 := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec0, req0)
	csrf := ""
	for _, c := range rec0.Result().Cookies() {
		if c.Name == "cwp_csrf" {
			csrf = c.Value
		}
	}
	req := httptest.NewRequest(http.MethodPost, "/register",
		strings.NewReader("_csrf="+csrf+"&email=x@example.com&password=123"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "cwp_csrf", Value: csrf})
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), "至少 8 位") {
		t.Error("短密码应被拒绝并提示")
	}
}

func TestSourceDetailRender_FailedStatus(t *testing.T) {
	tmpl, err := NewTemplates()
	if err != nil {
		t.Fatal("NewTemplates:", err)
	}
	var buf bytes.Buffer
	err = tmpl.Render(&buf, "source_detail.html", map[string]any{
		"Title": "T", "Status": "failed", "SourceType": "upload", "SourceID": "x",
	})
	if err != nil {
		t.Fatal("Render:", err)
	}
	if !bytes.Contains(buf.Bytes(), []byte("status-bar")) {
		t.Errorf("failed 状态应渲染 status-bar，输出: %q", buf.String()[:min(200, buf.Len())])
	}
}

func TestLogin_RateLimited(t *testing.T) {
	srv := newTestServer(t)
	claimOwnerAndLogin(t, srv, "rl@example.com", "password123")

	// 用同一 IP 连续错误登录超过阈值 → 429
	var lastCode int
	for i := 0; i < 25; i++ {
		req0 := httptest.NewRequest(http.MethodGet, "/login", nil)
		rec0 := httptest.NewRecorder()
		srv.Router().ServeHTTP(rec0, req0)
		csrf := ""
		for _, c := range rec0.Result().Cookies() {
			if c.Name == "cwp_csrf" {
				csrf = c.Value
			}
		}
		req := httptest.NewRequest(http.MethodPost, "/login",
			strings.NewReader("_csrf="+csrf+"&email=rl@example.com&password=wrong"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: "cwp_csrf", Value: csrf})
		rec := httptest.NewRecorder()
		srv.Router().ServeHTTP(rec, req)
		lastCode = rec.Code
	}
	if lastCode != http.StatusTooManyRequests {
		t.Errorf("超过登录尝试阈值应 429，实际 %d", lastCode)
	}
}

func TestQAResponse_RefusesWithoutCitations(t *testing.T) {
	// 模型有答案但未引用任何片段 → 拒答（Phase 7）
	code, body := evidenceQAResultToResponse(&provider.QAResult{Answer: "好像是这样的", Sources: nil})
	if code != http.StatusUnprocessableEntity {
		t.Errorf("无引用应 422 拒答，实际 %d", code)
	}
	if body["error"] == nil || body["answer"] != "" {
		t.Errorf("拒答响应应含 error 且 answer 为空: %+v", body)
	}
}

func TestQAResponse_RefusesEmptyAnswer(t *testing.T) {
	code, body := evidenceQAResultToResponse(&provider.QAResult{Answer: "", Sources: []provider.Source{{SegmentID: "seg-0001"}}})
	if code != http.StatusUnprocessableEntity {
		t.Errorf("空答案应拒答，实际 %d", code)
	}
	_ = body
}

func TestQAResponse_AllowsCitedAnswer(t *testing.T) {
	res := &provider.QAResult{
		Answer:  "有效回答",
		Sources: []provider.Source{{SegmentID: "seg-0001", Content: "原文", Start: 0, End: 1}},
	}
	code, body := evidenceQAResultToResponse(res)
	if code != http.StatusOK {
		t.Errorf("有答案且有引用应 200，实际 %d", code)
	}
	if body["answer"] != "有效回答" {
		t.Errorf("答案未透传: %+v", body)
	}
}

func TestVersionsPage_RendersAndRevert(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "v@example.com", "password123")
	ctx := context.Background()

	// 造数据：episode + 2 个转录版本 + 2 个卡片版本
	p, err := srv.store.CreatePodcast(ctx, "https://feed.xml", "Pod", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.store.MergeEpisodes(ctx, p.ID, []models.Episode{{GUID: "g1", Title: "Ep", AudioURL: "https://a.mp3"}}); err != nil {
		t.Fatal(err)
	}
	eps, _ := srv.store.ListEpisodes(ctx, p.ID)
	sourceID := eps[0].ID
	job, _ := srv.store.EnqueueJob(ctx, models.SourceEpisode, sourceID, models.JobTranscribe)
	tv1, _ := srv.store.CreateArtifactVersion(ctx, models.SourceEpisode, sourceID, store.KindTranscript, "groq", "m1", "1", job.ID, `{"v":1}`)
	srv.store.CreateArtifactVersion(ctx, models.SourceEpisode, sourceID, store.KindTranscript, "groq", "m2", "2", job.ID, `{"v":2}`)
	cv1, _ := srv.store.CreateArtifactVersion(ctx, models.SourceEpisode, sourceID, store.KindKnowledgeCard, "groq", "m1", "1", job.ID, `{"c":1}`)
	srv.store.CreateArtifactVersion(ctx, models.SourceEpisode, sourceID, store.KindKnowledgeCard, "groq", "m2", "2", job.ID, `{"c":2}`)
	srv.store.SetCurrentVersion(ctx, models.SourceEpisode, sourceID, store.KindTranscript, tv1)
	srv.store.SetCurrentVersion(ctx, models.SourceEpisode, sourceID, store.KindKnowledgeCard, cv1)

	// 版本页渲染
	rec := doWithCookie(srv, cookie, http.MethodGet, "/sources/episode/"+sourceID+"/versions")
	if rec.Code != http.StatusOK {
		t.Fatalf("版本页应 200，实际 %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "v2") || !strings.Contains(body, "v1") {
		t.Errorf("版本页应列出 v1/v2，实际: %s", body)
	}

	// 回退卡片到 v2（需要 CSRF）
	req := httptest.NewRequest(http.MethodGet, "/sources/episode/"+sourceID+"/versions", nil)
	req.AddCookie(cookie)
	recCSRF := httptest.NewRecorder()
	srv.Router().ServeHTTP(recCSRF, req)
	csrf := ""
	for _, c := range recCSRF.Result().Cookies() {
		if c.Name == "cwp_csrf" {
			csrf = c.Value
		}
	}
	req2 := httptest.NewRequest(http.MethodPost, "/sources/episode/"+sourceID+"/versions/revert",
		strings.NewReader("_csrf="+csrf+"&kind=knowledge_card&version=2"))
	req2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req2.AddCookie(cookie)
	req2.AddCookie(&http.Cookie{Name: "cwp_csrf", Value: csrf})
	rec2 := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusSeeOther {
		t.Fatalf("回退应重定向，实际 %d", rec2.Code)
	}
	cur, err := srv.store.GetCurrentVersion(ctx, models.SourceEpisode, sourceID, store.KindKnowledgeCard)
	if err != nil {
		t.Fatal(err)
	}
	if cur.Version != 2 {
		t.Errorf("回退后卡片当前版本应为 2，实际 %d", cur.Version)
	}
}

func TestProcessBatch_EnqueuesMultipleAndRedirects(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "batch@example.com", "password123")
	ctx := context.Background()

	// 造 3 集（2 个 unprocessed + 1 个 processed）
	p, _ := srv.store.CreatePodcast(ctx, "https://feed.xml", "Pod", "", "")
	srv.store.MergeEpisodes(ctx, p.ID, []models.Episode{
		{GUID: "g1", Title: "Ep1", AudioURL: "https://a.mp3"},
		{GUID: "g2", Title: "Ep2", AudioURL: "https://b.mp3"},
		{GUID: "g3", Title: "Ep3", AudioURL: "https://c.mp3"},
	})
	eps, _ := srv.store.ListEpisodes(ctx, p.ID)
	// 把第 3 集标记为 processed（不应出现在可选中）
	srv.store.UpdateEpisodeStatus(ctx, eps[2].ID, models.StatusProcessed)

	// 构造 CSRF
	req0 := httptest.NewRequest(http.MethodGet, "/podcasts/"+p.ID, nil)
	req0.AddCookie(cookie)
	rec0 := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec0, req0)
	csrf := ""
	for _, c := range rec0.Result().Cookies() {
		if c.Name == "cwp_csrf" {
			csrf = c.Value
		}
	}

	// 批量入队 2 集
	body := "_csrf=" + csrf + "&source_type=episode&podcast_id=" + p.ID +
		"&source_id=" + eps[0].ID + "&source_id=" + eps[1].ID
	req := httptest.NewRequest(http.MethodPost, "/api/process-batch", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	req.AddCookie(&http.Cookie{Name: "cwp_csrf", Value: csrf})
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("应重定向，实际 %d", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if !strings.Contains(loc, "enqueued=2") || !strings.Contains(loc, "skipped=0") {
		t.Errorf("重定向 URL 应含 enqueued=2&skipped=0，实际 %s", loc)
	}

	// 验证 2 个 job 入队
	jobs, _ := srv.store.ListQueuedOrRunning(ctx)
	if len(jobs) != 2 {
		t.Errorf("应入队 2 个 job，实际 %d", len(jobs))
	}
}

func TestProcessBatch_SkipsAlreadyQueued(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "batch2@example.com", "password123")
	ctx := context.Background()

	p, _ := srv.store.CreatePodcast(ctx, "https://feed.xml", "Pod", "", "")
	srv.store.MergeEpisodes(ctx, p.ID, []models.Episode{
		{GUID: "g1", Title: "Ep1", AudioURL: "https://a.mp3"},
	})
	eps, _ := srv.store.ListEpisodes(ctx, p.ID)
	// 先入队一次
	srv.store.EnqueueJob(ctx, models.SourceEpisode, eps[0].ID, models.JobTranscribe)

	// CSRF
	req0 := httptest.NewRequest(http.MethodGet, "/podcasts/"+p.ID, nil)
	req0.AddCookie(cookie)
	rec0 := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec0, req0)
	csrf := ""
	for _, c := range rec0.Result().Cookies() {
		if c.Name == "cwp_csrf" {
			csrf = c.Value
		}
	}

	// 再次批量入队同一集 → 应 skipped=1
	body := "_csrf=" + csrf + "&source_type=episode&podcast_id=" + p.ID + "&source_id=" + eps[0].ID
	req := httptest.NewRequest(http.MethodPost, "/api/process-batch", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	req.AddCookie(&http.Cookie{Name: "cwp_csrf", Value: csrf})
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)

	loc := rec.Header().Get("Location")
	if !strings.Contains(loc, "enqueued=0") || !strings.Contains(loc, "skipped=1") {
		t.Errorf("应 enqueued=0&skipped=1（已 queued），实际 %s", loc)
	}
}

// TestPageParam 验证 pageParam 的解析：缺省/非法回退 1，合法保留。
func TestPageParam(t *testing.T) {
	cases := []struct {
		query string
		want  int
	}{
		{"", 1},           // 缺省
		{"page=0", 1},     // 非法（≤0）
		{"page=abc", 1},   // 非法（非数字）
		{"page=3", 3},     // 合法
		{"page=2&x=1", 2}, // 与其他参数共存
	}
	for _, c := range cases {
		req := httptest.NewRequest(http.MethodGet, "/?"+c.query, nil)
		if got := pageParam(req); got != c.want {
			t.Errorf("pageParam(%q) = %d, want %d", c.query, got, c.want)
		}
	}
}

// TestTotalPages 验证 totalPages 向上取整分页。
func TestTotalPages(t *testing.T) {
	if got := totalPages(0, 10); got != 0 {
		t.Errorf("totalPages(0,10) = %d, want 0", got)
	}
	if got := totalPages(1, 10); got != 1 {
		t.Errorf("totalPages(1,10) = %d, want 1", got)
	}
	if got := totalPages(10, 10); got != 1 {
		t.Errorf("totalPages(10,10) = %d, want 1", got)
	}
	if got := totalPages(11, 10); got != 2 {
		t.Errorf("totalPages(11,10) = %d, want 2", got)
	}
	if got := totalPages(5, 0); got != 0 {
		t.Errorf("totalPages(5,0) = %d, want 0", got)
	}
}

// TestProcess_EnqueuesSingleAndRedirects 验证单集入队并重定向到 source 详情。
func TestProcess_EnqueuesSingleAndRedirects(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "proc@example.com", "password123")
	ctx := context.Background()

	p, _ := srv.store.CreatePodcast(ctx, "https://feed.xml", "Pod", "", "")
	srv.store.MergeEpisodes(ctx, p.ID, []models.Episode{{GUID: "g1", Title: "Ep", AudioURL: "https://a.mp3"}})
	eps, _ := srv.store.ListEpisodes(ctx, p.ID)

	// GET 拿 CSRF
	req0 := httptest.NewRequest(http.MethodGet, "/uploads", nil)
	req0.AddCookie(cookie)
	rec0 := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec0, req0)
	csrf := ""
	for _, c := range rec0.Result().Cookies() {
		if c.Name == "cwp_csrf" {
			csrf = c.Value
		}
	}

	body := "_csrf=" + csrf + "&source_type=episode&source_id=" + eps[0].ID
	req := httptest.NewRequest(http.MethodPost, "/api/process", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	req.AddCookie(&http.Cookie{Name: "cwp_csrf", Value: csrf})
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("应 303 重定向，实际 %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); !strings.Contains(loc, "/sources/episode/"+eps[0].ID) {
		t.Errorf("重定向应指向 source 详情，实际 %q", rec.Header().Get("Location"))
	}
	jobs, _ := srv.store.ListQueuedOrRunning(ctx)
	if len(jobs) != 1 {
		t.Errorf("应入队 1 个 job，实际 %d", len(jobs))
	}
}

// TestPodcastsList_Renders 验证播客列表页在认证后正常渲染。
func TestPodcastsList_Renders(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "podlist@example.com", "password123")
	ctx := context.Background()

	srv.store.CreatePodcast(ctx, "https://feed.xml", "甲播客", "desc", "")
	req := httptest.NewRequest(http.MethodGet, "/podcasts", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("播客列表应 200，实际 %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "甲播客") {
		t.Errorf("页面应含播客标题，实际 %s", rec.Body.String())
	}
}

// TestUploads_Renders 验证上传列表页在认证后正常渲染（含上传条目）。
func TestUploads_Renders(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "uplist@example.com", "password123")
	ctx := context.Background()

	srv.store.CreateUpload(ctx, "音轨.mp3", "audio/mpeg", 1024)
	req := httptest.NewRequest(http.MethodGet, "/uploads", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("上传列表应 200，实际 %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "音轨.mp3") {
		t.Errorf("页面应含上传文件名，实际 %s", rec.Body.String())
	}
}

// TestUploadNew_GET_Renders 验证上传新文件页 GET 渲染表单。
func TestUploadNew_GET_Renders(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "upnew@example.com", "password123")

	req := httptest.NewRequest(http.MethodGet, "/uploads/new", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("上传新文件页应 200，实际 %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "audio") {
		t.Errorf("页面应含文件上传控件，实际 %s", rec.Body.String())
	}
}

// TestSettings_GET_Renders 验证设置页 GET 渲染（默认 Groq Provider）。
func TestSettings_GET_Renders(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "settings@example.com", "password123")

	req := httptest.NewRequest(http.MethodGet, "/settings", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("设置页应 200，实际 %d", rec.Code)
	}
	// 默认渲染应含 Provider 选择字段（groq）
	if !strings.Contains(rec.Body.String(), "groq") {
		t.Errorf("设置页应含默认 groq provider，实际 %s", rec.Body.String())
	}
}

// TestProgressAPI_ReturnsJSON 验证进度 API 返回 active/queued/recent JSON。
func TestProgressAPI_ReturnsJSON(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "progapi@example.com", "password123")
	ctx := context.Background()

	p, _ := srv.store.CreatePodcast(ctx, "https://feed.xml", "Pod", "", "")
	srv.store.MergeEpisodes(ctx, p.ID, []models.Episode{{GUID: "g1", Title: "Ep", AudioURL: "https://a.mp3"}})
	eps, _ := srv.store.ListEpisodes(ctx, p.ID)
	job, _ := srv.store.EnqueueJob(ctx, models.SourceEpisode, eps[0].ID, models.JobTranscribe)
	srv.store.MarkJobRunning(ctx, job.ID) // 让其为 active

	req := httptest.NewRequest(http.MethodGet, "/api/progress", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("进度 API 应 200，实际 %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"active"`) {
		t.Errorf("应含 active 字段，实际 %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), eps[0].ID) {
		t.Errorf("应含 source_id，实际 %s", rec.Body.String())
	}
}

// TestProgress_Page_Renders 验证进度页渲染（含队列标题）。
func TestProgress_Page_Renders(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "progpage@example.com", "password123")
	ctx := context.Background()

	p, _ := srv.store.CreatePodcast(ctx, "https://feed.xml", "Pod", "", "")
	srv.store.MergeEpisodes(ctx, p.ID, []models.Episode{{GUID: "g1", Title: "进度测试集", AudioURL: "https://a.mp3"}})
	eps, _ := srv.store.ListEpisodes(ctx, p.ID)
	srv.store.EnqueueJob(ctx, models.SourceEpisode, eps[0].ID, models.JobTranscribe)

	req := httptest.NewRequest(http.MethodGet, "/progress", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("进度页应 200，实际 %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "进度测试集") {
		t.Errorf("页面应含队列中的集标题，实际 %s", rec.Body.String())
	}
}

// TestKeyPointsSearch 验证 KeyPoint 搜索接口：空查询返回空、命中返回结果。
func TestKeyPointsSearch(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "kpsearch@example.com", "password123")
	ctx := context.Background()

	p, _ := srv.store.CreatePodcast(ctx, "https://feed.xml", "Pod", "", "")
	srv.store.MergeEpisodes(ctx, p.ID, []models.Episode{{GUID: "g1", Title: "Ep", AudioURL: "https://a.mp3"}})
	eps, _ := srv.store.ListEpisodes(ctx, p.ID)
	card := &provider.KnowledgeCard{
		Title:     "T",
		Summary:   provider.CitedText{Text: "S", Citations: []string{"seg-0001"}},
		KeyPoints: []provider.KeyPoint{{Content: "sovereign wealth funds change global investment", Description: "long-term investors", Citations: []string{"seg-0001"}}},
	}
	segs := []provider.Segment{{ID: "seg-0001", Start: 0, End: 5, Text: "sovereign wealth"}}
	if err := srv.store.IndexKeyPoints(ctx, models.SourceEpisode, eps[0].ID, "ep1", 1, card, segs); err != nil {
		t.Fatalf("IndexKeyPoints: %v", err)
	}

	// 空查询 → 空 results
	req := httptest.NewRequest(http.MethodGet, "/api/keypoints/search?q=", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("空查询应 200，实际 %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"results":[]`) {
		t.Errorf("空查询应返回空 results，实际 %s", rec.Body.String())
	}

	// 命中查询
	req = httptest.NewRequest(http.MethodGet, "/api/keypoints/search?q=wealth", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("搜索应 200，实际 %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "sovereign wealth funds") {
		t.Errorf("应命中 KeyPoint，实际 %s", rec.Body.String())
	}
}

// TestKeyPointsPage_Renders 验证 KeyPoint 全局页渲染（含 KeyPoint 内容）。
func TestKeyPointsPage_Renders(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "kppage@example.com", "password123")
	ctx := context.Background()

	p, _ := srv.store.CreatePodcast(ctx, "https://feed.xml", "Pod", "", "")
	srv.store.MergeEpisodes(ctx, p.ID, []models.Episode{{GUID: "g1", Title: "Ep", AudioURL: "https://a.mp3"}})
	eps, _ := srv.store.ListEpisodes(ctx, p.ID)
	card := &provider.KnowledgeCard{
		Title:     "T",
		Summary:   provider.CitedText{Text: "S", Citations: []string{"seg-0001"}},
		KeyPoints: []provider.KeyPoint{{Content: "global investment insight", Citations: []string{"seg-0001"}}},
	}
	segs := []provider.Segment{{ID: "seg-0001", Start: 0, End: 5, Text: "x"}}
	srv.store.IndexKeyPoints(ctx, models.SourceEpisode, eps[0].ID, "ep1", 1, card, segs)

	req := httptest.NewRequest(http.MethodGet, "/keypoints", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("KeyPoint 页应 200，实际 %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "global investment insight") {
		t.Errorf("页面应含 KeyPoint 内容，实际 %s", rec.Body.String())
	}
}

// TestGraphAPI 验证图谱 JSON 接口返回结构（nodes/links/collections）。
func TestGraphAPI(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "graphapi@example.com", "password123")

	req := httptest.NewRequest(http.MethodGet, "/api/graph", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("图谱 API 应 200，实际 %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"nodes"`) || !strings.Contains(body, `"links"`) || !strings.Contains(body, `"collections"`) {
		t.Errorf("应含 nodes/links/collections，实际 %s", body)
	}
}

// TestGraphPage_Renders 验证知识图谱页渲染。
func TestGraphPage_Renders(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "graphpage@example.com", "password123")

	req := httptest.NewRequest(http.MethodGet, "/graph", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("图谱页应 200，实际 %d", rec.Code)
	}
}

// TestAnnotation_SaveAndDelete 验证标注 API：保存返回 action=saved，空 body 删除。
func TestAnnotation_SaveAndDelete(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "annot@example.com", "password123")

	// 空 body → 删除
	rec := postForm(t, srv, cookie, "/api/annotation",
		"source_type=episode&source_id=ep-1&segment_ids=seg-0001&body=")
	if !strings.Contains(rec.Body.String(), `"action":"deleted"`) {
		t.Errorf("空 body 应删除，实际 %s", rec.Body.String())
	}

	// 非空 body → 保存
	rec = postForm(t, srv, cookie, "/api/annotation",
		"source_type=episode&source_id=ep-1&segment_ids=seg-0001&body=重要+标注&time_start=0&time_end=5")
	if rec.Code != http.StatusOK {
		t.Fatalf("保存标注应 200，实际 %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"action":"saved"`) {
		t.Errorf("非空 body 应保存，实际 %s", rec.Body.String())
	}
}

// TestPin_Toggle 验证 Pin API：首次 pin 返回 pinned=true，再次 toggle 返回 false。
func TestPin_Toggle(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "pin@example.com", "password123")

	body := "source_type=episode&source_id=ep-1&segment_ids=seg-0001&source_title=Ep&time_start=0&time_end=5"
	rec := postForm(t, srv, cookie, "/api/pin", body)
	if !strings.Contains(rec.Body.String(), `"pinned":true`) {
		t.Errorf("首次 pin 应 true，实际 %s", rec.Body.String())
	}
	rec = postForm(t, srv, cookie, "/api/pin", body)
	if !strings.Contains(rec.Body.String(), `"pinned":false`) {
		t.Errorf("再次 toggle 应 false，实际 %s", rec.Body.String())
	}
}

// TestCollection_CreateAndList 验证 Collection API：POST 创建、GET 列表。
func TestCollection_CreateAndList(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "col@example.com", "password123")

	rec := postForm(t, srv, cookie, "/api/collection", "title=主权基金专题&description=desc")
	if rec.Code != http.StatusOK {
		t.Fatalf("创建 Collection 应 200，实际 %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "主权基金专题") {
		t.Errorf("应返回创建的 Collection，实际 %s", rec.Body.String())
	}

	// GET 列表
	req := httptest.NewRequest(http.MethodGet, "/api/collection", nil)
	req.AddCookie(cookie)
	rec2 := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec2, req)
	if rec2.Code != http.StatusOK {
		t.Fatalf("Collection 列表应 200，实际 %d", rec2.Code)
	}
	if !strings.Contains(rec2.Body.String(), "主权基金专题") {
		t.Errorf("列表应含 Collection，实际 %s", rec2.Body.String())
	}
}
