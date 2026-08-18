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
	ProposalPool         int
	ProposalRefillNeeded bool
	BriefsPending        int
	ReadyToWrite         int
	Reviewing            int
	Blocked              int
	Ready                int
	Archived             int
}

type workbenchBriefMaterial struct {
	ID          string
	SourceTitle string
	Content     string
}

type workbenchBriefView struct {
	ID, ProposalID, Status, Thesis, Audience, Outline, MaterialPlan, ConflictPlan, Style string
	TargetLength                                                                         *int
	Materials                                                                            []workbenchBriefMaterial
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
	data := map[string]any{"Profiles": profiles, "ModelPrices": prices, "CSRF": auth.CSRFValue(r), "RefillNotice": r.URL.Query().Get("refill")}
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
		refillRunning, refillError := srv.proposalRefillState(profile.ID)
		data["RefillRunning"], data["RefillError"] = refillRunning, refillError
		data["Proposals"] = proposals
		data["Drafts"] = drafts
		briefs, err := srv.store.ListArticleBriefs(r.Context(), profile.ID)
		if err != nil {
			http.Error(w, "加载写作简报失败", http.StatusInternalServerError)
			return
		}
		data["Briefs"] = briefs
		briefViews := make([]workbenchBriefView, 0, len(briefs))
		for _, brief := range briefs {
			view := workbenchBriefView{ID: brief.ID, ProposalID: brief.ProposalID, Status: brief.Status, Thesis: brief.Thesis, Audience: brief.Audience, Outline: brief.Outline, MaterialPlan: brief.MaterialPlan, ConflictPlan: brief.ConflictPlan, Style: brief.Style, TargetLength: brief.TargetLength}
			var materialIDs []string
			if json.Unmarshal([]byte(brief.MaterialPlan), &materialIDs) == nil {
				for _, id := range materialIDs {
					keyPoint, keyPointErr := srv.store.GetKeyPoint(r.Context(), id)
					if keyPointErr == nil {
						view.Materials = append(view.Materials, workbenchBriefMaterial{ID: keyPoint.ID, SourceTitle: keyPoint.SourceTitle, Content: keyPoint.Content})
					}
				}
			}
			briefViews = append(briefViews, view)
		}
		data["BriefViews"] = briefViews
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
	board.ProposalRefillNeeded = board.ProposalPool < scoutBrainstormCount
	return board
}

// handleArticleWriterRun generates an initial immutable revision only from a confirmed Brief.
func (srv *Server) handleArticleWriterRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}
	draftID, err := srv.runInitialWriter(r, strings.TrimSpace(r.FormValue("brief_id")))
	if err != nil {
		writeEditorialError(w, err)
		return
	}
	http.Redirect(w, r, "/workbench/drafts/"+draftID, http.StatusSeeOther)
}

// handleArticleRevisionWriterRun turns review findings into a new immutable AI edit.
func (srv *Server) handleArticleRevisionWriterRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}
	draftID, err := srv.runRevisionWriter(r, r.FormValue("revision_id"))
	if err != nil {
		writeEditorialError(w, err)
		return
	}
	http.Redirect(w, r, "/workbench/drafts/"+draftID, http.StatusSeeOther)
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
	status := strings.TrimSpace(r.FormValue("status"))
	if err := srv.store.SetArticleProposalStatus(r.Context(), strings.TrimSpace(r.FormValue("proposal_id")), status); err != nil {
		http.Error(w, "更新选题失败："+err.Error(), http.StatusBadRequest)
		return
	}
	redirect := "/workbench?profile=" + profileID
	if status != "proposed" {
		srv.scheduleProposalRefill(profileID)
		redirect += "&refill=automatic"
	}
	http.Redirect(w, r, redirect, http.StatusSeeOther)
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
	fromID, toID := strings.TrimSpace(r.URL.Query().Get("compare_from")), strings.TrimSpace(r.URL.Query().Get("compare_to"))
	data, err := srv.loadArticleDraftDetail(r.Context(), id, fromID, toID)
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		writeEditorialError(w, err)
		return
	}
	srv.tmpl.Render(w, "article_draft.html", map[string]any{"Draft": data.Draft, "Revisions": data.Revisions, "HasComparableRevisions": data.HasComparableRevisions, "ReviewsByRevision": data.ReviewsByRevision, "Comparison": data.Comparison, "CurrentMarkdown": data.CurrentMarkdown, "CurrentRevision": data.CurrentRevision, "CurrentRichHTML": template.HTML(wechatRichText(data.CurrentMarkdown)), "CurrentReady": data.CurrentReady, "CSRF": auth.CSRFValue(r)})
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
	draftID, err := srv.runEvidenceReview(r, r.FormValue("revision_id"))
	if err != nil {
		writeEditorialError(w, err)
		return
	}
	http.Redirect(w, r, "/workbench/drafts/"+draftID, http.StatusSeeOther)
}

// handleStyleReviewRun records independent, non-blocking style advice for an exact revision.
func (srv *Server) handleStyleReviewRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}
	draftID, err := srv.runStyleReview(r, r.FormValue("revision_id"))
	if err != nil {
		writeEditorialError(w, err)
		return
	}
	http.Redirect(w, r, "/workbench/drafts/"+draftID, http.StatusSeeOther)
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
		item, err := srv.evidenceReviewItem(r.Context(), mapping, revision.Markdown, proposal.EditorialProfileID, providerName)
		if err != nil {
			return provider.EvidenceReviewRequest{}, err
		}
		request.Items = append(request.Items, item)
	}
	return request, nil
}

func (srv *Server) evidenceReviewItem(ctx context.Context, mapping *models.EvidenceMap, markdown, profileID, providerName string) (provider.EvidenceReviewItem, error) {
	if !strings.Contains(markdown, mapping.Excerpt) {
		return provider.EvidenceReviewItem{}, fmt.Errorf("EvidenceMap 摘录未出现在正文中")
	}
	var ids []string
	if err := json.Unmarshal([]byte(mapping.KeyPointIDs), &ids); err != nil {
		return provider.EvidenceReviewItem{}, err
	}
	item := provider.EvidenceReviewItem{Kind: string(mapping.Kind), Excerpt: mapping.Excerpt}
	for _, id := range ids {
		keyPoint, err := srv.store.GetKeyPoint(ctx, id)
		if err != nil {
			return provider.EvidenceReviewItem{}, err
		}
		usable, err := srv.store.CanUseSourceForPublication(ctx, profileID, keyPoint.SourceType, keyPoint.SourceID)
		if err != nil || !usable {
			return provider.EvidenceReviewItem{}, errors.New("EvidenceMap 包含未授权或不可公开素材")
		}
		external, err := srv.store.CanSendSourceToProvider(ctx, keyPoint.SourceType, keyPoint.SourceID, providerName)
		if err != nil || !external {
			return provider.EvidenceReviewItem{}, errors.New("EvidenceMap 包含不可发送给审校模型的素材")
		}
		var citations []string
		if err := json.Unmarshal([]byte(keyPoint.CitationsJSON), &citations); err != nil || len(citations) == 0 {
			return provider.EvidenceReviewItem{}, errors.New("EvidenceMap 包含无 Citation KeyPoint")
		}
		item.Materials = append(item.Materials, provider.ArticleMaterial{KeyPointID: keyPoint.ID, SourceID: keyPoint.SourceID, SourceTitle: keyPoint.SourceTitle, Content: keyPoint.Content, Description: keyPoint.Description, Citations: citations})
	}
	if mapping.Kind != models.EvidenceRhetorical && len(item.Materials) == 0 {
		return provider.EvidenceReviewItem{}, errors.New("事实表达缺少证据素材")
	}
	return item, nil
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
