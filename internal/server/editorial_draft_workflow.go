package server

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/woyin/orangecast/internal/models"
)

type articleDraftDetailData struct {
	Draft                  *models.ArticleDraft
	Revisions              []*models.ArticleRevision
	ReviewsByRevision      map[string][]articleReviewView
	Comparison             *revisionComparison
	CurrentMarkdown        string
	CurrentRevision        *models.ArticleRevision
	CurrentReady           bool
	HasComparableRevisions bool
}

func (srv *Server) loadArticleDraftDetail(ctx context.Context, draftID, fromID, toID string) (*articleDraftDetailData, error) {
	draft, err := srv.store.GetArticleDraft(ctx, draftID)
	if err != nil {
		return nil, err
	}
	revisions, err := srv.store.ListArticleRevisions(ctx, draftID)
	if err != nil {
		return nil, internalEditorial("加载修订历史失败")
	}
	allReviews, err := srv.store.ListArticleReviewsForDraft(ctx, draftID)
	if err != nil {
		return nil, internalEditorial("加载审校记录失败")
	}
	reviewsByRevision := articleReviewViews(allReviews, revisions)
	byID := make(map[string]*models.ArticleRevision, len(revisions))
	var currentRevision *models.ArticleRevision
	for _, revision := range revisions {
		byID[revision.ID] = revision
		if draft.CurrentRevisionID != nil && revision.ID == *draft.CurrentRevisionID {
			currentRevision = revision
		}
	}
	comparison, err := revisionComparisonFor(byID, fromID, toID)
	if err != nil {
		return nil, badEditorial(err.Error())
	}
	currentReady := false
	if currentRevision != nil {
		currentReady, err = srv.store.IsRevisionReadyForPublication(ctx, currentRevision.ID)
		if err != nil {
			return nil, internalEditorial("检查当前修订证据门禁失败")
		}
	}
	currentMarkdown := ""
	if len(revisions) > 0 {
		currentMarkdown = revisions[0].Markdown
	}
	return &articleDraftDetailData{Draft: draft, Revisions: revisions, ReviewsByRevision: reviewsByRevision, Comparison: comparison, CurrentMarkdown: currentMarkdown, CurrentRevision: currentRevision, CurrentReady: currentReady, HasComparableRevisions: len(revisions) > 1}, nil
}

func articleReviewViews(allReviews map[string][]*models.ArticleReview, revisions []*models.ArticleRevision) map[string][]articleReviewView {
	views := make(map[string][]articleReviewView, len(revisions))
	for _, revision := range revisions {
		for _, review := range allReviews[revision.ID] {
			view := articleReviewView{ArticleReview: review}
			if err := json.Unmarshal([]byte(review.IssuesJSON), &view.Issues); err != nil {
				view.Issues = []string{"审校记录格式异常"}
			}
			views[revision.ID] = append(views[revision.ID], view)
		}
	}
	return views
}

func revisionComparisonFor(byID map[string]*models.ArticleRevision, fromID, toID string) (*revisionComparison, error) {
	if fromID == "" && toID == "" {
		return nil, nil
	}
	from, to := byID[fromID], byID[toID]
	if fromID == "" || toID == "" || from == nil || to == nil || fromID == toID {
		return nil, errors.New("请选择同一文章的两个不同修订进行对比")
	}
	return &revisionComparison{From: from, To: to, Lines: lineDiff(from.Markdown, to.Markdown)}, nil
}
