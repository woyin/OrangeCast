package server

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

// TestSourceDetail_RendersEpisode 验证 episode source 详情页渲染标题与音频。
func TestSourceDetail_RendersEpisode(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "sd@example.com", "password123")
	ctx := context.Background()

	p, _ := srv.store.CreatePodcast(ctx, "https://f.xml", "Pod", "", "")
	srv.store.MergeEpisodes(ctx, p.ID, []models.Episode{{GUID: "g1", Title: "通胀专题", AudioURL: "https://a.mp3"}})
	eps, _ := srv.store.ListEpisodes(ctx, p.ID)
	sourceID := eps[0].ID

	// 写入 transcript + 知识卡片
	job, _ := srv.store.EnqueueJob(ctx, models.SourceEpisode, sourceID, models.JobTranscribe)
	vt, _ := srv.store.CreateArtifactVersion(ctx, models.SourceEpisode, sourceID, store.KindTranscript, "groq", "m", "1", job.ID,
		`{"segments":[{"id":"seg-0001","start":0,"end":5,"text":"通胀是物价上升"}]}`)
	srv.store.SetCurrentVersion(ctx, models.SourceEpisode, sourceID, store.KindTranscript, vt)
	cardPayload := `{"title":"通胀专题","summary":{"text":"概览","citations":["seg-0001"]},"keyPoints":[],"chapters":[],"quotes":[],"tags":[]}`
	vc, _ := srv.store.CreateArtifactVersion(ctx, models.SourceEpisode, sourceID, store.KindKnowledgeCard, "groq", "m", "1", job.ID, cardPayload)
	srv.store.SetCurrentVersion(ctx, models.SourceEpisode, sourceID, store.KindKnowledgeCard, vc)

	rec := doWithCookie(srv, cookie, http.MethodGet, "/sources/episode/"+sourceID)
	if rec.Code != http.StatusOK {
		t.Fatalf("source 详情应 200，实际 %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "通胀专题") {
		t.Errorf("页面应含标题，实际 %s", rec.Body.String())
	}
}

// TestSourceDetail_UploadAudio 验证 upload source 详情页渲染（无证据时不 404）。
func TestSourceDetail_UploadAudio(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "sdu@example.com", "password123")
	ctx := context.Background()

	up, _ := srv.store.CreateUpload(ctx, "音轨.wav", "audio/wav", 10)
	rec := doWithCookie(srv, cookie, http.MethodGet, "/sources/upload/"+up.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("upload source 详情应 200，实际 %d", rec.Code)
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

// TestRegister_InvalidEmailRejected 验证非法邮箱被拒绝。
func TestRegister_InvalidEmailRejected(t *testing.T) {
	srv := newTestServer(t)
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
		strings.NewReader("_csrf="+csrf+"&email=not-an-email&password=password123"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "cwp_csrf", Value: csrf})
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), "邮箱格式无效") {
		t.Errorf("非法邮箱应被拒绝并提示，实际 %s", rec.Body.String())
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

// TestLogin_Success 验证正确密码登录成功后重定向到 dashboard。
func TestLogin_Success(t *testing.T) {
	srv := newTestServer(t)
	claimOwnerAndLogin(t, srv, "logins@example.com", "password123")

	// GET /login 拿 CSRF
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
		strings.NewReader("_csrf="+csrf+"&email=logins@example.com&password=password123"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "cwp_csrf", Value: csrf})
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("登录成功应 303 重定向，实际 %d", rec.Code)
	}
	if !strings.Contains(rec.Header().Get("Location"), "/dashboard") {
		t.Errorf("应重定向到 dashboard，实际 %q", rec.Header().Get("Location"))
	}
}

// TestLogin_WrongPassword 验证错误密码渲染错误页（200 + 错误提示）。
func TestLogin_WrongPassword(t *testing.T) {
	srv := newTestServer(t)
	claimOwnerAndLogin(t, srv, "loginw@example.com", "password123")

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
		strings.NewReader("_csrf="+csrf+"&email=loginw@example.com&password=wrongpass"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "cwp_csrf", Value: csrf})
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("错误密码应渲染错误页 200，实际 %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "邮箱或密码错误") {
		t.Errorf("应显示登录错误，实际 %s", rec.Body.String())
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

// TestRevertVersion_ErrorPaths 验证回退的非法输入路径：无效版本号、无效 kind、版本不存在。
func TestRevertVersion_ErrorPaths(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "revert@example.com", "password123")
	ctx := context.Background()

	p, _ := srv.store.CreatePodcast(ctx, "https://feed.xml", "Pod", "", "")
	srv.store.MergeEpisodes(ctx, p.ID, []models.Episode{{GUID: "g1", Title: "Ep", AudioURL: "https://a.mp3"}})
	eps, _ := srv.store.ListEpisodes(ctx, p.ID)
	sourceID := eps[0].ID

	// 无效版本号 → 400
	rec := postForm(t, srv, cookie, "/sources/episode/"+sourceID+"/versions/revert", "kind=transcript&version=abc")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("无效版本号应 400，实际 %d", rec.Code)
	}
	// 无效 kind → 400
	rec = postForm(t, srv, cookie, "/sources/episode/"+sourceID+"/versions/revert", "kind=bogus&version=1")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("无效 kind 应 400，实际 %d", rec.Code)
	}
	// 版本不存在 → 404
	job, _ := srv.store.EnqueueJob(ctx, models.SourceEpisode, sourceID, models.JobTranscribe)
	v, _ := srv.store.CreateArtifactVersion(ctx, models.SourceEpisode, sourceID, store.KindTranscript, "groq", "m", "1", job.ID, `{"v":1}`)
	srv.store.SetCurrentVersion(ctx, models.SourceEpisode, sourceID, store.KindTranscript, v)
	rec = postForm(t, srv, cookie, "/sources/episode/"+sourceID+"/versions/revert", "kind=transcript&version=99")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("版本不存在应 404，实际 %d", rec.Code)
	}
}

// TestVersions_Nonexistent404 验证版本页对不存在的 source 返回 404。
func TestVersions_Nonexistent404(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "ver404@example.com", "password123")
	rec := doWithCookie(srv, cookie, http.MethodGet, "/sources/episode/nonexistent/versions")
	if rec.Code != http.StatusNotFound {
		t.Errorf("不存在的 source 版本页应 404，实际 %d", rec.Code)
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

// TestProcessBatch_NonPost405 验证非 POST 请求返回 405。
func TestProcessBatch_NonPost405(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "batch405@example.com", "password123")
	rec := doWithCookie(srv, cookie, http.MethodGet, "/api/process-batch")
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("非 POST 应 405，实际 %d", rec.Code)
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

// TestProcess_NonPost405 验证非 POST 请求返回 405。
func TestProcess_NonPost405(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "proc405@example.com", "password123")
	rec := doWithCookie(srv, cookie, http.MethodGet, "/api/process")
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("非 POST 应 405，实际 %d", rec.Code)
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

// TestPodcastDetail_Renders 验证播客详情页渲染（含批量入队回显参数）。
func TestPodcastDetail_Renders(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "poddet@example.com", "password123")
	ctx := context.Background()

	p, _ := srv.store.CreatePodcast(ctx, "https://feed.xml", "测试播客", "desc", "")
	srv.store.MergeEpisodes(ctx, p.ID, []models.Episode{{GUID: "g1", Title: "Ep", AudioURL: "https://a.mp3"}})

	// 带批量入队回显参数
	rec := doWithCookie(srv, cookie, http.MethodGet, "/podcasts/"+p.ID+"?enqueued=1&skipped=0")
	if rec.Code != http.StatusOK {
		t.Fatalf("播客详情应 200，实际 %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "测试播客") {
		t.Errorf("页面应含播客标题，实际 %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Ep") {
		t.Errorf("页面应含单集标题，实际 %s", rec.Body.String())
	}
}

// TestPodcastDetail_NotFound 验证不存在的播客返回 404。
func TestPodcastDetail_NotFound(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "pod404@example.com", "password123")
	rec := doWithCookie(srv, cookie, http.MethodGet, "/podcasts/nonexistent")
	if rec.Code != http.StatusNotFound {
		t.Errorf("不存在的播客应 404，实际 %d", rec.Code)
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

// TestCollection_MissingTitle 验证创建 Collection 缺 title 返回 400。
func TestCollection_MissingTitle(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "coltitle@example.com", "password123")
	rec := postForm(t, srv, cookie, "/api/collection", "description=desc")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("缺 title 应 400，实际 %d", rec.Code)
	}
}

// TestSettings_POST_SavesAndRedirects 验证设置保存 POST 更新 settings 并重定向。
func TestSettings_POST_SavesAndRedirects(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "settingspost@example.com", "password123")
	ctx := context.Background()

	rec := postForm(t, srv, cookie, "/settings",
		"transcription_model=whisper-large-v3&qa_provider=openai")
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("设置保存应 303 重定向，实际 %d", rec.Code)
	}
	if !strings.Contains(rec.Header().Get("Location"), "saved=1") {
		t.Errorf("重定向应含 saved=1，实际 %q", rec.Header().Get("Location"))
	}
	st, _ := srv.store.GetSettings(ctx)
	if st.TranscriptionModel == nil || *st.TranscriptionModel != "whisper-large-v3" {
		t.Errorf("转录模型应保存，实际 %v", st.TranscriptionModel)
	}
	if st.QAProvider == nil || *st.QAProvider != "openai" {
		t.Errorf("QA Provider 应保存，实际 %v", st.QAProvider)
	}
}

// TestSearch_Renders 验证搜索页渲染（含查询词）。
func TestSearch_Renders(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "search@example.com", "password123")

	req := httptest.NewRequest(http.MethodGet, "/search?q=wealth", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("搜索页应 200，实际 %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "wealth") {
		t.Errorf("页面应含查询词 wealth，实际 %s", rec.Body.String())
	}
}

// TestCollectionItem_AddAndRemove 验证 Collection 条目 API：action=add 加入、action=remove 移除。
func TestCollectionItem_AddAndRemove(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "colitem@example.com", "password123")
	ctx := context.Background()

	col, _ := srv.store.CreateCollection(ctx, "专题", "desc")

	// 加入条目
	rec := postForm(t, srv, cookie, "/api/collection/item",
		"collection_id="+col.ID+"&source_type=episode&source_id=ep-1&segment_ids=seg-0001&source_title=Ep&time_start=0&time_end=5")
	if rec.Code != http.StatusOK {
		t.Fatalf("加入条目应 200，实际 %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"ok":true`) {
		t.Errorf("加入应 ok=true，实际 %s", rec.Body.String())
	}
	items, _ := srv.store.ListCollectionItems(ctx, col.ID)
	if len(items) != 1 {
		t.Fatalf("应 1 个条目，实际 %d", len(items))
	}

	// 移除条目
	rec = postForm(t, srv, cookie, "/api/collection/item",
		"collection_id="+col.ID+"&source_type=episode&source_id=ep-1&segment_ids=seg-0001&action=remove")
	if rec.Code != http.StatusOK {
		t.Fatalf("移除条目应 200，实际 %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"action":"removed"`) {
		t.Errorf("移除应 action=removed，实际 %s", rec.Body.String())
	}
	items, _ = srv.store.ListCollectionItems(ctx, col.ID)
	if len(items) != 0 {
		t.Errorf("移除后应 0 个条目，实际 %d", len(items))
	}
}

// TestUploadNew_POST_CreatesAndRedirects 验证上传新文件 POST：校验类型、保存并重定向。
func TestUploadNew_POST_CreatesAndRedirects(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "uppost@example.com", "password123")

	// GET 拿 CSRF
	req0 := httptest.NewRequest(http.MethodGet, "/uploads/new", nil)
	req0.AddCookie(cookie)
	rec0 := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec0, req0)
	csrf := ""
	for _, c := range rec0.Result().Cookies() {
		if c.Name == "cwp_csrf" {
			csrf = c.Value
		}
	}

	// multipart 上传音频
	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	mw.WriteField("_csrf", csrf)
	fw, _ := mw.CreateFormFile("audio", "test.mp3")
	fw.Write([]byte("fake-audio"))
	mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/uploads/new", body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.AddCookie(cookie)
	req.AddCookie(&http.Cookie{Name: "cwp_csrf", Value: csrf})
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("上传成功应 303 重定向，实际 %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Header().Get("Location"), "/sources/upload/") {
		t.Errorf("重定向应指向上传 source，实际 %q", rec.Header().Get("Location"))
	}
	// 验证 upload 已创建
	us, _ := srv.store.ListUploads(context.Background())
	if len(us) != 1 || us[0].OriginalFilename != "test.mp3" {
		t.Errorf("应创建 1 个 upload，实际 %+v", us)
	}
}

// TestUploadNew_POST_RejectsInvalidType 验证非音频文件被拒绝。
func TestUploadNew_POST_RejectsInvalidType(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "uprej@example.com", "password123")

	// GET 拿 CSRF
	req0 := httptest.NewRequest(http.MethodGet, "/uploads/new", nil)
	req0.AddCookie(cookie)
	rec0 := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec0, req0)
	csrf := ""
	for _, c := range rec0.Result().Cookies() {
		if c.Name == "cwp_csrf" {
			csrf = c.Value
		}
	}

	// 上传 .txt 文件 → 应拒绝
	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	mw.WriteField("_csrf", csrf)
	fw, _ := mw.CreateFormFile("audio", "notes.txt")
	fw.Write([]byte("text"))
	mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/uploads/new", body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.AddCookie(cookie)
	req.AddCookie(&http.Cookie{Name: "cwp_csrf", Value: csrf})
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("类型拒绝应渲染错误页 200，实际 %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "仅支持 mp3/m4a/wav") {
		t.Errorf("应显示类型错误，实际 %s", rec.Body.String())
	}
}

// TestUploadNew_POST_NoFile 验证缺少文件时被拒绝。
func TestUploadNew_POST_NoFile(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "upnofile@example.com", "password123")

	req0 := httptest.NewRequest(http.MethodGet, "/uploads/new", nil)
	req0.AddCookie(cookie)
	rec0 := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec0, req0)
	csrf := ""
	for _, c := range rec0.Result().Cookies() {
		if c.Name == "cwp_csrf" {
			csrf = c.Value
		}
	}

	// 无文件字段
	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	mw.WriteField("_csrf", csrf)
	mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/uploads/new", body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.AddCookie(cookie)
	req.AddCookie(&http.Cookie{Name: "cwp_csrf", Value: csrf})
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("缺文件应渲染错误页 200，实际 %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "请选择音频文件") {
		t.Errorf("应显示缺文件错误，实际 %s", rec.Body.String())
	}
}

// TestUploadNew_POST_MalformedMultipart 验证 multipart 解析失败时渲染错误页。
func TestUploadNew_POST_MalformedMultipart(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "upmal@example.com", "password123")

	req0 := httptest.NewRequest(http.MethodGet, "/uploads/new", nil)
	req0.AddCookie(cookie)
	rec0 := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec0, req0)
	csrf := ""
	for _, c := range rec0.Result().Cookies() {
		if c.Name == "cwp_csrf" {
			csrf = c.Value
		}
	}

	// 发送非 multipart 的 POST → ParseMultipartForm 失败
	body := strings.NewReader("_csrf=" + csrf + "&foo=bar")
	req := httptest.NewRequest(http.MethodPost, "/uploads/new", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	req.AddCookie(&http.Cookie{Name: "cwp_csrf", Value: csrf})
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("multipart 解析失败应渲染错误页 200，实际 %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "上传失败") {
		t.Errorf("应显示上传失败错误，实际 %s", rec.Body.String())
	}
}

// TestDownloadMarkdown 验证 KnowledgeNote Markdown 下载：
// 有卡片+转录返回 200 与 frontmatter，无卡片返回 404。
func TestDownloadMarkdown(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "downloadmd@example.com", "password123")
	ctx := context.Background()

	p, _ := srv.store.CreatePodcast(ctx, "https://f.xml", "Pod", "", "")
	srv.store.MergeEpisodes(ctx, p.ID, []models.Episode{{GUID: "g1", Title: "Ep", AudioURL: "https://a.mp3"}})
	eps, _ := srv.store.ListEpisodes(ctx, p.ID)
	sourceID := eps[0].ID

	// 无卡片 → 404
	req := httptest.NewRequest(http.MethodGet, "/sources/episode/"+sourceID+"/download", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("无卡片应 404，实际 %d", rec.Code)
	}

	// 写入 transcript 与知识卡片版本（同一 job）
	job, _ := srv.store.EnqueueJob(ctx, models.SourceEpisode, sourceID, models.JobTranscribe)
	vt, _ := srv.store.CreateArtifactVersion(ctx, models.SourceEpisode, sourceID, store.KindTranscript, "groq", "m", "1", job.ID,
		`{"segments":[{"id":"seg-0001","start":0,"end":5,"text":"通胀是物价上升"}]}`)
	srv.store.SetCurrentVersion(ctx, models.SourceEpisode, sourceID, store.KindTranscript, vt)
	cardPayload := `{"title":"通胀解析","summary":{"text":"概览","citations":["seg-0001"]},"keyPoints":[{"content":"要点","description":"d","citations":["seg-0001"]}],"chapters":[],"quotes":[],"tags":["经济"]}`
	v, _ := srv.store.CreateArtifactVersion(ctx, models.SourceEpisode, sourceID, store.KindKnowledgeCard, "groq", "m", "1", job.ID, cardPayload)
	srv.store.SetCurrentVersion(ctx, models.SourceEpisode, sourceID, store.KindKnowledgeCard, v)

	// 有卡片 → 200 + frontmatter
	req = httptest.NewRequest(http.MethodGet, "/sources/episode/"+sourceID+"/download", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("有卡片应 200，实际 %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "通胀解析") {
		t.Errorf("Markdown 应含标题，实际 %s", rec.Body.String())
	}
	if !strings.Contains(rec.Header().Get("Content-Type"), "text/markdown") {
		t.Errorf("Content-Type 应为 markdown，实际 %q", rec.Header().Get("Content-Type"))
	}
}

// TestDownloadMarkdown_WithGenerated 验证 with_generated=1 时下沉 Paraphrase 与 StudyChat 块。
func TestDownloadMarkdown_WithGenerated(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "mdgen@example.com", "password123")
	ctx := context.Background()

	p, _ := srv.store.CreatePodcast(ctx, "https://f.xml", "Pod", "", "")
	srv.store.MergeEpisodes(ctx, p.ID, []models.Episode{{GUID: "g1", Title: "Ep", AudioURL: "https://a.mp3"}})
	eps, _ := srv.store.ListEpisodes(ctx, p.ID)
	sourceID := eps[0].ID

	job, _ := srv.store.EnqueueJob(ctx, models.SourceEpisode, sourceID, models.JobTranscribe)
	vt, _ := srv.store.CreateArtifactVersion(ctx, models.SourceEpisode, sourceID, store.KindTranscript, "groq", "m", "1", job.ID,
		`{"segments":[{"id":"seg-0001","start":0,"end":5,"text":"通胀是物价上升"}]}`)
	srv.store.SetCurrentVersion(ctx, models.SourceEpisode, sourceID, store.KindTranscript, vt)
	cardPayload := `{"title":"通胀解析","summary":{"text":"概览","citations":["seg-0001"]},"keyPoints":[],"chapters":[],"quotes":[],"tags":[]}`
	vc, _ := srv.store.CreateArtifactVersion(ctx, models.SourceEpisode, sourceID, store.KindKnowledgeCard, "groq", "m", "1", job.ID, cardPayload)
	srv.store.SetCurrentVersion(ctx, models.SourceEpisode, sourceID, store.KindKnowledgeCard, vc)

	// 造 Paraphrase + StudyChat 消息
	srv.store.CreateParaphrase(ctx, models.SourceEpisode, sourceID, "解释通胀", "通胀就是购买力下降", "groq", "m", []string{"seg-0001"},
		[]provider.Segment{{ID: "seg-0001", Start: 0, End: 5, Text: "通胀是物价上升"}})
	sess, _ := srv.store.CreateStudySession(ctx, models.SourceEpisode, sourceID, "会话")
	srv.store.AppendStudyMessage(ctx, sess.ID, "assistant", "通胀就是货币购买力下降", []string{"seg-0001"}, false)

	req := httptest.NewRequest(http.MethodGet, "/sources/episode/"+sourceID+"/download?with_generated=1", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("with_generated 应 200，实际 %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "通胀就是购买力下降") {
		t.Errorf("应下沉 Paraphrase 块，实际 %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "通胀就是货币购买力下降") {
		t.Errorf("应下沉 StudyChat 块，实际 %s", rec.Body.String())
	}
}

// TestDownloadMarkdown_CorruptCardPayload 验证知识卡片载荷损坏时返回 500。
func TestDownloadMarkdown_CorruptCardPayload(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "mdcorrupt@example.com", "password123")
	ctx := context.Background()
	p, _ := srv.store.CreatePodcast(ctx, "https://f.xml", "Pod", "", "")
	srv.store.MergeEpisodes(ctx, p.ID, []models.Episode{{GUID: "g1", Title: "Ep", AudioURL: "https://a.mp3"}})
	eps, _ := srv.store.ListEpisodes(ctx, p.ID)
	sourceID := eps[0].ID

	job, _ := srv.store.EnqueueJob(ctx, models.SourceEpisode, sourceID, models.JobTranscribe)
	vt, _ := srv.store.CreateArtifactVersion(ctx, models.SourceEpisode, sourceID, store.KindTranscript, "groq", "m", "1", job.ID,
		`{"segments":[{"id":"seg-0001","start":0,"end":5,"text":"通胀是物价上升"}]}`)
	srv.store.SetCurrentVersion(ctx, models.SourceEpisode, sourceID, store.KindTranscript, vt)
	// 写入损坏的卡片载荷
	vc, _ := srv.store.CreateArtifactVersion(ctx, models.SourceEpisode, sourceID, store.KindKnowledgeCard, "groq", "m", "1", job.ID, `{bad json`)
	srv.store.SetCurrentVersion(ctx, models.SourceEpisode, sourceID, store.KindKnowledgeCard, vc)

	req := httptest.NewRequest(http.MethodGet, "/sources/episode/"+sourceID+"/download", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("卡片载荷损坏应 500，实际 %d", rec.Code)
	}
}

// TestAudio_ServesEvidence 验证 /api/audio 优先返回证据音频文件。
func TestAudio_ServesEvidence(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "audio@example.com", "password123")
	ctx := context.Background()

	// 建 episode 作为 source
	p, _ := srv.store.CreatePodcast(ctx, "https://f.xml", "Pod", "", "")
	srv.store.MergeEpisodes(ctx, p.ID, []models.Episode{{GUID: "g1", Title: "Ep", AudioURL: "https://a.mp3"}})
	eps, _ := srv.store.ListEpisodes(ctx, p.ID)
	sourceID := eps[0].ID

	// 写入证据音频记录 + 文件
	if err := srv.store.UpsertEvidenceAudio(ctx, models.SourceEpisode, sourceID, "episode_"+sourceID+".mp3", "mp3", 11, "abc"); err != nil {
		t.Fatalf("UpsertEvidenceAudio: %v", err)
	}
	os.MkdirAll(srv.cfg.EvidenceDir, 0o755)
	if err := os.WriteFile(filepath.Join(srv.cfg.EvidenceDir, "episode_"+sourceID+".mp3"), []byte("fake-audio"), 0o644); err != nil {
		t.Fatalf("写证据文件: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/audio/episode/"+sourceID, nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("证据音频应 200，实际 %d", rec.Code)
	}
	if rec.Body.String() != "fake-audio" {
		t.Errorf("应返回证据音频内容，实际 %q", rec.Body.String())
	}

	// 无音频 → 404
	req = httptest.NewRequest(http.MethodGet, "/api/audio/episode/nonexistent", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("无音频应 404，实际 %d", rec.Code)
	}
}

// TestAudio_UploadFallback 验证 upload source 无证据时回退到原始落盘文件。
func TestAudio_UploadFallback(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "audioup@example.com", "password123")
	ctx := context.Background()

	up, _ := srv.store.CreateUpload(ctx, "a.wav", "audio/wav", 10)
	// 原始落盘文件
	rawDir := filepath.Join(srv.cfg.TempDir, "uploads")
	os.MkdirAll(rawDir, 0o755)
	os.WriteFile(filepath.Join(rawDir, up.ID), []byte("raw-audio"), 0o644)

	req := httptest.NewRequest(http.MethodGet, "/api/audio/upload/"+up.ID, nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("upload 回退应 200，实际 %d", rec.Code)
	}
	if rec.Body.String() != "raw-audio" {
		t.Errorf("应返回原始音频内容，实际 %q", rec.Body.String())
	}
}

// TestPodcastNew_POST_Subscribes 验证订阅新播客 POST：抓取 feed、创建播客、合并单集并重定向。
func TestPodcastNew_POST_Subscribes(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "podnew@example.com", "password123")
	ctx := context.Background()

	// 注入 fake fetchFeed（隔离网络）
	srv.fetchFeed = func(feedURL string) (*models.Podcast, []models.Episode, error) {
		return &models.Podcast{FeedURL: feedURL, Title: "测试播客", Description: "desc"},
			[]models.Episode{{GUID: "g1", Title: "Ep1", AudioURL: "https://a.mp3"}}, nil
	}

	rec := postForm(t, srv, cookie, "/podcasts/new", "feed_url=https://feed.example.com/pod.xml")
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("订阅成功应 303 重定向，实际 %d: %s", rec.Code, rec.Body.String())
	}
	loc := rec.Header().Get("Location")
	if !strings.Contains(loc, "/podcasts/") {
		t.Errorf("重定向应指向播客详情，实际 %q", loc)
	}

	// 验证播客与单集已创建
	ps, _ := srv.store.ListPodcasts(ctx)
	if len(ps) != 1 || ps[0].Title != "测试播客" {
		t.Errorf("应创建 1 个播客，实际 %+v", ps)
	}
	eps, _ := srv.store.ListEpisodes(ctx, ps[0].ID)
	if len(eps) != 1 || eps[0].Title != "Ep1" {
		t.Errorf("应合并 1 集，实际 %+v", eps)
	}
}

// TestPodcastNew_POST_FetchError 验证抓取失败时渲染错误页而非崩溃。
func TestPodcastNew_POST_FetchError(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "podnewerr@example.com", "password123")

	srv.fetchFeed = func(feedURL string) (*models.Podcast, []models.Episode, error) {
		return nil, nil, errFetchFailed{}
	}
	rec := postForm(t, srv, cookie, "/podcasts/new", "feed_url=https://feed.example.com/nope.xml")
	if rec.Code != http.StatusOK {
		t.Fatalf("抓取失败应渲染错误页 200，实际 %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "抓取/解析 feed 失败") {
		t.Errorf("应显示抓取错误，实际 %s", rec.Body.String())
	}
}

type errFetchFailed struct{}

func (errFetchFailed) Error() string { return "fetch failed" }
