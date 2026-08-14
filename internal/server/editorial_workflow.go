package server

import (
	"context"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

type cachedWriterResult struct {
	Result        *provider.ArticleWritingResult `json:"result"`
	Provider      string                         `json:"provider"`
	Model         string                         `json:"model"`
	PromptVersion string                         `json:"promptVersion"`
	CostCents     int64                          `json:"costCents"`
}

// editorialContextForRevision is the single lineage seam shared by writing,
// review, policy, and cost workflows. Changes to material ownership no longer
// require each HTTP handler to reproduce this traversal.
func (srv *Server) editorialContextForRevision(ctx context.Context, revision *models.ArticleRevision) (*models.ArticleDraft, *models.ArticleBrief, *models.ArticleProposal, *models.EditorialProfile, error) {
	draft, err := srv.store.GetArticleDraft(ctx, revision.DraftID)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	brief, err := srv.store.GetArticleBrief(ctx, draft.BriefID)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	proposal, err := srv.store.GetArticleProposal(ctx, brief.ProposalID)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	profile, err := srv.store.GetEditorialProfile(ctx, proposal.EditorialProfileID)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	return draft, brief, proposal, profile, nil
}
