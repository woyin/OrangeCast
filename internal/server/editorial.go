// Content workbench HTTP handlers (ADR-0021 / roadmap Phase 8).
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"strconv"
	"strings"

	"github.com/woyin/orangecast/internal/auth"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
)

var errBudget = errors.New("invalid budget")

// editorialBoard is a compact, mutually intelligible view of an Owner's
// production queue. It deliberately derives state from immutable workflow
// objects instead of creating another mutable board record.
type editorialBoard struct {
	ProposalPool  int
	BriefsPending int
	ReadyToWrite  int
	Reviewing     int
	Blocked       int
	Ready         int
	Archived      int
}

// handleWorkbench renders the initial personal editorial board.
func (srv *Server) handleWorkbench(w http.ResponseWriter, r *http.Request) {
	profiles, err := srv.store.ListEditorialProfiles(r.Context())
	if err != nil {
		http.Error(w, "加载编辑画像失败", http.StatusInternalServerError)
		return
	}
	prices, err := srv.store.ListModelPrices(r.Context())
	if err != nil {
		http.Error(w, "加载模型价格失败", http.StatusInternalServerError)
		return
	}
	data := map[string]any{"Profiles": profiles, "ModelPrices": prices, "CSRF": auth.CSRFValue(r)}
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
		data["Board"] = buildEditorialBoard(proposals, briefs, drafts)
		scopes, err := srv.store.ListScopedSources(r.Context(), profile.ID)
		if err != nil {
			http.Error(w, "加载素材范围失败", http.StatusInternalServerError)
			return
		}
		data["Scopes"] = scopes
		options, err := srv.store.ListSourceOptions(r.Context())
		if err != nil {
			http.Error(w, "加载可选素材失败", http.StatusInternalServerError)
			return
		}
		data["SourceOptions"] = options
	}
	if err := srv.tmpl.Render(w, "workbench.html", data); err != nil {
		http.Error(w, "渲染工作台失败", http.StatusInternalServerError)
	}
}

func buildEditorialBoard(proposals []*models.ArticleProposal, briefs []*models.ArticleBrief, drafts []*models.ArticleDraft) editorialBoard {
	board := editorialBoard{}
	draftsByBrief := make(map[string]bool, len(drafts))
	for _, proposal := range proposals {
		if proposal.Status == "proposed" {
			board.ProposalPool++
		}
	}
	for _, draft := range drafts {
		draftsByBrief[draft.BriefID] = true
		switch draft.Status {
		case "drafting", "reviewing":
			board.Reviewing++
		case "blocked":
			board.Blocked++
		case "ready":
			board.Ready++
		case "archived":
			board.Archived++
		}
	}
	for _, brief := range briefs {
		switch brief.Status {
		case "draft":
			board.BriefsPending++
		case "confirmed":
			if !draftsByBrief[brief.ID] {
				board.ReadyToWrite++
			}
		}
	}
	return board
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
	settings, err := srv.store.GetSettings(r.Context())
	if err != nil {
		http.Error(w, "读取 Writer 配置失败", http.StatusInternalServerError)
		return
	}
	taskConfig := editorialTaskConfig(settings, editorialRoleWriter)
	bundle, err := srv.bundleFor(taskConfig)
	if err != nil || bundle.Writer == nil {
		http.Error(w, "Writer Provider 不可用", http.StatusBadRequest)
		return
	}
	claimed, err := srv.store.ClaimEditorialTask(r.Context(), "writer_initial", brief.ID)
	if err != nil {
		http.Error(w, "领取 Writer 任务失败", http.StatusInternalServerError)
		return
	}
	if !claimed {
		if existing, findErr := srv.store.GetArticleDraftByBrief(r.Context(), brief.ID); findErr == nil {
			http.Redirect(w, r, "/workbench/drafts/"+existing.ID, http.StatusSeeOther)
			return
		}
		http.Error(w, "该 Brief 的 Writer 任务正在执行，请稍后刷新", http.StatusConflict)
		return
	}
	finishContext := context.WithoutCancel(r.Context())
	finished := false
	defer func() {
		if !finished {
			_ = srv.store.FinishEditorialTask(finishContext, "writer_initial", brief.ID, errors.New("Writer 任务未完成"))
		}
	}()
	request, err := srv.writerRequest(r, profile, brief, proposal, bundle.Writer.Name())
	if err != nil {
		http.Error(w, "素材不满足写作条件："+err.Error(), http.StatusBadRequest)
		return
	}
	draft, err := srv.store.GetArticleDraftByBrief(r.Context(), brief.ID)
	if errors.Is(err, store.ErrNotFound) {
		draft, err = srv.store.CreateArticleDraft(r.Context(), brief.ID, proposal.Title)
	}
	if err != nil {
		http.Error(w, "创建文章草稿失败", http.StatusInternalServerError)
		return
	}
	if draft.CurrentRevisionID != nil {
		_ = srv.store.FinishEditorialTask(finishContext, "writer_initial", brief.ID, nil)
		finished = true
		http.Redirect(w, r, "/workbench/drafts/"+draft.ID, http.StatusSeeOther)
		return
	}
	providerName := bundle.Writer.Name()
	modelName := provider.EffectiveTaskModel(taskConfig)
	promptVersion := provider.ArticleWriterPromptVersion
	var result *provider.ArticleWritingResult
	var cost *int64
	if payload, cacheErr := srv.store.GetEditorialTaskResult(r.Context(), "writer_initial", brief.ID); cacheErr == nil {
		var cached cachedWriterResult
		if json.Unmarshal([]byte(payload), &cached) != nil || cached.Result == nil {
			http.Error(w, "Writer 缓存结果损坏", http.StatusInternalServerError)
			return
		}
		result, providerName, modelName, promptVersion = cached.Result, cached.Provider, cached.Model, cached.PromptVersion
		cachedCost := cached.CostCents
		cost = &cachedCost
	} else if !errors.Is(cacheErr, store.ErrNotFound) {
		http.Error(w, "读取 Writer 缓存失败", http.StatusInternalServerError)
		return
	}
	if result == nil {
		if err := srv.checkEditorialBudget(r.Context(), profile.ID, &draft.ID, providerName, modelName); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		result, err = bundle.Writer.WriteArticle(r.Context(), request)
		if err != nil {
			primary := providerName + "/" + modelName
			fallbackConfig, ok := srv.editorialFallbackConfig(r.Context(), editorialRoleWriter)
			fallbackBundle, fallbackErr := srv.bundleFor(fallbackConfig)
			if !ok || fallbackErr != nil || fallbackBundle.Writer == nil {
				http.Error(w, "生成文章失败："+err.Error(), http.StatusBadRequest)
				return
			}
			providerName, modelName = fallbackBundle.Writer.Name(), provider.EffectiveTaskModel(fallbackConfig)
			request, fallbackErr = srv.writerRequest(r, profile, brief, proposal, providerName)
			if fallbackErr == nil {
				fallbackErr = srv.checkEditorialBudget(r.Context(), profile.ID, &draft.ID, providerName, modelName)
			}
			if fallbackErr == nil {
				result, fallbackErr = fallbackBundle.Writer.WriteArticle(r.Context(), request)
			}
			if fallbackErr != nil {
				http.Error(w, "Writer 首选与备用 Provider 均失败："+fallbackErr.Error(), http.StatusBadRequest)
				return
			}
			result.Usage.FallbackFrom = primary
		}
		cost, err = srv.recordEditorialUsage(r.Context(), profile.ID, &draft.ID, "writer_initial", "draft", draft.ID, providerName, modelName, promptVersion, result.Usage)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		encoded, _ := json.Marshal(cachedWriterResult{Result: result, Provider: providerName, Model: modelName, PromptVersion: promptVersion, CostCents: *cost})
		if err := srv.store.SaveEditorialTaskResult(r.Context(), "writer_initial", brief.ID, string(encoded)); err != nil {
			http.Error(w, "缓存 Writer 结果失败", http.StatusInternalServerError)
			return
		}
	}
	evidenceMaps := make([]models.EvidenceMap, 0, len(result.EvidenceMaps))
	for _, mapping := range result.EvidenceMaps {
		ids, _ := json.Marshal(mapping.KeyPointIDs)
		evidenceMaps = append(evidenceMaps, models.EvidenceMap{Kind: models.EvidenceMapKind(mapping.Kind), Excerpt: mapping.Excerpt, KeyPointIDs: string(ids)})
	}
	_, err = srv.store.CreateArticleRevisionWithEvidenceMaps(r.Context(), models.ArticleRevision{DraftID: draft.ID, Title: result.Title, Markdown: result.Markdown, Origin: "writer", Provider: &providerName, Model: &modelName, PromptVersion: &promptVersion, CostCents: cost}, evidenceMaps)
	if err != nil {
		http.Error(w, "保存 Writer 修订或证据映射失败", http.StatusInternalServerError)
		return
	}
	if err := srv.store.FinishEditorialTask(finishContext, "writer_initial", brief.ID, nil); err != nil {
		http.Error(w, "完成 Writer 任务记录失败", http.StatusInternalServerError)
		return
	}
	finished = true
	http.Redirect(w, r, "/workbench/drafts/"+draft.ID, http.StatusSeeOther)
}

// handleArticleRevisionWriterRun turns review findings into a new immutable AI edit.
func (srv *Server) handleArticleRevisionWriterRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}
	revision, err := srv.store.GetArticleRevision(r.Context(), strings.TrimSpace(r.FormValue("revision_id")))
	if err != nil {
		http.Error(w, "读取文章修订失败", http.StatusBadRequest)
		return
	}
	draft, err := srv.store.GetArticleDraft(r.Context(), revision.DraftID)
	if err != nil {
		http.Error(w, "读取草稿失败", http.StatusBadRequest)
		return
	}
	brief, err := srv.store.GetArticleBrief(r.Context(), draft.BriefID)
	if err != nil {
		http.Error(w, "读取 Brief 失败", http.StatusBadRequest)
		return
	}
	proposal, err := srv.store.GetArticleProposal(r.Context(), brief.ProposalID)
	if err != nil {
		http.Error(w, "读取选题失败", http.StatusBadRequest)
		return
	}
	profile, err := srv.store.GetEditorialProfile(r.Context(), proposal.EditorialProfileID)
	if err != nil {
		http.Error(w, "读取编辑画像失败", http.StatusBadRequest)
		return
	}
	settings, err := srv.store.GetSettings(r.Context())
	if err != nil {
		http.Error(w, "读取 Writer 配置失败", http.StatusInternalServerError)
		return
	}
	taskConfig := editorialTaskConfig(settings, editorialRoleWriter)
	bundle, err := srv.bundleFor(taskConfig)
	if err != nil || bundle.Writer == nil {
		http.Error(w, "Writer Provider 不可用", http.StatusBadRequest)
		return
	}
	request, err := srv.writerRequest(r, profile, brief, proposal, bundle.Writer.Name())
	if err != nil {
		http.Error(w, "素材不满足写作条件："+err.Error(), http.StatusBadRequest)
		return
	}
	reviews, err := srv.store.ListArticleReviews(r.Context(), revision.ID)
	if err != nil {
		http.Error(w, "读取审校记录失败", http.StatusInternalServerError)
		return
	}
	for _, review := range reviews {
		var issues []string
		if json.Unmarshal([]byte(review.IssuesJSON), &issues) == nil {
			request.RevisionFeedback = append(request.RevisionFeedback, issues...)
		}
	}
	if len(request.RevisionFeedback) == 0 {
		http.Error(w, "该修订没有可供处理的审校反馈", http.StatusBadRequest)
		return
	}
	request.Title, request.ExistingMarkdown = revision.Title, revision.Markdown
	providerName := bundle.Writer.Name()
	modelName := provider.EffectiveTaskModel(taskConfig)
	promptVersion := provider.ArticleWriterPromptVersion
	if err := srv.checkEditorialBudget(r.Context(), profile.ID, &draft.ID, providerName, modelName); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	result, err := bundle.Writer.WriteArticle(r.Context(), request)
	if err != nil {
		primary := providerName + "/" + modelName
		fallbackConfig, ok := srv.editorialFallbackConfig(r.Context(), editorialRoleWriter)
		fallbackBundle, fallbackErr := srv.bundleFor(fallbackConfig)
		if !ok || fallbackErr != nil || fallbackBundle.Writer == nil {
			http.Error(w, "生成新修订失败："+err.Error(), http.StatusBadRequest)
			return
		}
		providerName, modelName = fallbackBundle.Writer.Name(), provider.EffectiveTaskModel(fallbackConfig)
		request, fallbackErr = srv.writerRequest(r, profile, brief, proposal, providerName)
		if fallbackErr == nil {
			for _, review := range reviews {
				var issues []string
				if json.Unmarshal([]byte(review.IssuesJSON), &issues) == nil {
					request.RevisionFeedback = append(request.RevisionFeedback, issues...)
				}
			}
			request.Title, request.ExistingMarkdown = revision.Title, revision.Markdown
			fallbackErr = srv.checkEditorialBudget(r.Context(), profile.ID, &draft.ID, providerName, modelName)
		}
		if fallbackErr == nil {
			result, fallbackErr = fallbackBundle.Writer.WriteArticle(r.Context(), request)
		}
		if fallbackErr != nil {
			http.Error(w, "Writer 首选与备用 Provider 均失败："+fallbackErr.Error(), http.StatusBadRequest)
			return
		}
		result.Usage.FallbackFrom = primary
	}
	cost, err := srv.recordEditorialUsage(r.Context(), profile.ID, &draft.ID, "writer_revision", "revision", revision.ID, providerName, modelName, promptVersion, result.Usage)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	evidenceMaps := make([]models.EvidenceMap, 0, len(result.EvidenceMaps))
	for _, mapping := range result.EvidenceMaps {
		ids, _ := json.Marshal(mapping.KeyPointIDs)
		evidenceMaps = append(evidenceMaps, models.EvidenceMap{Kind: models.EvidenceMapKind(mapping.Kind), Excerpt: mapping.Excerpt, KeyPointIDs: string(ids)})
	}
	_, err = srv.store.CreateArticleRevisionWithEvidenceMaps(r.Context(), models.ArticleRevision{DraftID: draft.ID, Title: result.Title, Markdown: result.Markdown, Origin: "ai_edit", Provider: &providerName, Model: &modelName, PromptVersion: &promptVersion, CostCents: cost}, evidenceMaps)
	if err != nil {
		http.Error(w, "保存 Writer 修订或证据映射失败", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/workbench/drafts/"+draft.ID, http.StatusSeeOther)
}

func (srv *Server) writerRequest(r *http.Request, profile *models.EditorialProfile, brief *models.ArticleBrief, proposal *models.ArticleProposal, providerName string) (provider.ArticleWritingRequest, error) {
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
		external, err := srv.store.CanSendSourceToProvider(r.Context(), keyPoint.SourceType, keyPoint.SourceID, providerName)
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
	var currentRevision *models.ArticleRevision
	reviewsByRevision := make(map[string][]articleReviewView, len(revisions))
	byID := make(map[string]*models.ArticleRevision, len(revisions))
	if len(revisions) > 0 {
		currentMarkdown = revisions[0].Markdown
	}
	allReviews, err := srv.store.ListArticleReviewsForDraft(r.Context(), id)
	if err != nil {
		http.Error(w, "加载审校记录失败", http.StatusInternalServerError)
		return
	}
	for _, revision := range revisions {
		byID[revision.ID] = revision
		if draft.CurrentRevisionID != nil && revision.ID == *draft.CurrentRevisionID {
			currentRevision = revision
		}
		for _, review := range allReviews[revision.ID] {
			view := articleReviewView{ArticleReview: review}
			if err := json.Unmarshal([]byte(review.IssuesJSON), &view.Issues); err != nil {
				view.Issues = []string{"审校记录格式异常"}
			}
			reviewsByRevision[revision.ID] = append(reviewsByRevision[revision.ID], view)
		}
	}
	var comparison *revisionComparison
	fromID, toID := strings.TrimSpace(r.URL.Query().Get("compare_from")), strings.TrimSpace(r.URL.Query().Get("compare_to"))
	if fromID != "" || toID != "" {
		from, to := byID[fromID], byID[toID]
		if fromID == "" || toID == "" || from == nil || to == nil || fromID == toID {
			http.Error(w, "请选择同一文章的两个不同修订进行对比", http.StatusBadRequest)
			return
		}
		comparison = &revisionComparison{From: from, To: to, Lines: lineDiff(from.Markdown, to.Markdown)}
	}
	currentReady := false
	if currentRevision != nil {
		currentReady, err = srv.store.IsRevisionReadyForPublication(r.Context(), currentRevision.ID)
		if err != nil {
			http.Error(w, "检查当前修订证据门禁失败", http.StatusInternalServerError)
			return
		}
	}
	srv.tmpl.Render(w, "article_draft.html", map[string]any{"Draft": draft, "Revisions": revisions, "HasComparableRevisions": len(revisions) > 1, "ReviewsByRevision": reviewsByRevision, "Comparison": comparison, "CurrentMarkdown": currentMarkdown, "CurrentRevision": currentRevision, "CurrentRichHTML": template.HTML(wechatRichText(currentMarkdown)), "CurrentReady": currentReady, "CSRF": auth.CSRFValue(r)})
}

type articleReviewView struct {
	*models.ArticleReview
	Issues []string
}

type revisionComparison struct {
	From  *models.ArticleRevision
	To    *models.ArticleRevision
	Lines []revisionDiffLine
}

// handleArticleRevisionCreate appends an immutable Owner revision; it never overwrites text.
func (srv *Server) handleArticleRevisionCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}
	draftID, markdown := strings.TrimSpace(r.FormValue("draft_id")), r.FormValue("markdown")
	inheritedMaps, err := srv.inheritedEvidenceMaps(r, draftID, markdown)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "保存修订失败：文章草稿不存在", http.StatusBadRequest)
			return
		}
		http.Error(w, "读取当前证据映射失败", http.StatusInternalServerError)
		return
	}
	revision, err := srv.store.CreateArticleRevisionWithEvidenceMaps(r.Context(), models.ArticleRevision{
		DraftID: draftID, Title: strings.TrimSpace(r.FormValue("title")), Markdown: markdown, Origin: "owner",
	}, inheritedMaps)
	if err != nil {
		http.Error(w, "保存修订失败："+err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/workbench/drafts/"+revision.DraftID, http.StatusSeeOther)
}

// inheritedEvidenceMaps retains only mappings whose exact supported expression
// survives an Owner edit. Changed or removed expressions intentionally lose
// their old mapping and must be reviewed with fresh evidence rather than being
// silently blessed by a previous revision.
func (srv *Server) inheritedEvidenceMaps(r *http.Request, draftID, markdown string) ([]models.EvidenceMap, error) {
	draft, err := srv.store.GetArticleDraft(r.Context(), draftID)
	if err != nil || draft.CurrentRevisionID == nil {
		return nil, err
	}
	previousMaps, err := srv.store.ListEvidenceMaps(r.Context(), *draft.CurrentRevisionID)
	if err != nil {
		return nil, err
	}
	inherited := make([]models.EvidenceMap, 0, len(previousMaps))
	for _, mapping := range previousMaps {
		if strings.TrimSpace(mapping.Excerpt) == "" || !strings.Contains(markdown, mapping.Excerpt) {
			continue
		}
		inherited = append(inherited, models.EvidenceMap{Kind: mapping.Kind, Excerpt: mapping.Excerpt, KeyPointIDs: mapping.KeyPointIDs})
	}
	return inherited, nil
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
	settings, err := srv.store.GetSettings(r.Context())
	if err != nil {
		http.Error(w, "读取审校配置失败", http.StatusInternalServerError)
		return
	}
	taskConfig := editorialTaskConfig(settings, editorialRoleEvidence)
	bundle, err := srv.bundleFor(taskConfig)
	if err != nil || bundle.EvidenceReviewer == nil {
		http.Error(w, "EvidenceReviewer Provider 不可用", http.StatusBadRequest)
		return
	}
	request, err := srv.evidenceReviewRequest(r, revision, bundle.EvidenceReviewer.Name())
	if err != nil {
		http.Error(w, "证据映射不满足审校条件："+err.Error(), http.StatusBadRequest)
		return
	}
	draft, _, _, profile, err := srv.editorialContextForRevision(r.Context(), revision)
	if err != nil {
		http.Error(w, "读取文章上下文失败", http.StatusBadRequest)
		return
	}
	providerName := bundle.EvidenceReviewer.Name()
	modelName := provider.EffectiveTaskModel(taskConfig)
	promptVersion := provider.EvidenceReviewerPromptVersion
	if err := srv.checkEditorialBudget(r.Context(), profile.ID, &draft.ID, providerName, modelName); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	result, err := bundle.EvidenceReviewer.ReviewEvidence(r.Context(), request)
	if err != nil {
		primary := providerName + "/" + modelName
		fallbackConfig, ok := srv.editorialFallbackConfig(r.Context(), editorialRoleEvidence)
		fallbackBundle, fallbackErr := srv.bundleFor(fallbackConfig)
		if !ok || fallbackErr != nil || fallbackBundle.EvidenceReviewer == nil {
			http.Error(w, "证据审校失败："+err.Error(), http.StatusBadRequest)
			return
		}
		providerName, modelName = fallbackBundle.EvidenceReviewer.Name(), provider.EffectiveTaskModel(fallbackConfig)
		request, fallbackErr = srv.evidenceReviewRequest(r, revision, providerName)
		if fallbackErr == nil {
			fallbackErr = srv.checkEditorialBudget(r.Context(), profile.ID, &draft.ID, providerName, modelName)
		}
		if fallbackErr == nil {
			result, fallbackErr = fallbackBundle.EvidenceReviewer.ReviewEvidence(r.Context(), request)
		}
		if fallbackErr != nil {
			http.Error(w, "EvidenceReviewer 首选与备用 Provider 均失败："+fallbackErr.Error(), http.StatusBadRequest)
			return
		}
		result.Usage.FallbackFrom = primary
	}
	issues, _ := json.Marshal(result.Issues)
	cost, err := srv.recordEditorialUsage(r.Context(), profile.ID, &draft.ID, "evidence_review", "revision", revision.ID, providerName, modelName, promptVersion, result.Usage)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if _, err := srv.store.CreateArticleReview(r.Context(), models.ArticleReview{RevisionID: revision.ID, Kind: "evidence", Status: result.Status, IssuesJSON: string(issues), Provider: &providerName, Model: &modelName, PromptVersion: &promptVersion, CostCents: cost}); err != nil {
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
	taskConfig := editorialTaskConfig(settings, editorialRoleStyle)
	bundle, err := srv.bundleFor(taskConfig)
	if err != nil || bundle.StyleEditor == nil {
		http.Error(w, "StyleEditor Provider 不可用", http.StatusBadRequest)
		return
	}
	mapsForPolicy, err := srv.store.ListEvidenceMaps(r.Context(), revision.ID)
	if err != nil {
		http.Error(w, "读取文章素材失败", http.StatusInternalServerError)
		return
	}
	if len(mapsForPolicy) > 0 {
		if _, err := srv.evidenceReviewRequest(r, revision, bundle.StyleEditor.Name()); err != nil {
			http.Error(w, "文章素材不可发送给 StyleEditor："+err.Error(), http.StatusBadRequest)
			return
		}
	}
	draft, _, _, profile, err := srv.editorialContextForRevision(r.Context(), revision)
	if err != nil {
		http.Error(w, "读取文章上下文失败", http.StatusBadRequest)
		return
	}
	providerName := bundle.StyleEditor.Name()
	modelName := provider.EffectiveTaskModel(taskConfig)
	promptVersion := provider.StyleEditorPromptVersion
	if err := srv.checkEditorialBudget(r.Context(), profile.ID, &draft.ID, providerName, modelName); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	result, err := bundle.StyleEditor.ReviewStyle(r.Context(), request)
	if err != nil {
		primary := providerName + "/" + modelName
		fallbackConfig, ok := srv.editorialFallbackConfig(r.Context(), editorialRoleStyle)
		fallbackBundle, fallbackErr := srv.bundleFor(fallbackConfig)
		if !ok || fallbackErr != nil || fallbackBundle.StyleEditor == nil {
			http.Error(w, "风格审校失败："+err.Error(), http.StatusBadRequest)
			return
		}
		providerName, modelName = fallbackBundle.StyleEditor.Name(), provider.EffectiveTaskModel(fallbackConfig)
		if _, fallbackErr = srv.evidenceReviewRequest(r, revision, providerName); fallbackErr == nil {
			fallbackErr = srv.checkEditorialBudget(r.Context(), profile.ID, &draft.ID, providerName, modelName)
		}
		if fallbackErr == nil {
			result, fallbackErr = fallbackBundle.StyleEditor.ReviewStyle(r.Context(), request)
		}
		if fallbackErr != nil {
			http.Error(w, "StyleEditor 首选与备用 Provider 均失败："+fallbackErr.Error(), http.StatusBadRequest)
			return
		}
		result.Usage.FallbackFrom = primary
	}
	issues, _ := json.Marshal(result.Issues)
	cost, err := srv.recordEditorialUsage(r.Context(), profile.ID, &draft.ID, "style_review", "revision", revision.ID, providerName, modelName, promptVersion, result.Usage)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if _, err := srv.store.CreateArticleReview(r.Context(), models.ArticleReview{RevisionID: revision.ID, Kind: "style", Status: result.Status, IssuesJSON: string(issues), Provider: &providerName, Model: &modelName, PromptVersion: &promptVersion, CostCents: cost}); err != nil {
		http.Error(w, "保存风格审校失败", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/workbench/drafts/"+revision.DraftID, http.StatusSeeOther)
}

func (srv *Server) styleReviewRequest(r *http.Request, revision *models.ArticleRevision) (provider.StyleReviewRequest, error) {
	_, brief, _, profile, err := srv.editorialContextForRevision(r.Context(), revision)
	if err != nil {
		return provider.StyleReviewRequest{}, err
	}
	return provider.StyleReviewRequest{Title: revision.Title, Markdown: revision.Markdown, TargetAudience: profile.TargetAudience, Voice: profile.Voice, StyleGuide: profile.StyleGuide, TargetLength: brief.TargetLength}, nil
}

func (srv *Server) evidenceReviewRequest(r *http.Request, revision *models.ArticleRevision, providerName string) (provider.EvidenceReviewRequest, error) {
	_, _, proposal, _, err := srv.editorialContextForRevision(r.Context(), revision)
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
			external, err := srv.store.CanSendSourceToProvider(r.Context(), keyPoint.SourceType, keyPoint.SourceID, providerName)
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
	if ref := strings.TrimSpace(r.FormValue("source_ref")); ref != "" {
		parts := strings.SplitN(ref, "|", 2)
		if len(parts) == 2 {
			sourceType = models.SourceType(parts[0])
			sourceID = parts[1]
		}
	}
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
	articleBudget, err := optionalBudget(r.FormValue("per_article_budget_cents"))
	if err != nil {
		http.Error(w, "单篇预算必须是非负整数", http.StatusBadRequest)
		return
	}
	profile, err := srv.store.CreateEditorialProfile(r.Context(), models.EditorialProfile{
		Name:                  name,
		TargetAudience:        strings.TrimSpace(r.FormValue("target_audience")),
		Voice:                 strings.TrimSpace(r.FormValue("voice")),
		StyleGuide:            strings.TrimSpace(r.FormValue("style_guide")),
		SourceAttribution:     strings.TrimSpace(r.FormValue("source_attribution")),
		MonthlyBudgetCents:    budget,
		PerArticleBudgetCents: articleBudget,
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
