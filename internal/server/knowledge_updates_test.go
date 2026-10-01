package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
)

func updateHTTPFixture(t *testing.T) (*Server, *http.Cookie, *store.KnowledgeArticleRecord, *knowledgeStageFake, string) {
	t.Helper()
	srv := newTestServer(t)
	session := claimOwnerAndLogin(t, srv, "updates@example.com", "password123")
	_, note := seedKnowledgeLearning(t, srv)
	srv.cfg.PodBaseURL, srv.cfg.PodAPIKey, srv.cfg.PodModel, srv.cfg.PodReviewModel = "https://example.test/v1", "test", "writer", "reviewer"
	fake := &knowledgeStageFake{}
	srv.worker.WithBundleResolver(func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{KnowledgeArticle: fake}, nil
	})
	rec := postForm(t, srv, session, "/knowledge-articles/generate", "")
	if rec.Code != 303 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	for i := 0; i < 4; i++ {
		if err := srv.worker.ProcessOne(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	a, err := srv.store.GetKnowledgeArticle(t.Context(), strings.TrimPrefix(rec.Header().Get("Location"), "/knowledge-articles/"))
	if err != nil || a.PassedRevision != 1 {
		t.Fatal(a, err)
	}
	n, _ := srv.store.GetOwnerNote(t.Context(), note)
	if _, err = srv.store.UpdateOwnerNote(t.Context(), note, n.Content+" 我补充了适用条件。", n.CitationsJSON, n.ReferencesJSON, n.Revision); err != nil {
		t.Fatal(err)
	}
	return srv, session, a, fake, note
}
func TestKnowledgeUpdateHTTPJourneyAndReadOnlyGET(t *testing.T) {
	srv, session, a, fake, _ := updateHTTPFixture(t)
	ctx := t.Context()
	post := func(action, id string) *httptest.ResponseRecorder {
		return postForm(t, srv, session, "/knowledge-updates/action", url.Values{"action": {action}, "proposal_id": {id}, "article_id": {a.ID}, "expected_revision": {"1"}, "reason": {"我想核对边界"}}.Encode())
	}
	rec := post("inspect", "")
	if rec.Code != 303 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	path := rec.Header().Get("Location")
	id := strings.TrimPrefix(path, "/knowledge-updates/")
	before := fake.calls
	for _, path := range []string{"/knowledge-updates", path, "/knowledge-articles/" + a.ID} {
		rec = doWithCookie(srv, session, "GET", path)
		if rec.Code != 200 || rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatal(path, rec.Code, rec.Header(), rec.Body.String())
		}
	}
	if fake.calls != before {
		t.Fatal("GET called model")
	}
	for _, action := range []string{"defer", "resume", "analyse", "analyse"} {
		if rec = post(action, id); rec.Code != 303 {
			t.Fatal(action, rec.Code, rec.Body.String())
		}
	}
	jobs, _ := srv.store.ListQueuedOrRunning(ctx)
	if len(jobs) != 1 {
		t.Fatal("duplicate analysis", jobs)
	}
	srv.worker.ProcessOne(ctx)
	page := doWithCookie(srv, session, "GET", path)
	if page.Code != 200 || !strings.Contains(page.Body.String(), "补充适用条件") || !strings.Contains(page.Body.String(), "接受判断") {
		t.Fatal(page.Code, page.Body.String())
	}
	if rec = post("accept", id); rec.Code != 303 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	srv.worker.ProcessOne(ctx)
	a, _ = srv.store.GetKnowledgeArticle(ctx, a.ID)
	if a.WorkingRevision != 2 || a.PassedRevision != 1 {
		t.Fatal(a)
	}
	srv.worker.ProcessOne(ctx)
	p, _ := srv.store.GetKnowledgeUpdateProposal(ctx, id)
	if p.State != "completed" || p.GeneratedRevision != 2 {
		t.Fatal(p)
	}
	page = doWithCookie(srv, session, "GET", path)
	if page.Code != 200 || !strings.Contains(page.Body.String(), "提案生成稿 v2") || !strings.Contains(page.Body.String(), "更新稿已通过") {
		t.Fatal(page.Code, page.Body.String())
	}
	if rec = post("inspect", ""); rec.Code != 409 {
		t.Fatal("stale parent accepted", rec.Code)
	}
	if rec = post("ignore", id); rec.Code != 409 {
		t.Fatal("completed disposition", rec.Code)
	}
}
func TestKnowledgeUpdateSchedulerIsIndependentAndDoesNotReplay(t *testing.T) {
	srv, _, a, fake, _ := updateHTTPFixture(t)
	ctx := t.Context()
	before := fake.calls
	if err := srv.RunKnowledgeUpdates(ctx); err != nil {
		t.Fatal(err)
	}
	proposals, err := srv.store.ListKnowledgeUpdateProposals(ctx, a.ID, "pending")
	if err != nil || len(proposals) != 1 {
		t.Fatal(proposals, err)
	}
	if fake.calls != before {
		t.Fatal("disabled updater called AI")
	}
	jobs, _ := srv.store.ListQueuedOrRunning(ctx)
	if len(jobs) != 0 {
		t.Fatal("disabled updater queued", jobs)
	}
	if err = srv.store.SetKnowledgeUpdateSettings(ctx, store.KnowledgeUpdateSettings{Enabled: true, DailyLimit: 1}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err = srv.RunKnowledgeUpdates(ctx); err != nil {
			t.Fatal(err)
		}
	}
	jobs, _ = srv.store.ListQueuedOrRunning(ctx)
	if len(jobs) != 1 {
		t.Fatal("scheduler duplicated input", jobs)
	}
	srv.worker.ProcessOne(ctx)
	if err = srv.RunKnowledgeUpdates(ctx); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		srv.worker.ProcessOne(ctx)
	}
	p, _ := srv.store.GetKnowledgeUpdateProposal(ctx, proposals[0].ID)
	if p.State != "completed" || fake.calls != before+3 {
		t.Fatal(p, fake.calls, before)
	}
	if err = srv.RunKnowledgeUpdates(ctx); err != nil {
		t.Fatal(err)
	}
	jobs, _ = srv.store.ListQueuedOrRunning(ctx)
	if len(jobs) != 0 {
		t.Fatal("completed update replay", jobs)
	}
	settings, _ := srv.store.GetKnowledgeArticleSettings(ctx)
	if settings.Enabled {
		t.Fatal("update changed discovery preference")
	}
}
func TestKnowledgeUpdateHTTPAuthorizationValidationAndSettings(t *testing.T) {
	srv, session, a, _, _ := updateHTTPFixture(t)
	for _, path := range []string{"/knowledge-updates", "/knowledge-updates/missing", "/knowledge-updates/action"} {
		if rec := doWithCookie(srv, nil, "GET", path); rec.Code != 303 {
			t.Fatal(path, rec.Code)
		}
	}
	request := httptest.NewRequest("POST", "/knowledge-updates/action", strings.NewReader("action=settings&enabled=on&daily_limit=1"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(session)
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, request)
	if rec.Code != 403 {
		t.Fatal(rec.Code)
	}
	for _, tc := range []struct {
		body string
		code int
	}{{"action=unknown", 400}, {"action=settings&daily_limit=bad", 400}, {"action=settings&daily_limit=0", 400}, {"action=settings&daily_limit=2&enabled=on", 303}, {"action=inspect&article_id=" + a.ID + "&expected_revision=bad", 400}, {"action=inspect&article_id=missing&expected_revision=1", 404}, {"action=analyse&proposal_id=missing", 404}} {
		rec = postForm(t, srv, session, "/knowledge-updates/action", tc.body)
		if rec.Code != tc.code {
			t.Fatal(tc.body, rec.Code, rec.Body.String())
		}
	}
	srv.cfg.PodAPIKey = ""
	for _, body := range []string{"action=settings&daily_limit=2&enabled=on", "action=analyse&proposal_id=missing", "action=accept&proposal_id=missing", "action=retry&proposal_id=missing"} {
		if rec = postForm(t, srv, session, "/knowledge-updates/action", body); rec.Code != 400 {
			t.Fatal(body, rec.Code)
		}
	}
	if rec = doWithCookie(srv, session, "GET", "/knowledge-updates/missing"); rec.Code != 404 {
		t.Fatal(rec.Code)
	}
	if rec = doWithCookie(srv, session, "GET", "/knowledge-updates/action"); rec.Code != 405 {
		t.Fatal(rec.Code)
	}
	for _, handler := range []http.HandlerFunc{srv.handleKnowledgeUpdates, srv.handleKnowledgeUpdateDetail, srv.handleKnowledgeUpdateAction} {
		rec = httptest.NewRecorder()
		handler(rec, httptest.NewRequest("PUT", "/knowledge-updates", nil))
		if rec.Code != 405 {
			t.Fatal(rec.Code)
		}
	}
	if knowledgeUpdateStatus("unknown") != "待处理" || knowledgeUpdateAction("unknown") != "待核对" {
		t.Fatal("unknown labels")
	}
}
func TestKnowledgeQuestionGenerationPrefersExistingArticle(t *testing.T) {
	srv, session, a, _, _ := updateHTTPFixture(t)
	ctx := t.Context()
	q, err := srv.store.CreateLearningQuestion(ctx, store.LearningQuestion{Body: "怎样区分来源和理解？", Goal: "整理边界"})
	if err != nil {
		t.Fatal(err)
	}
	var current provider.KnowledgeArticleRequest
	parent, _ := srv.store.GetKnowledgeRevision(ctx, a.ID, 1)
	json.Unmarshal([]byte(parent.InputJSON), &current)
	for _, m := range current.Materials {
		q, err = srv.store.ChangeLearningQuestion(ctx, q.ID, q.Revision, store.LearningQuestionChange{Action: "link", Link: provider.LearningQuestionLink{Kind: "note", ObjectID: m.ID}})
		if err != nil {
			t.Fatal(err)
		}
	}
	current.Question, err = srv.store.FreezeLearningQuestion(ctx, q.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	// Build current material snapshots after the fixture's note edit.
	req, _, _, err := srv.store.BuildKnowledgeDiscoveryRequest(ctx, a.ProfileID, a.Provider, store.KnowledgeScope{QuestionID: q.ID}, false)
	if err != nil {
		t.Fatal(err)
	}
	current.Materials = req.Materials
	var blocks []provider.KnowledgeBlock
	json.Unmarshal([]byte(parent.BlocksJSON), &blocks)
	if _, err = srv.store.SaveKnowledgeDraft(ctx, a.ID, 1, parent.Title, blocks, current); err != nil {
		t.Fatal(err)
	}
	q, err = srv.store.ChangeLearningQuestion(ctx, q.ID, q.Revision, store.LearningQuestionChange{Action: "link", Link: provider.LearningQuestionLink{Kind: "article", ObjectID: a.ID}})
	if err != nil {
		t.Fatal(err)
	}
	rec := postForm(t, srv, session, "/questions/action", url.Values{"action": {"generate"}, "question_id": {q.ID}, "expected_revision": {jsonValue(q.Revision)}}.Encode())
	if rec.Code != 303 || rec.Header().Get("Location") != "/knowledge-articles/"+a.ID {
		t.Fatal(rec.Code, rec.Header(), rec.Body.String())
	}
	articles, _ := srv.store.ListKnowledgeArticles(ctx)
	if len(articles) != 1 {
		t.Fatal("same question created article", len(articles))
	}
	q, err = srv.store.ChangeLearningQuestion(ctx, q.ID, q.Revision, store.LearningQuestionChange{Action: "edit", Body: "另一种使用目的", Goal: "不同问题"})
	if err != nil {
		t.Fatal(err)
	}
	found, err := srv.store.KnowledgeArticleForQuestion(ctx, a.ProfileID, q.ID)
	if err != nil || found != nil {
		t.Fatal("changed purpose swallowed", found, err)
	}
}
