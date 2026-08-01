package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/woyin/orangecast/internal/store"
)

const sessionCookieName = "cwp_session"
const csrfCookieName = "cwp_csrf"
const sessionTTL = 30 * 24 * time.Hour

type ctxKey string

const userCtxKey ctxKey = "user_id"

// RequireAuth 中间件：校验 session cookie，将 userID 注入 context。
// 未登录重定向到 /login（浏览器请求）或返回 401（API 请求）。
func RequireAuth(s *store.Store) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			c, err := r.Cookie(sessionCookieName)
			if err != nil || c.Value == "" {
				deny(w, r)
				return
			}
			userID, err := s.GetSessionByToken(r.Context(), c.Value)
			if errors.Is(err, store.ErrNotFound) {
				// 清除无效 cookie
				http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: "", MaxAge: -1, Path: "/"})
				deny(w, r)
				return
			}
			if err != nil {
				http.Error(w, "内部错误", http.StatusInternalServerError)
				return
			}
			ctx := context.WithValue(r.Context(), userCtxKey, userID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func deny(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		http.Error(w, "未授权", http.StatusUnauthorized)
		return
	}
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// UserIDFromContext 从 context 取已认证的 userID。
func UserIDFromContext(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(userCtxKey).(string)
	return v, ok
}

// SetSessionCookie 创建 session 并写 cookie。
// secure 由调用方根据 PUBLIC_URL 判定（可信代理场景，ADR-0013）。
func SetSessionCookie(w http.ResponseWriter, r *http.Request, s *store.Store, userID string, secure bool) error {
	expires := time.Now().Add(sessionTTL).UTC().Format(time.RFC3339)
	token, err := s.CreateSession(r.Context(), userID, expires)
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   secure,
		MaxAge:   int(sessionTTL.Seconds()),
	})
	return nil
}

// ClearSessionCookie 登出：删除 session 记录并清除 cookie。
func ClearSessionCookie(w http.ResponseWriter, r *http.Request, s *store.Store) {
	if c, err := r.Cookie(sessionCookieName); err == nil {
		_ = s.DeleteSession(r.Context(), c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: "", MaxAge: -1, Path: "/"})
}

// ---- CSRF（double-submit cookie）----

// CSRFProtect 验证所有状态变更请求（POST/PUT/PATCH/DELETE）携带与 cookie 一致的 CSRF token。
// GET/HEAD/OPTIONS 仅确保 cookie 存在（GET 页面需要 token 渲染进表单）。
// 这是轻量无状态方案：token 值同时写入 cookie 与表单，同源可读、跨站不可读（SameSite=Lax + 未知随机值）。
func CSRFProtect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 确保 CSRF cookie 存在
		c, err := r.Cookie(csrfCookieName)
		if err != nil || c.Value == "" {
			c = &http.Cookie{
				Name: csrfCookieName, Value: randomToken(), Path: "/",
				HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: int(sessionTTL.Seconds()),
			}
			http.SetCookie(w, c)
		}

		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}

		// 状态变更请求：表单字段或头必须与 cookie 一致
		got := r.FormValue("_csrf")
		if got == "" {
			got = r.Header.Get("X-CSRF-Token")
		}
		if got == "" || c.Value == "" || got != c.Value {
			http.Error(w, "CSRF 校验失败", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// CSRFValue 返回当前请求的 CSRF token（用于渲染进表单）。
func CSRFValue(r *http.Request) string {
	if c, err := r.Cookie(csrfCookieName); err == nil {
		return c.Value
	}
	return ""
}

func randomToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// ---- 登录限流（内存固定窗口，按客户端 IP）----

// RateLimiter 简单的每 IP 固定窗口限流。
type RateLimiter struct {
	mu      sync.Mutex
	window  time.Duration
	limit   int
	buckets map[string]*windowCount
}

type windowCount struct {
	start time.Time
	count int
}

// NewRateLimiter 创建限流器：每 window 窗口内最多 limit 次。
func NewRateLimiter(limit int, window time.Duration) *RateLimiter {
	return &RateLimiter{
		window: window, limit: limit,
		buckets: map[string]*windowCount{},
	}
}

// Allow 判断 key 是否在窗口内仍有余量。
func (l *RateLimiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	w, ok := l.buckets[key]
	if !ok || now.Sub(w.start) >= l.window {
		l.buckets[key] = &windowCount{start: now, count: 1}
		return true
	}
	w.count++
	return w.count <= l.limit
}

// Cleanup 定期清理过期窗口，防内存增长。
func (l *RateLimiter) Cleanup() {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	for k, w := range l.buckets {
		if now.Sub(w.start) >= l.window {
			delete(l.buckets, k)
		}
	}
}

// ---- 客户端 IP（可信代理模型，ADR-0013）----

// ClientIP 返回客户端 IP。仅当 RemoteAddr 属于受信任代理 CIDR 时信任 X-Forwarded-For
// 的第一个地址；否则使用直连地址。绝不信任任意转发头。
func ClientIP(r *http.Request, trustedProxies []string) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(strings.TrimSpace(host))
	for _, cidr := range trustedProxies {
		_, network, perr := net.ParseCIDR(cidr)
		if perr != nil {
			continue
		}
		if ip != nil && network.Contains(ip) {
			if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
				first := strings.TrimSpace(strings.Split(xff, ",")[0])
				if p := net.ParseIP(first); p != nil {
					return p.String()
				}
			}
			return host
		}
	}
	return host
}
