package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWorkbenchCreatesAndRendersEditorialProfile(t *testing.T) {
	srv := newTestServer(t)
	session := claimOwnerAndLogin(t, srv, "editor@example.com", "password123")

	get := httptest.NewRequest(http.MethodGet, "/workbench", nil)
	get.AddCookie(session)
	getRec := httptest.NewRecorder()
	srv.Router().ServeHTTP(getRec, get)
	if getRec.Code != http.StatusOK || !strings.Contains(getRec.Body.String(), "内容工作台") {
		t.Fatalf("workbench should render: status=%d body=%q", getRec.Code, getRec.Body.String())
	}
	csrf := ""
	for _, cookie := range getRec.Result().Cookies() {
		if cookie.Name == "cwp_csrf" {
			csrf = cookie.Value
		}
	}
	if csrf == "" {
		t.Fatal("workbench GET should issue a CSRF cookie")
	}

	post := httptest.NewRequest(http.MethodPost, "/workbench/profiles",
		strings.NewReader("_csrf="+csrf+"&name=%E6%8A%80%E6%9C%AF%E5%91%A8%E5%88%8A&source_attribution=standard&monthly_budget_cents=1200"))
	post.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	post.AddCookie(session)
	post.AddCookie(&http.Cookie{Name: "cwp_csrf", Value: csrf})
	postRec := httptest.NewRecorder()
	srv.Router().ServeHTTP(postRec, post)
	if postRec.Code != http.StatusSeeOther || !strings.HasPrefix(postRec.Header().Get("Location"), "/workbench?profile=") {
		t.Fatalf("profile creation should redirect to selected workbench: status=%d location=%q", postRec.Code, postRec.Header().Get("Location"))
	}

	profiles, err := srv.store.ListEditorialProfiles(post.Context())
	if err != nil || len(profiles) != 1 || profiles[0].Name != "技术周刊" || profiles[0].MonthlyBudgetCents == nil || *profiles[0].MonthlyBudgetCents != 1200 {
		t.Fatalf("profile must persist: profiles=%+v err=%v", profiles, err)
	}
}

func TestEditorialProfileCreationRejectsInvalidBudget(t *testing.T) {
	srv := newTestServer(t)
	session := claimOwnerAndLogin(t, srv, "editor@example.com", "password123")
	get := httptest.NewRequest(http.MethodGet, "/workbench", nil)
	get.AddCookie(session)
	getRec := httptest.NewRecorder()
	srv.Router().ServeHTTP(getRec, get)
	var csrf string
	for _, cookie := range getRec.Result().Cookies() {
		if cookie.Name == "cwp_csrf" {
			csrf = cookie.Value
		}
	}
	post := httptest.NewRequest(http.MethodPost, "/workbench/profiles", strings.NewReader("_csrf="+csrf+"&name=x&monthly_budget_cents=-1"))
	post.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	post.AddCookie(session)
	post.AddCookie(&http.Cookie{Name: "cwp_csrf", Value: csrf})
	postRec := httptest.NewRecorder()
	srv.Router().ServeHTTP(postRec, post)
	if postRec.Code != http.StatusBadRequest {
		t.Fatalf("negative budget should be rejected: status=%d", postRec.Code)
	}
}
