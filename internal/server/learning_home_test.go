package server

import (
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/store"
)

func TestDashboardNextActionsReadonlyAndPreferenceCAS(t *testing.T) {
	srv := newTestServer(t)
	session := claimOwnerAndLogin(t, srv, "home@example.test", "password123")
	q, err := srv.store.CreateLearningQuestion(t.Context(), store.LearningQuestion{Body: "怎么学会？"})
	if err != nil {
		t.Fatal(err)
	}
	var before, after int
	srv.store.DB.QueryRow(`SELECT total_changes()`).Scan(&before)
	for _, path := range []string{"/dashboard", "/api/learning-actions"} {
		rec := doWithCookie(srv, session, "GET", path)
		if rec.Code != 200 || rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatal(rec.Code, rec.Body.String())
		}
	}
	srv.store.DB.QueryRow(`SELECT total_changes()`).Scan(&after)
	if before != after {
		t.Fatal("GET wrote data", before, after)
	}
	p, _ := srv.store.GetLearningPreferences(t.Context())
	form := url.Values{"question_id": {q.ID}, "expected_revision": {strconv.Itoa(p.Revision)}, "request_key": {uuid.NewString()}}
	for i := 0; i < 2; i++ {
		rec := postForm(t, srv, session, "/api/learning-actions", form.Encode())
		if rec.Code != 303 {
			t.Fatal(rec.Code, rec.Body.String())
		}
	}
	form.Set("request_key", uuid.NewString())
	if rec := postForm(t, srv, session, "/api/learning-actions", form.Encode()); rec.Code != 409 {
		t.Fatal(rec.Code)
	}
	page := doWithCookie(srv, session, "GET", "/dashboard")
	if !strings.Contains(page.Body.String(), q.Body) || !strings.Contains(page.Body.String(), "你选定的当前学习问题") {
		t.Fatal(page.Body.String())
	}
	if rec := doWithCookie(srv, nil, "GET", "/api/learning-actions"); rec.Code != 401 {
		t.Fatal("authentication", rec.Code)
	}
}
