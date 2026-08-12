// Content workbench HTTP handlers (ADR-0021 / roadmap Phase 8).
package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/woyin/orangecast/internal/auth"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
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
		briefs, err := srv.store.ListArticleBriefs(r.Context(), profile.ID)
		if err != nil {
			http.Error(w, "加载写作简报失败", http.StatusInternalServerError)
			return
		}
		data["Briefs"] = briefs
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

// handleArticleWriterRun generates an initial immutable revision only from a confirmed Brief.
func (srv *Server) handleArticleWriterRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}
	brief, err := srv.store.GetArticleBrief(r.Context(), strings.TrimSpace(r.FormValue("brief_id")))
	if err != nil || brief.Status != "confirmed" {
		http.Error(w, "只有已确认的 Brief 才能生成文章", http.StatusBadRequest)
		return
	}
	proposal, err := srv.store.GetArticleProposal(r.Context(), brief.ProposalID)
	if err != nil {
		http.Error(w, "读取选题失败", http.StatusInternalServerError)
		return
	}
	profile, err := srv.store.GetEditorialProfile(r.Context(), proposal.EditorialProfileID)
	if err != nil {
		http.Error(w, "读取编辑画像失败", http.StatusInternalServerError)
		return
	}
	request, err := srv.writerRequest(r, profile, brief, proposal)
	if err != nil {
		http.Error(w, "素材不满足写作条件："+err.Error(), http.StatusBadRequest)
		return
	}
	settings, err := srv.store.GetSettings(r.Context())
	if err != nil {
		http.Error(w, "读取 Writer 配置失败", http.StatusInternalServerError)
		return
	}
	bundle, err := srv.bundleFor(provider.TaskConfig{Provider: ptrStr(settings.AnalysisProvider), Model: ptrStr(settings.AnalysisModel)})
	if err != nil || bundle.Writer == nil {
		http.Error(w, "Writer Provider 不可用", http.StatusBadRequest)
		return
	}
	result, err := bundle.Writer.WriteArticle(request)
	if err != nil {
		http.Error(w, "生成文章失败："+err.Error(), http.StatusBadRequest)
		return
	}
	draft, err := srv.store.CreateArticleDraft(r.Context(), brief.ID, result.Title)
	if err != nil {
		http.Error(w, "创建文章草稿失败", http.StatusInternalServerError)
		return
	}
	providerName := bundle.Writer.Name()
	revision, err := srv.store.CreateArticleRevision(r.Context(), models.ArticleRevision{DraftID: draft.ID, Title: result.Title, Markdown: result.Markdown, Origin: "writer", Provider: &providerName})
	if err != nil {
		http.Error(w, "保存 Writer 修订失败", http.StatusInternalServerError)
		return
	}
	for _, mapping := range result.EvidenceMaps {
		ids, _ := json.Marshal(mapping.KeyPointIDs)
		if _, err := srv.store.CreateEvidenceMap(r.Context(), models.EvidenceMap{RevisionID: revision.ID, Kind: models.EvidenceMapKind(mapping.Kind), Excerpt: mapping.Excerpt, KeyPointIDs: string(ids)}); err != nil {
			http.Error(w, "保存证据映射失败", http.StatusInternalServerError)
			return
		}
	}
	http.Redirect(w, r, "/workbench/drafts/"+draft.ID, http.StatusSeeOther)
}

func (srv *Server) writerRequest(r *http.Request, profile *models.EditorialProfile, brief *models.ArticleBrief, proposal *models.ArticleProposal) (provider.ArticleWritingRequest, error) {
	var keyPointIDs []string
	if err := json.Unmarshal([]byte(brief.MaterialPlan), &keyPointIDs); err != nil || len(keyPointIDs) == 0 {
		return provider.ArticleWritingRequest{}, errors.New("Brief 必须选择至少一个 KeyPoint")
	}
	request := provider.ArticleWritingRequest{Title: proposal.Title, Thesis: brief.Thesis, Audience: brief.Audience, Outline: brief.Outline, Style: brief.Style, TargetLength: brief.TargetLength, SourceAttribution: profile.SourceAttribution}
	seen := map[string]bool{}
	for _, id := range keyPointIDs {
		if seen[id] {
			continue
		}
		seen[id] = true
		keyPoint, err := srv.store.GetKeyPoint(r.Context(), id)
		if err != nil {
			return provider.ArticleWritingRequest{}, err
		}
		usable, err := srv.store.CanUseSourceForPublication(r.Context(), profile.ID, keyPoint.SourceType, keyPoint.SourceID)
		if err != nil || !usable {
			return provider.ArticleWritingRequest{}, errors.New("存在未授权或不可公开的素材")
		}
		external, err := srv.store.CanSendSourceToExternalProvider(r.Context(), keyPoint.SourceType, keyPoint.SourceID)
		if err != nil || !external {
			return provider.ArticleWritingRequest{}, errors.New("存在不允许发送给外部 Writer 的素材")
		}
		var citations []string
		if err := json.Unmarshal([]byte(keyPoint.CitationsJSON), &citations); err != nil || len(citations) == 0 {
			return provider.ArticleWritingRequest{}, errors.New("存在无有效 Citation 的 KeyPoint")
		}
		request.Materials = append(request.Materials, provider.ArticleMaterial{KeyPointID: keyPoint.ID, SourceID: keyPoint.SourceID, SourceTitle: keyPoint.SourceTitle, Content: keyPoint.Content, Description: keyPoint.Description, Citations: citations})
	}
	return request, nil
}

// handleArticleProposalCreate records an Owner-created candidate topic.
func (srv *Server) handleArticleProposalCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}
	profileID := strings.TrimSpace(r.FormValue("profile_id"))
	proposal, err := srv.store.CreateArticleProposal(r.Context(), models.ArticleProposal{
		EditorialProfileID: profileID,
		Kind:               strings.TrimSpace(r.FormValue("kind")),
		Title:              strings.TrimSpace(r.FormValue("title")),
		Thesis:             strings.TrimSpace(r.FormValue("thesis")),
		Audience:           strings.TrimSpace(r.FormValue("audience")),
		Rationale:          strings.TrimSpace(r.FormValue("rationale")),
		CandidateKeyPoints: strings.TrimSpace(r.FormValue("candidate_keypoints")),
	})
	if err != nil {
		http.Error(w, "创建选题失败："+err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/workbench?profile="+proposal.EditorialProfileID, http.StatusSeeOther)
}

// handleArticleProposalStatus records the Owner's explicit proposal decision.
func (srv *Server) handleArticleProposalStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}
	profileID := strings.TrimSpace(r.FormValue("profile_id"))
	if err := srv.store.SetArticleProposalStatus(r.Context(), strings.TrimSpace(r.FormValue("proposal_id")), strings.TrimSpace(r.FormValue("status"))); err != nil {
		http.Error(w, "更新选题失败："+err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/workbench?profile="+profileID, http.StatusSeeOther)
}

// handleArticleBriefCreate records a reviewable material and structure contract.
func (srv *Server) handleArticleBriefCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}
	profileID := strings.TrimSpace(r.FormValue("profile_id"))
	targetLength, err := optionalTargetLength(r.FormValue("target_length"))
	if err != nil {
		http.Error(w, "目标字数必须是正整数", http.StatusBadRequest)
		return
	}
	brief, err := srv.store.CreateArticleBrief(r.Context(), models.ArticleBrief{
		ProposalID:   strings.TrimSpace(r.FormValue("proposal_id")),
		Thesis:       strings.TrimSpace(r.FormValue("thesis")),
		Audience:     strings.TrimSpace(r.FormValue("audience")),
		Outline:      strings.TrimSpace(r.FormValue("outline")),
		MaterialPlan: strings.TrimSpace(r.FormValue("material_plan")),
		ConflictPlan: strings.TrimSpace(r.FormValue("conflict_plan")),
		Style:        strings.TrimSpace(r.FormValue("style")),
		TargetLength: targetLength,
	})
	if err != nil {
		http.Error(w, "创建写作简报失败："+err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/workbench?profile="+profileID+"&brief="+brief.ID, http.StatusSeeOther)
}

// handleArticleBriefConfirm is the explicit authorization point for later writer jobs.
func (srv *Server) handleArticleBriefConfirm(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}
	profileID := strings.TrimSpace(r.FormValue("profile_id"))
	if err := srv.store.ConfirmArticleBrief(r.Context(), strings.TrimSpace(r.FormValue("brief_id"))); err != nil {
		http.Error(w, "确认写作简报失败："+err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/workbench?profile="+profileID, http.StatusSeeOther)
}

// handleArticleDraftCreate opens a durable draft only after the Owner confirmed its brief.
func (srv *Server) handleArticleDraftCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}
	draft, err := srv.store.CreateArticleDraft(r.Context(), strings.TrimSpace(r.FormValue("brief_id")), strings.TrimSpace(r.FormValue("title")))
	if err != nil {
		http.Error(w, "创建文章草稿失败："+err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/workbench/drafts/"+draft.ID, http.StatusSeeOther)
}

// handleArticleDraftDetail renders revision history and Owner editing controls for one draft.
func (srv *Server) handleArticleDraftDetail(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/workbench/drafts/"))
	if id == "" {
		http.NotFound(w, r)
		return
	}
	draft, err := srv.store.GetArticleDraft(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	revisions, err := srv.store.ListArticleRevisions(r.Context(), id)
	if err != nil {
		http.Error(w, "加载修订历史失败", http.StatusInternalServerError)
		return
	}
	currentMarkdown := ""
	if len(revisions) > 0 {
		currentMarkdown = revisions[0].Markdown
	}
	srv.tmpl.Render(w, "article_draft.html", map[string]any{"Draft": draft, "Revisions": revisions, "CurrentMarkdown": currentMarkdown, "CSRF": auth.CSRFValue(r)})
}

// handleArticleRevisionCreate appends an immutable Owner revision; it never overwrites text.
func (srv *Server) handleArticleRevisionCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}
	revision, err := srv.store.CreateArticleRevision(r.Context(), models.ArticleRevision{
		DraftID:  strings.TrimSpace(r.FormValue("draft_id")),
		Title:    strings.TrimSpace(r.FormValue("title")),
		Markdown: r.FormValue("markdown"),
		Origin:   "owner",
	})
	if err != nil {
		http.Error(w, "保存修订失败："+err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/workbench/drafts/"+revision.DraftID, http.StatusSeeOther)
}

// handleArticleReviewCreate records an exact-revision review; evidence status drives the hard gate.
func (srv *Server) handleArticleReviewCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}
	review, err := srv.store.CreateArticleReview(r.Context(), models.ArticleReview{
		RevisionID: strings.TrimSpace(r.FormValue("revision_id")),
		Kind:       strings.TrimSpace(r.FormValue("kind")),
		Status:     strings.TrimSpace(r.FormValue("status")),
		IssuesJSON: strings.TrimSpace(r.FormValue("issues")),
	})
	if err != nil {
		http.Error(w, "记录审校失败："+err.Error(), http.StatusBadRequest)
		return
	}
	revision, err := srv.store.GetArticleRevision(r.Context(), review.RevisionID)
	if err != nil {
		http.Error(w, "读取文章修订失败", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/workbench/drafts/"+revision.DraftID, http.StatusSeeOther)
}

// handleEvidenceReviewRun executes an independent evidence review for the current exact revision.
func (srv *Server) handleEvidenceReviewRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}
	revision, err := srv.store.GetArticleRevision(r.Context(), strings.TrimSpace(r.FormValue("revision_id")))
	if err != nil {
		http.Error(w, "读取文章修订失败", http.StatusBadRequest)
		return
	}
	request, err := srv.evidenceReviewRequest(r, revision)
	if err != nil {
		http.Error(w, "证据映射不满足审校条件："+err.Error(), http.StatusBadRequest)
		return
	}
	settings, err := srv.store.GetSettings(r.Context())
	if err != nil {
		http.Error(w, "读取审校配置失败", http.StatusInternalServerError)
		return
	}
	bundle, err := srv.bundleFor(provider.TaskConfig{Provider: ptrStr(settings.AnalysisProvider), Model: ptrStr(settings.AnalysisModel)})
	if err != nil || bundle.EvidenceReviewer == nil {
		http.Error(w, "EvidenceReviewer Provider 不可用", http.StatusBadRequest)
		return
	}
	result, err := bundle.EvidenceReviewer.ReviewEvidence(request)
	if err != nil {
		http.Error(w, "证据审校失败："+err.Error(), http.StatusBadRequest)
		return
	}
	issues, _ := json.Marshal(result.Issues)
	providerName := bundle.EvidenceReviewer.Name()
	if _, err := srv.store.CreateArticleReview(r.Context(), models.ArticleReview{RevisionID: revision.ID, Kind: "evidence", Status: result.Status, IssuesJSON: string(issues), Provider: &providerName}); err != nil {
		http.Error(w, "保存证据审校失败", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/workbench/drafts/"+revision.DraftID, http.StatusSeeOther)
}

// handleStyleReviewRun records independent, non-blocking style advice for an exact revision.
func (srv *Server) handleStyleReviewRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}
	revision, err := srv.store.GetArticleRevision(r.Context(), strings.TrimSpace(r.FormValue("revision_id")))
	if err != nil {
		http.Error(w, "读取文章修订失败", http.StatusBadRequest)
		return
	}
	request, err := srv.styleReviewRequest(r, revision)
	if err != nil {
		http.Error(w, "读取编辑画像失败", http.StatusBadRequest)
		return
	}
	settings, err := srv.store.GetSettings(r.Context())
	if err != nil {
		http.Error(w, "读取审校配置失败", http.StatusInternalServerError)
		return
	}
	bundle, err := srv.bundleFor(provider.TaskConfig{Provider: ptrStr(settings.AnalysisProvider), Model: ptrStr(settings.AnalysisModel)})
	if err != nil || bundle.StyleEditor == nil {
		http.Error(w, "StyleEditor Provider 不可用", http.StatusBadRequest)
		return
	}
	result, err := bundle.StyleEditor.ReviewStyle(request)
	if err != nil {
		http.Error(w, "风格审校失败："+err.Error(), http.StatusBadRequest)
		return
	}
	issues, _ := json.Marshal(result.Issues)
	providerName := bundle.StyleEditor.Name()
	if _, err := srv.store.CreateArticleReview(r.Context(), models.ArticleReview{RevisionID: revision.ID, Kind: "style", Status: result.Status, IssuesJSON: string(issues), Provider: &providerName}); err != nil {
		http.Error(w, "保存风格审校失败", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/workbench/drafts/"+revision.DraftID, http.StatusSeeOther)
}

func (srv *Server) styleReviewRequest(r *http.Request, revision *models.ArticleRevision) (provider.StyleReviewRequest, error) {
	draft, err := srv.store.GetArticleDraft(r.Context(), revision.DraftID)
	if err != nil {
		return provider.StyleReviewRequest{}, err
	}
	brief, err := srv.store.GetArticleBrief(r.Context(), draft.BriefID)
	if err != nil {
		return provider.StyleReviewRequest{}, err
	}
	proposal, err := srv.store.GetArticleProposal(r.Context(), brief.ProposalID)
	if err != nil {
		return provider.StyleReviewRequest{}, err
	}
	profile, err := srv.store.GetEditorialProfile(r.Context(), proposal.EditorialProfileID)
	if err != nil {
		return provider.StyleReviewRequest{}, err
	}
	return provider.StyleReviewRequest{Title: revision.Title, Markdown: revision.Markdown, TargetAudience: profile.TargetAudience, Voice: profile.Voice, StyleGuide: profile.StyleGuide, TargetLength: brief.TargetLength}, nil
}

func (srv *Server) evidenceReviewRequest(r *http.Request, revision *models.ArticleRevision) (provider.EvidenceReviewRequest, error) {
	draft, err := srv.store.GetArticleDraft(r.Context(), revision.DraftID)
	if err != nil {
		return provider.EvidenceReviewRequest{}, err
	}
	brief, err := srv.store.GetArticleBrief(r.Context(), draft.BriefID)
	if err != nil {
		return provider.EvidenceReviewRequest{}, err
	}
	proposal, err := srv.store.GetArticleProposal(r.Context(), brief.ProposalID)
	if err != nil {
		return provider.EvidenceReviewRequest{}, err
	}
	maps, err := srv.store.ListEvidenceMaps(r.Context(), revision.ID)
	if err != nil {
		return provider.EvidenceReviewRequest{}, err
	}
	if len(maps) == 0 {
		return provider.EvidenceReviewRequest{}, errors.New("Revision 没有 EvidenceMap")
	}
	request := provider.EvidenceReviewRequest{Title: revision.Title, Markdown: revision.Markdown}
	for _, mapping := range maps {
		if !strings.Contains(revision.Markdown, mapping.Excerpt) {
			return provider.EvidenceReviewRequest{}, fmt.Errorf("EvidenceMap 摘录未出现在正文中")
		}
		var ids []string
		if err := json.Unmarshal([]byte(mapping.KeyPointIDs), &ids); err != nil {
			return provider.EvidenceReviewRequest{}, err
		}
		item := provider.EvidenceReviewItem{Kind: string(mapping.Kind), Excerpt: mapping.Excerpt}
		for _, id := range ids {
			keyPoint, err := srv.store.GetKeyPoint(r.Context(), id)
			if err != nil {
				return provider.EvidenceReviewRequest{}, err
			}
			usable, err := srv.store.CanUseSourceForPublication(r.Context(), proposal.EditorialProfileID, keyPoint.SourceType, keyPoint.SourceID)
			if err != nil || !usable {
				return provider.EvidenceReviewRequest{}, errors.New("EvidenceMap 包含未授权或不可公开素材")
			}
			external, err := srv.store.CanSendSourceToExternalProvider(r.Context(), keyPoint.SourceType, keyPoint.SourceID)
			if err != nil || !external {
				return provider.EvidenceReviewRequest{}, errors.New("EvidenceMap 包含不可发送给审校模型的素材")
			}
			var citations []string
			if err := json.Unmarshal([]byte(keyPoint.CitationsJSON), &citations); err != nil || len(citations) == 0 {
				return provider.EvidenceReviewRequest{}, errors.New("EvidenceMap 包含无 Citation KeyPoint")
			}
			item.Materials = append(item.Materials, provider.ArticleMaterial{KeyPointID: keyPoint.ID, SourceID: keyPoint.SourceID, SourceTitle: keyPoint.SourceTitle, Content: keyPoint.Content, Description: keyPoint.Description, Citations: citations})
		}
		if mapping.Kind != models.EvidenceRhetorical && len(item.Materials) == 0 {
			return provider.EvidenceReviewRequest{}, errors.New("事实表达缺少证据素材")
		}
		request.Items = append(request.Items, item)
	}
	return request, nil
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

func optionalTargetLength(raw string) (*int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return nil, errBudget
	}
	return &value, nil
}
