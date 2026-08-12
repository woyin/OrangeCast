package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/woyin/orangecast/internal/models"
)

func TestWechatRichTextEscapesRevisionHTMLAndRendersMarkdown(t *testing.T) {
	rich := wechatRichText("# 标题\n\n一段 <script>alert(1)</script>\n- 第一项\n- 第二项")
	for _, want := range []string{"<h1>标题</h1>", "&lt;script&gt;alert(1)&lt;/script&gt;", "<ul><li>第一项</li><li>第二项</li></ul>"} {
		if !strings.Contains(rich, want) {
			t.Fatalf("rich text should contain %q: %s", want, rich)
		}
	}
	if strings.Contains(rich, "<script>") {
		t.Fatalf("revision HTML must not be trusted: %s", rich)
	}
}

func TestWechatRichTextCoversHeadingsAndEmptySourceList(t *testing.T) {
	rich := wechatRichText("## 二级\n### 三级\n\n正文")
	for _, want := range []string{"<h2>二级</h2>", "<h3>三级</h3>", "<p>正文</p>"} {
		if !strings.Contains(rich, want) {
			t.Fatalf("rich text should contain %q: %s", want, rich)
		}
	}
	if got := markdownSourceList(nil); got != "- 本文未使用外部来源。\n" {
		t.Fatalf("empty source list should be explicit: %q", got)
	}
}

func TestPublicationPackageRequiresReadyCurrentRevision(t *testing.T) {
	srv := newTestServer(t)
	ctx := context.Background()
	profile, err := srv.store.CreateEditorialProfile(ctx, models.EditorialProfile{Name: "品牌"})
	if err != nil {
		t.Fatal(err)
	}
	proposal, err := srv.store.CreateArticleProposal(ctx, models.ArticleProposal{EditorialProfileID: profile.ID, Title: "选题"})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.store.SetArticleProposalStatus(ctx, proposal.ID, "accepted"); err != nil {
		t.Fatal(err)
	}
	brief, err := srv.store.CreateArticleBrief(ctx, models.ArticleBrief{ProposalID: proposal.ID, Thesis: "论点", Outline: "# 结构", MaterialPlan: "[]", ConflictPlan: "[]"})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.store.ConfirmArticleBrief(ctx, brief.ID); err != nil {
		t.Fatal(err)
	}
	draft, err := srv.store.CreateArticleDraft(ctx, brief.ID, "文章")
	if err != nil {
		t.Fatal(err)
	}
	first, err := srv.store.CreateArticleRevision(ctx, models.ArticleRevision{DraftID: draft.ID, Title: "文章", Markdown: "# 第一版", Origin: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.store.CreateArticleReview(ctx, models.ArticleReview{RevisionID: first.ID, Kind: "evidence", Status: "passed", IssuesJSON: "[]"}); err != nil {
		t.Fatal(err)
	}

	call := func(revisionID string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/workbench/revisions/"+revisionID+"/package", nil)
		rec := httptest.NewRecorder()
		srv.handlePublicationPackage(rec, req)
		return rec
	}
	if rec := call(first.ID); rec.Code != http.StatusOK {
		t.Fatalf("ready current revision should publish: %d %s", rec.Code, rec.Body.String())
	}
	second, err := srv.store.CreateArticleRevision(ctx, models.ArticleRevision{DraftID: draft.ID, Title: "文章", Markdown: "# 第二版", Origin: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	if rec := call(first.ID); rec.Code != http.StatusConflict {
		t.Fatalf("passed historic revision must not publish after an edit: %d", rec.Code)
	}
	if rec := call(second.ID); rec.Code != http.StatusConflict {
		t.Fatalf("unreviewed current revision must not publish: %d", rec.Code)
	}
	if _, err := srv.store.CreateArticleReview(ctx, models.ArticleReview{RevisionID: second.ID, Kind: "evidence", Status: "passed", IssuesJSON: "[]"}); err != nil {
		t.Fatal(err)
	}
	if rec := call(second.ID); rec.Code != http.StatusOK {
		t.Fatalf("ready current replacement should publish: %d %s", rec.Code, rec.Body.String())
	}
}
