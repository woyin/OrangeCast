package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestKnowledgeListControlsAndPrivatePaginationParameters(t *testing.T) {
	srv := newTestServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/knowledge-articles?article_page=2&source_page=2&source_query=旧来源&article_status=ready&candidate_status=ready&_csrf=secret-must-not-be-linked", nil)
	srv.handleKnowledgeArticles(rec, req)
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, text := range []string{"上一页文章", "上一页来源", "value=\"ready\"", "已成稿（旧记录）", "查找来源标题"} {
		if !strings.Contains(body, text) {
			t.Fatal("missing list control", text)
		}
	}
	if strings.Contains(body, "_csrf=secret-must-not-be-linked") || strings.Contains(body, "_csrf%3Dsecret-must-not-be-linked") {
		t.Fatal("private token leaked into page URL")
	}
	for _, path := range []string{"/notes?source_query=旧来源", "/search?source_query=旧来源"} {
		rec = httptest.NewRecorder()
		req = httptest.NewRequest(http.MethodGet, path, nil)
		if strings.HasPrefix(path, "/notes") {
			srv.handleNotes(rec, req)
		} else {
			srv.handleSearch(rec, req)
		}
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), "来源选择分页") || !strings.Contains(rec.Body.String(), "旧来源") {
			t.Fatal("source picker regression", rec.Code, rec.Body.String())
		}
	}
}
