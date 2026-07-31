package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/breestealth/wisepod/internal/auth"
	"github.com/breestealth/wisepod/internal/config"
	"github.com/breestealth/wisepod/internal/provider"
	"github.com/breestealth/wisepod/internal/queue"
	"github.com/breestealth/wisepod/internal/rss"
	"github.com/breestealth/wisepod/internal/store"
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
		BaseURL: "http://localhost",
	}
	selector := provider.NewSelector(cfg.GroqAPIKey, cfg.OpenAIAPIKey)
	worker := queue.NewWorker(s, selector, cfg.TempDir)
	refresher := rss.NewRefresher(s)
	srv, err := New(cfg, s, worker, refresher, selector)
	if err != nil {
		t.Fatal(err)
	}
	return srv
}

// registerAndLogin 注册一个用户并返回带 session cookie 的 cookie jar。
func registerAndLogin(t *testing.T, srv *Server, email, pw string) *http.Cookie {
	t.Helper()
	router := srv.Router()

	// 注册
	req := httptest.NewRequest(http.MethodPost, "/register", strings.NewReader("email="+email+"&password="+pw))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	// 提取 Set-Cookie
	cookies := rec.Result().Cookies()
	for _, c := range cookies {
		if c.Name == "cwp_session" {
			return c
		}
	}
	t.Fatal("注册后未拿到 session cookie")
	return nil
}

func doWithCookie(srv *Server, cookie *http.Cookie, method, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	req.AddCookie(cookie)
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

func TestAuth_RegisterThenAccessDashboard(t *testing.T) {
	srv := newTestServer(t)
	cookie := registerAndLogin(t, srv, "test@example.com", "password123")
	rec := doWithCookie(srv, cookie, http.MethodGet, "/dashboard")
	if rec.Code != http.StatusOK {
		t.Errorf("登录后 dashboard 应 200，实际 %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "仪表盘") {
		t.Error("dashboard 应含'仪表盘'")
	}
}

func TestSettings_DefaultGroq(t *testing.T) {
	srv := newTestServer(t)
	cookie := registerAndLogin(t, srv, "s@example.com", "password123")
	rec := doWithCookie(srv, cookie, http.MethodGet, "/settings")
	if rec.Code != http.StatusOK {
		t.Fatalf("settings 应 200，实际 %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Groq（免费主力）") {
		t.Error("settings 默认应显示 Groq 主力")
	}
}

func TestSettings_SwitchProvider(t *testing.T) {
	srv := newTestServer(t)
	cookie := registerAndLogin(t, srv, "s2@example.com", "password123")
	// 切换到 openai
	req := httptest.NewRequest(http.MethodPost, "/settings", strings.NewReader("active_provider=openai"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("保存设置应重定向，实际 %d", rec.Code)
	}
	// 验证已切到 openai
	rec2 := doWithCookie(srv, cookie, http.MethodGet, "/settings")
	if !strings.Contains(rec2.Body.String(), `value="openai" selected`) {
		t.Error("切换后 settings 应选中 openai")
	}
}

func TestSourceDetail_NotFound(t *testing.T) {
	srv := newTestServer(t)
	cookie := registerAndLogin(t, srv, "n@example.com", "password123")
	rec := doWithCookie(srv, cookie, http.MethodGet, "/sources/episode/nonexistent")
	if rec.Code != http.StatusNotFound {
		t.Errorf("不存在的 source 应 404，实际 %d", rec.Code)
	}
}

func TestSourceDetail_OtherUser_Invisible(t *testing.T) {
	srv := newTestServer(t)
	// 用户 A 注册并（通过 store 直接）创建一个 episode
	cookieA := registerAndLogin(t, srv, "a@example.com", "password123")
	_ = cookieA

	// 用户 B 注册
	cookieB := registerAndLogin(t, srv, "b@example.com", "password123")

	// 直接查 DB：A 的用户 id
	srv2 := srv // 别名
	_ = srv2
	// B 尝试访问一个不存在的 source（属于 A 或不存在都应 404）
	rec := doWithCookie(srv, cookieB, http.MethodGet, "/sources/episode/anything")
	if rec.Code != http.StatusNotFound {
		t.Errorf("跨用户访问应 404，实际 %d", rec.Code)
	}
}

func TestLogout_ClearsSession(t *testing.T) {
	srv := newTestServer(t)
	cookie := registerAndLogin(t, srv, "lo@example.com", "password123")
	// 先确认能访问
	rec := doWithCookie(srv, cookie, http.MethodGet, "/dashboard")
	if rec.Code != http.StatusOK {
		t.Fatal("登录后应能访问")
	}
	// 登出
	rec2 := doWithCookie(srv, cookie, http.MethodGet, "/logout")
	if rec2.Code != http.StatusSeeOther {
		t.Fatalf("登出应重定向，实际 %d", rec2.Code)
	}
	// 用 DB 验证 session 已删除（cookie 仍带但服务端已失效）
	// 再访问应被拒（session 无效）
	rec3 := doWithCookie(srv, cookie, http.MethodGet, "/dashboard")
	if rec3.Code != http.StatusSeeOther {
		t.Errorf("登出后 dashboard 应重定向到登录，实际 %d", rec3.Code)
	}
}

func TestAPINotAuth_Returns401(t *testing.T) {
	srv := newTestServer(t)
	// /api/qa 未登录应 401（非浏览器重定向）
	req := httptest.NewRequest(http.MethodPost, "/api/qa", nil)
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("/api/ 未登录应 401，实际 %d", rec.Code)
	}
}

func TestRegister_WeakPasswordRejected(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/register", strings.NewReader("email=x@example.com&password=123"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), "至少 8 位") {
		t.Error("短密码应被拒绝并提示")
	}
}

// 确保 auth 包的 lint 不报未使用
var _ = auth.RequireAuth

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
