// Content workbench HTTP handlers (ADR-0021 / roadmap Phase 8).
package server

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/woyin/orangecast/internal/auth"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/store"
)

var errBudget = errors.New("invalid budget")

// handleWorkbench renders the initial personal editorial board.
func (srv *Server) handleWorkbench(w http.ResponseWriter, r *http.Request) {
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
		proposals, err := srv.store.ListArticleProposals(r.Context(), profile.ID)
		if err != nil {
			http.Error(w, "加载提案失败", http.StatusInternalServerError)
			return
		}
		drafts, err := srv.store.ListArticleDrafts(r.Context(), profile.ID)
		if err != nil {
			http.Error(w, "加载文章失败", http.StatusInternalServerError)
			return
		}
		data["Profile"] = profile
		data["Proposals"] = proposals
		data["Drafts"] = drafts
		scopes, err := srv.store.ListScopedSources(r.Context(), profile.ID)
		if err != nil {
			http.Error(w, "加载素材范围失败", http.StatusInternalServerError)
			return
		}
		data["Scopes"] = scopes
	}
	if err := srv.tmpl.Render(w, "workbench.html", data); err != nil {
		http.Error(w, "渲染工作台失败", http.StatusInternalServerError)
	}
}

// handleEditorialSourceScope grants or revokes an explicit source authorization.
func (srv *Server) handleEditorialSourceScope(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}
	profileID := strings.TrimSpace(r.FormValue("profile_id"))
	sourceType := models.SourceType(strings.TrimSpace(r.FormValue("source_type")))
	sourceID := strings.TrimSpace(r.FormValue("source_id"))
	var err error
	if r.FormValue("action") == "revoke" {
		err = srv.store.RevokeSourceScope(r.Context(), profileID, sourceType, sourceID)
	} else {
		err = srv.store.GrantSourceScope(r.Context(), profileID, sourceType, sourceID)
	}
	if err != nil {
		http.Error(w, "更新素材范围失败："+err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/workbench?profile="+profileID, http.StatusSeeOther)
}

// handleEditorialProfileCreate creates an Owner-controlled EditorialProfile.
func (srv *Server) handleEditorialProfileCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	budget, err := optionalBudget(r.FormValue("monthly_budget_cents"))
	if err != nil {
		http.Error(w, "月度预算必须是非负整数", http.StatusBadRequest)
		return
	}
	profile, err := srv.store.CreateEditorialProfile(r.Context(), models.EditorialProfile{
		Name:               name,
		TargetAudience:     strings.TrimSpace(r.FormValue("target_audience")),
		Voice:              strings.TrimSpace(r.FormValue("voice")),
		StyleGuide:         strings.TrimSpace(r.FormValue("style_guide")),
		SourceAttribution:  strings.TrimSpace(r.FormValue("source_attribution")),
		MonthlyBudgetCents: budget,
	})
	if err != nil {
		http.Error(w, "创建编辑画像失败："+err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/workbench?profile="+profile.ID, http.StatusSeeOther)
}

func optionalBudget(raw string) (*int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < 0 {
		return nil, errBudget
	}
	return &value, nil
}
