// claim_review.go 独立语义审校持久任务（C11 / ADR-0024 §6）。
// 输入冻结修订 ID：读取该修订的 ClaimMap 与正文，调用独立审校角色，
// 结果写入 claim_reviews 表（B02 幂等意图）；审校与写作是分离的角色。
package queue

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
)

// claimReviewerProvider 独立审校接口。
type claimReviewerProvider interface {
	ReviewClaims(ctx context.Context, req provider.ClaimReviewRequest) (*provider.ClaimReviewResult, provider.TaskUsage, error)
	Name() string
}

// EnqueueClaimReviewJob 入队审校任务（幂等意图：同修订同轮只一个）。
func (w *Worker) EnqueueClaimReviewJob(ctx context.Context, draftID, revisionID string) (*models.ProcessingJob, error) {
	snapshot, _ := json.Marshal(map[string]string{"draft_id": draftID, "revision_id": revisionID})
	job, created, err := w.store.EnqueueJobIdempotent(ctx, store.JobIntentSpec{
		SourceType: models.SourceEpisode, SourceID: draftID, JobType: models.JobClaimReview,
		IntentID:          fmt.Sprintf("claim_review:%s:%s", draftID, revisionID),
		InputSnapshotJSON: string(snapshot),
	})
	if err != nil {
		return nil, err
	}
	if !created {
		return nil, nil
	}
	return job, nil
}

// doClaimReviewJob 执行独立审校：读 ClaimMap + 正文 → 审校 → CreateClaimReview。
func (w *Worker) doClaimReviewJob(ctx context.Context, job *models.ProcessingJob, bundle *provider.ProviderBundle) error {
	prov, ok := bundle.Analysis.(claimReviewerProvider)
	if !ok {
		return fmt.Errorf("Analysis Provider 不支持独立审校")
	}
	exec, err := w.store.GetJobExecution(ctx, job.ID)
	if err != nil {
		return err
	}
	var snapshot struct {
		DraftID    string `json:"draft_id"`
		RevisionID string `json:"revision_id"`
	}
	if err := json.Unmarshal([]byte(exec.InputSnapshotJSON), &snapshot); err != nil || snapshot.RevisionID == "" {
		return fmt.Errorf("任务缺少修订快照")
	}

	// 读取 ClaimMap（授权材料 = ClaimMap 条目中的材料 ID）。
	entries, err := w.store.ListClaimMap(ctx, snapshot.DraftID, snapshot.RevisionID)
	if err != nil {
		return fmt.Errorf("读取 ClaimMap: %w", err)
	}
	authorized := map[string]bool{}
	claimMap := make([]provider.ClaimMapEntry, 0, len(entries))
	for _, e := range entries {
		for _, id := range e.MaterialIDs {
			authorized[id] = true
		}
		claimMap = append(claimMap, provider.ClaimMapEntry{
			Excerpt: e.Excerpt, ClaimKind: e.ClaimKind,
			MaterialIDs: e.MaterialIDs, SourceTitle: e.SourceTitle,
		})
	}
	var authorizedIDs []string
	for id := range authorized {
		authorizedIDs = append(authorizedIDs, id)
	}

	// 读取正文（从 article_revision 或 draft 当前版本）。
	markdown, err := w.store.GetArticleMarkdown(ctx, snapshot.RevisionID)
	if err != nil {
		return fmt.Errorf("读取正文: %w", err)
	}

	req := provider.ClaimReviewRequest{
		Markdown:      markdown,
		ClaimMap:      claimMap,
		AuthorizedIDs: authorizedIDs,
	}
	w.markRemoteCallStarted(ctx, job) // R02-b：到达远端调用边界
	result, usage, err := prov.ReviewClaims(ctx, req)
	if err != nil {
		return fmt.Errorf("独立审校: %w", err)
	}
	// 结果写入 claim_reviews 表。
	status := result.Status
	var issues []string
	for _, f := range result.Findings {
		issues = append(issues, f.Excerpt+"： "+f.IssueKind+" — "+f.Detail)
	}
	issuesJSON, _ := json.Marshal(issues)
	provName := prov.Name()
	review, err := w.store.CreateClaimReview(ctx, models.ClaimReview{
		WorkRevisionID: snapshot.RevisionID,
		Status:         status,
		IssuesJSON:     string(issuesJSON),
		Provider:       &provName,
	})
	if err != nil {
		return err
	}
	if usage.InputUnits > 0 || usage.OutputUnits > 0 {
		w.recordCallUsage(ctx, job, "claim_review", provName, "", usage)
	}
	resultJSON, _ := json.Marshal(map[string]any{"review_id": review.ID, "status": status})
	return w.store.SaveJobResult(ctx, job.ID, string(resultJSON), models.JobResultComplete)
}
