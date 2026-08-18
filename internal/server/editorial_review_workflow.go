package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

type editorialReviewContext struct {
	revision *models.ArticleRevision
	draft    *models.ArticleDraft
	brief    *models.ArticleBrief
	proposal *models.ArticleProposal
	profile  *models.EditorialProfile
	config   provider.TaskConfig
}

func (srv *Server) loadEditorialReviewContext(ctx context.Context, revisionID, role string) (*editorialReviewContext, error) {
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
		return nil, internalEditorial("读取审校配置失败")
	}
	return &editorialReviewContext{revision: revision, draft: draft, brief: brief, proposal: proposal, profile: profile, config: editorialTaskConfig(settings, role)}, nil
}

func (srv *Server) saveEditorialReview(ctx context.Context, reviewContext *editorialReviewContext, kind, providerName, modelName, promptVersion string, resultStatus string, issues []string, cost *int64) error {
	encoded, _ := json.Marshal(issues)
	if _, err := srv.store.CreateArticleReview(ctx, models.ArticleReview{RevisionID: reviewContext.revision.ID, Kind: kind, Status: resultStatus, IssuesJSON: string(encoded), Provider: &providerName, Model: &modelName, PromptVersion: &promptVersion, CostCents: cost}); err != nil {
		return err
	}
	return nil
}

func (srv *Server) runEvidenceReview(r *http.Request, revisionID string) (string, error) {
	ctx := r.Context()
	reviewContext, err := srv.loadEditorialReviewContext(ctx, revisionID, editorialRoleEvidence)
	if err != nil {
		return "", err
	}
	bundle, err := srv.bundleFor(reviewContext.config)
	if err != nil || bundle.EvidenceReviewer == nil {
		return "", badEditorial("EvidenceReviewer Provider 不可用")
	}
	primaryName := bundle.EvidenceReviewer.Name()
	request, err := srv.evidenceReviewRequest(r, reviewContext.revision, primaryName)
	if err != nil {
		return "", badEditorial("证据映射不满足审校条件：" + err.Error())
	}
	primaryModel := provider.EffectiveTaskModel(reviewContext.config)
	if err := srv.checkEditorialBudget(ctx, reviewContext.profile.ID, &reviewContext.draft.ID, primaryName, primaryModel); err != nil {
		return "", conflictEditorial(err.Error())
	}
	result, providerName, modelName, err := callEditorialWithFallback(
		srv,
		ctx,
		editorialRoleEvidence,
		reviewContext.config,
		primaryName,
		func() (*provider.EvidenceReviewResult, error) {
			return bundle.EvidenceReviewer.ReviewEvidence(ctx, request)
		},
		func(fallbackConfig provider.TaskConfig, fallbackBundle *provider.ProviderBundle) (*provider.EvidenceReviewResult, string, error) {
			if fallbackBundle.EvidenceReviewer == nil {
				return nil, "", errors.New("EvidenceReviewer Provider 不可用")
			}
			fallbackName := fallbackBundle.EvidenceReviewer.Name()
			fallbackRequest, requestErr := srv.evidenceReviewRequest(r, reviewContext.revision, fallbackName)
			if requestErr != nil {
				return nil, fallbackName, requestErr
			}
			fallbackModel := provider.EffectiveTaskModel(fallbackConfig)
			if budgetErr := srv.checkEditorialBudget(ctx, reviewContext.profile.ID, &reviewContext.draft.ID, fallbackName, fallbackModel); budgetErr != nil {
				return nil, fallbackName, budgetErr
			}
			fallbackResult, callErr := fallbackBundle.EvidenceReviewer.ReviewEvidence(ctx, fallbackRequest)
			return fallbackResult, fallbackName, callErr
		},
		func(result *provider.EvidenceReviewResult, from string) { result.Usage.FallbackFrom = from },
		"证据审校失败",
		"EvidenceReviewer 首选与备用 Provider 均失败",
	)
	if err != nil {
		return "", badEditorial(err.Error())
	}
	cost, err := srv.recordEditorialUsage(ctx, reviewContext.profile.ID, &reviewContext.draft.ID, "evidence_review", "revision", reviewContext.revision.ID, providerName, modelName, provider.EvidenceReviewerPromptVersion, result.Usage)
	if err != nil {
		return "", internalEditorial(err.Error())
	}
	if err := srv.saveEditorialReview(ctx, reviewContext, "evidence", providerName, modelName, provider.EvidenceReviewerPromptVersion, result.Status, result.Issues, cost); err != nil {
		return "", internalEditorial("保存证据审校失败")
	}
	issuesJSON, _ := json.Marshal(result.Issues)
	if _, err := srv.store.CreateClaimReview(ctx, models.ClaimReview{WorkRevisionID: reviewContext.revision.ID, Status: result.Status, IssuesJSON: string(issuesJSON), Provider: &providerName, Model: &modelName, PromptVersion: providerStringPtr(provider.EvidenceReviewerPromptVersion), CostCents: cost}); err != nil {
		return "", internalEditorial("保存主张审校失败")
	}
	return reviewContext.revision.DraftID, nil
}

func (srv *Server) runStyleReview(r *http.Request, revisionID string) (string, error) {
	ctx := r.Context()
	reviewContext, err := srv.loadEditorialReviewContext(ctx, revisionID, editorialRoleStyle)
	if err != nil {
		return "", err
	}
	bundle, err := srv.bundleFor(reviewContext.config)
	if err != nil || bundle.StyleEditor == nil {
		return "", badEditorial("StyleEditor Provider 不可用")
	}
	primaryName := bundle.StyleEditor.Name()
	request, err := srv.styleReviewRequest(r, reviewContext.revision)
	if err != nil {
		return "", badEditorial("读取编辑画像失败")
	}
	if err := srv.validateStyleReviewPolicy(r, reviewContext.revision, primaryName); err != nil {
		return "", err
	}
	primaryModel := provider.EffectiveTaskModel(reviewContext.config)
	if err := srv.checkEditorialBudget(ctx, reviewContext.profile.ID, &reviewContext.draft.ID, primaryName, primaryModel); err != nil {
		return "", conflictEditorial(err.Error())
	}
	result, providerName, modelName, err := callEditorialWithFallback(
		srv,
		ctx,
		editorialRoleStyle,
		reviewContext.config,
		primaryName,
		func() (*provider.StyleReviewResult, error) { return bundle.StyleEditor.ReviewStyle(ctx, request) },
		func(fallbackConfig provider.TaskConfig, fallbackBundle *provider.ProviderBundle) (*provider.StyleReviewResult, string, error) {
			if fallbackBundle.StyleEditor == nil {
				return nil, "", errors.New("StyleEditor Provider 不可用")
			}
			fallbackName := fallbackBundle.StyleEditor.Name()
			if policyErr := srv.validateStyleReviewPolicy(r, reviewContext.revision, fallbackName); policyErr != nil {
				return nil, fallbackName, policyErr
			}
			fallbackModel := provider.EffectiveTaskModel(fallbackConfig)
			if budgetErr := srv.checkEditorialBudget(ctx, reviewContext.profile.ID, &reviewContext.draft.ID, fallbackName, fallbackModel); budgetErr != nil {
				return nil, fallbackName, budgetErr
			}
			fallbackResult, callErr := fallbackBundle.StyleEditor.ReviewStyle(ctx, request)
			return fallbackResult, fallbackName, callErr
		},
		func(result *provider.StyleReviewResult, from string) { result.Usage.FallbackFrom = from },
		"风格审校失败",
		"StyleEditor 首选与备用 Provider 均失败",
	)
	if err != nil {
		return "", badEditorial(err.Error())
	}
	cost, err := srv.recordEditorialUsage(ctx, reviewContext.profile.ID, &reviewContext.draft.ID, "style_review", "revision", reviewContext.revision.ID, providerName, modelName, provider.StyleEditorPromptVersion, result.Usage)
	if err != nil {
		return "", internalEditorial(err.Error())
	}
	if err := srv.saveEditorialReview(ctx, reviewContext, "style", providerName, modelName, provider.StyleEditorPromptVersion, result.Status, result.Issues, cost); err != nil {
		return "", internalEditorial("保存风格审校失败")
	}
	return reviewContext.revision.DraftID, nil
}

func (srv *Server) validateStyleReviewPolicy(r *http.Request, revision *models.ArticleRevision, providerName string) error {
	maps, err := srv.store.ListEvidenceMaps(r.Context(), revision.ID)
	if err != nil {
		return internalEditorial("读取文章素材失败")
	}
	if len(maps) == 0 {
		return nil
	}
	if _, err := srv.evidenceReviewRequest(r, revision, providerName); err != nil {
		return badEditorial("文章素材不可发送给 StyleEditor：" + err.Error())
	}
	return nil
}
