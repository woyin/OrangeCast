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
	proposals, err := srv.automaticCreationProposals(ctx, profile.ID, batch.ID, result)
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
	return provider.ScoutRequest{Audience: profile.TargetAudience, Voice: profile.Voice, Mode: provider.ScoutModeCrossEpisode, ProposalCount: scoutProposalTarget, Themes: []provider.ScoutTheme{{ID: "automatic-discovery", Name: "近期学习变化", Description: "仅使用当前 DiscoveryWindow 中已审学习成果；每条候选必须覆盖至少两个不同 Episode。", Materials: materials}}}, nil
}

func (srv *Server) automaticCreationProposals(ctx context.Context, profileID, batchID string, result *provider.ScoutResult) ([]models.CreationProposal, error) {
	existing, err := srv.store.ListCreationProposals(ctx, profileID)
	if err != nil {
		return nil, err
	}
	history, err := srv.store.ListCreationHistory(ctx, profileID)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, proposal := range existing {
		seen[normalizeEditorialTitle(proposal.WorkingTitle)+"\x00"+normalizeEditorialTitle(proposal.ProposedClaim)] = true
	}
	out := make([]models.CreationProposal, 0, len(result.Proposals))
	for _, candidate := range result.Proposals {
		key := normalizeEditorialTitle(candidate.Title) + "\x00" + normalizeEditorialTitle(candidate.Thesis)
		if seen[key] {
			continue
		}
		materialIDs, err := json.Marshal(candidate.CandidateKeyPointIDs)
		if err != nil {
			return nil, err
		}
		possibleHistory := []string{}
		for _, work := range history {
			if normalizeEditorialTitle(work.CoreClaim) == normalizeEditorialTitle(candidate.Thesis) || normalizeEditorialTitle(work.Title) == normalizeEditorialTitle(candidate.Title) {
				possibleHistory = append(possibleHistory, work.ID)
			}
		}
		relationship := ""
		if len(possibleHistory) > 0 {
			relationship = "possible_duplicate:" + strings.Join(possibleHistory, ",")
		}
		out = append(out, models.CreationProposal{EditorialProfileID: profileID, ProposalBatchID: batchID, CreationForm: "article", WorkingTitle: candidate.Title, ProposedClaim: candidate.Thesis, Audience: candidate.Audience, Rationale: candidate.Rationale, MaterialIDsJSON: string(materialIDs), HistoryRelationship: relationship})
		seen[key] = true
	}
	return out, nil
}
