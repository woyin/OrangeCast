package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
)

func TestLearningQuestionsHTTPPrivacyCASAndScopedGeneration(t *testing.T) {
	srv := newTestServer(t)
	session := claimOwnerAndLogin(t, srv, "questions@example.com", "password123")
	ep, note := seedKnowledgeLearning(t, srv)
	for _, path := range []string{"/questions", "/questions/missing"} {
		if rec := doWithCookie(srv, nil, "GET", path); rec.Code != 303 {
			t.Fatal(path, rec.Code)
		}
	}
	request := httptest.NewRequest("POST", "/questions/action", strings.NewReader("action=create&body=test"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(session)
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, request)
	if rec.Code != 403 {
		t.Fatal(rec.Code)
	}
	create := postForm(t, srv, session, "/questions/action", url.Values{"action": {"create"}, "body": {"如何理解来源？"}, "goal": {"整理依据"}, "target_date": {"2026-10-20"}}.Encode())
	if create.Code != 303 {
		t.Fatal(create.Code, create.Body.String())
	}
	path := create.Header().Get("Location")
	id := strings.TrimPrefix(path, "/questions/")
	page := doWithCookie(srv, session, "GET", path)
	if page.Code != 200 || !strings.Contains(page.Body.String(), "当前范围少于两项合格材料") || page.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(page.Code, page.Body.String())
	}
	for _, path := range []string{"/questions?q=来源", "/questions?status=active", "/questions?status=resolved"} {
		rec := doWithCookie(srv, session, "GET", path)
		if rec.Code != 200 {
			t.Fatal(rec.Code, rec.Body.String())
		}
	}
	post := func(action string, revision int, extra url.Values) *httptest.ResponseRecorder {
		extra.Set("action", action)
		extra.Set("question_id", id)
		extra.Set("expected_revision", strconv.Itoa(revision))
		return postForm(t, srv, session, "/questions/action", extra.Encode())
	}
	linked := post("link", 1, url.Values{"kind": {"source"}, "object_id": {"episode:" + ep}})
	if linked.Code != 303 {
		t.Fatal(linked.Code, linked.Body.String())
	}
	if stale := post("edit", 1, url.Values{"body": {"stale edit"}}); stale.Code != 409 {
		t.Fatal(stale.Code)
	}
	q, _ := srv.store.GetLearningQuestion(t.Context(), id)
	if invalid := post("status", q.Revision, url.Values{"status": {"mastered"}}); invalid.Code != 400 {
		t.Fatal(invalid.Code)
	}
	if rec := post("note", q.Revision, url.Values{"source": {"episode:" + ep}, "content": {"我的理解"}, "anchor_json": {`{"no_position":true}`}}); rec.Code != 303 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	q, _ = srv.store.GetLearningQuestion(t.Context(), id)
	notes, _ := srv.store.ListOwnerNotes(t.Context(), models.SourceEpisode, ep)
	foundNew := false
	for _, n := range notes {
		if n.Content == "我的理解" && n.Kind == "owner_reflection" {
			foundNew = true
		}
	}
	if len(notes) != 3 || !foundNew {
		t.Fatal(notes)
	}
	if rec := post("generate", q.Revision, url.Values{}); rec.Code != 400 {
		t.Fatal("missing config", rec.Code)
	}
	srv.cfg.PodBaseURL, srv.cfg.PodAPIKey, srv.cfg.PodModel = "https://example.test/v1", "test", "model"
	generated := post("generate", q.Revision, url.Values{})
	if generated.Code != 303 {
		t.Fatal(generated.Code, generated.Body.String())
	}
	articlePath := generated.Header().Get("Location")
	article, err := srv.store.GetKnowledgeArticle(t.Context(), strings.TrimPrefix(articlePath, "/knowledge-articles/"))
	if err != nil {
		t.Fatal(err)
	}
	var frozen provider.KnowledgeArticleRequest
	if err = json.Unmarshal([]byte(article.InputJSON), &frozen); err != nil || frozen.Question.ID != id || frozen.Question.Revision != q.Revision {
		t.Fatal(err, frozen.Question)
	}
	rename := post("edit", q.Revision, url.Values{"body": {"新问题"}, "goal": {"新目标"}})
	if rename.Code != 303 {
		t.Fatal(rename.Code, rename.Body.String())
	}
	filteredPage := doWithCookie(srv, session, "GET", "/knowledge-articles?list_question="+id)
	if filteredPage.Code != 200 || !strings.Contains(filteredPage.Body.String(), articlePath) {
		t.Fatal(filteredPage.Code, filteredPage.Body.String())
	}
	queryReq := httptest.NewRequest("GET", "/knowledge-articles?list_question="+id, nil)
	queryReq.ParseForm()
	if knowledgeListQuery(queryReq, "article").QuestionID != id {
		t.Fatal("question list filter not routed")
	}
	articlePage := doWithCookie(srv, session, "GET", articlePath)
	if articlePage.Code != 200 || !strings.Contains(articlePage.Body.String(), "问题已有新版本") || !strings.Contains(articlePage.Body.String(), "如何理解来源？") {
		t.Fatal(articlePage.Code, articlePage.Body.String())
	}
	q, _ = srv.store.GetLearningQuestion(t.Context(), id)
	if rec := post("status", q.Revision, url.Values{"status": {"resolved"}}); rec.Code != 303 {
		t.Fatal(rec.Code)
	}
	page = doWithCookie(srv, session, "GET", path)
	if page.Code != 200 || !strings.Contains(page.Body.String(), "已解决") {
		t.Fatal(page.Code, page.Body.String())
	}
	q, _ = srv.store.GetLearningQuestion(t.Context(), id)
	if rec := post("delete", q.Revision, url.Values{}); rec.Code != 303 {
		t.Fatal(rec.Code)
	}
	if _, err = srv.store.GetOwnerNote(t.Context(), note); err != nil {
		t.Fatal("question delete removed source material", err)
	}
	articlePage = doWithCookie(srv, session, "GET", articlePath)
	if articlePage.Code != 200 || !strings.Contains(articlePage.Body.String(), "原问题已删除") {
		t.Fatal(articlePage.Code, articlePage.Body.String())
	}
	if rec := doWithCookie(srv, session, "GET", path); rec.Code != 404 {
		t.Fatal(rec.Code)
	}
	for _, body := range []string{"action=create", "action=edit&question_id=" + id, "action=unknown&question_id=x&expected_revision=1", "action=create&body=test&target_date=invalid"} {
		if rec := postForm(t, srv, session, "/questions/action", body); rec.Code != 400 && rec.Code != 404 {
			t.Fatal(rec.Code)
		}
	}
}
func TestLearningQuestionRelationsDetailAndAutomaticPause(t *testing.T) {
	srv := newTestServer(t)
	session := claimOwnerAndLogin(t, srv, "question-relations@example.com", "password123")
	ep, note := seedKnowledgeLearning(t, srv)
	ctx := t.Context()
	q, err := srv.store.CreateLearningQuestion(ctx, store.LearningQuestion{Body: "目标问题"})
	if err != nil {
		t.Fatal(err)
	}
	snap, err := srv.store.FreezeSourceSnapshot(ctx, models.SourceEpisode, ep)
	if err != nil {
		t.Fatal(err)
	}
	for _, link := range []provider.LearningQuestionLink{{Kind: "note", ObjectID: note}, {Kind: "evidence", ObjectID: snap.ID}} {
		q, err = srv.store.ChangeLearningQuestion(ctx, q.ID, q.Revision, store.LearningQuestionChange{Action: "link", Link: link})
		if err != nil {
			t.Fatal(err)
		}
	}
	v := srv.questionRelationView(httptest.NewRequest("GET", "/", nil), store.LearningQuestionRelation{LearningQuestionLink: provider.LearningQuestionLink{Kind: "note", ObjectID: "missing"}})
	if !v.Missing {
		t.Fatal(v)
	}
	profile, _ := srv.store.EnsureDefaultEditorialProfile(ctx)
	req, _, _ := srv.store.BuildKnowledgeArticleRequest(ctx, profile.ID, "pod")
	a, _, err := srv.store.ReserveKnowledgeArticle(ctx, profile.ID, "pod", "model", req, false)
	if err != nil {
		t.Fatal(err)
	}
	q, err = srv.store.ChangeLearningQuestion(ctx, q.ID, q.Revision, store.LearningQuestionChange{Action: "suggest", Link: provider.LearningQuestionLink{Kind: "article", ObjectID: a.ID}})
	if err != nil {
		t.Fatal(err)
	}
	rec := doWithCookie(srv, session, "GET", "/questions/"+q.ID+"?material_q=来源")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "待确认建议") {
		t.Fatal(rec.Code, rec.Body.String())
	}
	settings, _ := srv.store.GetKnowledgeArticleSettings(ctx)
	settings.Enabled = true
	settings.QuestionID = q.ID
	settings.DebounceMinutes = 0
	if err = srv.store.SetKnowledgeArticleSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	q, err = srv.store.ChangeLearningQuestion(ctx, q.ID, q.Revision, store.LearningQuestionChange{Action: "status", Status: "paused"})
	if err != nil {
		t.Fatal(err)
	}
	srv.cfg.PodBaseURL, srv.cfg.PodAPIKey, srv.cfg.PodModel = "https://example.test/v1", "test", "model"
	var before, after int
	srv.store.DB.QueryRow(`SELECT count(*) FROM processing_jobs`).Scan(&before)
	if err = srv.RunKnowledgeArticles(ctx); err != nil {
		t.Fatal(err)
	}
	srv.store.DB.QueryRow(`SELECT count(*) FROM processing_jobs`).Scan(&after)
	if before != after {
		t.Fatal("paused question admitted automation")
	}
	page := doWithCookie(srv, session, "GET", "/knowledge-articles")
	if page.Code != 200 || !strings.Contains(page.Body.String(), "自动发现范围") {
		t.Fatal(page.Code, page.Body.String())
	}
	if rec = postForm(t, srv, session, "/questions/action", "action=note&question_id="+q.ID+"&expected_revision="+strconv.Itoa(q.Revision)+"&source=invalid&content=x"); rec.Code != 400 {
		t.Fatal(rec.Code)
	}
	if rec = doWithCookie(srv, session, http.MethodPut, "/questions/"+q.ID); rec.Code != 403 {
		t.Fatal(rec.Code)
	}
	if rec = doWithCookie(srv, session, http.MethodPut, "/questions"); rec.Code != 403 {
		t.Fatal(rec.Code)
	}
	if rec = postForm(t, srv, session, "/questions/action", "action=note&question_id="+q.ID+"&expected_revision="+strconv.Itoa(q.Revision)+"&source=episode:missing&content=x"); rec.Code != 404 {
		t.Fatal(rec.Code)
	}
}

func TestLearningQuestionHandlersMethodAndJSONResult(t *testing.T) {
	srv := newTestServer(t)
	for _, handler := range []http.HandlerFunc{srv.handleLearningQuestions, srv.handleLearningQuestionDetail, srv.handleLearningQuestionAction} {
		rec := httptest.NewRecorder()
		method := "PUT"
		handler(rec, httptest.NewRequest(method, "/questions", nil))
		if rec.Code != 405 {
			t.Fatal(rec.Code)
		}
	}
	r := httptest.NewRequest("POST", "/questions/action", nil)
	r.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()
	questionResult(rec, r, "/questions/id")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"href":"/questions/id"`) {
		t.Fatal(rec.Body.String())
	}
}
