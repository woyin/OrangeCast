package server

import (
	"bytes"
	"context"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/woyin/orangecast/internal/models"
)

const opmlFixture = `<opml version="2.0"><body><outline type="rss" text="A" xmlUrl="https://a.example/feed.xml"/><outline type="rss" text="B" xmlUrl="https://b.example/fail.xml"/></body></opml>`

const emptyOPMLFixture = `<opml version="2.0"><body></body></opml>`

// postMultipart 以 multipart 表单上传一个文件字段（模拟浏览器文件选择）。
func postMultipart(t *testing.T, srv *Server, cookie *http.Cookie, path, field, filename, content string) *httptest.ResponseRecorder {
	t.Helper()
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile(field, filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	csrf := csrfCookieValue(t, srv, cookie, path)
	if err := writer.WriteField("_csrf", csrf); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, path, body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.AddCookie(cookie)
	req.AddCookie(&http.Cookie{Name: "cwp_csrf", Value: csrf})
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	return rec
}

// csrfCookieValue 读取当前会话可用的 CSRF 值（GET 受保护页面时由中间件下发）。
func csrfCookieValue(t *testing.T, srv *Server, cookie *http.Cookie, path string) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	for _, c := range rec.Result().Cookies() {
		if c.Name == "cwp_csrf" {
			return c.Value
		}
	}
	t.Fatal("未取得 CSRF cookie")
	return ""
}

// TestParseOPMLFeeds_FlattensNestedOutlines 展平嵌套大纲并去重。
func TestParseOPMLFeeds_FlattensNestedOutlines(t *testing.T) {
	raw := []byte(`<?xml version="1.0"?>
<opml version="2.0"><head><title>x</title></head><body>
  <outline text="分组">
    <outline type="rss" text="A" title="节目A" xmlUrl="https://a.example/feed.xml"/>
  </outline>
  <outline type="rss" text="B" xmlUrl="https://b.example/feed.xml"/>
  <outline type="rss" xmlUrl="https://a.example/feed.xml"/>
  <outline type="link" xmlUrl="https://c.example/page"/>
</body></opml>`)
	feeds, err := parseOPMLFeeds(raw)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(feeds) != 2 {
		t.Fatalf("应得到 2 条订阅，实际 %d: %+v", len(feeds), feeds)
	}
	if feeds[0].URL != "https://a.example/feed.xml" || feeds[0].Title != "节目A" {
		t.Errorf("嵌套条目应保留标题: %+v", feeds[0])
	}
	if feeds[1].Title != "B" {
		t.Errorf("无 title 时回退 text: %+v", feeds[1])
	}
}

// TestParseOPMLFeeds_InvalidXML 非法 XML 显式报错。
func TestParseOPMLFeeds_InvalidXML(t *testing.T) {
	if _, err := parseOPMLFeeds([]byte("not xml")); err == nil {
		t.Fatal("非法 XML 应报错")
	}
}

// TestOPMLImport_GetForm 未登录访问受保护；登录后返回表单。
func TestOPMLImport_GetForm(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "opml@example.com", "password123")
	rec := doWithCookie(srv, cookie, http.MethodGet, "/podcasts/opml")
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 %d", rec.Code)
	}
	if rec.Body.Len() == 0 {
		t.Error("表单页面不应为空")
	}
}

// TestOPMLImport_PostImportsFeeds 上传含两条订阅的 OPML：成功订阅、重复跳过、失败逐条报告。
func TestOPMLImport_PostImportsFeeds(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "opml-post@example.com", "password123")
	srv.fetchFeed = func(feedURL string) (*models.Podcast, []models.Episode, error) {
		if strings.Contains(feedURL, "fail") {
			return nil, nil, errors.New("feed 抓取失败")
		}
		return &models.Podcast{Title: "节目 " + feedURL}, []models.Episode{{GUID: "g-" + feedURL, Title: "E", AudioURL: "https://a.mp3"}}, nil
	}
	raw := opmlFixture

	rec := postMultipart(t, srv, cookie, "/podcasts/opml", "opml", "subs.opml", raw)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "成功 1 个") {
		t.Errorf("应报告 1 条导入成功")
	}
	if !strings.Contains(body, "失败 1 个") {
		t.Error("应报告失败条目")
	}
	podcasts, err := srv.store.ListPodcasts(context.Background())
	if err != nil || len(podcasts) != 1 {
		t.Fatalf("应写入 1 个订阅: %v %v", podcasts, err)
	}
	if podcasts[0].Title != "节目 https://a.example/feed.xml" {
		t.Errorf("订阅标题应来自 feed: %q", podcasts[0].Title)
	}

	// 再次导入同一文件：已存在的订阅跳过，不重复创建。
	rec2 := postMultipart(t, srv, cookie, "/podcasts/opml", "opml", "subs.opml", raw)
	if !strings.Contains(rec2.Body.String(), "已存在跳过 1 个") {
		t.Errorf("重复导入应跳过已存在订阅")
	}
}

// TestOPMLImport_RejectsInvalidFile 非法文件与空订阅清单都给出可见提示。
func TestOPMLImport_RejectsInvalidFile(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "opml-bad@example.com", "password123")
	rec := postMultipart(t, srv, cookie, "/podcasts/opml", "opml", "bad.opml", "not xml")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "解析 OPML 失败") {
		t.Fatalf("应报告解析失败: %d", rec.Code)
	}
	rec2 := postMultipart(t, srv, cookie, "/podcasts/opml", "opml", "empty.opml", emptyOPMLFixture)
	if !strings.Contains(rec2.Body.String(), "没有可导入的订阅地址") {
		t.Error("空清单应给出明确提示")
	}
}
