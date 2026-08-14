package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
)

// editorialHTTPError keeps transport status mapping at the HTTP seam while
// the workflow implementation returns ordinary, testable errors.
type editorialHTTPError struct {
	status  int
	message string
}

func (e *editorialHTTPError) Error() string { return e.message }

func badEditorial(message string) error {
	return &editorialHTTPError{status: http.StatusBadRequest, message: message}
}
func conflictEditorial(message string) error {
	return &editorialHTTPError{status: http.StatusConflict, message: message}
}
func internalEditorial(message string) error {
	return &editorialHTTPError{status: http.StatusInternalServerError, message: message}
}

func writeEditorialError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	var transportErr *editorialHTTPError
	if errors.As(err, &transportErr) {
		status = transportErr.status
	}
	http.Error(w, err.Error(), status)
}

type initialWriterAuthorization struct {
	brief    *models.ArticleBrief
	profile  *models.EditorialProfile
	proposal *models.ArticleProposal
	config   provider.TaskConfig
	bundle   *provider.ProviderBundle
}

func (srv *Server) loadInitialWriterAuthorization(ctx context.Context, briefID string) (*initialWriterAuthorization, error) {
	brief, err := srv.store.GetArticleBrief(ctx, briefID)
	if err != nil || brief.Status != "confirmed" {
		return nil, badEditorial("只有已确认的 Brief 才能生成文章")
	}
	proposal, err := srv.store.GetArticleProposal(ctx, brief.ProposalID)
	if err != nil {
		return nil, internalEditorial("读取选题失败")
	}
	profile, err := srv.store.GetEditorialProfile(ctx, proposal.EditorialProfileID)
	if err != nil {
		return nil, internalEditorial("读取编辑画像失败")
	}
	settings, err := srv.store.GetSettings(ctx)
	if err != nil {
		return nil, internalEditorial("读取 Writer 配置失败")
	}
	config := editorialTaskConfig(settings, editorialRoleWriter)
	bundle, err := srv.bundleFor(config)
	if err != nil || bundle.Writer == nil {
		return nil, badEditorial("Writer Provider 不可用")
	}
	return &initialWriterAuthorization{brief: brief, proposal: proposal, profile: profile, config: config, bundle: bundle}, nil
}

func (srv *Server) claimInitialWriter(ctx context.Context, briefID string) (bool, error) {
	claimed, err := srv.store.ClaimEditorialTask(ctx, "writer_initial", briefID)
	if err != nil {
		return false, internalEditorial("领取 Writer 任务失败")
	}
	return claimed, nil
}

func (srv *Server) initialWriterDraft(ctx context.Context, brief *models.ArticleBrief, title string) (*models.ArticleDraft, error) {
	draft, err := srv.store.GetArticleDraftByBrief(ctx, brief.ID)
	if errors.Is(err, store.ErrNotFound) {
		draft, err = srv.store.CreateArticleDraft(ctx, brief.ID, title)
	}
	if err != nil {
		return nil, internalEditorial("创建文章草稿失败")
	}
	return draft, nil
}

func (srv *Server) initialWriterResult(r *http.Request, authorization *initialWriterAuthorization, draft *models.ArticleDraft) (*cachedWriterResult, error) {
	ctx := r.Context()
	primaryProvider := authorization.bundle.Writer.Name()
	primaryModel := provider.EffectiveTaskModel(authorization.config)
	promptVersion := provider.ArticleWriterPromptVersion
	if payload, err := srv.store.GetEditorialTaskResult(ctx, "writer_initial", authorization.brief.ID); err == nil {
		var cached cachedWriterResult
		if json.Unmarshal([]byte(payload), &cached) != nil || cached.Result == nil {
			return nil, internalEditorial("Writer 缓存结果损坏")
		}
		return &cached, nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return nil, internalEditorial("读取 Writer 缓存失败")
	}
	request, err := srv.writerRequest(r, authorization.profile, authorization.brief, authorization.proposal, primaryProvider)
	if err != nil {
		return nil, badEditorial("素材不满足写作条件：" + err.Error())
	}
	if err := srv.checkEditorialBudget(ctx, authorization.profile.ID, &draft.ID, primaryProvider, primaryModel); err != nil {
		return nil, conflictEditorial(err.Error())
	}
	result, selectedProvider, selectedModel, err := srv.callWriterWithFallback(r, authorization, request, primaryProvider, primaryModel, &draft.ID)
	if err != nil {
		return nil, badEditorial(err.Error())
	}
	cost, err := srv.recordEditorialUsage(ctx, authorization.profile.ID, &draft.ID, "writer_initial", "draft", draft.ID, selectedProvider, selectedModel, promptVersion, result.Usage)
	if err != nil {
		return nil, internalEditorial(err.Error())
	}
	cached := &cachedWriterResult{Result: result, Provider: selectedProvider, Model: selectedModel, PromptVersion: promptVersion, CostCents: *cost}
	encoded, _ := json.Marshal(cached)
	if err := srv.store.SaveEditorialTaskResult(ctx, "writer_initial", authorization.brief.ID, string(encoded)); err != nil {
		return nil, internalEditorial("缓存 Writer 结果失败")
	}
	return cached, nil
}

func (srv *Server) callWriterWithFallback(r *http.Request, authorization *initialWriterAuthorization, request provider.ArticleWritingRequest, primaryProvider, primaryModel string, draftID *string) (*provider.ArticleWritingResult, string, string, error) {
	result, err := authorization.bundle.Writer.WriteArticle(r.Context(), request)
	if err == nil {
		return result, primaryProvider, primaryModel, nil
	}
	fallbackConfig, configured := srv.editorialFallbackConfig(r.Context(), editorialRoleWriter)
	fallbackBundle, fallbackErr := srv.bundleFor(fallbackConfig)
	if !configured || fallbackErr != nil || fallbackBundle.Writer == nil {
		return nil, "", "", fmt.Errorf("生成文章失败：%w", err)
	}
	fallbackProvider := fallbackBundle.Writer.Name()
	fallbackModel := provider.EffectiveTaskModel(fallbackConfig)
	fallbackRequest, fallbackErr := srv.writerRequest(r, authorization.profile, authorization.brief, authorization.proposal, fallbackProvider)
	if fallbackErr == nil {
		fallbackErr = srv.checkEditorialBudget(r.Context(), authorization.profile.ID, draftID, fallbackProvider, fallbackModel)
	}
	if fallbackErr != nil {
		return nil, "", "", fmt.Errorf("Writer 首选与备用 Provider 均失败：%w", fallbackErr)
	}
	result, fallbackErr = fallbackBundle.Writer.WriteArticle(r.Context(), fallbackRequest)
	if fallbackErr != nil {
		return nil, "", "", fmt.Errorf("Writer 首选与备用 Provider 均失败：%w", fallbackErr)
	}
	result.Usage.FallbackFrom = primaryProvider + "/" + primaryModel
	return result, fallbackProvider, fallbackModel, nil
}

func evidenceMapsFromWriter(result *provider.ArticleWritingResult) []models.EvidenceMap {
	maps := make([]models.EvidenceMap, 0, len(result.EvidenceMaps))
	for _, mapping := range result.EvidenceMaps {
		ids, _ := json.Marshal(mapping.KeyPointIDs)
		maps = append(maps, models.EvidenceMap{Kind: models.EvidenceMapKind(mapping.Kind), Excerpt: mapping.Excerpt, KeyPointIDs: string(ids)})
	}
	return maps
}

func (srv *Server) runInitialWriter(r *http.Request, briefID string) (string, error) {
	ctx := r.Context()
	authorization, err := srv.loadInitialWriterAuthorization(ctx, briefID)
	if err != nil {
		return "", err
	}
	claimed, err := srv.claimInitialWriter(ctx, briefID)
	if err != nil {
		return "", err
	}
	if !claimed {
		if existing, findErr := srv.store.GetArticleDraftByBrief(ctx, briefID); findErr == nil {
			return existing.ID, nil
		}
		return "", conflictEditorial("该 Brief 的 Writer 任务正在执行，请稍后刷新")
	}
	finishContext := context.WithoutCancel(ctx)
	finished := false
	defer func() {
		if !finished {
			_ = srv.store.FinishEditorialTask(finishContext, "writer_initial", briefID, errors.New("Writer 任务未完成"))
		}
	}()
	draft, err := srv.initialWriterDraft(ctx, authorization.brief, authorization.proposal.Title)
	if err != nil {
		return "", err
	}
	if draft.CurrentRevisionID != nil {
		_ = srv.store.FinishEditorialTask(finishContext, "writer_initial", briefID, nil)
		finished = true
		return draft.ID, nil
	}
	cached, err := srv.initialWriterResult(r, authorization, draft)
	if err != nil {
		return "", err
	}
	_, err = srv.store.CreateArticleRevisionWithEvidenceMaps(ctx, models.ArticleRevision{DraftID: draft.ID, Title: cached.Result.Title, Markdown: cached.Result.Markdown, Origin: "writer", Provider: &cached.Provider, Model: &cached.Model, PromptVersion: &cached.PromptVersion, CostCents: int64Ptr(cached.CostCents)}, evidenceMapsFromWriter(cached.Result))
	if err != nil {
		return "", internalEditorial("保存 Writer 修订或证据映射失败")
	}
	if err := srv.store.FinishEditorialTask(finishContext, "writer_initial", briefID, nil); err != nil {
		return "", internalEditorial("完成 Writer 任务记录失败")
	}
	finished = true
	return draft.ID, nil
}

func int64Ptr(value int64) *int64 { return &value }
