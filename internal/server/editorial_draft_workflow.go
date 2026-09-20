package server

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/store"
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
	// R20：新契约门禁状态。
	NewContract     bool
	ReadinessIssues []string
	ClaimJob        *models.ProcessingJob
	StyleJob        *models.ProcessingJob
	LatestClaim     *models.ClaimReview
	LatestStyle     *models.ArticleReview
	// R21：durable AI 修订任务状态。
	RevisionJob *models.ProcessingJob
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
	newContract := false
	var readinessIssues []string
	var claimJob, styleJob *models.ProcessingJob
	var latestClaim *models.ClaimReview
	var latestStyle *models.ArticleReview
	var revisionJobData *models.ProcessingJob
	if currentRevision != nil {
		// R20：统一就绪判定（旧文章内部仍走 evidence+style 兼容规则），
		// 并加载当前修订的审校任务状态供页面展示。
		readiness, rerr := srv.store.EvaluateArticlePublicationReadiness(ctx, currentRevision.ID)
		if rerr != nil {
			return nil, internalEditorial("检查当前修订交付门禁失败")
		}
		newContract = readiness.NewContract
		readinessIssues = readiness.Issues
		currentReady = readiness.Ready
		latestClaim, latestStyle, claimJob, styleJob, err = srv.loadDurableReviewState(ctx, currentRevision.ID)
		if err != nil {
			return nil, err
		}
		revisionJob, jerr := srv.store.ClaimRevisionJobForRevision(ctx, currentRevision.ID)
		if jerr != nil && !errors.Is(jerr, store.ErrNotFound) {
			return nil, internalEditorial("读取 AI 修订任务失败")
		}
		revisionJobData = revisionJob
	}
	currentMarkdown := ""
	if len(revisions) > 0 {
		currentMarkdown = revisions[0].Markdown
	}
	return &articleDraftDetailData{Draft: draft, Revisions: revisions, ReviewsByRevision: reviewsByRevision, Comparison: comparison, CurrentMarkdown: currentMarkdown, CurrentRevision: currentRevision, CurrentReady: currentReady, HasComparableRevisions: len(revisions) > 1, NewContract: newContract, ReadinessIssues: readinessIssues, ClaimJob: claimJob, StyleJob: styleJob, LatestClaim: latestClaim, LatestStyle: latestStyle, RevisionJob: revisionJobData}, nil
}

// loadDurableReviewState 读取页面所需的持久审校状态：只把 store.ErrNotFound
// 当作缺失（nil），其余数据库错误显式返回，不吞错。
func (srv *Server) loadDurableReviewState(ctx context.Context, revisionID string) (*models.ClaimReview, *models.ArticleReview, *models.ProcessingJob, *models.ProcessingJob, error) {
	claim, err := srv.store.LatestDurableClaimReview(ctx, revisionID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, nil, nil, nil, internalEditorial("读取主张审校记录失败")
	}
	style, err := srv.store.LatestDurableStyleReview(ctx, revisionID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, nil, nil, nil, internalEditorial("读取风格审校记录失败")
	}
	claimJob, err := srv.store.ReviewJobForRevision(ctx, revisionID, store.ReviewKindClaim)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, nil, nil, nil, internalEditorial("读取主张审校任务失败")
	}
	styleJob, err := srv.store.ReviewJobForRevision(ctx, revisionID, store.ReviewKindStyle)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, nil, nil, nil, internalEditorial("读取风格审校任务失败")
	}
	return claim, style, claimJob, styleJob, nil
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
