package server

import (
	"net/http"
	"strings"

	"github.com/woyin/orangecast/internal/auth"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/store"
)

// handleThemes renders a profile-scoped cross-episode Theme board.
func (srv *Server) handleThemes(w http.ResponseWriter, r *http.Request) {
	profiles, err := srv.store.ListEditorialProfiles(r.Context())
	if err != nil {
		http.Error(w, "加载编辑画像失败", http.StatusInternalServerError)
		return
	}
	data := map[string]any{"Profiles": profiles, "CSRF": auth.CSRFValue(r)}
	profileID := strings.TrimSpace(r.URL.Query().Get("profile"))
	if profileID == "" && len(profiles) > 0 {
		profileID = profiles[0].ID
	}
	if profileID != "" {
		profile, err := srv.store.GetEditorialProfile(r.Context(), profileID)
		if err != nil {
			if err == store.ErrNotFound {
				http.NotFound(w, r)
				return
			}
			http.Error(w, "加载编辑画像失败", http.StatusInternalServerError)
			return
		}
		themes, err := srv.store.ListThemes(r.Context(), profile.ID)
		if err != nil {
			http.Error(w, "加载主题失败", http.StatusInternalServerError)
			return
		}
		data["Profile"] = profile
		data["Themes"] = themes
	}
	if err := srv.tmpl.Render(w, "themes.html", data); err != nil {
		http.Error(w, "渲染主题失败", http.StatusInternalServerError)
	}
}

// handleThemeCreate records a suggested theme before it is confirmed for Scout use.
func (srv *Server) handleThemeCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}
	theme, err := srv.store.CreateTheme(r.Context(), models.Theme{EditorialProfileID: strings.TrimSpace(r.FormValue("profile_id")), Name: strings.TrimSpace(r.FormValue("name")), Description: strings.TrimSpace(r.FormValue("description"))})
	if err != nil {
		http.Error(w, "创建主题失败："+err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/themes?profile="+theme.EditorialProfileID, http.StatusSeeOther)
}

// handleThemeStatus records confirmation or dismissal of a theme suggestion.
func (srv *Server) handleThemeStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}
	profileID := strings.TrimSpace(r.FormValue("profile_id"))
	if err := srv.store.SetThemeStatus(r.Context(), strings.TrimSpace(r.FormValue("theme_id")), strings.TrimSpace(r.FormValue("status"))); err != nil {
		http.Error(w, "更新主题失败："+err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/themes?profile="+profileID, http.StatusSeeOther)
}

// handleThemeKeyPoint records an explainable support, complement, or conflict relation.
func (srv *Server) handleThemeKeyPoint(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}
	profileID := strings.TrimSpace(r.FormValue("profile_id"))
	if err := srv.store.AddKeyPointToTheme(r.Context(), strings.TrimSpace(r.FormValue("theme_id")), strings.TrimSpace(r.FormValue("keypoint_id")), strings.TrimSpace(r.FormValue("relationship"))); err != nil {
		http.Error(w, "添加主题材料失败："+err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/themes?profile="+profileID, http.StatusSeeOther)
}
