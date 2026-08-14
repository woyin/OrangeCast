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

func (srv *Server) handleCuratorRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}
	proposal, err := srv.store.GetArticleProposal(r.Context(), strings.TrimSpace(r.FormValue("proposal_id")))
	if err != nil || proposal.Status != "accepted" {
		http.Error(w, "只有已接受提案才能生成 Brief", http.StatusBadRequest)
		return
	}
	profile, err := srv.store.GetEditorialProfile(r.Context(), proposal.EditorialProfileID)
	if err != nil {
		http.Error(w, "读取编辑画像失败", http.StatusBadRequest)
		return
	}
	settings, err := srv.store.GetSettings(r.Context())
	if err != nil {
		http.Error(w, "读取 Curator 配置失败", http.StatusInternalServerError)
		return
	}
	config := editorialTaskConfig(settings, editorialRoleCurator)
	bundle, err := srv.bundleFor(config)
	if err != nil || bundle.Curator == nil {
		http.Error(w, "Curator Provider 不可用", http.StatusBadRequest)
		return
	}
	claimed, err := srv.store.ClaimEditorialTask(r.Context(), "curator", proposal.ID)
	if err != nil {
		http.Error(w, "领取 Curator 任务失败", http.StatusInternalServerError)
		return
	}
	if !claimed {
		if brief, e := srv.store.GetArticleBriefByProposal(r.Context(), proposal.ID); e == nil {
			http.Redirect(w, r, "/workbench?profile="+profile.ID+"#brief-"+brief.ID, http.StatusSeeOther)
			return
		}
		http.Error(w, "Curator 正在执行", http.StatusConflict)
		return
	}
	finishContext := context.WithoutCancel(r.Context())
	finished := false
	defer func() {
		if !finished {
			_ = srv.store.FinishEditorialTask(finishContext, "curator", proposal.ID, errors.New("Curator 任务未完成"))
		}
	}()
	var ids []string
	if json.Unmarshal([]byte(proposal.CandidateKeyPoints), &ids) != nil || len(ids) == 0 {
		http.Error(w, "提案没有候选材料", http.StatusBadRequest)
		return
	}
	request := provider.CuratorRequest{Title: proposal.Title, Thesis: proposal.Thesis, Audience: proposal.Audience, Voice: profile.Voice}
	providerName := bundle.Curator.Name()
	for _, id := range ids {
		kp, e := srv.store.GetKeyPoint(r.Context(), id)
		if e != nil {
			http.Error(w, "候选材料不存在", http.StatusBadRequest)
			return
		}
		usable, e := srv.store.CanUseSourceForPublication(r.Context(), profile.ID, kp.SourceType, kp.SourceID)
		if e != nil || !usable {
			http.Error(w, "候选材料不在可发布范围", http.StatusBadRequest)
			return
		}
		send, e := srv.store.CanSendSourceToProvider(r.Context(), kp.SourceType, kp.SourceID, providerName)
		if e != nil || !send {
			http.Error(w, "候选材料不可发送给 Curator", http.StatusBadRequest)
			return
		}
		var citations []string
		if json.Unmarshal([]byte(kp.CitationsJSON), &citations) != nil || len(citations) == 0 {
			http.Error(w, "候选材料 Citation 无效", http.StatusBadRequest)
			return
		}
		request.Materials = append(request.Materials, provider.ArticleMaterial{KeyPointID: kp.ID, SourceID: kp.SourceID, SourceTitle: kp.SourceTitle, Content: kp.Content, Description: kp.Description, Citations: citations})
	}
	modelName := provider.EffectiveTaskModel(config)
	promptVersion := provider.CuratorPromptVersion
	if err := srv.checkEditorialBudget(r.Context(), profile.ID, nil, providerName, modelName); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	result, err := bundle.Curator.Curate(r.Context(), request)
	if err != nil {
		primary := providerName + "/" + modelName
		fallbackConfig, ok := srv.editorialFallbackConfig(r.Context(), editorialRoleCurator)
		fallbackBundle, fallbackErr := srv.bundleFor(fallbackConfig)
		if !ok || fallbackErr != nil || fallbackBundle.Curator == nil {
			http.Error(w, "Curator 生成失败："+err.Error(), http.StatusBadRequest)
			return
		}
		providerName, modelName = fallbackBundle.Curator.Name(), provider.EffectiveTaskModel(fallbackConfig)
		fallbackErr = srv.validateEditorialMaterialsProvider(r.Context(), request.Materials, providerName)
		if fallbackErr == nil {
			fallbackErr = srv.checkEditorialBudget(r.Context(), profile.ID, nil, providerName, modelName)
		}
		if fallbackErr == nil {
			result, fallbackErr = fallbackBundle.Curator.Curate(r.Context(), request)
		}
		if fallbackErr != nil {
			http.Error(w, "Curator 首选与备用 Provider 均失败："+fallbackErr.Error(), http.StatusBadRequest)
			return
		}
		result.Usage.FallbackFrom = primary
	}
	if _, err := srv.recordEditorialUsage(r.Context(), profile.ID, nil, "curator", "proposal", proposal.ID, providerName, modelName, promptVersion, result.Usage); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	materials, _ := json.Marshal(result.SelectedKeyPointIDs)
	conflicts, _ := json.Marshal(result.ConflictPlan)
	brief, err := srv.store.CreateArticleBrief(r.Context(), models.ArticleBrief{ProposalID: proposal.ID, Thesis: result.Thesis, Audience: result.Audience, Outline: result.Outline, MaterialPlan: string(materials), ConflictPlan: string(conflicts), Style: result.Style, TargetLength: result.TargetLength})
	if err != nil {
		http.Error(w, "保存 Curator Brief 失败："+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := srv.store.FinishEditorialTask(finishContext, "curator", proposal.ID, nil); err != nil {
		http.Error(w, "完成 Curator 任务失败", http.StatusInternalServerError)
		return
	}
	finished = true
	http.Redirect(w, r, "/workbench?profile="+profile.ID+"#brief-"+brief.ID, http.StatusSeeOther)
}

func (srv *Server) validateEditorialMaterialsProvider(ctx context.Context, materials []provider.ArticleMaterial, providerName string) error {
	for _, material := range materials {
		keyPoint, err := srv.store.GetKeyPoint(ctx, material.KeyPointID)
		if err != nil {
			return err
		}
		allowed, err := srv.store.CanSendSourceToProvider(ctx, keyPoint.SourceType, keyPoint.SourceID, providerName)
		if err != nil {
			return err
		}
		if !allowed {
			return errors.New("备用 Provider 不符合素材 ModelDataPolicy")
		}
	}
	return nil
}
