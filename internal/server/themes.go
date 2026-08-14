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
	profile, err := srv.store.GetEditorialProfile(r.Context(), profileID)
	if err != nil {
		http.Error(w, "读取编辑画像失败", http.StatusBadRequest)
		return
	}
	settings, err := srv.store.GetSettings(r.Context())
	if err != nil {
		http.Error(w, "读取 Scout 配置失败", http.StatusInternalServerError)
		return
	}
	taskConfig := editorialTaskConfig(settings, editorialRoleScout)
	bundle, err := srv.bundleFor(taskConfig)
	if err != nil || bundle.Scout == nil {
		http.Error(w, "Scout Provider 不可用", http.StatusBadRequest)
		return
	}
	request, err := srv.scoutRequest(r, profile, bundle.Scout.Name())
	if err != nil {
		http.Error(w, "主题不满足 Scout 条件："+err.Error(), http.StatusBadRequest)
		return
	}
	providerName := bundle.Scout.Name()
	modelName := provider.EffectiveTaskModel(taskConfig)
	promptVersion := provider.ScoutPromptVersion
	if err := srv.checkEditorialBudget(r.Context(), profile.ID, nil, providerName, modelName); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	result, err := bundle.Scout.Scout(r.Context(), request)
	if err != nil {
		primary := providerName + "/" + modelName
		fallbackConfig, ok := srv.editorialFallbackConfig(r.Context(), editorialRoleScout)
		fallbackBundle, fallbackErr := srv.bundleFor(fallbackConfig)
		if !ok || fallbackErr != nil || fallbackBundle.Scout == nil {
			http.Error(w, "Scout 生成失败："+err.Error(), http.StatusBadRequest)
			return
		}
		providerName, modelName = fallbackBundle.Scout.Name(), provider.EffectiveTaskModel(fallbackConfig)
		request, fallbackErr = srv.scoutRequest(r, profile, providerName)
		if fallbackErr == nil {
			fallbackErr = srv.checkEditorialBudget(r.Context(), profile.ID, nil, providerName, modelName)
		}
		if fallbackErr == nil {
			result, fallbackErr = fallbackBundle.Scout.Scout(r.Context(), request)
		}
		if fallbackErr != nil {
			http.Error(w, "Scout 首选与备用 Provider 均失败："+fallbackErr.Error(), http.StatusBadRequest)
			return
		}
		result.Usage.FallbackFrom = primary
	}
	cost, err := srv.recordEditorialUsage(r.Context(), profile.ID, nil, "scout", "profile", profile.ID, providerName, modelName, promptVersion, result.Usage)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	existing, err := srv.store.ListArticleProposals(r.Context(), profileID)
	if err != nil {
		http.Error(w, "读取既有提案失败", http.StatusInternalServerError)
		return
	}
	titles := map[string]bool{}
	historicalTitles := make([]string, 0, len(existing))
	for _, proposal := range existing {
		normalized := normalizeEditorialTitle(proposal.Title)
		titles[normalized] = true
		historicalTitles = append(historicalTitles, normalized)
	}
	created := 0
	firstCreated := true
	for _, candidate := range result.Proposals {
		key := normalizeEditorialTitle(candidate.Title)
		if titles[key] || editorialTitleNearDuplicate(key, historicalTitles) {
			continue
		}
		ids, _ := json.Marshal(candidate.CandidateKeyPointIDs)
		proposalCost := (*int64)(nil)
		if firstCreated {
			proposalCost = cost
			firstCreated = false
		}
		if _, err := srv.store.CreateArticleProposal(r.Context(), models.ArticleProposal{EditorialProfileID: profileID, Kind: candidate.Kind, Title: candidate.Title, Thesis: candidate.Thesis, Audience: candidate.Audience, Rationale: candidate.Rationale, CandidateKeyPoints: string(ids), Provider: &providerName, Model: &modelName, PromptVersion: &promptVersion, CostCents: proposalCost}); err != nil {
			http.Error(w, "保存 Scout 提案失败", http.StatusInternalServerError)
			return
		}
		titles[key] = true
		historicalTitles = append(historicalTitles, key)
		created++
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
