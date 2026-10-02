package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
)

func TestQuestionStudyCoverageV4HTTPQueuedCheckedHistoryAndRetry(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "study-coverage@example.com", "password123")
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Model    string                          `json:"model"`
			Messages []provider.QuestionStudyMessage `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			t.Error(err)
			return
		}
		var output any
		if in.Model == "generate" {
			var scope provider.QuestionStudyScope
			if err := json.Unmarshal([]byte(in.Messages[1].Content), &scope); err != nil {
				t.Error(err)
				return
			}
			refs := []provider.QuestionStudyReference{}
			owners := []provider.QuestionStudyOwnerReference{}
			for _, m := range scope.Materials {
				if m.Kind == "owner_reflection" {
					owners = append(owners, provider.QuestionStudyOwnerReference{MaterialKey: m.Key, Revision: m.Revision})
					continue
				}
				refs = append(refs, provider.QuestionStudyReference{MaterialKey: m.Key, Revision: m.Revision, SegmentIDs: []string{m.Segments[0].SegmentID}})
			}
			output = provider.QuestionStudyAnswer{Version: provider.QuestionStudyPromptVersion, State: "answered", OwnerUnderstanding: owners, SourceClaims: []provider.QuestionStudyClaim{{Text: "原始来源条件", References: refs[:1]}}, AIExplanations: []provider.QuestionStudyClaim{{Text: "AI条件解释", References: refs[:1]}}, Consensus: []provider.QuestionStudyClaim{{Text: "共同条件", Conditions: "限定于给定材料", References: refs}}, Disagreements: []provider.QuestionStudyClaim{{Text: "不同条件", Conditions: "不同来源条件", References: refs}}}
		} else {
			output = provider.QuestionStudyReview{Version: "question-study-review-v1", Verdict: "accept", Reason: "明确条件", Checks: []provider.QuestionStudyClaimCheck{{Key: "source:0", Relevant: true, Supported: true, ConditionsPreserved: true}, {Key: "ai:0", Relevant: true, Supported: true, ConditionsPreserved: true}, {Key: "consensus:0", Relevant: true, Supported: true, ConditionsPreserved: true}, {Key: "disagreement:0", Relevant: true, Supported: true, ConditionsPreserved: true}}}
		}
		raw, _ := json.Marshal(output)
		_ = json.NewEncoder(w).Encode(map[string]any{"model": in.Model, "choices": []map[string]any{{"message": map[string]string{"content": string(raw)}}}, "usage": map[string]int{"prompt_tokens": 11, "completion_tokens": 7}})
	}))
	defer remote.Close()
	srv.cfg.PodModel = "generate"
	srv.cfg.PodQuestionStudyReviewModel = "review"
	srv.selector.WithPod("fixture", remote.URL, "generate")
	q, err := srv.store.CreateLearningQuestion(t.Context(), store.LearningQuestion{Body: "比较条件"})
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{"条件甲", "条件乙"} {
		d, err := srv.store.CreatePastedDocument(t.Context(), body, body)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = srv.store.CreateOwnerNote(t.Context(), models.OwnerNote{SourceType: "document", SourceID: d.ID, Kind: "owner_reflection", Content: "我理解应先确认条件"}); err != nil {
			t.Fatal(err)
		}
		q, err = srv.store.ChangeLearningQuestion(t.Context(), q.ID, q.Revision, store.LearningQuestionChange{Action: "link", Link: provider.LearningQuestionLink{Kind: "source", SourceType: "document", SourceID: d.ID}})
		if err != nil {
			t.Fatal(err)
		}
	}
	session, err := srv.store.StartQuestionStudySession(t.Context(), q.ID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	path := "/questions/" + q.ID + "/study"
	form := url.Values{"action": {"ask"}, "session_id": {session.ID}, "expected_revision": {"1"}, "request_key": {uuid.NewString()}, "scope_confirmed": {"yes"}, "input": {"比较条件"}}
	rec := postForm(t, srv, cookie, path, form.Encode())
	if rec.Code != 303 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	location := rec.Header().Get("Location")
	for i := 0; i < 2; i++ {
		if err = srv.worker.ProcessOne(t.Context()); err != nil {
			t.Fatal(err)
		}
		page := doWithCookie(srv, cookie, "GET", location)
		if page.Code != 200 {
			t.Fatal(page.Code, page.Body.String())
		}
	}
	page := doWithCookie(srv, cookie, "GET", location)
	for _, want := range []string{"原始来源条件", "AI条件解释", "共同条件", "不同条件", "?position=", "检查任务及费用", "你的理解", "我理解应先确认条件"} {
		if !strings.Contains(page.Body.String(), want) {
			t.Fatal("missing checked history", want, page.Body.String())
		}
	}
	req := httptest.NewRequest("GET", location, nil)
	req.AddCookie(cookie)
	req.Header.Set("Accept", "application/json")
	status := httptest.NewRecorder()
	srv.Router().ServeHTTP(status, req)
	if status.Code != 200 || !strings.Contains(status.Body.String(), "accepted") {
		t.Fatal(status.Code, status.Body.String())
	}
	turns, err := srv.store.QuestionStudyTurnStatuses(t.Context(), session.ID)
	if err != nil || len(turns) != 1 {
		t.Fatal(turns, err)
	}
	// A checked job is not retryable, and an unrelated/malformed command cannot widen scope.
	form.Set("action", "retry")
	form.Set("job_id", turns[0].CheckJobID)
	form.Set("request_key", uuid.NewString())
	form.Set("expected_revision", "1")
	if rec = postForm(t, srv, cookie, path, form.Encode()); rec.Code != 409 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	for _, bad := range []struct {
		action, job, revision string
		code                  int
	}{{"retry", "missing", "1", 404}, {"ask", "", "bad", 400}, {"unknown", "", "1", 400}} {
		form.Set("action", bad.action)
		form.Set("job_id", bad.job)
		form.Set("expected_revision", bad.revision)
		if rec = postForm(t, srv, cookie, path, form.Encode()); rec.Code != bad.code {
			t.Fatal(bad, rec.Code)
		}
	}
	fresh, err := srv.store.StartQuestionStudySession(t.Context(), q.ID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	form = url.Values{"action": {"ask"}, "session_id": {fresh.ID}, "expected_revision": {"1"}, "request_key": {uuid.NewString()}, "scope_confirmed": {"yes"}, "input": {"已知付费响应恢复"}}
	if _, err = srv.store.DB.Exec(`CREATE TRIGGER question_http_receipt_fault BEFORE INSERT ON usage_records WHEN new.operation='question_study_generate' BEGIN SELECT RAISE(FAIL,'receipt fault');END`); err != nil {
		t.Fatal(err)
	}
	if rec = postForm(t, srv, cookie, path, form.Encode()); rec.Code != 303 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	if err = srv.worker.ProcessOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	failed, err := srv.store.QuestionStudyTurnStatuses(t.Context(), fresh.ID)
	if err != nil || len(failed) != 1 {
		t.Fatal(failed, err)
	}
	var revision int
	if err = srv.store.DB.QueryRow(`SELECT control_revision FROM processing_jobs WHERE id=?`, failed[0].GenerationJobID).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	if _, err = srv.store.DB.Exec(`DROP TRIGGER question_http_receipt_fault`); err != nil {
		t.Fatal(err)
	}
	form.Set("action", "retry")
	form.Set("job_id", failed[0].GenerationJobID)
	form.Set("request_key", uuid.NewString())
	form.Set("expected_revision", strconv.Itoa(revision))
	rec = postForm(t, srv, cookie, path, form.Encode())
	if rec.Code != 303 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	replay := postForm(t, srv, cookie, path, form.Encode())
	if replay.Code != 303 || replay.Header().Get("Location") != rec.Header().Get("Location") {
		t.Fatal("recovery command replay changed", replay.Code)
	}
	for i := 0; i < 2; i++ {
		if err = srv.worker.ProcessOne(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	page = doWithCookie(srv, cookie, "GET", rec.Header().Get("Location"))
	if page.Code != 200 || !strings.Contains(page.Body.String(), "原始来源条件") {
		t.Fatal(page.Code, page.Body.String())
	}

}

func TestQuestionStudyCoverageV4HTTPFailuresKeepSubmittedDraft(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "study-command-errors@example.com", "password123")
	q, err := srv.store.CreateLearningQuestion(t.Context(), store.LearningQuestion{Body: "错误命令"})
	if err != nil {
		t.Fatal(err)
	}
	session, err := srv.store.StartQuestionStudySession(t.Context(), q.ID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	path := "/questions/" + q.ID + "/study"
	form := url.Values{"action": {"ask"}, "session_id": {session.ID}, "expected_revision": {"1"}, "request_key": {uuid.NewString()}, "input": {"明确保留我的草稿"}, "material_key": {"selected-key"}, "allow_unknown": {"yes"}}
	rec := postForm(t, srv, cookie, path, form.Encode())
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "明确保留我的草稿") || !strings.Contains(rec.Body.String(), "selected-key") {
		t.Fatal(rec.Code, rec.Body.String())
	}
	// JSON command errors still pass the actual auth/CSRF middleware.
	csrfPage := doWithCookie(srv, cookie, "GET", path)
	var csrf string
	for _, c := range csrfPage.Result().Cookies() {
		if c.Name == "cwp_csrf" {
			csrf = c.Value
		}
	}
	req := httptest.NewRequest("POST", path, strings.NewReader("_csrf="+csrf+"&"+form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.AddCookie(cookie)
	req.AddCookie(&http.Cookie{Name: "cwp_csrf", Value: csrf})
	jsonError := httptest.NewRecorder()
	srv.Router().ServeHTTP(jsonError, req)
	if jsonError.Code != 400 {
		t.Fatal("JSON scope confirmation not enforced", jsonError.Code, jsonError.Body.String())
	}
	if denied := doWithCookie(srv, cookie, "PUT", path); denied.Code != 403 {
		t.Fatal("mutation without CSRF was admitted", denied.Code)
	}
	rec = httptest.NewRecorder()
	srv.handleQuestionStudy(rec, httptest.NewRequest("PUT", path, nil))
	if rec.Code != 405 {
		t.Fatal(rec.Code)
	}
	if rec = doWithCookie(srv, cookie, "GET", "/questions/missing/study"); rec.Code != 404 {
		t.Fatal(rec.Code)
	}
	srv.store.DB.Close()
	if rec = doWithCookie(srv, cookie, "GET", path); rec.Code < 400 {
		t.Fatal("closed database rendered successful page", rec.Code)
	}
}
