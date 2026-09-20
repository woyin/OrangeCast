package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

const automaticDiscoveryInterval = 5 * time.Minute

// StartAutomaticDiscovery starts the controlled, profile-authorized scheduler.
// It never runs from a GET request and stops with the service context.
func (srv *Server) StartAutomaticDiscovery(ctx context.Context) {
	go func() {
		_ = srv.RunAutomaticDiscovery(ctx)
		ticker := time.NewTicker(automaticDiscoveryInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = srv.RunAutomaticDiscovery(ctx)
			}
		}
	}()
}

// RunAutomaticDiscovery checks every explicitly enabled profile once. A mutex
// serializes a process; ReserveAutomaticProposalBatch serializes all processes
// against the durable material-snapshot idempotency key.
func (srv *Server) RunAutomaticDiscovery(ctx context.Context) error {
	srv.discoveryMu.Lock()
	defer srv.discoveryMu.Unlock()
	settings, err := srv.store.ListEnabledDiscoverySettings(ctx)
	if err != nil {
		return err
	}
	var firstErr error
	for _, setting := range settings {
		if err := srv.runAutomaticDiscoveryForProfile(ctx, setting); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (srv *Server) runAutomaticDiscoveryForProfile(ctx context.Context, settings *models.DiscoverySettings) error {
	decision, err := srv.store.EvaluateAutomaticDiscovery(ctx, settings.EditorialProfileID, time.Now().UTC())
	if err != nil || !decision.Ready {
		return err
	}
	changes, err := srv.store.ListDiscoveryWindowChanges(ctx, settings.EditorialProfileID, decision.WindowStartAt)
	if err != nil {
		return err
	}
	snapshot, idempotencyKey, err := automaticDiscoverySnapshot(settings.EditorialProfileID, decision.WindowStartAt, changes)
	if err != nil {
		return err
	}
	providerName, modelName := settings.Provider, provider.EffectiveTaskModel(provider.TaskConfig{Provider: settings.Provider, Model: settings.Model})
	batch, claimed, err := srv.store.ReserveAutomaticProposalBatch(ctx, models.ProposalBatch{
		EditorialProfileID:   settings.EditorialProfileID,
		WindowStartAt:        decision.WindowStartAt,
		MaterialSnapshotJSON: snapshot,
		IdempotencyKey:       idempotencyKey,
		Provider:             providerStringPtr(providerName),
		Model:                providerStringPtr(modelName),
	})
	if err != nil || !claimed {
		return err
	}
	return srv.executeAutomaticProposalBatch(ctx, settings, batch, changes)
}

func automaticDiscoverySnapshot(profileID, windowStart string, changes []*models.MaterialChange) (string, string, error) {
	type snapshotChange struct {
		ID, KeyPointID, SourceType, SourceID, ChangeKind, SnapshotHash, CreatedAt string
	}
	payload := struct {
		ProfileID   string           `json:"profileId"`
		WindowStart string           `json:"windowStart"`
		Changes     []snapshotChange `json:"changes"`
	}{ProfileID: profileID, WindowStart: windowStart, Changes: make([]snapshotChange, 0, len(changes))}
	for _, change := range changes {
		payload.Changes = append(payload.Changes, snapshotChange{change.ID, change.KeyPointID, change.SourceType, change.SourceID, change.ChangeKind, change.SnapshotHash, change.CreatedAt})
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", "", err
	}
	sum := sha256.Sum256(encoded)
	return string(encoded), fmt.Sprintf("automatic-discovery:%x", sum), nil
}

func (srv *Server) executeAutomaticProposalBatch(ctx context.Context, settings *models.DiscoverySettings, batch *models.ProposalBatch, changes []*models.MaterialChange) error {
	profile, err := srv.store.GetEditorialProfile(ctx, settings.EditorialProfileID)
	if err != nil {
		return srv.failAutomaticProposalBatch(ctx, batch, settings.Provider, settings.Model, err, nil)
	}
	config := provider.TaskConfig{Provider: settings.Provider, Model: settings.Model}
	bundle, err := srv.bundleFor(config)
	if err != nil || bundle.Scout == nil {
		return srv.failAutomaticProposalBatch(ctx, batch, settings.Provider, settings.Model, fmt.Errorf("Scout Provider 不可用"), nil)
	}
	providerName := bundle.Scout.Name()
	modelName := provider.EffectiveTaskModel(config)
	if err := srv.checkEditorialBudget(ctx, profile.ID, nil, providerName, modelName); err != nil {
		return srv.failAutomaticProposalBatch(ctx, batch, providerName, modelName, err, nil)
	}
	request, err := srv.automaticDiscoveryRequest(ctx, profile, providerName, changes)
	if err != nil {
		return srv.failAutomaticProposalBatch(ctx, batch, providerName, modelName, err, nil)
	}
	// R15（复核）：当前窗口集合按明确 theme.ID 识别（不依赖切片顺序）；
	// 发送快照 = 当前窗口 + 有限历史——新增价值判定只看当前窗口，伪造校验看全集。
	var currentMaterials, sentMaterials []provider.ArticleMaterial
	for _, th := range request.Themes {
		sentMaterials = append(sentMaterials, th.Materials...)
		if th.ID == "automatic-discovery" {
			currentMaterials = append(currentMaterials, th.Materials...)
		}
	}
	result, err := bundle.Scout.Scout(ctx, request)
	if err != nil {
		return srv.failAutomaticProposalBatch(ctx, batch, providerName, modelName, fmt.Errorf("自动发现调用失败: %w", err), nil)
	}
	cost, usageErr := srv.recordEditorialUsage(ctx, profile.ID, nil, "automatic_discovery", "proposal_batch", batch.ID, providerName, modelName, provider.ScoutPromptVersion, result.Usage)
	if usageErr != nil {
		return srv.failAutomaticProposalBatch(ctx, batch, providerName, modelName, usageErr, nil)
	}
	if settings.BatchBudgetCents != nil && cost != nil && *cost > *settings.BatchBudgetCents {
		return srv.failAutomaticProposalBatch(ctx, batch, providerName, modelName, fmt.Errorf("实际费用 %d 分超过本批上限 %d 分", *cost, *settings.BatchBudgetCents), cost)
	}
	proposals, err := srv.automaticCreationProposals(ctx, profile.ID, batch.ID, result, currentMaterials, sentMaterials)
	if err != nil {
		return srv.failAutomaticProposalBatch(ctx, batch, providerName, modelName, err, cost)
	}
	shortage := ""
	if len(proposals) < scoutProposalTarget {
		shortage = fmt.Sprintf("当前素材仅支持 %d 条实质不同方向（目标 %d 条）", len(proposals), scoutProposalTarget)
	}
	if err := srv.store.FinalizeAutomaticProposalBatch(ctx, batch.ID, providerName, modelName, shortage, cost, proposals); err != nil {
		return err
	}
	return nil
}

func (srv *Server) failAutomaticProposalBatch(ctx context.Context, batch *models.ProposalBatch, providerName, modelName string, cause error, cost *int64) error {
	if cause == nil {
		cause = fmt.Errorf("unknown automatic discovery failure")
	}
	if err := srv.store.FailAutomaticProposalBatch(ctx, batch.ID, providerName, modelName, cause.Error(), cost); err != nil {
		return fmt.Errorf("%v; recording visible batch failure: %w", cause, err)
	}
	return cause
}

// automaticDiscoveryRequest forms a bounded, current-material-only cross-
// Episode request. The synthetic grouping is deliberate: Themes are no longer
// an approval gate under ADR-0022, while Scout's wire contract still groups
// evidence materials for prompt presentation.
func (srv *Server) automaticDiscoveryRequest(ctx context.Context, profile *models.EditorialProfile, providerName string, changes []*models.MaterialChange) (provider.ScoutRequest, error) {
	materials := make([]provider.ArticleMaterial, 0, len(changes))
	sources := map[string]bool{}
	seen := map[string]bool{}
	for _, change := range changes {
		if seen[change.KeyPointID] {
			continue
		}
		keyPoint, err := srv.store.GetKeyPoint(ctx, change.KeyPointID)
		if err != nil {
			return provider.ScoutRequest{}, err
		}
		if keyPoint.SourceType != models.SourceEpisode || keyPoint.QualityStatus != models.KeyPointReady && keyPoint.QualityStatus != models.KeyPointOwnerConfirmed || keyPoint.StaleAt != "" {
			return provider.ScoutRequest{}, fmt.Errorf("DiscoveryWindow 包含不可用 KeyPoint")
		}
		eligible, err := srv.store.IsKeyPointEligibleForProfile(ctx, profile.ID, keyPoint.ID)
		if err != nil || !eligible {
			return provider.ScoutRequest{}, fmt.Errorf("DiscoveryWindow 包含与画像不相关的 KeyPoint")
		}
		canSend, err := srv.store.CanSendSourceToProvider(ctx, keyPoint.SourceType, keyPoint.SourceID, providerName)
		if err != nil || !canSend {
			return provider.ScoutRequest{}, fmt.Errorf("DiscoveryWindow 包含不可发送给 Scout 的素材")
		}
		var citations []string
		if err := json.Unmarshal([]byte(keyPoint.CitationsJSON), &citations); err != nil || len(citations) == 0 {
			return provider.ScoutRequest{}, fmt.Errorf("DiscoveryWindow 包含无 Citation KeyPoint")
		}
		materials = append(materials, provider.ArticleMaterial{KeyPointID: keyPoint.ID, SourceID: keyPoint.SourceID, SourceTitle: keyPoint.SourceTitle, Content: keyPoint.Content, Description: keyPoint.Description, Citations: citations})
		sources[keyPoint.SourceID] = true
		seen[keyPoint.ID] = true
	}
	if len(materials) < 2 || len(sources) < 2 {
		return provider.ScoutRequest{}, fmt.Errorf("自动发现需要至少两个不同 Episode 的已审学习成果")
	}
	// R15：有界历史召回——以当前窗口材料为种子，语义检索有限历史相关素材；
	// 逐项遵守 Provider 策略与画像资格，不发送全库，不合格项静默跳过（召回尽力而为）。
	const maxHistoricalMaterials = 6
	currentIDs := map[string]bool{}
	for _, m := range materials {
		currentIDs[m.KeyPointID] = true
	}
	historical := make([]provider.ArticleMaterial, 0, maxHistoricalMaterials)
	historySeen := map[string]bool{}
	for _, m := range materials {
		if len(historical) >= maxHistoricalMaterials {
			break
		}
		recalled, err := srv.store.SearchKeyPointsHybrid(ctx, m.Content, maxHistoricalMaterials+2)
		if err != nil {
			continue // 召回失败不阻塞发现（当前窗口材料仍完整发送）
		}
		for _, kp := range recalled {
			if len(historical) >= maxHistoricalMaterials {
				break
			}
			if currentIDs[kp.ID] || historySeen[kp.ID] {
				continue
			}
			if kp.QualityStatus != models.KeyPointReady && kp.QualityStatus != models.KeyPointOwnerConfirmed || kp.StaleAt != "" {
				continue
			}
			eligible, err := srv.store.IsKeyPointEligibleForProfile(ctx, profile.ID, kp.ID)
			if err != nil || !eligible {
				continue // R15 复核：画像资格逐条校验，错误或不相关均排除
			}
			canSend, err := srv.store.CanSendSourceToProvider(ctx, kp.SourceType, kp.SourceID, providerName)
			if err != nil || !canSend {
				continue // LocalOnly/撤销授权：历史材料同样逐项遵守策略
			}
			var citations []string
			if err := json.Unmarshal([]byte(kp.CitationsJSON), &citations); err != nil || len(citations) == 0 {
				continue
			}
			historySeen[kp.ID] = true
			historical = append(historical, provider.ArticleMaterial{KeyPointID: kp.ID, SourceID: kp.SourceID, SourceTitle: kp.SourceTitle, Content: kp.Content, Description: kp.Description, Citations: citations})
		}
	}
	// R15：有界作品历史（精确记录，供重复检查与 follow_up 论证）。
	works, err := srv.store.ListCreationHistory(ctx, profile.ID)
	if err != nil {
		return provider.ScoutRequest{}, err
	}
	const maxHistoricalWorks = 10
	historyView := make([]provider.ScoutHistoricalWork, 0, maxHistoricalWorks)
	for i, work := range works {
		if i >= maxHistoricalWorks {
			break
		}
		historyView = append(historyView, provider.ScoutHistoricalWork{Title: work.Title, CoreClaim: work.CoreClaim, Status: work.Status})
	}
	themes := []provider.ScoutTheme{{ID: "automatic-discovery", Name: "近期学习变化", Description: "仅使用当前 DiscoveryWindow 中已审学习成果；每条候选必须覆盖至少两个不同 Episode，且必须引用至少一条当前窗口材料（新增价值）。", Materials: materials}}
	if len(historical) > 0 {
		themes = append(themes, provider.ScoutTheme{ID: "historical-context", Name: "历史相关素材（有限召回）", Description: "仅供对照与延续；候选不得只由本组材料构成。", Materials: historical})
	}
	return provider.ScoutRequest{Audience: profile.TargetAudience, Voice: profile.Voice, Mode: provider.ScoutModeCrossEpisode, ProposalCount: scoutProposalTarget, Themes: themes, HistoricalWorks: historyView}, nil
}

// automaticCreationProposals 严格校验 Scout 输出（C05/R15）：
//   - 候选材料 ID 必须属于发送快照（伪造素材 ID 的候选被丢弃并记录）；
//   - 每个候选必须引用至少一条当前窗口材料（新增价值——纯历史重组被挡），
//     且至少覆盖两个不同 Episode（跨集要求）；
//   - 高置信 HardDuplicate（同标题且同主张，对已有提案或作品历史）直接丢弃；
//     仅标题或仅主张相似的疑似相似保留并标记 possible_duplicate；
//     follow_up 候选引用了当前窗口新证据时保留并标记 follow_up_with_new_evidence；
//   - 结果不足时保留实际产出数并记录原因（不凑数）。
func (srv *Server) automaticCreationProposals(ctx context.Context, profileID, batchID string, result *provider.ScoutResult, currentMaterials, sentMaterials []provider.ArticleMaterial) ([]models.CreationProposal, error) {
	existing, err := srv.store.ListCreationProposals(ctx, profileID)
	if err != nil {
		return nil, err
	}
	history, err := srv.store.ListCreationHistory(ctx, profileID)
	if err != nil {
		return nil, err
	}
	// R15 复核：currentIDs 只含当前窗口材料（新增价值判定），sentIDs 含完整
	// 发送快照（当前 + 有限历史，伪造校验用）——两者显式区分。
	sentIDs := map[string]bool{}
	currentIDs := map[string]bool{}
	sourceOfMaterial := map[string]string{}
	for _, m := range sentMaterials {
		sentIDs[m.KeyPointID] = true
		sourceOfMaterial[m.KeyPointID] = m.SourceID
	}
	for _, m := range currentMaterials {
		currentIDs[m.KeyPointID] = true
	}
	seenExact := map[string]bool{}
	// R15（复核）：保留成对身份（同一条既有提案的 title+claim），near-match 必须
	// 成对比较——标题匹配提案 A、主张匹配提案 B 不得误判 HardDuplicate。
	seenPairs := []struct{ title, claim string }{}
	for _, proposal := range existing {
		pair := struct{ title, claim string }{normalizeEditorialTitle(proposal.WorkingTitle), normalizeEditorialTitle(proposal.ProposedClaim)}
		seenExact[pair.title+"\x00"+pair.claim] = true
		seenPairs = append(seenPairs, pair)
	}
	out := make([]models.CreationProposal, 0, len(result.Proposals))
	for _, candidate := range result.Proposals {
		// C05：伪造素材 ID 的候选直接丢弃（不静默改为空材料集）。
		fabricated := false
		for _, id := range candidate.CandidateKeyPointIDs {
			if !sentIDs[id] {
				fabricated = true
				break
			}
		}
		if fabricated || len(candidate.CandidateKeyPointIDs) == 0 {
			continue
		}
		// R15：新增价值——必须引用至少一条当前窗口材料，纯历史重组被挡。
		citesCurrent := false
		for _, id := range candidate.CandidateKeyPointIDs {
			if currentIDs[id] {
				citesCurrent = true
				break
			}
		}
		if !citesCurrent {
			continue
		}
		// 跨集要求：候选引用的素材须来自至少两个不同 Episode。
		srcSet := map[string]bool{}
		for _, id := range candidate.CandidateKeyPointIDs {
			if src := sourceOfMaterial[id]; src != "" {
				srcSet[src] = true
			}
		}
		if len(srcSet) < 2 {
			continue
		}
		if strings.TrimSpace(candidate.Title) == "" || strings.TrimSpace(candidate.Thesis) == "" {
			continue // R15 复核：空标题/空主张显式拒绝（near-dup helper 对空串有特殊语义）
		}
		titleKey := normalizeEditorialTitle(candidate.Title)
		claimKey := normalizeEditorialTitle(candidate.Thesis)
		// R15（复核）：复用既有可解释边界 editorialTitleNearDuplicate
		// （包含且最短 ≥6 字符，或 bigram Jaccard ≥0.65，与手工 Scout 去重一致），
		// 分别作用于规范化标题与主张：
		//   HardDuplicate：已有提案/作品历史的标题与主张双 near-dup → 丢弃；
		//   possible_duplicate：单项 near-dup → 保留并标记来历。
		hardDuplicate := seenExact[titleKey+"\x00"+claimKey]
		for _, pair := range seenPairs {
			if editorialTitleNearDuplicate(titleKey, []string{pair.title}) &&
				editorialTitleNearDuplicate(claimKey, []string{pair.claim}) {
				hardDuplicate = true
				break
			}
		}
		historyMatchIDs := []string{}
		for _, work := range history {
			wTitle := normalizeEditorialTitle(work.Title)
			wClaim := normalizeEditorialTitle(work.CoreClaim)
			if wTitle == titleKey && wClaim == claimKey {
				hardDuplicate = true
				continue
			}
			titleNear := editorialTitleNearDuplicate(titleKey, []string{wTitle})
			claimNear := editorialTitleNearDuplicate(claimKey, []string{wClaim})
			if titleNear && claimNear {
				hardDuplicate = true
				continue
			}
			if titleNear || claimNear {
				historyMatchIDs = append(historyMatchIDs, work.ID)
			}
		}
		if hardDuplicate {
			continue
		}
		materialIDs, err := json.Marshal(candidate.CandidateKeyPointIDs)
		if err != nil {
			return nil, err
		}
		// R15（复核）：两个事实可同时成立——组合格式保留完整信息：
		// "follow_up_with_new_evidence;possible_duplicate:<ids>"。
		var markers []string
		if candidate.Kind == "follow_up" && citesCurrent {
			markers = append(markers, "follow_up_with_new_evidence")
		}
		if len(historyMatchIDs) > 0 {
			markers = append(markers, "possible_duplicate:"+strings.Join(historyMatchIDs, ","))
		}
		relationship := strings.Join(markers, ";")
		out = append(out, models.CreationProposal{EditorialProfileID: profileID, ProposalBatchID: batchID, CreationForm: "article", WorkingTitle: candidate.Title, ProposedClaim: candidate.Thesis, Audience: candidate.Audience, Rationale: candidate.Rationale, MaterialIDsJSON: string(materialIDs), HistoryRelationship: relationship})
		seenExact[titleKey+"\x00"+claimKey] = true
		seenPairs = append(seenPairs, struct{ title, claim string }{titleKey, claimKey})
	}
	return out, nil
}
