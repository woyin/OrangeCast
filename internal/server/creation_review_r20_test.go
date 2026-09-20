package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
)

// seedNewContractRevision 复用 seedR19ConfirmedBrief：确认 Brief + 持久链接 +
// 生产 Writer 入队 + SaveClaimWritingOutput 原子落库，产出新契约修订。
func seedNewContractRevision(t *testing.T, srv *Server, guid string) (*models.ArticleRevision, string, string) {
	t.Helper()
	ctx := t.Context()
	brief, kpID := seedR19ConfirmedBrief(t, srv, guid)
	writeJob, err := srv.store.EnqueueClaimWritingForCreationBrief(ctx, brief.ID)
	if err != nil {
		t.Fatalf("write enqueue: %v", err)
	}
	link, err := srv.store.GetCreationArticleLinkByCreationBrief(ctx, brief.ID)
	if err != nil {
		t.Fatal(err)
	}
	draft, err := srv.store.GetArticleDraftByBrief(ctx, link.ArticleBriefID)
	if err != nil {
		t.Fatal(err)
	}
	const markdown = "# R20\n\n来源说过这句话。Owner 的判断在此。"
	rev, err := srv.store.SaveClaimWritingOutput(ctx, store.ClaimWritingOutput{
		DraftID: draft.ID, JobID: writeJob.ID,
		CreationBriefID: brief.ID, BriefVersion: brief.ConfirmedVersion,
		CreationArticleLinkID: link.ID, ArticleProposalID: link.ArticleProposalID, ArticleBriefID: link.ArticleBriefID,
		OwnerClaim: "Owner", Title: "R20 标题", Markdown: markdown,
		ProviderName: "test", ModelName: "m1", PromptVersion: provider.ClaimWriterPromptVersion,
		AuthorizedIDs: []string{kpID},
		Entries: []models.ClaimMapEntry{
			{Excerpt: "来源说过这句话。", ClaimKind: provider.ClaimSource, MaterialIDs: []string{kpID}, SourceTitle: "来源"},
			{Excerpt: "Owner 的判断在此。", ClaimKind: provider.ClaimOwner},
		},
	})
	if err != nil {
		t.Fatalf("save writing output: %v", err)
	}
	return rev, draft.ID, kpID
}

// check 断言无错误（fixture 准备步骤不得吞错产生假阳性）。
func check(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// mustRunJob 将任务标记为 running（检查错误）。
func mustRunJob(t *testing.T, srv *Server, jobID string) {
	t.Helper()
	if _, err := srv.store.MarkJobRunning(t.Context(), jobID); err != nil {
		t.Fatal(err)
	}
}

// postReviewForm 带会话与 CSRF 的真实路由审校 POST。
func postReviewForm(t *testing.T, srv *Server, session *http.Cookie, csrf, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(session)
	req.AddCookie(&http.Cookie{Name: "cwp_csrf", Value: csrf})
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	return rec
}

// TestReviewRoutesR20 A：真实路由——claims 创建/failed 重试同 job、
// style durable enqueue 零 Provider、evidence 409 且零 Provider。
func TestReviewRoutesR20(t *testing.T) {
	srv := newTestServer(t)
	rev, _, _ := seedNewContractRevision(t, srv, "r20a")
	evidence := &fakeEvidenceReviewer{status: "passed"}
	style := &fakeStyleEditor{status: "passed"}
	srv.bundleFor = func(provider.TaskConfig) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{EvidenceReviewer: evidence, StyleEditor: style}, nil
	}
	session := claimOwnerAndLogin(t, srv, "r20a@example.com", "password123")
	// 登录后 GET workbench 取真实 CSRF cookie。
	getReq := httptest.NewRequest(http.MethodGet, "/workbench", nil)
	getReq.AddCookie(session)
	getRec := httptest.NewRecorder()
	srv.Router().ServeHTTP(getRec, getReq)
	csrf := ""
	for _, cookie := range getRec.Result().Cookies() {
		if cookie.Name == "cwp_csrf" {
			csrf = cookie.Value
		}
	}
	if csrf == "" {
		t.Fatal("workbench GET 未设置 CSRF cookie")
	}

	// claims POST：入队一个 claim_review job 并重定向。
	rec := postReviewForm(t, srv, session, csrf, "/workbench/reviews/claims", "_csrf="+csrf+"&revision_id="+rev.ID)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("claims POST: %d %s", rec.Code, rec.Body.String())
	}
	job, err := srv.store.ReviewJobForRevision(t.Context(), rev.ID, store.ReviewKindClaim)
	if err != nil || job.JobType != models.JobClaimReview || job.Status != models.StatusQueued {
		t.Fatalf("claims job: %v %+v", err, job)
	}
	// 重复点击复用同一 job。
	rec = postReviewForm(t, srv, session, csrf, "/workbench/reviews/claims", "_csrf="+csrf+"&revision_id="+rev.ID)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("claims repeat POST: %d", rec.Code)
	}
	same, err := srv.store.ReviewJobForRevision(t.Context(), rev.ID, store.ReviewKindClaim)
	check(t, err)
	if same.ID != job.ID {
		t.Fatalf("重复点击必须复用同 job: %s vs %s", same.ID, job.ID)
	}
	// failed → 再次 POST 真实重试同 job（DB 状态 queued）。
	if err := srv.store.MarkJobFailed(t.Context(), job.ID, "boom"); err != nil {
		t.Fatal(err)
	}
	rec = postReviewForm(t, srv, session, csrf, "/workbench/reviews/claims", "_csrf="+csrf+"&revision_id="+rev.ID)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("claims retry POST: %d", rec.Code)
	}
	retried, err := srv.store.ReviewJobForRevision(t.Context(), rev.ID, store.ReviewKindClaim)
	check(t, err)
	if retried.ID != job.ID || retried.Status != models.StatusQueued {
		t.Fatalf("failed 重试必须 reset 同 job 为 queued: %+v", retried)
	}
	persisted, err := srv.store.GetJob(t.Context(), job.ID)
	check(t, err)
	if persisted.Status != models.StatusQueued {
		t.Fatalf("DB 状态未 reset: %s", persisted.Status)
	}

	// style POST（新契约）：durable enqueue，同步 Provider 零调用。
	styleRec := postReviewForm(t, srv, session, csrf, "/workbench/reviews/style", "_csrf="+csrf+"&revision_id="+rev.ID)
	if styleRec.Code != http.StatusSeeOther {
		t.Fatalf("style POST: %d %s", styleRec.Code, styleRec.Body.String())
	}
	styleJob, err := srv.store.ReviewJobForRevision(t.Context(), rev.ID, store.ReviewKindStyle)
	if err != nil || styleJob.JobType != models.JobStyleReview {
		t.Fatalf("style job: %v %+v", err, styleJob)
	}
	if len(style.requests) != 0 {
		t.Fatalf("新契约 style POST 不得同步调用 Provider: %d", len(style.requests))
	}
	// evidence POST（新契约）：409 且 Provider 零调用。
	evidenceRec := postReviewForm(t, srv, session, csrf, "/workbench/reviews/evidence", "_csrf="+csrf+"&revision_id="+rev.ID)
	if evidenceRec.Code != http.StatusConflict {
		t.Fatalf("新契约 evidence POST 必须 409: %d %s", evidenceRec.Code, evidenceRec.Body.String())
	}
	if !strings.Contains(evidenceRec.Body.String(), "主张审校") {
		t.Fatalf("409 文案必须指向主张审校: %s", evidenceRec.Body.String())
	}
	if len(evidence.requests) != 0 {
		t.Fatalf("evidence Provider 零调用: %d", len(evidence.requests))
	}
}

// TestPublicationGateR20 B：交付门禁矩阵（table-driven）。
func TestPublicationGateR20(t *testing.T) {
	cases := []struct {
		name    string
		setup   func(t *testing.T, srv *Server, rev *models.ArticleRevision)
		want    int
		wantMsg string
	}{
		{"missing style", func(t *testing.T, srv *Server, rev *models.ArticleRevision) {
			job, err := srv.store.EnqueueRevisionReview(t.Context(), rev.ID, store.ReviewKindClaim)
			check(t, err)
			mustRunJob(t, srv, job.ID)
			_, err = srv.store.SaveClaimReviewOutput(t.Context(), job.ID, &provider.ClaimReviewResult{Status: provider.ClaimReviewPassed}, "p", "m")
			check(t, err)
		}, http.StatusConflict, "风格审校"},
		{"claim failed", func(t *testing.T, srv *Server, rev *models.ArticleRevision) {
			job, err := srv.store.EnqueueRevisionReview(t.Context(), rev.ID, store.ReviewKindClaim)
			check(t, err)
			mustRunJob(t, srv, job.ID)
			_, err = srv.store.SaveClaimReviewOutput(t.Context(), job.ID, &provider.ClaimReviewResult{
				Status:   provider.ClaimReviewFailed,
				Findings: []provider.ClaimReviewFinding{{Excerpt: "来源说过这句话。", IssueKind: "misattributed", Detail: "归因错误"}},
			}, "p", "m")
			check(t, err)
		}, http.StatusConflict, "主张审校未通过"},
		{"style failed", func(t *testing.T, srv *Server, rev *models.ArticleRevision) {
			claimJob, err := srv.store.EnqueueRevisionReview(t.Context(), rev.ID, store.ReviewKindClaim)
			check(t, err)
			mustRunJob(t, srv, claimJob.ID)
			_, err = srv.store.SaveClaimReviewOutput(t.Context(), claimJob.ID, &provider.ClaimReviewResult{Status: provider.ClaimReviewPassed}, "p", "m")
			check(t, err)
			styleJob, err := srv.store.EnqueueRevisionReview(t.Context(), rev.ID, store.ReviewKindStyle)
			check(t, err)
			mustRunJob(t, srv, styleJob.ID)
			_, err = srv.store.SaveStyleReviewOutput(t.Context(), styleJob.ID, &provider.StyleReviewResult{Status: "failed", Issues: []string{"节奏拖沓"}}, "p", "m")
			check(t, err)
		}, http.StatusConflict, "风格审校未通过"},
		{"legacy only", func(t *testing.T, srv *Server, rev *models.ArticleRevision) {
			// 仅旧 EvidenceReview 拷贝/advisory 行（origin_job_id=''）。
			p, m := "legacy-p", "legacy-m"
			if _, err := srv.store.CreateClaimReview(t.Context(), models.ClaimReview{WorkRevisionID: rev.ID, Status: "passed", Provider: &p, Model: &m, PromptVersion: &m}); err != nil {
				t.Fatal(err)
			}
			if _, err := srv.store.CreateArticleReview(t.Context(), models.ArticleReview{RevisionID: rev.ID, Kind: "style", Status: "advisory", IssuesJSON: `["旧"]`, Provider: &p, Model: &m}); err != nil {
				t.Fatal(err)
			}
		}, http.StatusConflict, "旧版"},
		{"valid export", func(t *testing.T, srv *Server, rev *models.ArticleRevision) {
			claimJob, err := srv.store.EnqueueRevisionReview(t.Context(), rev.ID, store.ReviewKindClaim)
			check(t, err)
			mustRunJob(t, srv, claimJob.ID)
			_, err = srv.store.SaveClaimReviewOutput(t.Context(), claimJob.ID, &provider.ClaimReviewResult{Status: provider.ClaimReviewPassed}, "p", "m")
			check(t, err)
			styleJob, err := srv.store.EnqueueRevisionReview(t.Context(), rev.ID, store.ReviewKindStyle)
			check(t, err)
			mustRunJob(t, srv, styleJob.ID)
			_, err = srv.store.SaveStyleReviewOutput(t.Context(), styleJob.ID, &provider.StyleReviewResult{Status: "passed"}, "p", "m")
			check(t, err)
		}, http.StatusOK, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newTestServer(t)
			rev, _, _ := seedNewContractRevision(t, srv, "r20b")
			tc.setup(t, srv, rev)
			req := httptest.NewRequest(http.MethodGet, "/workbench/revisions/"+rev.ID+"/package", nil)
			rec := httptest.NewRecorder()
			srv.handlePublicationPackage(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("%s: want %d got %d body=%s", tc.name, tc.want, rec.Code, rec.Body.String())
			}
			if tc.wantMsg != "" && !strings.Contains(rec.Body.String(), tc.wantMsg) {
				t.Fatalf("%s: 409 文案缺少 %q: %s", tc.name, tc.wantMsg, rec.Body.String())
			}
			if tc.want == http.StatusOK {
				for _, want := range []string{"来源说过这句话", "来源"} {
					if !strings.Contains(rec.Body.String(), want) {
						t.Fatalf("导出内容缺少 %q", want)
					}
				}
			}
		})
	}

	// 单独：非当前修订（其他 revision）即使审校 passed 也 409。
	srv := newTestServer(t)
	rev, draftID, _ := seedNewContractRevision(t, srv, "r20c")
	_, err := srv.store.CreateArticleRevision(t.Context(), models.ArticleRevision{DraftID: draftID, Title: "v2", Markdown: "# v2", Origin: "owner"})
	check(t, err)
	req := httptest.NewRequest(http.MethodGet, "/workbench/revisions/"+rev.ID+"/package", nil)
	rec := httptest.NewRecorder()
	srv.handlePublicationPackage(rec, req)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "当前修订") {
		t.Fatalf("非当前修订必须 409: %d %s", rec.Code, rec.Body.String())
	}
}

// TestArticleDraftPageR20 C：页面按钮与 job 文案（新契约 vs 旧文章）。
func TestArticleDraftPageR20(t *testing.T) {
	srv := newTestServer(t)
	rev, draftID, _ := seedNewContractRevision(t, srv, "r20d")
	// 构造 failed claim job + last_error 供页面展示。
	job, err := srv.store.EnqueueRevisionReview(t.Context(), rev.ID, store.ReviewKindClaim)
	check(t, err)
	check(t, srv.store.MarkJobFailed(t.Context(), job.ID, "来源策略禁止外发"))
	_, err = srv.store.EnqueueRevisionReview(t.Context(), rev.ID, store.ReviewKindStyle)
	check(t, err)

	session := claimOwnerAndLogin(t, srv, "r20d@example.com", "password123")
	req := httptest.NewRequest(http.MethodGet, "/workbench/drafts/"+draftID, nil)
	req.AddCookie(session)
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("draft page: %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"运行/重试主张审校", "运行/重试风格审校",
		"主张审校与风格审校均通过后才能导出",
		"主张审校任务：failed", "来源策略禁止外发",
		"缺少独立风格审校",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("新契约页面缺少 %q", want)
		}
	}
	if strings.Contains(body, "运行独立证据审校") {
		t.Fatal("新契约页面不得出现证据审校按钮")
	}

	// 旧文章：保留 Evidence + 同步 style 按钮，无新契约按钮/文案。
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
	_, err = srv.store.CreateArticleRevision(ctx, models.ArticleRevision{DraftID: draft.ID, Title: "旧文章", Markdown: "# 旧正文", Origin: "owner"})
	check(t, err)
	req2 := httptest.NewRequest(http.MethodGet, "/workbench/drafts/"+draft.ID, nil)
	req2.AddCookie(session)
	rec2 := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("legacy page: %d", rec2.Code)
	}
	legacyBody := rec2.Body.String()
	for _, want := range []string{"运行独立证据审校", "运行独立风格审校"} {
		if !strings.Contains(legacyBody, want) {
			t.Fatalf("旧文章页面缺少 %q", want)
		}
	}
	if strings.Contains(legacyBody, "运行/重试主张审校") {
		t.Fatal("旧文章页面不得出现新契约按钮")
	}
}

// TestNewContractPageRejectsLegacyClaimRowR20（item 2）：新契约页面只有旧
// EvidenceReview 兼容副本（origin_job_id=”）时，不得显示为已完成独立 ClaimReview，
// 也不得误解锁导出。
func TestNewContractPageRejectsLegacyClaimRowR20(t *testing.T) {
	srv := newTestServer(t)
	rev, draftID, _ := seedNewContractRevision(t, srv, "r20e")
	p, m, v := "legacy-p", "legacy-m", "legacy-v"
	_, err := srv.store.CreateClaimReview(t.Context(), models.ClaimReview{WorkRevisionID: rev.ID, Status: "passed", Provider: &p, Model: &m, PromptVersion: &v})
	check(t, err)
	session := claimOwnerAndLogin(t, srv, "r20e@example.com", "password123")
	req := httptest.NewRequest(http.MethodGet, "/workbench/drafts/"+draftID, nil)
	req.AddCookie(session)
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("draft page: %d", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, "最新 ClaimReview：passed") {
		t.Fatal("旧兼容副本不得显示为已完成独立 ClaimReview")
	}
	if !strings.Contains(body, "仅存在旧版证据审校记录") {
		t.Fatalf("页面应显示仅旧审校门禁: %s", body)
	}
	// 导出不得误解锁。
	pkgReq := httptest.NewRequest(http.MethodGet, "/workbench/revisions/"+rev.ID+"/package", nil)
	pkgRec := httptest.NewRecorder()
	srv.handlePublicationPackage(pkgRec, pkgReq)
	if pkgRec.Code != http.StatusConflict || !strings.Contains(pkgRec.Body.String(), "旧版") {
		t.Fatalf("仅旧审校必须 409: %d %s", pkgRec.Code, pkgRec.Body.String())
	}
}

// TestCorruptedClaimMapBlocksReviewAndExportR20（item 3）：ClaimMap JSON 损坏时
// 读取/入队/导出全部失败，不得静默降级。
func TestCorruptedClaimMapBlocksReviewAndExportR20(t *testing.T) {
	srv := newTestServer(t)
	rev, _, _ := seedNewContractRevision(t, srv, "r20f")
	// 正常完成 claim+style 审校后导出 200，再损坏 ClaimMap → 导出阻断。
	claimJob, err := srv.store.EnqueueRevisionReview(t.Context(), rev.ID, store.ReviewKindClaim)
	check(t, err)
	mustRunJob(t, srv, claimJob.ID)
	_, err = srv.store.SaveClaimReviewOutput(t.Context(), claimJob.ID, &provider.ClaimReviewResult{Status: provider.ClaimReviewPassed}, "p", "m")
	check(t, err)
	styleJob, err := srv.store.EnqueueRevisionReview(t.Context(), rev.ID, store.ReviewKindStyle)
	check(t, err)
	mustRunJob(t, srv, styleJob.ID)
	_, err = srv.store.SaveStyleReviewOutput(t.Context(), styleJob.ID, &provider.StyleReviewResult{Status: "passed"}, "p", "m")
	check(t, err)
	pkgReq := httptest.NewRequest(http.MethodGet, "/workbench/revisions/"+rev.ID+"/package", nil)
	pkgRec := httptest.NewRecorder()
	srv.handlePublicationPackage(pkgRec, pkgReq)
	if pkgRec.Code != http.StatusOK {
		t.Fatalf("基线导出应 200: %d %s", pkgRec.Code, pkgRec.Body.String())
	}
	// 损坏 material_ids_json。
	_, err = srv.store.DB.ExecContext(t.Context(), `UPDATE claim_map_entries SET material_ids_json='{broken' WHERE revision_id=?`, rev.ID)
	check(t, err)
	pkgRec2 := httptest.NewRecorder()
	srv.handlePublicationPackage(pkgRec2, pkgReq)
	if pkgRec2.Code == http.StatusOK {
		t.Fatal("损坏 ClaimMap 不得导出成功")
	}
}

// TestClaimMapSourcesDeterministicOrderR20（item 5）：来源输出按 material ID 排序，
// 不随 map 迭代顺序漂移（跨两个真实来源）。
func TestClaimMapSourcesDeterministicOrderR20(t *testing.T) {
	srv := newTestServer(t)
	rev, draftID, kpA := seedNewContractRevision(t, srv, "r20g")
	// 第二个真实来源（自带转录与 KeyPoint）。
	epB := seedDiscoveryEpisode(t, srv, "r20g-b", "另一个来源的讨论。", "seg-b")
	kps, _, err := srv.store.ListKeyPointsFiltered(t.Context(), store.KeyPointFilter{SourceID: epB}, 1, 10)
	check(t, err)
	if len(kps) == 0 {
		t.Fatal("第二个来源缺少 KeyPoint fixture")
	}
	kpB := kps[0].ID
	// ClaimMap 单条目乱序引用两个材料（排序前与 ID 顺序相反也无妨）。
	_, err = srv.store.DB.ExecContext(t.Context(),
		`UPDATE claim_map_entries SET material_ids_json=? WHERE revision_id=? AND claim_kind='source_claim'`,
		`["`+kpB+`","`+kpA+`"]`, rev.ID)
	check(t, err)
	draft, err := srv.store.GetArticleDraft(t.Context(), draftID)
	check(t, err)
	sources, err := srv.claimMapSources(httptest.NewRequest(http.MethodGet, "/", nil), rev, draft.EditorialProfileID)
	check(t, err)
	if len(sources) != 2 {
		t.Fatalf("来源数=%d: %v", len(sources), sources)
	}
	// 期望顺序：material ID 较小者来源在前。
	row := srv.store.DB.QueryRowContext(t.Context(), `SELECT source_title FROM keypoint_index WHERE id IN (?,?) ORDER BY id`, kpA, kpB)
	var firstTitle string
	check(t, row.Scan(&firstTitle))
	if sources[0] != firstTitle {
		t.Fatalf("来源顺序不确定: got %v want first=%q", sources, firstTitle)
	}
	// 重复调用输出稳定。
	again, err := srv.claimMapSources(httptest.NewRequest(http.MethodGet, "/", nil), rev, draft.EditorialProfileID)
	check(t, err)
	if strings.Join(again, "|") != strings.Join(sources, "|") {
		t.Fatalf("两次调用顺序漂移: %v vs %v", sources, again)
	}
}
