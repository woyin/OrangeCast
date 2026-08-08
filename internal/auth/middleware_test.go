package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/woyin/orangecast/internal/store"
)

// TestRateLimiter_AllowsUpToLimit 验证窗口内前 limit 次放行、超限拒绝。
func TestRateLimiter_AllowsUpToLimit(t *testing.T) {
	l := NewRateLimiter(3, time.Minute)
	ip := "203.0.113.1"
	for i := 0; i < 3; i++ {
		if !l.Allow(ip) {
			t.Fatalf("第 %d 次应放行", i+1)
		}
	}
	if l.Allow(ip) {
		t.Fatal("第 4 次（超限）应拒绝")
	}
	// 不同 key 有独立配额
	if !l.Allow("203.0.113.2") {
		t.Fatal("不同 IP 应有独立配额")
	}
}

// TestRateLimiter_WindowExpiryReset 验证窗口过期后计数重置。
func TestRateLimiter_WindowExpiryReset(t *testing.T) {
	l := NewRateLimiter(1, 10*time.Millisecond)
	ip := "203.0.113.1"
	if !l.Allow(ip) {
		t.Fatal("首次应放行")
	}
	if l.Allow(ip) {
		t.Fatal("窗口内超限应拒绝")
	}
	<-time.After(15 * time.Millisecond)
	if !l.Allow(ip) {
		t.Fatal("窗口过期后应重置并放行")
	}
}

// TestRateLimiter_Cleanup 验证 Cleanup 移除过期窗口。
func TestRateLimiter_Cleanup(t *testing.T) {
	l := NewRateLimiter(1, 10*time.Millisecond)
	l.Allow("a")
	l.Allow("b")
	<-time.After(15 * time.Millisecond)
	l.Cleanup()
	l.mu.Lock()
	n := len(l.buckets)
	l.mu.Unlock()
	if n != 0 {
		t.Errorf("Cleanup 应清空过期窗口，实际还剩 %d", n)
	}
}

// TestClientIP 验证 ClientIP 的直连与可信代理转发头逻辑。
func TestClientIP(t *testing.T) {
	// 直连（非代理）：用 X-Forwarded-For 也不应被信任。
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "198.51.100.7:1234"
	req.Header.Set("X-Forwarded-For", "203.0.113.9")
	if got := ClientIP(req, nil); got != "198.51.100.7" {
		t.Errorf("直连应返回 RemoteAddr，实际 %q", got)
	}

	// 可信代理：信任 X-Forwarded-For 第一个地址。
	trusted := []string{"10.0.0.0/8"}
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.RemoteAddr = "10.0.0.9:9999"
	req2.Header.Set("X-Forwarded-For", "203.0.113.9, 10.0.0.9")
	if got := ClientIP(req2, trusted); got != "203.0.113.9" {
		t.Errorf("可信代理应信任 XFF 首地址，实际 %q", got)
	}

	// 不受信代理：忽略转发头。
	req3 := httptest.NewRequest(http.MethodGet, "/", nil)
	req3.RemoteAddr = "198.51.100.7:1234"
	req3.Header.Set("X-Forwarded-For", "203.0.113.9")
	if got := ClientIP(req3, trusted); got != "198.51.100.7" {
		t.Errorf("非可信代理应忽略 XFF，实际 %q", got)
	}
}

// TestCSRFValue_FromContext 验证 CSRFValue 优先从 context 取注入的 token。
func TestCSRFValue_FromContext(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if got := CSRFValue(req); got != "" {
		t.Errorf("无 cookie/context 时应返回空串，实际 %q", got)
	}
}

// store 测试辅助：打开临时 SQLite。
func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("打开测试库: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// TestRequireAuth 验证：无 cookie 的 API 返回 401，带有效 session 则通过并注入 userID。
func TestRequireAuth(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	u, err := s.ClaimOwner(ctx, "test@example.com", "$argon2id$fakehash")
	if err != nil {
		t.Fatalf("创建用户: %v", err)
	}
	hw := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uid, ok := UserIDFromContext(r.Context())
		if !ok || uid != u.ID {
			t.Errorf("应注入 %s，实际 uid=%q ok=%v", u.ID, uid, ok)
		}
		w.WriteHeader(http.StatusOK)
	})

	// 无 cookie → API 401
	req := httptest.NewRequest(http.MethodGet, "/api/foo", nil)
	rec := httptest.NewRecorder()
	RequireAuth(s)(hw).ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("未登录 API 应 401，实际 %d", rec.Code)
	}

	// 无 cookie → 页面重定向到 /login
	req = httptest.NewRequest(http.MethodGet, "/sources/x", nil)
	rec = httptest.NewRecorder()
	RequireAuth(s)(hw).ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login" {
		t.Fatalf("未登录页面应 303 重定向 /login，实际 %d %q", rec.Code, rec.Header().Get("Location"))
	}

	// 有效 session → 通过
	token, err := s.CreateSession(ctx, u.ID, time.Now().Add(time.Hour).UTC().Format(time.RFC3339))
	if err != nil {
		t.Fatalf("创建 session: %v", err)
	}
	req = httptest.NewRequest(http.MethodGet, "/api/foo", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	rec = httptest.NewRecorder()
	RequireAuth(s)(hw).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("有效 session 应放行，实际 %d", rec.Code)
	}
}

// TestCSRFProtect 验证：GET 放行，POST 缺 token 或与 cookie 不一致则 403。
func TestCSRFProtect(t *testing.T) {
	hw := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	// GET 放行
	req := httptest.NewRequest(http.MethodGet, "/settings", nil)
	rec := httptest.NewRecorder()
	CSRFProtect(hw).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET 应放行，实际 %d", rec.Code)
	}
	// 从 Set-Cookie 取 CSRF token
	var csrf string
	for _, c := range rec.Result().Cookies() {
		if c.Name == csrfCookieName {
			csrf = c.Value
		}
	}
	if csrf == "" {
		t.Fatal("应设置 CSRF cookie")
	}

	// POST 带匹配 token → 放行
	req = httptest.NewRequest(http.MethodPost, "/settings", nil)
	req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: csrf})
	req.PostForm = map[string][]string{"_csrf": {csrf}}
	rec = httptest.NewRecorder()
	CSRFProtect(hw).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("匹配 token 应放行，实际 %d", rec.Code)
	}

	// POST 缺 token → 403
	req = httptest.NewRequest(http.MethodPost, "/settings", nil)
	req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: csrf})
	req.PostForm = map[string][]string{}
	rec = httptest.NewRecorder()
	CSRFProtect(hw).ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("缺 token 应 403，实际 %d", rec.Code)
	}

	// POST token 不匹配 → 403
	req = httptest.NewRequest(http.MethodPost, "/settings", nil)
	req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: csrf})
	req.PostForm = map[string][]string{"_csrf": {"wrong-token"}}
	rec = httptest.NewRecorder()
	CSRFProtect(hw).ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("token 不匹配应 403，实际 %d", rec.Code)
	}
}

// TestSetSessionCookie 验证设置会话 cookie：写入 session 记录并 Set-Cookie。
func TestSetSessionCookie(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	u, err := s.ClaimOwner(ctx, "a@b.com", "$argon2id$fakehash")
	if err != nil {
		t.Fatalf("ClaimOwner: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	if err := SetSessionCookie(rec, req, s, u.ID, true); err != nil {
		t.Fatalf("SetSessionCookie: %v", err)
	}
	// 应设置 secure cookie
	c := rec.Result().Cookies()
	if len(c) != 1 || c[0].Name != sessionCookieName {
		t.Fatalf("应设置 session cookie，实际 %+v", c)
	}
	if !c[0].Secure || !c[0].HttpOnly {
		t.Errorf("cookie 应为 Secure+HttpOnly，实际 %+v", c[0])
	}
	// session 记录应存在
	if _, err := s.GetSessionByToken(ctx, c[0].Value); err != nil {
		t.Errorf("session 记录应存在: %v", err)
	}
}

// TestClearSessionCookie 验证登出：删除 session 记录并清除 cookie。
func TestClearSessionCookie(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	u, _ := s.ClaimOwner(ctx, "a@b.com", "$argon2id$fakehash")
	token, _ := s.CreateSession(ctx, u.ID, time.Now().Add(time.Hour).UTC().Format(time.RFC3339))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	rec := httptest.NewRecorder()
	ClearSessionCookie(rec, req, s)
	// session 记录已删除
	if _, err := s.GetSessionByToken(ctx, token); err != store.ErrNotFound {
		t.Errorf("session 应被删除，err=%v", err)
	}
	// cookie 被清除（MaxAge -1）
	var cleared bool
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookieName && c.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Error("应清除 session cookie")
	}
}
