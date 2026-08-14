package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

type revisionWriterAuthorization struct {
	revision *models.ArticleRevision
	draft    *models.ArticleDraft
	brief    *models.ArticleBrief
	proposal *models.ArticleProposal
	profile  *models.EditorialProfile
	config   provider.TaskConfig
	bundle   *provider.ProviderBundle
}

func (srv *Server) loadRevisionWriterAuthorization(ctx context.Context, revisionID string) (*revisionWriterAuthorization, error) {
	revision, err := srv.store.GetArticleRevision(ctx, revisionID)
	if err != nil {
		return nil, badEditorial("读取文章修订失败")
	}
	draft, brief, proposal, profile, err := srv.editorialContextForRevision(ctx, revision)
	if err != nil {
		return nil, badEditorial("读取文章上下文失败")
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
	return &revisionWriterAuthorization{revision: revision, draft: draft, brief: brief, proposal: proposal, profile: profile, config: config, bundle: bundle}, nil
}

func revisionFeedback(reviews []*models.ArticleReview) []string {
	feedback := make([]string, 0)
	for _, review := range reviews {
		var issues []string
		if json.Unmarshal([]byte(review.IssuesJSON), &issues) == nil {
			feedback = append(feedback, issues...)
		}
	}
	return feedback
}

func (srv *Server) revisionWriterRequest(r *http.Request, authorization *revisionWriterAuthorization, reviews []*models.ArticleReview, providerName string) (provider.ArticleWritingRequest, error) {
	request, err := srv.writerRequest(r, authorization.profile, authorization.brief, authorization.proposal, providerName)
	if err != nil {
		return provider.ArticleWritingRequest{}, err
	}
	request.RevisionFeedback = revisionFeedback(reviews)
	if len(request.RevisionFeedback) == 0 {
		return provider.ArticleWritingRequest{}, errors.New("该修订没有可供处理的审校反馈")
	}
	request.Title, request.ExistingMarkdown = authorization.revision.Title, authorization.revision.Markdown
	return request, nil
}

func (srv *Server) runRevisionWriter(r *http.Request, revisionID string) (string, error) {
	ctx := r.Context()
	authorization, err := srv.loadRevisionWriterAuthorization(ctx, strings.TrimSpace(revisionID))
	if err != nil {
		return "", err
	}
	reviews, err := srv.store.ListArticleReviews(ctx, authorization.revision.ID)
	if err != nil {
		return "", internalEditorial("读取审校记录失败")
	}
	primaryName := authorization.bundle.Writer.Name()
	request, err := srv.revisionWriterRequest(r, authorization, reviews, primaryName)
	if err != nil {
		return "", badEditorial("素材不满足写作条件：" + err.Error())
	}
	primaryModel := provider.EffectiveTaskModel(authorization.config)
	if err := srv.checkEditorialBudget(ctx, authorization.profile.ID, &authorization.draft.ID, primaryName, primaryModel); err != nil {
		return "", conflictEditorial(err.Error())
	}
	result, providerName, modelName, err := callEditorialWithFallback(
		srv,
		ctx,
		editorialRoleWriter,
		authorization.config,
		primaryName,
		func() (*provider.ArticleWritingResult, error) {
			return authorization.bundle.Writer.WriteArticle(ctx, request)
		},
		func(fallbackConfig provider.TaskConfig, fallbackBundle *provider.ProviderBundle) (*provider.ArticleWritingResult, string, error) {
			if fallbackBundle.Writer == nil {
				return nil, "", errors.New("Writer Provider 不可用")
			}
			fallbackName := fallbackBundle.Writer.Name()
			fallbackRequest, requestErr := srv.revisionWriterRequest(r, authorization, reviews, fallbackName)
			if requestErr != nil {
				return nil, fallbackName, requestErr
			}
			fallbackModel := provider.EffectiveTaskModel(fallbackConfig)
			if budgetErr := srv.checkEditorialBudget(ctx, authorization.profile.ID, &authorization.draft.ID, fallbackName, fallbackModel); budgetErr != nil {
				return nil, fallbackName, budgetErr
			}
			fallbackResult, callErr := fallbackBundle.Writer.WriteArticle(ctx, fallbackRequest)
			return fallbackResult, fallbackName, callErr
		},
		func(result *provider.ArticleWritingResult, from string) { result.Usage.FallbackFrom = from },
		"生成新修订失败",
		"Writer 首选与备用 Provider 均失败",
	)
	if err != nil {
		return "", badEditorial(err.Error())
	}
	promptVersion := provider.ArticleWriterPromptVersion
	cost, err := srv.recordEditorialUsage(ctx, authorization.profile.ID, &authorization.draft.ID, "writer_revision", "revision", authorization.revision.ID, providerName, modelName, promptVersion, result.Usage)
	if err != nil {
		return "", internalEditorial(err.Error())
	}
	_, err = srv.store.CreateArticleRevisionWithEvidenceMaps(ctx, models.ArticleRevision{DraftID: authorization.draft.ID, Title: result.Title, Markdown: result.Markdown, Origin: "ai_edit", Provider: &providerName, Model: &modelName, PromptVersion: &promptVersion, CostCents: cost}, evidenceMapsFromWriter(result))
	if err != nil {
		return "", internalEditorial("保存 Writer 修订或证据映射失败")
	}
	return authorization.draft.ID, nil
}
