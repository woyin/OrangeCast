package server

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

type weeklyFake struct {
	calls int
	fail  bool
}

func (f *weeklyFake) Name() string { return "pod" }
func (f *weeklyFake) KnowledgeArticleStep(_ context.Context, req provider.KnowledgeArticleRequest) (*provider.KnowledgeArticleResult, provider.TaskUsage, error) {
	f.calls++
	if f.fail {
		return nil, provider.TaskUsage{}, fmt.Errorf("回顾连接失败")
	}
	return &provider.KnowledgeArticleResult{Questions: []provider.LearningReviewQuestion{{Question: "怎样区分来源和个人理解？", AnswerBasis: "隐藏依据标记：先检查来源再标明解释。", MaterialIDs: []string{req.Materials[0].ID}}}}, provider.TaskUsage{InputUnits: 10, OutputUnits: 20}, nil
}
func TestLearningReviewAnswerBeforeEvidenceAndExplicitReflection(t *testing.T) {
	srv := newTestServer(t)
	session := claimOwnerAndLogin(t, srv, "weekly@example.com", "password123")
	_, _ = seedKnowledgeLearning(t, srv)
	srv.cfg.PodBaseURL, srv.cfg.PodAPIKey, srv.cfg.PodModel = "https://example.test/v1", "test", "model"
	fake := &weeklyFake{}
	srv.worker.WithBundleResolver(func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{KnowledgeArticle: fake}, nil
	})
	if page := doWithCookie(srv, session, "GET", "/review"); page.Code != 200 || !strings.Contains(page.Body.String(), "UTC") {
		t.Fatal(page.Code, page.Body.String())
	}
	if err := srv.RunLearningReviews(t.Context(), time.Now()); err != nil || fake.calls != 0 {
		t.Fatal("default automated review", err)
	}
	result := postForm(t, srv, session, "/review/action", "action=generate")
	if result.Code != 303 {
		t.Fatal(result.Code, result.Body.String())
	}
	path := result.Header().Get("Location")
	if err := srv.worker.ProcessOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	batches, _ := srv.store.ListLearningReviewBatches(t.Context())
	items, _ := srv.store.ListLearningReviewItems(t.Context(), batches[0].ID)
	page := doWithCookie(srv, session, "GET", path)
	if page.Code != 200 || strings.Contains(page.Body.String(), "隐藏依据标记") {
		t.Fatal("basis leaked before answer", page.Code, page.Body.String())
	}
	id := items[0].ID
	vals := url.Values{"action": {"answer"}, "item": {id}, "expected_revision": {"0"}, "answer": {"我认为需要先识别原话，再解释自己怎么理解。"}, "assessment": {"partial"}}
	response := postForm(t, srv, session, "/review/action", vals.Encode())
	if response.Code != 303 {
		t.Fatal(response.Code, response.Body.String())
	}
	page = doWithCookie(srv, session, "GET", path)
	if page.Code != 200 || !strings.Contains(page.Body.String(), "隐藏依据标记") || !strings.Contains(page.Body.String(), "总费用 未知") {
		t.Fatal(page.Code, page.Body.String())
	}
	response = postForm(t, srv, session, "/review/action", vals.Encode())
	if response.Code != 400 || !strings.Contains(response.Body.String(), vals.Get("answer")) {
		t.Fatal("stale answer draft lost")
	}
	response = postForm(t, srv, session, "/review/action", url.Values{"action": {"save_note"}, "item": {id}, "expected_revision": {"1"}}.Encode())
	if response.Code != 303 {
		t.Fatal(response.Code, response.Body.String())
	}
	items, _ = srv.store.ListLearningReviewItems(t.Context(), batches[0].ID)
	note, e := srv.store.GetOwnerNote(t.Context(), items[0].NoteID)
	if e != nil || note.Kind != "owner_reflection" {
		t.Fatal("answer fabricated source note", e)
	}
	_ = postForm(t, srv, session, "/review/action", "action=generate")
	_ = srv.worker.ProcessOne(t.Context())
	if fake.calls != 1 {
		t.Fatal("same week recharged")
	}
	prefs := postForm(t, srv, session, "/review/action", "action=settings&enabled=on&timezone=Asia%2FSingapore&weekday=1&clock_time=18%3A00")
	if prefs.Code != 303 {
		t.Fatal(prefs.Code, prefs.Body.String())
	}
	articles, _ := srv.store.GetKnowledgeArticleSettings(t.Context())
	if articles.Enabled {
		t.Fatal("review enabled articles")
	}
	var req provider.KnowledgeArticleRequest
	_ = json.Unmarshal([]byte(batches[0].InputJSON), &req)
	if req.Stage != "weekly_review" {
		t.Fatal("frozen stage")
	}
	for _, action := range []string{"wrong", "retry", "later", "reveal"} {
		if p := postForm(t, srv, session, "/review/action", "action="+action); p.Code != 400 {
			t.Fatal(action, p.Code)
		}
	}
	if p := postForm(t, srv, session, "/review", ""); p.Code != 405 {
		t.Fatal(p.Code)
	}
	if p := doWithCookie(srv, session, http.MethodGet, "/review/action"); p.Code != 405 {
		t.Fatal(p.Code)
	}
}
