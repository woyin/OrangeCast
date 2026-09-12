package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// TestDocumentDigestEnqueue_Success 验证针对 Document Source 单击生成精读文。
func TestDocumentDigestEnqueue_Success(t *testing.T) {
	srv, session, csrf, _ := seedDigestEpisode(t)
	ctx := t.Context()

	doc, err := srv.store.CreatePastedDocument(ctx, "架构设计文档", "本文档描述系统架构设计要点。")
	if err != nil {
		t.Fatal(err)
	}

	form := url.Values{
		"source_type": {"document"},
		"source_id":   {doc.ID},
		"_csrf":       {csrf},
	}
	req := httptest.NewRequest(http.MethodPost, "/api/digest", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(session)
	req.AddCookie(&http.Cookie{Name: "cwp_csrf", Value: csrf})
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("文档入队精读应 303, 实际 %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Header().Get("Location"), "/sources/document/"+doc.ID) {
		t.Fatalf("应重定向回文档详情, 实际 %s", rec.Header().Get("Location"))
	}
}
