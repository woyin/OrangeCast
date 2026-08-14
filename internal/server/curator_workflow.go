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

type curatorAuthorization struct {
	proposal *models.ArticleProposal
	profile  *models.EditorialProfile
	config   provider.TaskConfig
	bundle   *provider.ProviderBundle
}

type curatorRunResult struct {
	ProfileID string
	BriefID   string
}

func (srv *Server) loadCuratorAuthorization(ctx context.Context, proposalID string) (*curatorAuthorization, error) {
	proposal, err := srv.store.GetArticleProposal(ctx, proposalID)
	if err != nil || proposal.Status != "accepted" {
		return nil, badEditorial("只有已接受提案才能生成 Brief")
	}
	profile, err := srv.store.GetEditorialProfile(ctx, proposal.EditorialProfileID)
	if err != nil {
		return nil, badEditorial("读取编辑画像失败")
	}
	settings, err := srv.store.GetSettings(ctx)
	if err != nil {
		return nil, internalEditorial("读取 Curator 配置失败")
	}
	config := editorialTaskConfig(settings, editorialRoleCurator)
	bundle, err := srv.bundleFor(config)
	if err != nil || bundle.Curator == nil {
		return nil, badEditorial("Curator Provider 不可用")
	}
	return &curatorAuthorization{proposal: proposal, profile: profile, config: config, bundle: bundle}, nil
}

func (srv *Server) curatorRequest(ctx context.Context, authorization *curatorAuthorization, providerName string) (provider.CuratorRequest, error) {
	var ids []string
	if json.Unmarshal([]byte(authorization.proposal.CandidateKeyPoints), &ids) != nil || len(ids) == 0 {
		return provider.CuratorRequest{}, errors.New("提案没有候选材料")
	}
	request := provider.CuratorRequest{Title: authorization.proposal.Title, Thesis: authorization.proposal.Thesis, Audience: authorization.proposal.Audience, Voice: authorization.profile.Voice}
	for _, id := range ids {
		kp, err := srv.store.GetKeyPoint(ctx, id)
		if err != nil {
			return provider.CuratorRequest{}, errors.New("候选材料不存在")
		}
		usable, err := srv.store.CanUseSourceForPublication(ctx, authorization.profile.ID, kp.SourceType, kp.SourceID)
		if err != nil || !usable {
			return provider.CuratorRequest{}, errors.New("候选材料不在可发布范围")
		}
		send, err := srv.store.CanSendSourceToProvider(ctx, kp.SourceType, kp.SourceID, providerName)
		if err != nil || !send {
			return provider.CuratorRequest{}, errors.New("候选材料不可发送给 Curator")
		}
		var citations []string
		if json.Unmarshal([]byte(kp.CitationsJSON), &citations) != nil || len(citations) == 0 {
			return provider.CuratorRequest{}, errors.New("候选材料 Citation 无效")
		}
		request.Materials = append(request.Materials, provider.ArticleMaterial{KeyPointID: kp.ID, SourceID: kp.SourceID, SourceTitle: kp.SourceTitle, Content: kp.Content, Description: kp.Description, Citations: citations})
	}
	return request, nil
}

func (srv *Server) runCurator(r *http.Request, proposalID string) (*curatorRunResult, error) {
	ctx := r.Context()
	authorization, err := srv.loadCuratorAuthorization(ctx, strings.TrimSpace(proposalID))
	if err != nil {
		return nil, err
	}
	claimed, err := srv.store.ClaimEditorialTask(ctx, "curator", authorization.proposal.ID)
	if err != nil {
		return nil, internalEditorial("领取 Curator 任务失败")
	}
	if !claimed {
		brief, findErr := srv.store.GetArticleBriefByProposal(ctx, authorization.proposal.ID)
		if findErr == nil {
			return &curatorRunResult{ProfileID: authorization.profile.ID, BriefID: brief.ID}, nil
		}
		return nil, conflictEditorial("Curator 正在执行")
	}
	finishContext := context.WithoutCancel(ctx)
	finished := false
	defer func() {
		if !finished {
			_ = srv.store.FinishEditorialTask(finishContext, "curator", authorization.proposal.ID, errors.New("Curator 任务未完成"))
		}
	}()
	primaryName := authorization.bundle.Curator.Name()
	request, err := srv.curatorRequest(ctx, authorization, primaryName)
	if err != nil {
		return nil, badEditorial(err.Error())
	}
	primaryModel := provider.EffectiveTaskModel(authorization.config)
	if err := srv.checkEditorialBudget(ctx, authorization.profile.ID, nil, primaryName, primaryModel); err != nil {
		return nil, conflictEditorial(err.Error())
	}
	result, providerName, modelName, err := callEditorialWithFallback(
		srv,
		ctx,
		editorialRoleCurator,
		authorization.config,
		primaryName,
		func() (*provider.CuratorResult, error) { return authorization.bundle.Curator.Curate(ctx, request) },
		func(fallbackConfig provider.TaskConfig, fallbackBundle *provider.ProviderBundle) (*provider.CuratorResult, string, error) {
			if fallbackBundle.Curator == nil {
				return nil, "", errors.New("Curator Provider 不可用")
			}
			fallbackName := fallbackBundle.Curator.Name()
			fallbackRequest, requestErr := srv.curatorRequest(ctx, authorization, fallbackName)
			if requestErr != nil {
				return nil, fallbackName, requestErr
			}
			fallbackModel := provider.EffectiveTaskModel(fallbackConfig)
			if budgetErr := srv.checkEditorialBudget(ctx, authorization.profile.ID, nil, fallbackName, fallbackModel); budgetErr != nil {
				return nil, fallbackName, budgetErr
			}
			fallbackResult, callErr := fallbackBundle.Curator.Curate(ctx, fallbackRequest)
			return fallbackResult, fallbackName, callErr
		},
		func(result *provider.CuratorResult, from string) { result.Usage.FallbackFrom = from },
		"Curator 生成失败",
		"Curator 首选与备用 Provider 均失败",
	)
	if err != nil {
		return nil, badEditorial(err.Error())
	}
	cost, err := srv.recordEditorialUsage(ctx, authorization.profile.ID, nil, "curator", "proposal", authorization.proposal.ID, providerName, modelName, provider.CuratorPromptVersion, result.Usage)
	if err != nil {
		return nil, internalEditorial(err.Error())
	}
	materials, _ := json.Marshal(result.SelectedKeyPointIDs)
	conflicts, _ := json.Marshal(result.ConflictPlan)
	brief, err := srv.store.CreateArticleBrief(ctx, models.ArticleBrief{ProposalID: authorization.proposal.ID, Thesis: result.Thesis, Audience: result.Audience, Outline: result.Outline, MaterialPlan: string(materials), ConflictPlan: string(conflicts), Style: result.Style, TargetLength: result.TargetLength})
	if err != nil {
		return nil, internalEditorial("保存 Curator Brief 失败：" + err.Error())
	}
	if err := srv.store.FinishEditorialTask(finishContext, "curator", authorization.proposal.ID, nil); err != nil {
		return nil, internalEditorial("完成 Curator 任务失败")
	}
	finished = true
	_ = cost
	return &curatorRunResult{ProfileID: authorization.profile.ID, BriefID: brief.ID}, nil
}
