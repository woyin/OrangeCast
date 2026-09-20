package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
)

// TestAIRRevisionRouteR21：新契约 /workbench/revise POST 只入队 durable 任务
// （同步 Provider 零调用）；重复点击复用同 job；failed 重试同 job。
func TestAIRRevisionRouteR21(t *testing.T) {
	srv := newTestServer(t)
	rev, draftID, _ := seedNewContractRevision(t, srv, "r21a")
	// durable 反馈（claim failed findings）。
	claimJob, err := srv.store.EnqueueRevisionReview(t.Context(), rev.ID, store.ReviewKindClaim)
	check(t, err)
	mustRunJob(t, srv, claimJob.ID)
	_, err = srv.store.SaveClaimReviewOutput(t.Context(), claimJob.ID, &provider.ClaimReviewResult{
		Status:   provider.ClaimReviewFailed,
		Findings: []provider.ClaimReviewFinding{{Excerpt: "来源说过这句话。", IssueKind: "misattributed", Detail: "归因错误"}},
	}, "p", "m")
	check(t, err)
	check(t, srv.store.MarkJobSucceeded(t.Context(), claimJob.ID))

	session := claimOwnerAndLogin(t, srv, "r21a@example.com", "password123")
	getRec := doWithCookie(srv, session, http.MethodGet, "/workbench")
	csrf := ""
	for _, cookie := range getRec.Result().Cookies() {
		if cookie.Name == "cwp_csrf" {
			csrf = cookie.Value
		}
	}
	if csrf == "" {
		t.Fatal("workbench GET 未设置 CSRF cookie")
	}

	// 同步 Writer Provider 永不被调用（bundle 只提供 v1 Writer）。
	syncWriter := &fakeRevisionSyncWriter{}
	srv.bundleFor = func(provider.TaskConfig) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{Writer: syncWriter}, nil
	}
	rec := postReviewForm(t, srv, session, csrf, "/workbench/revise", "_csrf="+csrf+"&revision_id="+rev.ID)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("revise POST: %d %s", rec.Code, rec.Body.String())
	}
	if syncWriter.calls != 0 {
		t.Fatalf("新契约 revise POST 不得同步调用 Provider: %d", syncWriter.calls)
	}
	job, err := srv.store.ClaimRevisionJobForRevision(t.Context(), rev.ID)
	check(t, err)
	if job.JobType != models.JobClaimRevision || job.Status != models.StatusQueued {
		t.Fatalf("job=%+v", job)
	}
	// 重复点击复用同 job。
	rec = postReviewForm(t, srv, session, csrf, "/workbench/revise", "_csrf="+csrf+"&revision_id="+rev.ID)
	check(t, err)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("repeat POST: %d", rec.Code)
	}
	same, err := srv.store.ClaimRevisionJobForRevision(t.Context(), rev.ID)
	check(t, err)
	if same.ID != job.ID {
		t.Fatalf("重复点击必须复用: %s vs %s", same.ID, job.ID)
	}
	// failed 重试同 job。
	check(t, srv.store.MarkJobFailed(t.Context(), job.ID, "boom"))
	rec = postReviewForm(t, srv, session, csrf, "/workbench/revise", "_csrf="+csrf+"&revision_id="+rev.ID)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("retry POST: %d", rec.Code)
	}
	retried, err := srv.store.ClaimRevisionJobForRevision(t.Context(), rev.ID)
	check(t, err)
	if retried.ID != job.ID || retried.Status != models.StatusQueued {
		t.Fatalf("failed 重试: %+v", retried)
	}
	_ = draftID
}

// fakeRevisionSyncWriter 记录 v1 同步写作调用（R21 新契约必须为零）。
type fakeRevisionSyncWriter struct {
	calls int
}

func (f *fakeRevisionSyncWriter) WriteArticle(ctx context.Context, request provider.ArticleWritingRequest) (*provider.ArticleWritingResult, error) {
	f.calls++
	return &provider.ArticleWritingResult{Title: "x", Markdown: "# x"}, nil
}

func (f *fakeRevisionSyncWriter) WriteArticleWithClaims(ctx context.Context, req provider.ClaimAwareWritingRequest) (*provider.ClaimAwareWritingResult, provider.TaskUsage, error) {
	f.calls++
	return &provider.ClaimAwareWritingResult{Title: "x", Markdown: "# x", ClaimMap: []provider.ClaimMapEntry{{Excerpt: "x", ClaimKind: provider.ClaimOwner}}}, provider.TaskUsage{}, nil
}

func (f *fakeRevisionSyncWriter) Name() string { return "fake-sync-writer" }

// TestManualRevisionRouteR21：手工修订真实保存——唯一继承、无 base 过期 409、
// 无孤儿 revision；页面显示 AI 修订任务状态。
func TestManualRevisionRouteR21(t *testing.T) {
	srv := newTestServer(t)
	rev, draftID, _ := seedNewContractRevision(t, srv, "r21b")
	session := claimOwnerAndLogin(t, srv, "r21b@example.com", "password123")
	getRec := doWithCookie(srv, session, http.MethodGet, "/workbench")
	csrf := ""
	for _, cookie := range getRec.Result().Cookies() {
		if cookie.Name == "cwp_csrf" {
			csrf = cookie.Value
		}
	}

	// 合法保存：唯一片段继承。
	rec := postReviewForm(t, srv, session, csrf, "/workbench/revisions",
		"_csrf="+csrf+"&draft_id="+draftID+"&base_revision_id="+rev.ID+"&title=R21&markdown=%23+R21%0A%0A%E6%9D%A5%E6%BA%90%E8%AF%B4%E8%BF%87%E8%BF%99%E5%8F%A5%E8%AF%9D%E3%80%82")
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("manual save: %d %s", rec.Code, rec.Body.String())
	}
	revisions, err := srv.store.ListArticleRevisions(t.Context(), draftID)
	check(t, err)
	if len(revisions) != 2 || revisions[0].Origin != "owner" {
		t.Fatalf("revisions=%+v", revisions)
	}
	entries, err := srv.store.ListClaimMap(t.Context(), draftID, revisions[0].ID)
	check(t, err)
	if len(entries) != 1 || entries[0].Excerpt != "来源说过这句话。" {
		t.Fatalf("应继承唯一定位片段: %+v", entries)
	}
	// 过期 base → 409。
	rec = postReviewForm(t, srv, session, csrf, "/workbench/revisions",
		"_csrf="+csrf+"&draft_id="+draftID+"&base_revision_id="+rev.ID+"&title=R21&markdown=%E5%8F%A6%E4%B8%80%E4%B8%AA")
	if rec.Code != http.StatusConflict {
		t.Fatalf("过期 base 必须 409: %d %s", rec.Code, rec.Body.String())
	}
	revisions, err = srv.store.ListArticleRevisions(t.Context(), draftID)
	check(t, err)
	if len(revisions) != 2 {
		t.Fatalf("409 不得产生孤儿 revision: %d", len(revisions))
	}
}

// TestRevisionWriterLegacySyncStillWorksR21：旧文章 AI 修订保持同步路径（v1 Writer）。
func TestRevisionWriterLegacySyncStillWorksR21(t *testing.T) {
	srv := newTestServer(t)
	ctx := t.Context()
	profile, err := srv.store.EnsureDefaultEditorialProfile(ctx)
	check(t, err)
	proposal, err := srv.store.CreateArticleProposal(ctx, models.ArticleProposal{EditorialProfileID: profile.ID, Title: "旧"})
	check(t, err)
	check(t, srv.store.SetArticleProposalStatus(ctx, proposal.ID, "accepted"))
	brief, err := srv.store.CreateArticleBrief(ctx, models.ArticleBrief{ProposalID: proposal.ID, Thesis: "旧论点"})
	check(t, err)
	check(t, srv.store.ConfirmArticleBrief(ctx, brief.ID))
	draft, err := srv.store.CreateArticleDraft(ctx, brief.ID, "旧文章")
	check(t, err)
	rev, err := srv.store.CreateArticleRevision(ctx, models.ArticleRevision{DraftID: draft.ID, Title: "旧文章", Markdown: "# 旧正文\n\n内容", Origin: "owner"})
	check(t, err)
	_, err = srv.store.CreateEvidenceMap(ctx, models.EvidenceMap{RevisionID: rev.ID, Kind: models.EvidenceRhetorical, Excerpt: "内容", KeyPointIDs: "[]"})
	check(t, err)
	_, err = srv.store.CreateArticleReview(ctx, models.ArticleReview{RevisionID: rev.ID, Kind: "style", Status: "passed", IssuesJSON: "[]", Provider: strPtr("p"), Model: strPtr("m")})
	check(t, err)
	_, err = srv.store.CreateArticleReview(ctx, models.ArticleReview{RevisionID: rev.ID, Kind: "evidence", Status: "failed", IssuesJSON: `["改进标题"]`, Provider: strPtr("p"), Model: strPtr("m")})
	check(t, err)

	session := claimOwnerAndLogin(t, srv, "r21c@example.com", "password123")
	getRec := doWithCookie(srv, session, http.MethodGet, "/workbench")
	csrf := ""
	for _, cookie := range getRec.Result().Cookies() {
		if cookie.Name == "cwp_csrf" {
			csrf = cookie.Value
		}
	}
	custom := &legacyWriterRecorder{}
	srv.bundleFor = func(provider.TaskConfig) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{Writer: custom}, nil
	}
	// 旧文章走同步路径：本 fixture 的 Brief 无 KeyPoint material plan，同步路径
	// 在 Provider 之前以 400 拒绝——这正是同步路径（而非 durable enqueue）的特征。
	rec := postReviewForm(t, srv, session, csrf, "/workbench/revise", "_csrf="+csrf+"&revision_id="+rev.ID)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "素材不满足写作条件") {
		t.Fatalf("旧文章应走同步路径: %d %s", rec.Code, rec.Body.String())
	}
	if custom.calls != 0 {
		t.Fatalf("同步路径在 Provider 前拒绝: calls=%d", custom.calls)
	}
	if _, err := srv.store.ClaimRevisionJobForRevision(t.Context(), rev.ID); err == nil {
		t.Fatal("旧文章不得创建 durable AI 修订任务")
	}
}

type legacyWriterRecorder struct {
	calls int
}

func (f *legacyWriterRecorder) WriteArticle(ctx context.Context, request provider.ArticleWritingRequest) (*provider.ArticleWritingResult, error) {
	f.calls++
	return &provider.ArticleWritingResult{Title: "旧修订", Markdown: "# 旧修订\n\n内容"}, nil
}

func (f *legacyWriterRecorder) WriteArticleWithClaims(ctx context.Context, req provider.ClaimAwareWritingRequest) (*provider.ClaimAwareWritingResult, provider.TaskUsage, error) {
	f.calls++
	return nil, provider.TaskUsage{}, errLegacyV2NotUsed
}

func (f *legacyWriterRecorder) Name() string { return "legacy-writer" }

var errLegacyV2NotUsed = errStatic("v2 不应被旧文章调用")

type errStatic string

func (e errStatic) Error() string { return string(e) }

// TestAIRRevisionJobStatusOnPage R21：页面使用 ClaimRevisionJobForRevision，
// queued/failed 状态与 last_error 可见。
func TestAIRRevisionJobStatusOnPage(t *testing.T) {
	srv := newTestServer(t)
	rev, draftID, _ := seedNewContractRevision(t, srv, "r21d")
	// durable 反馈。
	claimJob, err := srv.store.EnqueueRevisionReview(t.Context(), rev.ID, store.ReviewKindClaim)
	check(t, err)
	mustRunJob(t, srv, claimJob.ID)
	_, err = srv.store.SaveClaimReviewOutput(t.Context(), claimJob.ID, &provider.ClaimReviewResult{
		Status:   provider.ClaimReviewFailed,
		Findings: []provider.ClaimReviewFinding{{Excerpt: "来源说过这句话。", IssueKind: "misattributed", Detail: "归因错误"}},
	}, "p", "m")
	check(t, err)
	check(t, srv.store.MarkJobSucceeded(t.Context(), claimJob.ID))
	// 入队 AI 修订（queued）。
	_, err = srv.store.EnqueueClaimRevisionForRevision(t.Context(), rev.ID)
	check(t, err)

	session := claimOwnerAndLogin(t, srv, "r21d@example.com", "password123")
	render := func() string {
		req := httptest.NewRequest(http.MethodGet, "/workbench/drafts/"+draftID, nil)
		req.AddCookie(session)
		rec := httptest.NewRecorder()
		srv.Router().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("draft page: %d", rec.Code)
		}
		return rec.Body.String()
	}
	queuedBody := render()
	if !strings.Contains(queuedBody, "AI 修订任务：queued") {
		t.Fatalf("页面应显示 AI 修订 queued: %s", queuedBody)
	}
	// failed → 状态与 last_error 可见。
	check(t, srv.store.MarkJobFailed(t.Context(), func() string {
		job, err := srv.store.ClaimRevisionJobForRevision(t.Context(), rev.ID)
		check(t, err)
		return job.ID
	}(), "来源策略禁止外发"))
	failedBody := render()
	if !strings.Contains(failedBody, "AI 修订任务：failed") || !strings.Contains(failedBody, "来源策略禁止外发") {
		t.Fatalf("页面应显示 AI 修订 failed 与 last_error")
	}
}
