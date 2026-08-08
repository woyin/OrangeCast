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
