package store

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/woyin/orangecast/internal/provider"
)

// QueueKnowledgePurposeRevision explicitly creates a new unreviewed input and
// directed revision. The previous passed body, hash and review remain immutable.
func (s *Store) QueueKnowledgePurposeRevision(ctx context.Context, id string, expected int, mode, instructions, reviewModel string) error {
	purpose, err := provider.FreezeWritingPurpose(mode)
	if err != nil {
		return err
	}
	if len([]rune(instructions)) > 4000 {
		return ErrInvalidEditorialState
	}
	article, err := s.GetKnowledgeArticle(ctx, id)
	if err != nil {
		return err
	}
	if expected < 1 || article.WorkingRevision != expected {
		return ErrConflict
	}
	parent, err := s.GetKnowledgeRevision(ctx, id, expected)
	if err != nil {
		return err
	}
	var req provider.KnowledgeArticleRequest
	if err = json.Unmarshal([]byte(parent.InputJSON), &req); err != nil {
		return err
	}
	if err = json.Unmarshal([]byte(parent.BlocksJSON), &req.Blocks); err != nil {
		return err
	}
	req.PromptVersion = provider.KnowledgeArticlePurposePromptVersion
	req.WritingPurpose = &purpose
	req.PreviewWritingPlan = false
	req.Update = nil // Direction/purpose is not knowledge increment.
	req.Instructions = strings.TrimSpace(instructions)
	req.ReviewModel = reviewModel
	req.Estimate = nil
	ensureKnowledgeStageConfigs(&req, article.Model)
	for _, stage := range []string{"review", "review_final"} {
		cfg := req.StageConfigs[stage]
		if reviewModel != "" {
			cfg.Model = reviewModel
			req.StageConfigs[stage] = cfg
		}
	}
	if err = s.CheckKnowledgeMaterials(ctx, article.ProfileID, article.Provider, req.Materials); err != nil {
		return err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE knowledge_articles SET status='revise',stage='revise',reason='用途变更，需要重新写作和审校',input_json=?,review_model=?,updated_at=datetime('now') WHERE id=? AND working_revision=? AND status NOT IN ('discover','select','write','review','revise','review_final','awaiting_plan')`, jsonString(req), reviewModel, id, expected)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrConflict
	}
	revision, err := appendKnowledgeRevision(ctx, tx, id, expected, parent.Title, req, req.Blocks, "owner:purpose", article.Provider, article.Model, req.PromptVersion)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE knowledge_articles SET working_revision=? WHERE id=?`, revision, id); err != nil {
		return err
	}
	if err = enqueueKnowledgeStage(ctx, tx, id, "revise", article.Provider, article.Model, req, false); err != nil {
		return err
	}
	return tx.Commit()
}
