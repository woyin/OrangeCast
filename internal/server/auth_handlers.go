// Package server 实现 CloudWisePod 的 HTTP 层：路由、handler、模板渲染与 REST API。
// 按职责拆分到多个文件（本文件：认领/登录/登出/Dashboard）。
package server

import (
	"context"
	"net/http"

	"github.com/woyin/orangecast/internal/auth"
	"github.com/woyin/orangecast/internal/store"
)

// handleRegister 首次认领唯一 Owner（ADR-0003）。
// GET：实例未认领时显示认领表单；已认领时重定向到 /login。
// POST：仅当 users 为空时创建 Owner，其余情况拒绝。
func (srv *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		claimed, err := srv.isClaimed(r.Context())
		if err != nil {
			http.Error(w, "内部错误", http.StatusInternalServerError)
			return
		}
		if claimed {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		srv.tmpl.Render(w, "register.html", map[string]any{"CSRF": auth.CSRFValue(r)})
		return
	}
	// POST：认领
	email := auth.NormalizeEmail(r.FormValue("email"))
	pw := r.FormValue("password")
	if err := auth.ValidateEmail(email); err != nil {
		srv.tmpl.Render(w, "register.html", map[string]any{"Error": "邮箱格式无效", "CSRF": auth.CSRFValue(r)})
		return
	}
	if err := auth.ValidatePassword(pw); err != nil {
		srv.tmpl.Render(w, "register.html", map[string]any{"Error": "密码至少 8 位", "CSRF": auth.CSRFValue(r)})
		return
	}
	hash, err := auth.HashPassword(pw)
	if err != nil {
		http.Error(w, "内部错误", http.StatusInternalServerError)
		return
	}
	u, err := srv.store.ClaimOwner(r.Context(), email, hash)
	if err != nil {
		if err == store.ErrOwnerExists {
			srv.tmpl.Render(w, "register.html", map[string]any{"Error": "实例已被认领", "CSRF": auth.CSRFValue(r)})
			return
		}
		srv.tmpl.Render(w, "register.html", map[string]any{"Error": "认领失败", "CSRF": auth.CSRFValue(r)})
		return
	}
	if err := auth.SetSessionCookie(w, r, srv.store, u.ID, srv.cfg.PublicSchemeIsHTTPS()); err != nil {
		http.Error(w, "内部错误", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}

// isClaimed 判断实例是否已被认领（users 表非空）。
func (srv *Server) isClaimed(ctx context.Context) (bool, error) {
	n, err := store.CountUsers(ctx, srv.store.DB)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}
func (srv *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		srv.tmpl.Render(w, "login.html", map[string]any{"CSRF": auth.CSRFValue(r)})
		return
	}
	// 登录限流（ADR-0013）：按客户端 IP 固定窗口
	if !srv.loginLimiter.Allow(auth.ClientIP(r, srv.cfg.TrustedProxies)) {
		http.Error(w, "尝试过于频繁，请稍后再试", http.StatusTooManyRequests)
		return
	}
	email := auth.NormalizeEmail(r.FormValue("email"))
	pw := r.FormValue("password")
	u, err := srv.store.GetUserByEmail(r.Context(), email)
	if err != nil {
		srv.tmpl.Render(w, "login.html", map[string]any{"Error": "邮箱或密码错误", "CSRF": auth.CSRFValue(r)})
		return
	}
	ok, err := auth.VerifyPassword(pw, u.PasswordHash)
	if err != nil || !ok {
		srv.tmpl.Render(w, "login.html", map[string]any{"Error": "邮箱或密码错误", "CSRF": auth.CSRFValue(r)})
		return
	}
	if err := auth.SetSessionCookie(w, r, srv.store, u.ID, srv.cfg.PublicSchemeIsHTTPS()); err != nil {
		http.Error(w, "内部错误", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}
func (srv *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	auth.ClearSessionCookie(w, r, srv.store)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}
func (srv *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	userID, _ := auth.UserIDFromContext(r.Context())
	u, _ := srv.store.GetUserByID(r.Context(), userID)
	srv.tmpl.Render(w, "dashboard.html", map[string]any{"Email": u.Email})
}
