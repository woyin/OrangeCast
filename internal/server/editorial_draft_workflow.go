package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

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
	// R22：当前修订实际来源（精确身份，去重）。
	ActualSources []SourceLink
}

// SourceLink 是当前修订实际引用的来源（精确 join keypoint_index 后去重）。
type SourceLink struct {
	Label string
	Href  string
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
	var actualSourcesData []SourceLink
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
		actualSources, aerr := srv.actualSourcesForRevision(ctx, currentRevision)
		if aerr != nil {
			return nil, internalEditorial("读取实际来源失败")
		}
		actualSourcesData = actualSources
	}
	currentMarkdown := ""
	if len(revisions) > 0 {
		currentMarkdown = revisions[0].Markdown
	}
	return &articleDraftDetailData{Draft: draft, Revisions: revisions, ReviewsByRevision: reviewsByRevision, Comparison: comparison, CurrentMarkdown: currentMarkdown, CurrentRevision: currentRevision, CurrentReady: currentReady, HasComparableRevisions: len(revisions) > 1, NewContract: newContract, ReadinessIssues: readinessIssues, ClaimJob: claimJob, StyleJob: styleJob, LatestClaim: latestClaim, LatestStyle: latestStyle, RevisionJob: revisionJobData, ActualSources: actualSourcesData}, nil
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

// actualSourcesForRevision R22：当前修订的"实际来源与笔记"（精确身份，不虚构）。
// 新契约从 claim_map_entries material IDs 解析：先精确查 KeyPoint（按来源去重，
// 链接 /sources/<type>/<id>）；KeyPoint 不存在再精确查 OwnerNote（生成独立笔记
// 锚点链接：episode/upload → /sources/<type>/<sourceID>#note-<noteID>，document →
// /documents/<sourceID>#note-<noteID>）。两表都不存在的 ID 才跳过；其他 DB 错误上抛。
// 旧文章严格只解析 KeyPoint（evidence_map ID 不回退解析 OwnerNote）。
func (srv *Server) actualSourcesForRevision(ctx context.Context, revision *models.ArticleRevision) ([]SourceLink, error) {
	newContract, err := srv.store.IsNewContractRevision(ctx, revision.ID)
	if err != nil {
		return nil, err
	}
	// materialIDs：新契约=claim_map 材料（KeyPoint+OwnerNote，均可精确解析）；
	// 旧契约=evidence_map keypoint IDs（严格只解析 KeyPoint，避免损坏 legacy ID
	// 意外匹配到 note）。
	var materialIDs []string
	if newContract {
		entries, err := srv.store.ListClaimMap(ctx, revision.DraftID, revision.ID)
		if err != nil {
			return nil, err
		}
		used := map[string]bool{}
		for _, e := range entries {
			for _, id := range e.MaterialIDs {
				used[id] = true
			}
		}
		for id := range used {
			materialIDs = append(materialIDs, id)
		}
	} else {
		maps, err := srv.store.ListEvidenceMaps(ctx, revision.ID)
		if err != nil {
			return nil, err
		}
		for _, m := range maps {
			var ids []string
			if err := json.Unmarshal([]byte(m.KeyPointIDs), &ids); err != nil {
				return nil, err
			}
			materialIDs = append(materialIDs, ids...)
		}
	}
	sort.Strings(materialIDs)
	seen := map[string]bool{}
	var out []SourceLink
	for _, id := range materialIDs {
		kp, err := srv.store.GetKeyPoint(ctx, id)
		if err == nil {
			key := string(kp.SourceType) + "\x00" + kp.SourceID
			if seen[key] {
				continue
			}
			seen[key] = true
			label := strings.TrimSpace(kp.SourceTitle)
			if label == "" {
				label = fmt.Sprintf("%s · %s", kp.SourceType, kp.SourceID)
			}
			out = append(out, SourceLink{Label: label, Href: fmt.Sprintf("/sources/%s/%s", kp.SourceType, kp.SourceID)})
			continue
		}
		if !errors.Is(err, store.ErrNotFound) {
			return nil, err
		}
		// 仅新契约 material 允许回退解析 OwnerNote；旧契约 legacy ID 严格只解析 KeyPoint。
		if !newContract {
			continue
		}
		// KeyPoint 不存在：精确解析 OwnerNote（独立可识别的笔记锚点链接）。
		note, noteErr := srv.store.GetOwnerNote(ctx, id)
		if noteErr != nil {
			if errors.Is(noteErr, store.ErrNotFound) {
				continue // 两表都不存在：跳过
			}
			return nil, noteErr
		}
		linkKey := "note\x00" + id
		if seen[linkKey] {
			continue
		}
		seen[linkKey] = true
		if models.SourceType(note.SourceType) == models.SourceDocument {
			out = append(out, SourceLink{Label: "笔记：" + truncateRunes(note.Content, 80), Href: fmt.Sprintf("/documents/%s#note-%s", note.SourceID, note.ID)})
		} else {
			out = append(out, SourceLink{Label: "笔记：" + truncateRunes(note.Content, 80), Href: fmt.Sprintf("/sources/%s/%s#note-%s", note.SourceType, note.SourceID, note.ID)})
		}
	}
	return out, nil
}
