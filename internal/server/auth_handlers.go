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

// handleDashboard 首页并列继续学习与继续创作（U01 / ADR-0024 §1）。
// 复用 AttentionQueue 呈现新重点、继续听、待处理问题，以及精读文、候选、
// Brief、写作/审校中的文章。GET 只读，不入队或调用模型。
func (srv *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	userID, _ := auth.UserIDFromContext(r.Context())
	u, _ := srv.store.GetUserByID(r.Context(), userID)
	data := map[string]any{"Email": u.Email, "CSRF": auth.CSRFValue(r)}

	// 注意力队列（学习 + 创作双泳道）。
	profiles, err := srv.store.ListEditorialProfiles(r.Context())
	if err == nil && len(profiles) > 0 {
		items, err := srv.store.AttentionQueue(r.Context(), profiles[0].ID)
		if err == nil {
			var learning, creation []store.AttentionItem
			for _, item := range items {
				if item.Lane == "learning" {
					learning = append(learning, item)
				} else {
					creation = append(creation, item)
				}
			}
			data["Attention"] = map[string]any{"Learning": learning, "Creation": creation}
		}
	}

	// 最近精读文。
	digests, err := srv.store.ListEpisodeDigests(r.Context())
	if err == nil && len(digests) > 3 {
		digests = digests[:3]
	}
	if len(digests) > 0 {
		data["RecentDigests"] = digests
	}

	// 创作素材选择。
	if len(profiles) > 0 {
		selections, err := srv.store.ListCreationSelections(r.Context(), profiles[0].ID)
		if err == nil && len(selections) > 0 {
			data["Selections"] = selections
		}
	}

	srv.tmpl.Render(w, "dashboard.html", data)
}
