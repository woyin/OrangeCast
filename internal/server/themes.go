package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/woyin/orangecast/internal/auth"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
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

// handleScoutRun generates proposed topics from confirmed, cross-episode themes.
func (srv *Server) handleScoutRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}
	profileID := strings.TrimSpace(r.FormValue("profile_id"))
	created, err := srv.runScout(r, profileID)
	if err != nil {
		writeEditorialError(w, err)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/themes?profile=%s&scouted=%d", profileID, created), http.StatusSeeOther)
}

func normalizeEditorialTitle(value string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(value)) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || (r >= '\u4e00' && r <= '\u9fff') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func editorialTitleNearDuplicate(candidate string, history []string) bool {
	if candidate == "" {
		return true
	}
	for _, old := range history {
		if old == "" {
			continue
		}
		if (strings.Contains(candidate, old) || strings.Contains(old, candidate)) && minInt(len([]rune(candidate)), len([]rune(old))) >= 6 {
			return true
		}
		a, b := titleBigrams(candidate), titleBigrams(old)
		intersection := 0
		for token := range a {
			if b[token] {
				intersection++
			}
		}
		union := len(a) + len(b) - intersection
		if union > 0 && float64(intersection)/float64(union) >= .65 {
			return true
		}
	}
	return false
}

func titleBigrams(value string) map[string]bool {
	r := []rune(value)
	out := map[string]bool{}
	if len(r) == 1 {
		out[string(r)] = true
		return out
	}
	for i := 0; i+1 < len(r); i++ {
		out[string(r[i:i+2])] = true
	}
	return out
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (srv *Server) scoutRequest(r *http.Request, profile *models.EditorialProfile, providerName string) (provider.ScoutRequest, error) {
	themes, err := srv.store.ListThemes(r.Context(), profile.ID)
	if err != nil {
		return provider.ScoutRequest{}, err
	}
	request := provider.ScoutRequest{Audience: profile.TargetAudience, Voice: profile.Voice}
	for _, theme := range themes {
		if theme.Status != "confirmed" {
			continue
		}
		relations, err := srv.store.ListThemeKeyPoints(r.Context(), theme.ID)
		if err != nil {
			return provider.ScoutRequest{}, err
		}
		scoutTheme := provider.ScoutTheme{ID: theme.ID, Name: theme.Name, Description: theme.Description}
		sources := map[string]bool{}
		for _, relation := range relations {
			keyPoint, err := srv.store.GetKeyPoint(r.Context(), relation.KeyPointID)
			if err != nil {
				return provider.ScoutRequest{}, err
			}
			usable, err := srv.store.CanUseSourceForPublication(r.Context(), profile.ID, keyPoint.SourceType, keyPoint.SourceID)
			if err != nil || !usable {
				return provider.ScoutRequest{}, fmt.Errorf("Theme %q 包含不可用素材", theme.Name)
			}
			external, err := srv.store.CanSendSourceToProvider(r.Context(), keyPoint.SourceType, keyPoint.SourceID, providerName)
			if err != nil || !external {
				return provider.ScoutRequest{}, fmt.Errorf("Theme %q 包含不可发送给 Scout 的素材", theme.Name)
			}
			var citations []string
			if err := json.Unmarshal([]byte(keyPoint.CitationsJSON), &citations); err != nil || len(citations) == 0 {
				return provider.ScoutRequest{}, fmt.Errorf("Theme %q 包含无 Citation KeyPoint", theme.Name)
			}
			scoutTheme.Materials = append(scoutTheme.Materials, provider.ArticleMaterial{KeyPointID: keyPoint.ID, SourceID: keyPoint.SourceID, SourceTitle: keyPoint.SourceTitle, Content: keyPoint.Content, Description: keyPoint.Description, Citations: citations})
			sources[keyPoint.SourceID] = true
		}
		if len(sources) >= 2 {
			request.Themes = append(request.Themes, scoutTheme)
		}
	}
	if len(request.Themes) == 0 {
		return provider.ScoutRequest{}, fmt.Errorf("需要至少一个含两个 Episode 的确认 Theme")
	}
	return request, nil
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
