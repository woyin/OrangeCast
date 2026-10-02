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

func writingCoveragePost(t *testing.T, srv *Server, session *http.Cookie, path string, values url.Values, wantsJSON bool) *httptest.ResponseRecorder {
	t.Helper()
	if !wantsJSON {
		return postForm(t, srv, session, path, values.Encode())
	}
	csrf := journeyCSRF(t, srv, session)
	req := httptest.NewRequest("POST", path, strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-CSRF-Token", csrf)
	req.AddCookie(&http.Cookie{Name: "cwp_csrf", Value: csrf})
	req.AddCookie(session)
	out := httptest.NewRecorder()
	srv.Router().ServeHTTP(out, req)
	return out
}

func writingCoverageFixture(t *testing.T, preview bool) (*Server, *http.Cookie, *store.KnowledgeArticleRecord) {
	t.Helper()
	srv := newTestServer(t)
	session := claimOwnerAndLogin(t, srv, "coverage-writing@example.com", "password123")
	seedKnowledgeLearning(t, srv)
	srv.cfg.PodBaseURL, srv.cfg.PodAPIKey, srv.cfg.PodModel = "https://fixture.invalid/v1", "fixture", "fixture"
	srv.worker.WithBundleResolver(func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{KnowledgeArticle: &knowledgeStageFake{}}, nil
	})
	profile, e := srv.store.EnsureDefaultEditorialProfile(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	a, _, e := srv.enqueueKnowledgeArticleScope(t.Context(), profile.ID, false, store.KnowledgeScope{PreviewWritingPlan: &preview})
	if e != nil {
		t.Fatal(e)
	}
	steps := 4
	if preview {
		steps = 2
	}
	for i := 0; i < steps; i++ {
		if e = srv.worker.ProcessOne(t.Context()); e != nil {
			t.Fatal(e)
		}
	}
	a, e = srv.store.GetKnowledgeArticle(t.Context(), a.ID)
	if e != nil {
		t.Fatal(e)
	}
	return srv, session, a
}

func TestKnowledgeWritingCoverageInvalidGenerationPreservesScope(t *testing.T) {
	srv := newTestServer(t)
	session := claimOwnerAndLogin(t, srv, "invalid-writing@example.com", "password123")
	ep, note := seedKnowledgeLearning(t, srv)
	srv.cfg.PodBaseURL, srv.cfg.PodAPIKey, srv.cfg.PodModel = "https://fixture.invalid/v1", "fixture", "fixture"
	values := url.Values{"writing_mode": {"unknown-purpose"}, "theme": {"draft-theme"}, "source": {"episode:" + ep}, "from": {"2026-01-01"}, "until": {"2026-12-31"}, "material": {note}, "writing_options_present": {"1"}, "preview_writing_plan": {"on"}, "explore_history": {"on"}, "history_cursor": {"0"}}
	before, _ := srv.store.ListKnowledgeArticles(t.Context())
	for _, jsonMode := range []bool{false, true} {
		out := writingCoveragePost(t, srv, session, "/knowledge-articles/generate", values, jsonMode)
		if out.Code != 400 {
			t.Fatal(out.Code, out.Body.String())
		}
		if jsonMode {
			if strings.Contains(out.Body.String(), "<html") || !strings.Contains(out.Body.String(), "未知文章用途") {
				t.Fatal(out.Body.String())
			}
		} else {
			for _, text := range []string{"unknown-purpose", "draft-theme", "2026-01-01", "2026-12-31", note, "episode:" + ep} {
				if !strings.Contains(out.Body.String(), text) {
					t.Fatal("scope input lost", text, out.Body.String())
				}
			}
		}
	}
	after, _ := srv.store.ListKnowledgeArticles(t.Context())
	if len(after) != len(before) {
		t.Fatal("invalid generation admitted article")
	}
}

func TestKnowledgeWritingCoveragePlanFailuresDoNotAdmitOrMutate(t *testing.T) {
	srv, session, a := writingCoverageFixture(t, true)
	plan, e := srv.store.GetKnowledgeWritingPlan(t.Context(), a.ID)
	if e != nil {
		t.Fatal(e)
	}
	beforeJobs, _ := srv.store.ListQueuedOrRunning(t.Context())
	cases := []struct {
		action, revision, hash, mode, outline string
		code                                  int
	}{
		{"plan_save", "1", "wrong", "", "original draft", 409}, {"plan_save", "99", plan.Hash, "", "original draft", 409}, {"plan_save", "1", plan.Hash, "unknown-mode", "kept draft", 400}, {"plan_confirm", "0", plan.Hash, "", "", 400}, {"plan_confirm", "1", plan.Hash, "practice", plan.Request.Topic.Outline, 400}, {"plan_confirm", "1", plan.Hash, "", "changed unsaved outline", 400},
	}
	for _, tc := range cases {
		for _, jsonMode := range []bool{false, true} {
			values := url.Values{"article_id": {a.ID}, "expected_revision": {tc.revision}, "action": {tc.action}, "plan_hash": {tc.hash}, "writing_mode": {tc.mode}, "outline": {tc.outline}}
			out := writingCoveragePost(t, srv, session, "/knowledge-articles/action", values, jsonMode)
			if out.Code != tc.code {
				t.Fatal(tc, out.Code, out.Body.String())
			}
			if !jsonMode && tc.outline != "" && tc.revision != "0" && !strings.Contains(out.Body.String(), tc.outline) {
				t.Fatal("SSR lost outline", out.Body.String())
			}
			fresh, e := srv.store.GetKnowledgeWritingPlan(t.Context(), a.ID)
			if e != nil || fresh.Hash != plan.Hash || fresh.State != "pending" {
				t.Fatal("failed action altered plan", fresh, e)
			}
		}
	}
	afterJobs, _ := srv.store.ListQueuedOrRunning(t.Context())
	if len(afterJobs) != len(beforeJobs) {
		t.Fatal("failed action admitted work")
	}
	if _, e = srv.store.DB.ExecContext(t.Context(), `UPDATE knowledge_writing_plans SET request_json='{' WHERE article_id=?`, a.ID); e != nil {
		t.Fatal(e)
	}
	out := doWithCookie(srv, session, "GET", "/knowledge-articles/"+a.ID)
	if out.Code != 500 || !strings.Contains(out.Body.String(), "读取写作计划失败") {
		t.Fatal(out.Code, out.Body.String())
	}
	values := url.Values{"article_id": {a.ID}, "expected_revision": {"1"}, "action": {"plan_confirm"}, "plan_hash": {plan.Hash}}
	out = writingCoveragePost(t, srv, session, "/knowledge-articles/action", values, true)
	if out.Code != 409 {
		t.Fatal(out.Code, out.Body.String())
	}
}

func TestKnowledgeWritingCoverageExplicitRevisionGuardsAndMissingPlan(t *testing.T) {
	srv, session, a := writingCoverageFixture(t, false)
	initial := a.WorkingRevision
	for _, mode := range []string{"unknown-mode", "practice"} {
		if mode == "practice" {
			srv.cfg.PodAPIKey = ""
		}
		out := writingCoveragePost(t, srv, session, "/knowledge-articles/action", url.Values{"article_id": {a.ID}, "expected_revision": {strconv.Itoa(initial)}, "action": {"revise_purpose"}, "writing_mode": {mode}, "instructions": {"retained requested change"}}, true)
		if out.Code != 400 {
			t.Fatal(out.Code, out.Body.String())
		}
		fresh, e := srv.store.GetKnowledgeArticle(t.Context(), a.ID)
		if e != nil || fresh.WorkingRevision != initial || fresh.PassedRevision != a.PassedRevision {
			t.Fatal("guard changed immutable revisions", fresh, e)
		}
	}
	out := writingCoveragePost(t, srv, session, "/knowledge-articles/action", url.Values{"article_id": {a.ID}, "expected_revision": {"1"}, "action": {"plan_confirm"}}, false)
	if out.Code != 400 {
		t.Fatal(out.Code, out.Body.String())
	}
	out = doWithCookie(srv, session, "GET", "/knowledge-articles/"+a.ID)
	if out.Code != 200 {
		t.Fatal("missing optional plan broke article", out.Code, out.Body.String())
	}
	out = writingCoveragePost(t, srv, session, "/knowledge-articles/action", url.Values{"article_id": {"missing"}, "expected_revision": {"1"}, "action": {"plan_save"}}, true)
	if out.Code != 404 {
		t.Fatal(out.Code)
	}
}

func TestKnowledgeWritingCoverageManualSaveCannotInjectPurposeSections(t *testing.T) {
	srv, session, a := writingCoverageFixture(t, false)
	rev, e := srv.store.GetKnowledgeRevision(t.Context(), a.ID, a.WorkingRevision)
	if e != nil {
		t.Fatal(e)
	}
	var blocks []provider.KnowledgeBlock
	if e = json.Unmarshal([]byte(rev.BlocksJSON), &blocks); e != nil {
		t.Fatal(e)
	}
	var frozen provider.KnowledgeArticleRequest
	if e = json.Unmarshal([]byte(rev.InputJSON), &frozen); e != nil {
		t.Fatal(e)
	}
	for i := range blocks {
		blocks[i].PurposeSection = "retained-section-" + strconv.Itoa(i)
	}
	if _, e = srv.store.SaveKnowledgeDraft(t.Context(), a.ID, 1, "section identity parent", blocks, frozen); e != nil {
		t.Fatal(e)
	}
	// A saved local parent owns section identity; request fields cannot replace it.
	values := url.Values{"article_id": {a.ID}, "expected_revision": {"2"}, "action": {"save"}, "title": {"manual title"}}
	for i, b := range blocks {
		n := strconv.Itoa(i)
		values.Add("block_text", b.Text)
		values.Set("block_id_"+n, b.ID)
		values.Set("block_kind_"+n, b.Kind)
		values.Set("block_purpose_section_"+n, "injected-section")
		for _, id := range b.MaterialIDs {
			values.Add("block_material_"+n, id)
		}
	}
	out := writingCoveragePost(t, srv, session, "/knowledge-articles/action", values, true)
	if out.Code != 200 {
		t.Fatal(out.Code, out.Body.String())
	}
	changed, e := srv.store.GetKnowledgeRevision(t.Context(), a.ID, 3)
	if e != nil {
		t.Fatal(e)
	}
	var saved []provider.KnowledgeBlock
	if e = json.Unmarshal([]byte(changed.BlocksJSON), &saved); e != nil {
		t.Fatal(e)
	}
	for i, b := range saved {
		if b.PurposeSection != blocks[i].PurposeSection {
			t.Fatal("caller injected purpose section", saved)
		}
	}
	old, e := srv.store.GetKnowledgeRevision(t.Context(), a.ID, 1)
	if e != nil || old.BlocksJSON != rev.BlocksJSON {
		t.Fatal("save mutated approved parent", old, e)
	}
}

func TestKnowledgeWritingCoverageExistingQuestionPurposeRequiresNewRevision(t *testing.T) {
	srv := newTestServer(t)
	session := claimOwnerAndLogin(t, srv, "question-purpose@example.com", "password123")
	ep, _ := seedKnowledgeLearning(t, srv)
	srv.cfg.PodBaseURL, srv.cfg.PodAPIKey, srv.cfg.PodModel = "https://fixture.invalid/v1", "fixture", "fixture"
	srv.worker.WithBundleResolver(func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{KnowledgeArticle: &knowledgeStageFake{}}, nil
	})
	q, e := srv.store.CreateLearningQuestion(t.Context(), store.LearningQuestion{Body: "如何区分来源与个人理解？", Goal: "说明身份边界", Status: "active"})
	if e != nil {
		t.Fatal(e)
	}
	q, e = srv.store.ChangeLearningQuestion(t.Context(), q.ID, q.Revision, store.LearningQuestionChange{Action: "link", Link: provider.LearningQuestionLink{Kind: "source", ObjectID: "episode:" + ep}})
	if e != nil {
		t.Fatal(e)
	}
	notes, e := srv.store.ListOwnerNotes(t.Context(), models.SourceEpisode, ep)
	if e != nil {
		t.Fatal(e)
	}
	for _, note := range notes {
		q, e = srv.store.ChangeLearningQuestion(t.Context(), q.ID, q.Revision, store.LearningQuestionChange{Action: "link", Link: provider.LearningQuestionLink{Kind: "note", ObjectID: note.ID}})
		if e != nil {
			t.Fatal(e)
		}
	}
	profile, e := srv.store.EnsureDefaultEditorialProfile(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	a, _, e := srv.enqueueKnowledgeArticleScope(t.Context(), profile.ID, false, store.KnowledgeScope{QuestionID: q.ID})
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 4; i++ {
		if e = srv.worker.ProcessOne(t.Context()); e != nil {
			t.Fatal(e)
		}
	}
	for _, jsonMode := range []bool{true, false} {
		out := writingCoveragePost(t, srv, session, "/knowledge-articles/generate", url.Values{"question_id": {q.ID}, "writing_mode": {"practice"}}, jsonMode)
		if out.Code != 400 || !strings.Contains(out.Body.String(), "用途新修订") {
			t.Fatal(out.Code, out.Body.String())
		}
	}
	current, e := srv.store.GetKnowledgeArticle(t.Context(), a.ID)
	if e != nil || current.WorkingRevision != 1 || current.PassedRevision != 1 {
		t.Fatal("scope shortcut changed article", current, e)
	}
	if _, _, e = srv.enqueueKnowledgeArticleScope(t.Context(), profile.ID, false, store.KnowledgeScope{QuestionID: q.ID, ExpectedQuestionRevision: q.Revision + 1}); e != store.ErrConflict {
		t.Fatal("stale question accepted", e)
	}
	if _, e = srv.store.DB.ExecContext(t.Context(), `UPDATE knowledge_articles SET input_json='{' WHERE id=?`, a.ID); e != nil {
		t.Fatal(e)
	}
	if _, _, e = srv.enqueueKnowledgeArticleScope(t.Context(), profile.ID, false, store.KnowledgeScope{QuestionID: q.ID, WritingMode: "synthesis"}); e == nil || !strings.Contains(e.Error(), "冻结输入损坏") {
		t.Fatal(e)
	}
}

func TestKnowledgeWritingCoverageMethodAndReadFailures(t *testing.T) {
	srv := newTestServer(t)
	handlers := []http.HandlerFunc{srv.handleKnowledgeArticles, srv.handleKnowledgeArticleGenerate, srv.handleKnowledgeArticleSettings, srv.handleKnowledgeArticleRetry, srv.handleKnowledgeArticleAction, srv.handleKnowledgeCandidate, srv.handleKnowledgeArticleDetail}
	for i, handler := range handlers {
		method := "GET"
		if i == 0 || i == 6 {
			method = "POST"
		}
		out := httptest.NewRecorder()
		handler(out, httptest.NewRequest(method, "/knowledge-articles", nil))
		if out.Code != 405 {
			t.Fatal(i, out.Code)
		}
	}
	if e := srv.store.DB.Close(); e != nil {
		t.Fatal(e)
	}
	out := httptest.NewRecorder()
	srv.handleKnowledgeArticles(out, httptest.NewRequest("GET", "/knowledge-articles", nil))
	if out.Code != 500 || !strings.Contains(out.Body.String(), "读取文章失败") {
		t.Fatal(out.Code, out.Body.String())
	}
	out = httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/knowledge-articles/settings", strings.NewReader("daily_limit=2&debounce_minutes=0&audience=Owner&style=legacy"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	srv.handleKnowledgeArticleSettings(out, req)
	if out.Code != 500 || !strings.Contains(out.Body.String(), "读取文章偏好失败") {
		t.Fatal(out.Code, out.Body.String())
	}
}
