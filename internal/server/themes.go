package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/woyin/orangecast/internal/auth"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
)

type themeMaterialView struct {
	KeyPointID   string
	SourceTitle  string
	SourceID     string
	Content      string
	Relationship string
	Missing      bool
}

type themePageView struct {
	ID              string
	Name            string
	Description     string
	Status          string
	Materials       []themeMaterialView
	DeepReadSources []themeSourceOption
	MaterialCount   int
	SourceCount     int
	Ready           bool
}

type themeSourceOption struct {
	ID            string
	SourceType    models.SourceType
	Title         string
	MaterialCount int
}

type themeKeyPointOption struct {
	ID          string
	SourceTitle string
	Content     string
}

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
		views := make([]themePageView, 0, len(themes))
		for _, theme := range themes {
			view := themePageView{ID: theme.ID, Name: theme.Name, Description: theme.Description, Status: theme.Status}
			relations, err := srv.store.ListThemeKeyPoints(r.Context(), theme.ID)
			if err != nil {
				http.Error(w, "加载主题材料失败", http.StatusInternalServerError)
				return
			}
			sources := map[string]bool{}
			sourceOptions := map[string]*themeSourceOption{}
			for _, relation := range relations {
				material := themeMaterialView{KeyPointID: relation.KeyPointID, Relationship: relation.Relationship}
				keyPoint, keyPointErr := srv.store.GetKeyPoint(r.Context(), relation.KeyPointID)
				if keyPointErr != nil {
					material.Missing = true
					view.Materials = append(view.Materials, material)
					continue
				}
				material.SourceTitle = keyPoint.SourceTitle
				material.SourceID = keyPoint.SourceID
				material.Content = keyPoint.Content
				sourceKey := string(keyPoint.SourceType) + "|" + keyPoint.SourceID
				sources[sourceKey] = true
				if keyPoint.SourceType == models.SourceEpisode {
					if option := sourceOptions[sourceKey]; option == nil {
						sourceOptions[sourceKey] = &themeSourceOption{ID: keyPoint.SourceID, SourceType: keyPoint.SourceType, Title: keyPoint.SourceTitle, MaterialCount: 1}
					} else {
						option.MaterialCount++
					}
				}
				view.Materials = append(view.Materials, material)
			}
			view.MaterialCount = len(view.Materials)
			view.SourceCount = len(sources)
			for _, option := range sourceOptions {
				view.DeepReadSources = append(view.DeepReadSources, *option)
			}
			sort.Slice(view.DeepReadSources, func(i, j int) bool { return view.DeepReadSources[i].Title < view.DeepReadSources[j].Title })
			view.Ready = theme.Status == "confirmed" && view.SourceCount >= 2
			views = append(views, view)
		}
		keyPoints, _, err := srv.store.ListKeyPointsFiltered(r.Context(), store.KeyPointFilter{}, 1, 500)
		if err != nil {
			http.Error(w, "加载 KeyPoint 选项失败", http.StatusInternalServerError)
			return
		}
		// SourceScope is no longer a creative-use gate. Archived Sources are
		// still rejected by AddKeyPointToTheme when the Owner chooses one.
		options := make([]themeKeyPointOption, 0, len(keyPoints))
		for _, keyPoint := range keyPoints {
			if keyPoint.QualityStatus != models.KeyPointReady && keyPoint.QualityStatus != models.KeyPointOwnerConfirmed || keyPoint.StaleAt != "" {
				continue
			}
			eligible, eligibilityErr := srv.store.IsKeyPointEligibleForProfile(r.Context(), profile.ID, keyPoint.ID)
			if eligibilityErr != nil || !eligible {
				continue
			}
			options = append(options, themeKeyPointOption{ID: keyPoint.ID, SourceTitle: keyPoint.SourceTitle, Content: keyPoint.Content})
		}
		data["Profile"] = profile
		data["Themes"] = themes
		data["ThemeViews"] = views
		data["KeyPointOptions"] = options
	}
	if err := srv.tmpl.Render(w, "themes.html", data); err != nil {
		http.Error(w, "渲染主题失败", http.StatusInternalServerError)
	}
}

// handleScoutRun asks Scout for up to the target number of distinct candidates.
// A smaller result is valid when the available material cannot support more.
func (srv *Server) handleScoutRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}
	profileID := strings.TrimSpace(r.FormValue("profile_id"))
	options := scoutOptions{Mode: strings.TrimSpace(r.FormValue("mode")), SourceID: strings.TrimSpace(r.FormValue("source_id")), ThemeID: strings.TrimSpace(r.FormValue("theme_id")), ProposalCount: scoutProposalTarget}
	created, err := srv.runScoutWithOptions(r, profileID, options)
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
	return srv.scoutRequestWithOptions(r.Context(), profile, providerName, scoutOptions{Mode: provider.ScoutModeCrossEpisode})
}

func (srv *Server) scoutRequestWithOptions(ctx context.Context, profile *models.EditorialProfile, providerName string, options scoutOptions) (provider.ScoutRequest, error) {
	mode := options.Mode
	if mode == "" {
		mode = provider.ScoutModeCrossEpisode
	}
	if mode != provider.ScoutModeCrossEpisode && mode != provider.ScoutModeDeepRead {
		return provider.ScoutRequest{}, fmt.Errorf("未知 Scout 模式")
	}
	if mode == provider.ScoutModeDeepRead && strings.TrimSpace(options.SourceID) == "" {
		return provider.ScoutRequest{}, fmt.Errorf("单集深读必须明确选择一个 Episode")
	}
	themes, err := srv.store.ListThemes(ctx, profile.ID)
	if err != nil {
		return provider.ScoutRequest{}, err
	}
	request := provider.ScoutRequest{Audience: profile.TargetAudience, Voice: profile.Voice, Mode: mode, SourceID: strings.TrimSpace(options.SourceID), ProposalCount: options.ProposalCount}
	for _, theme := range themes {
		if theme.Status != "confirmed" || options.ThemeID != "" && theme.ID != options.ThemeID {
			continue
		}
		relations, err := srv.store.ListThemeKeyPoints(ctx, theme.ID)
		if err != nil {
			return provider.ScoutRequest{}, err
		}
		scoutTheme := provider.ScoutTheme{ID: theme.ID, Name: theme.Name, Description: theme.Description}
		sources := map[string]bool{}
		for _, relation := range relations {
			keyPoint, err := srv.store.GetKeyPoint(ctx, relation.KeyPointID)
			if err != nil {
				return provider.ScoutRequest{}, err
			}
			if keyPoint.QualityStatus != models.KeyPointReady && keyPoint.QualityStatus != models.KeyPointOwnerConfirmed || keyPoint.StaleAt != "" {
				return provider.ScoutRequest{}, fmt.Errorf("Theme %q 包含尚未通过学习质量闸门的 KeyPoint", theme.Name)
			}
			eligible, eligibilityErr := srv.store.IsKeyPointEligibleForProfile(ctx, profile.ID, keyPoint.ID)
			if eligibilityErr != nil || !eligible {
				return provider.ScoutRequest{}, fmt.Errorf("Theme %q 包含已被当前画像排除的 KeyPoint", theme.Name)
			}
			if mode == provider.ScoutModeCrossEpisode && keyPoint.SourceType != models.SourceEpisode {
				continue
			}
			if mode == provider.ScoutModeDeepRead && keyPoint.SourceType != models.SourceEpisode {
				continue
			}
			if options.SourceID != "" && keyPoint.SourceID != options.SourceID {
				continue
			}
			usable, err := srv.store.CanUseSourceForPublication(ctx, profile.ID, keyPoint.SourceType, keyPoint.SourceID)
			if err != nil || !usable {
				return provider.ScoutRequest{}, fmt.Errorf("Theme %q 包含不可用素材", theme.Name)
			}
			external, err := srv.store.CanSendSourceToProvider(ctx, keyPoint.SourceType, keyPoint.SourceID, providerName)
			if err != nil || !external {
				return provider.ScoutRequest{}, fmt.Errorf("Theme %q 包含不可发送给 Scout 的素材", theme.Name)
			}
			var citations []string
			if err := json.Unmarshal([]byte(keyPoint.CitationsJSON), &citations); err != nil || len(citations) == 0 {
				return provider.ScoutRequest{}, fmt.Errorf("Theme %q 包含无 Citation KeyPoint", theme.Name)
			}
			scoutTheme.Materials = append(scoutTheme.Materials, provider.ArticleMaterial{KeyPointID: keyPoint.ID, SourceID: keyPoint.SourceID, SourceTitle: keyPoint.SourceTitle, Content: keyPoint.Content, Description: keyPoint.Description, Citations: citations})
			sources[string(keyPoint.SourceType)+"|"+keyPoint.SourceID] = true
		}
		if mode == provider.ScoutModeDeepRead && len(sources) >= 1 || mode == provider.ScoutModeCrossEpisode && len(sources) >= 2 {
			request.Themes = append(request.Themes, scoutTheme)
		}
	}
	if len(request.Themes) == 0 {
		if mode == provider.ScoutModeDeepRead {
			return provider.ScoutRequest{}, fmt.Errorf("需要一个已确认且包含所选 Episode KeyPoint 的 Theme")
		}
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
