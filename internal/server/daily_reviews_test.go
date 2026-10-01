package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
)

func dailyHTTPFixture(t *testing.T) (*Server, *http.Cookie, *weeklyFake, *store.ReviewSession) {
	t.Helper()
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "daily@example.com", "password123")
	seedKnowledgeLearning(t, srv)
	srv.cfg.PodBaseURL, srv.cfg.PodAPIKey, srv.cfg.PodModel = "https://example.test/v1", "test", "model"
	fake := &weeklyFake{}
	srv.worker.WithBundleResolver(func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{KnowledgeArticle: fake}, nil
	})
	result := postForm(t, srv, cookie, "/review/action", "action=generate")
	if result.Code != 303 {
		t.Fatal(result.Body.String())
	}
	if err := srv.worker.ProcessOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Add(time.Minute)
	srv.reviewClock = func() time.Time { return now }
	return srv, cookie, fake, nil
}

func TestDailyReviewHTTPDoesNotGenerateAndKeepsUnsubmittedExplanation(t *testing.T) {
	srv, cookie, fake, _ := dailyHTTPFixture(t)
	for _, path := range []string{"/review/daily", "/dashboard"} {
		page := doWithCookie(srv, cookie, "GET", path)
		if page.Code != 200 || !strings.Contains(page.Body.String(), "每日短回顾") || strings.Contains(page.Body.String(), "隐藏依据标记") {
			t.Fatal(path, page.Code, page.Body.String())
		}
	}
	var sessions int
	srv.store.DB.QueryRow(`SELECT count(*) FROM review_sessions`).Scan(&sessions)
	if sessions != 0 {
		t.Fatal("GET created session")
	}
	start := url.Values{"action": {"start"}, "size": {"3"}, "request_key": {uuid.NewString()}}
	response := postForm(t, srv, cookie, "/review/daily/action", start.Encode())
	if response.Code != 303 {
		t.Fatal(response.Code, response.Body.String())
	}
	path := response.Header().Get("Location")
	repeated := postForm(t, srv, cookie, "/review/daily/action", start.Encode())
	if repeated.Code != 303 || repeated.Header().Get("Location") != path {
		t.Fatal("start retried new session")
	}
	session, _ := srv.store.ActiveReviewSession(t.Context())
	items, _ := srv.store.ListReviewSessionItems(t.Context(), session.ID)
	item := items[0]
	page := doWithCookie(srv, cookie, "GET", path)
	if page.Code != 200 || strings.Contains(page.Body.String(), "隐藏依据标记") {
		t.Fatal("basis leaked", page.Body.String())
	}
	fields := url.Values{"action": {"reveal"}, "session": {session.ID}, "item": {item.ItemID}, "expected_revision": {"1"}, "request_key": {uuid.NewString()}, "answer": {"解释仍在构思，查看依据不代表回答。"}, "assessment": {"partial"}}
	response = postForm(t, srv, cookie, "/review/daily/action", fields.Encode())
	if response.Code != 303 {
		t.Fatal(response.Code, response.Body.String())
	}
	page = doWithCookie(srv, cookie, "GET", path)
	if !strings.Contains(page.Body.String(), fields.Get("answer")) || !strings.Contains(page.Body.String(), "隐藏依据标记") {
		t.Fatal("reveal lost draft or basis", page.Body.String())
	}
	history, _ := srv.store.LearningReviewAnswerHistory(t.Context(), item.ItemID)
	if len(history) != 0 {
		t.Fatal("reveal became answer")
	}
	fields.Set("action", "answer")
	fields.Set("expected_revision", "2")
	fields.Set("request_key", uuid.NewString())
	fields.Set("answer", "我的个人解释要标明证据边界。")
	response = postForm(t, srv, cookie, "/review/daily/action", fields.Encode())
	if response.Code != 303 {
		t.Fatal(response.Code, response.Body.String())
	}
	if repeated = postForm(t, srv, cookie, "/review/daily/action", fields.Encode()); repeated.Code != 303 {
		t.Fatal("answer retry", repeated.Code, repeated.Body.String())
	}
	fields.Set("answer", "旧窗口不同的解释")
	response = postForm(t, srv, cookie, "/review/daily/action", fields.Encode())
	if response.Code != 409 || !strings.Contains(response.Body.String(), fields.Get("answer")) {
		t.Fatal("conflict lost draft", response.Code, response.Body.String())
	}
	fields.Set("action", "save_note")
	fields.Set("expected_revision", "3")
	response = postForm(t, srv, cookie, "/review/daily/action", fields.Encode())
	if response.Code != 303 {
		t.Fatal(response.Code, response.Body.String())
	}
	page = doWithCookie(srv, cookie, "GET", path)
	if !strings.Contains(page.Body.String(), "已保存为个人理解笔记") {
		t.Fatal(page.Body.String())
	}
	if fake.calls != 1 {
		t.Fatal("daily review called AI", fake.calls)
	}
	if rec := doWithCookie(srv, nil, "GET", "/review/daily"); rec.Code != 303 || rec.Header().Get("Location") != "/login" {
		t.Fatal("private route", rec.Code)
	}
	if rec := doWithCookie(srv, cookie, "POST", "/review/daily/action"); rec.Code != 403 {
		t.Fatal("CSRF", rec.Code)
	}
}

func TestDailyReviewHTTPControlsErrorsAndReadOnlyHistory(t *testing.T) {
	srv, cookie, fake, _ := dailyHTTPFixture(t)
	for _, tt := range []struct {
		body string
		want int
	}{{"action=size&size=5", 303}, {"action=size&size=6", 400}, {"action=timezone&timezone=Asia%2FSingapore", 303}, {"action=timezone&timezone=bad", 400}, {"action=invalid", 400}, {"action=start&size=2&request_key=bad", 400}, {"action=answer&expected_revision=bad", 400}} {
		response := postForm(t, srv, cookie, "/review/daily/action", tt.body)
		if response.Code != tt.want {
			t.Fatal(tt, response.Code, response.Body.String())
		}
	}
	start := postForm(t, srv, cookie, "/review/daily/action", url.Values{"action": {"start"}, "request_key": {uuid.NewString()}}.Encode())
	if start.Code != 303 {
		t.Fatal(start.Body.String())
	}
	session, _ := srv.store.ActiveReviewSession(t.Context())
	items, _ := srv.store.ListReviewSessionItems(t.Context(), session.ID)
	schedules, _ := srv.store.ListReviewSchedules(t.Context(), "", 0)
	body := url.Values{"action": {"schedule_due"}, "item": {items[0].ItemID}, "expected_revision": {"1"}, "request_key": {uuid.NewString()}, "local_due": {"2026-10-04T08:00"}}
	response := postForm(t, srv, cookie, "/review/daily/action", body.Encode())
	if response.Code != 303 {
		t.Fatal(response.Code, response.Body.String())
	}
	var due string
	srv.store.DB.QueryRow(`SELECT due_utc FROM review_schedules WHERE item_id=?`, items[0].ItemID).Scan(&due)
	if due != "2026-10-04T00:00:00Z" {
		t.Fatal(due)
	}
	body.Set("action", "schedule_pause")
	body.Set("expected_revision", "2")
	body.Set("request_key", uuid.NewString())
	response = postForm(t, srv, cookie, "/review/daily/action", body.Encode())
	if response.Code != 303 {
		t.Fatal(response.Body.String())
	}
	page := doWithCookie(srv, cookie, "GET", start.Header().Get("Location"))
	if page.Code != 200 || !strings.Contains(page.Body.String(), "已暂停") {
		t.Fatal(page.Code, page.Body.String())
	}
	end := url.Values{"action": {"session_end"}, "session": {session.ID}, "expected_revision": {"1"}, "request_key": {uuid.NewString()}}
	response = postForm(t, srv, cookie, "/review/daily/action", end.Encode())
	if response.Code != 303 {
		t.Fatal(response.Code, response.Body.String())
	}
	for _, path := range []string{"/review/daily?offset=-1&history_offset=-1", "/review/daily?status=paused", "/review/daily?session=missing"} {
		page = doWithCookie(srv, cookie, "GET", path)
		want := 200
		if strings.Contains(path, "missing") {
			want = 404
		}
		if page.Code != want {
			t.Fatal(path, page.Code, page.Body.String())
		}
	}
	req := httptest.NewRequest("POST", "/review/daily/action", strings.NewReader("action=answer&expected_revision=1&session=missing&item=missing&request_key="+uuid.NewString()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()
	srv.handleDailyReviewAction(rec, req)
	if rec.Code == 200 {
		t.Fatal("invalid action accepted")
	}
	for _, handler := range []http.HandlerFunc{srv.handleDailyReview, srv.handleDailyReviewAction} {
		rec = httptest.NewRecorder()
		handler(rec, httptest.NewRequest("PATCH", "/review/daily", nil))
		if rec.Code != 405 {
			t.Fatal("method guard", rec.Code)
		}
	}
	if len(schedules) != 1 || fake.calls != 1 {
		t.Fatal(schedules, fake.calls)
	}
	history, _ := srv.store.ListReviewSessions(t.Context(), 0)
	raw, _ := json.Marshal(history)
	if len(history) != 1 || len(raw) == 0 {
		t.Fatal(history)
	}
}
