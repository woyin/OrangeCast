package auth

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/breestealth/wisepod/internal/store"
)

const sessionCookieName = "cwp_session"
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
func SetSessionCookie(w http.ResponseWriter, r *http.Request, s *store.Store, userID string) error {
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
		Secure:   r.URL.Scheme == "https",
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

// EnsureUnusedEmail 注册前校验邮箱未被占用。
func EnsureUnusedEmail(ctx context.Context, s *store.Store, email string) error {
	_, err := s.GetUserByEmail(ctx, email)
	if err == nil {
		return errors.New("邮箱已被注册")
	}
	if errors.Is(err, sql.ErrNoRows) || errors.Is(err, store.ErrNotFound) {
		return nil
	}
	return err
}
