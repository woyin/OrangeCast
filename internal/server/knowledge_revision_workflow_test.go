package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

func TestKnowledgeManualRevisionAndVersionDownload(t *testing.T) {
	srv := newTestServer(t)
	session := claimOwnerAndLogin(t, srv, "revisions@example.com", "password123")
	_, noteID := seedKnowledgeLearning(t, srv)
	srv.cfg.PodBaseURL, srv.cfg.PodAPIKey, srv.cfg.PodModel, srv.cfg.PodReviewModel = "https://example.test/v1", "test", "writer", "reviewer"
	fake := &knowledgeStageFake{}
	srv.worker.WithBundleResolver(func(job *models.ProcessingJob) (*provider.ProviderBundle, error) {
		execution, err := srv.store.GetJobExecution(t.Context(), job.ID)
		if err != nil {
			return nil, err
		}
		var input struct {
			Stage string `json:"stage"`
		}
		_ = json.Unmarshal([]byte(execution.InputSnapshotJSON), &input)
		if strings.HasPrefix(input.Stage, "review") && execution.ConfiguredModel != "reviewer" {
			t.Error("review model not frozen")
		}
		return &provider.ProviderBundle{KnowledgeArticle: fake}, nil
	})
	rec := postForm(t, srv, session, "/knowledge-articles/generate", "")
	if rec.Code != 303 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	path := rec.Header().Get("Location")
	for i := 0; i < 4; i++ {
		if err := srv.worker.ProcessOne(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	records, _ := srv.store.ListKnowledgeArticles(t.Context())
	article := records[0]
	revision, err := srv.store.GetKnowledgeRevision(t.Context(), article.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	var req provider.KnowledgeArticleRequest
	var blocks []provider.KnowledgeBlock
	_ = json.Unmarshal([]byte(revision.InputJSON), &req)
	_ = json.Unmarshal([]byte(revision.BlocksJSON), &blocks)
	vals := url.Values{"article_id": {article.ID}, "expected_revision": {"1"}, "action": {"save"}, "title": {"手动改进的工作稿"}}
	for i, block := range blocks {
		vals.Add("block_text", block.Text+" 人工补充条件。")
		vals.Set(fmt.Sprintf("block_kind_%d", i), block.Kind)
		for _, id := range block.MaterialIDs {
			vals.Add(fmt.Sprintf("block_material_%d", i), id)
		}
	}
	saved := postForm(t, srv, session, "/knowledge-articles/action", vals.Encode())
	if saved.Code != 303 {
		t.Fatal(saved.Code, saved.Body.String())
	}
	if stale := postForm(t, srv, session, "/knowledge-articles/action", vals.Encode()); stale.Code != 409 || !strings.Contains(stale.Body.String(), "人工补充条件") {
		t.Fatal("draft lost on conflict", stale.Code, stale.Body.String())
	}
	if unreviewed := doWithCookie(srv, session, http.MethodGet, path+"/download?revision=2"); unreviewed.Code != 409 {
		t.Fatal("new body inherited review")
	}
	if old := doWithCookie(srv, session, http.MethodGet, path+"/download?revision=1"); old.Code != 200 {
		t.Fatal("old pass lost", old.Code, old.Body.String())
	}
	if page := doWithCookie(srv, session, http.MethodGet, path); page.Code != 200 || !strings.Contains(page.Body.String(), "手动改进的工作稿") || !strings.Contains(page.Body.String(), "最近通过 v1") {
		t.Fatal(page.Code, page.Body.String())
	}
	for _, action := range []string{"feedback", "review"} {
		f := url.Values{"article_id": {article.ID}, "expected_revision": {"2"}, "action": {action}, "category": {"shallow"}, "comment": {"解释条件"}}
		if result := postForm(t, srv, session, "/knowledge-articles/action", f.Encode()); result.Code != 303 {
			t.Fatal(action, result.Code, result.Body.String())
		}
	}
	if err := srv.worker.ProcessOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if reviewed := doWithCookie(srv, session, http.MethodGet, path+"/download?revision=2"); reviewed.Code != 200 {
		t.Fatal(reviewed.Code, reviewed.Body.String())
	}
	note, _ := srv.store.GetOwnerNote(t.Context(), noteID)
	if _, err := srv.store.UpdateOwnerNote(t.Context(), noteID, "新的个人理解", "[]", note.ReferencesJSON, note.Revision); err != nil {
		t.Fatal(err)
	}
	if invalid := doWithCookie(srv, session, http.MethodGet, path+"/download?revision=2"); invalid.Code != 409 {
		t.Fatal("invalid evidence exported")
	}
	if state := doWithCookie(srv, session, http.MethodGet, path+"?state=1"); state.Code != 200 || !strings.Contains(state.Body.String(), "WorkingRevision") {
		t.Fatal(state.Code, state.Body.String())
	}
	if bad := postForm(t, srv, session, "/knowledge-articles/action", "article_id="+article.ID+"&action=unknown&expected_revision=2"); bad.Code != 400 {
		t.Fatal(bad.Code)
	}
}
