package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// TestDigestBatch_Success 测试批量生成精读文成功入队并重定向。
func TestDigestBatch_Success(t *testing.T) {
	srv, session, csrf, epID := seedDigestEpisode(t)

	ep, err := srv.store.GetEpisodeByID(t.Context(), epID)
	if err != nil {
		t.Fatalf("获取单集失败: %v", err)
	}

	form := url.Values{
		"source_type": {"episode"},
		"podcast_id":  {ep.PodcastID},
		"source_id":   {epID},
		"_csrf":       {csrf},
	}
	req := httptest.NewRequest(http.MethodPost, "/api/digest/batch", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(session)
	req.AddCookie(&http.Cookie{Name: "cwp_csrf", Value: csrf})

	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("批量入队应返回 303: %d, body: %s", rec.Code, rec.Body.String())
	}

	location := rec.Header().Get("Location")
	if !strings.Contains(location, "enqueued=1") {
		t.Fatalf("Location 头应包含 enqueued=1，实际为: %s", location)
	}
}
