package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

type scoutAuthorization struct {
	profile *models.EditorialProfile
	config  provider.TaskConfig
	bundle  *provider.ProviderBundle
}

func (srv *Server) loadScoutAuthorization(ctx context.Context, profileID string) (*scoutAuthorization, error) {
	profile, err := srv.store.GetEditorialProfile(ctx, profileID)
	if err != nil {
		return nil, badEditorial("读取编辑画像失败")
	}
	settings, err := srv.store.GetSettings(ctx)
	if err != nil {
		return nil, internalEditorial("读取 Scout 配置失败")
	}
	config := editorialTaskConfig(settings, editorialRoleScout)
	bundle, err := srv.bundleFor(config)
	if err != nil || bundle.Scout == nil {
		return nil, badEditorial("Scout Provider 不可用")
	}
	return &scoutAuthorization{profile: profile, config: config, bundle: bundle}, nil
}

func (srv *Server) generateScout(r *http.Request, authorization *scoutAuthorization) (*provider.ScoutResult, string, string, error) {
	ctx := r.Context()
	primaryName := authorization.bundle.Scout.Name()
	request, err := srv.scoutRequest(r, authorization.profile, primaryName)
	if err != nil {
		return nil, "", "", badEditorial("主题不满足 Scout 条件：" + err.Error())
	}
	primaryModel := provider.EffectiveTaskModel(authorization.config)
	if err := srv.checkEditorialBudget(ctx, authorization.profile.ID, nil, primaryName, primaryModel); err != nil {
		return nil, "", "", conflictEditorial(err.Error())
	}
	result, providerName, modelName, err := callEditorialWithFallback(
		srv,
		ctx,
		editorialRoleScout,
		authorization.config,
		primaryName,
		func() (*provider.ScoutResult, error) { return authorization.bundle.Scout.Scout(ctx, request) },
		func(fallbackConfig provider.TaskConfig, fallbackBundle *provider.ProviderBundle) (*provider.ScoutResult, string, error) {
			if fallbackBundle.Scout == nil {
				return nil, "", errors.New("Scout Provider 不可用")
			}
			fallbackName := fallbackBundle.Scout.Name()
			fallbackRequest, requestErr := srv.scoutRequest(r, authorization.profile, fallbackName)
			if requestErr != nil {
				return nil, fallbackName, requestErr
			}
			fallbackModel := provider.EffectiveTaskModel(fallbackConfig)
			if budgetErr := srv.checkEditorialBudget(ctx, authorization.profile.ID, nil, fallbackName, fallbackModel); budgetErr != nil {
				return nil, fallbackName, budgetErr
			}
			fallbackResult, callErr := fallbackBundle.Scout.Scout(ctx, fallbackRequest)
			return fallbackResult, fallbackName, callErr
		},
		func(result *provider.ScoutResult, from string) { result.Usage.FallbackFrom = from },
		"Scout 生成失败",
		"Scout 首选与备用 Provider 均失败",
	)
	if err != nil {
		return nil, "", "", badEditorial(err.Error())
	}
	return result, providerName, modelName, nil
}

func (srv *Server) persistScoutProposals(ctx context.Context, profileID, providerName, modelName string, result *provider.ScoutResult, cost *int64) (int, error) {
	existing, err := srv.store.ListArticleProposals(ctx, profileID)
	if err != nil {
		return 0, internalEditorial("读取既有提案失败")
	}
	titles := make(map[string]bool, len(existing)+len(result.Proposals))
	historicalTitles := make([]string, 0, len(existing)+len(result.Proposals))
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
		if _, err := srv.store.CreateArticleProposal(ctx, models.ArticleProposal{EditorialProfileID: profileID, Kind: candidate.Kind, Title: candidate.Title, Thesis: candidate.Thesis, Audience: candidate.Audience, Rationale: candidate.Rationale, CandidateKeyPoints: string(ids), Provider: &providerName, Model: &modelName, PromptVersion: providerStringPtr(provider.ScoutPromptVersion), CostCents: proposalCost}); err != nil {
			return 0, internalEditorial("保存 Scout 提案失败")
		}
		titles[key] = true
		historicalTitles = append(historicalTitles, key)
		created++
	}
	return created, nil
}

func providerStringPtr(value string) *string { return &value }

func (srv *Server) runScout(r *http.Request, profileID string) (int, error) {
	authorization, err := srv.loadScoutAuthorization(r.Context(), profileID)
	if err != nil {
		return 0, err
	}
	result, providerName, modelName, err := srv.generateScout(r, authorization)
	if err != nil {
		return 0, err
	}
	cost, err := srv.recordEditorialUsage(r.Context(), authorization.profile.ID, nil, "scout", "profile", authorization.profile.ID, providerName, modelName, provider.ScoutPromptVersion, result.Usage)
	if err != nil {
		return 0, internalEditorial(err.Error())
	}
	return srv.persistScoutProposals(r.Context(), authorization.profile.ID, providerName, modelName, result, cost)
}
