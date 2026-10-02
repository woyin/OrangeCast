package server

import (
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/store"
)

func TestQuestionStudyHTTPReadOnlyAdmissionAndPrivacy(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "question-study@example.com", "password123")
	q, err := srv.store.CreateLearningQuestion(t.Context(), store.LearningQuestion{Body: "对比适用条件"})
	if err != nil {
		t.Fatal(err)
	}
	path := "/questions/" + q.ID + "/study"
	if rec := doWithCookie(srv, nil, "GET", path); rec.Code != 303 {
		t.Fatal(rec.Code)
	}
	rec := doWithCookie(srv, cookie, "GET", path)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "开始新会话") {
		t.Fatal(rec.Code, rec.Body.String())
	}
	sessions, err := srv.store.ListQuestionStudySessions(t.Context(), q.ID)
	if err != nil || len(sessions) != 0 {
		t.Fatal("GET created session", sessions, err)
	}
	request := httptest.NewRequest("POST", path, strings.NewReader("action=start&request_key="+uuid.NewString()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(cookie)
	denied := httptest.NewRecorder()
	srv.Router().ServeHTTP(denied, request)
	if denied.Code != 403 {
		t.Fatal(denied.Code)
	}
	key := uuid.NewString()
	values := url.Values{"action": {"start"}, "request_key": {key}}
	started := postForm(t, srv, cookie, path, values.Encode())
	if started.Code != 303 {
		t.Fatal(started.Code, started.Body.String())
	}
	replay := postForm(t, srv, cookie, path, values.Encode())
	if replay.Header().Get("Location") != started.Header().Get("Location") {
		t.Fatal("duplicate session")
	}
	page := doWithCookie(srv, cookie, "GET", started.Header().Get("Location"))
	if page.Code != 200 || !strings.Contains(page.Body.String(), "scope_confirmed") {
		t.Fatal(page.Code, page.Body.String())
	}
	sessions, err = srv.store.ListQuestionStudySessions(t.Context(), q.ID)
	if err != nil || len(sessions) != 1 {
		t.Fatal(sessions, err)
	}
	ask := url.Values{"action": {"ask"}, "session_id": {sessions[0].ID}, "expected_revision": {"1"}, "request_key": {uuid.NewString()}, "input": {"解释条件"}}
	if rec := postForm(t, srv, cookie, path, ask.Encode()); rec.Code != 400 {
		t.Fatal(rec.Code)
	}
	ask.Set("scope_confirmed", "yes")
	if rec := postForm(t, srv, cookie, path, ask.Encode()); rec.Code != 400 {
		t.Fatal("missing model config", rec.Code)
	}
	other, err := srv.store.CreateLearningQuestion(t.Context(), store.LearningQuestion{Body: "其他问题"})
	if err != nil {
		t.Fatal(err)
	}
	if rec := doWithCookie(srv, cookie, "GET", "/questions/"+other.ID+"/study?session="+sessions[0].ID); rec.Code != 404 {
		t.Fatal("foreign session", rec.Code)
	}
	var jobs int
	if err = srv.store.DB.QueryRow(`SELECT COUNT(*) FROM processing_jobs WHERE job_type='question_study'`).Scan(&jobs); err != nil || jobs != 0 {
		t.Fatal(jobs, err)
	}
}
