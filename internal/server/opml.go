// opml.go OPML 订阅清单导入（学习补齐：从其他播客客户端迁移订阅）。
// 只复用既有 RSS 抓取与订阅写入路径：导入不改变任何订阅的处理策略与处理深度，
// 新订阅仍是 Manual（仅发现），是否自动处理由 Owner 逐条显式开启。
package server

import (
	"encoding/xml"
	"io"
	"net/http"
	"strings"

	"github.com/woyin/orangecast/internal/auth"
)

// opmlDocument OPML 2.0 文档（只取订阅地址与标题，忽略其余展示信息）。
type opmlDocument struct {
	Body struct {
		Outlines []opmlOutline `xml:"outline"`
	} `xml:"body"`
}

type opmlOutline struct {
	Text     string        `xml:"text,attr"`
	Title    string        `xml:"title,attr"`
	Type     string        `xml:"type,attr"`
	XMLURL   string        `xml:"xmlUrl,attr"`
	Outlines []opmlOutline `xml:"outline"`
}

// parseOPMLFeeds 展平 OPML 大纲树，返回去重后的订阅地址与标题。
// 纯函数：不做网络请求，便于单元测试。
func parseOPMLFeeds(raw []byte) ([]opmlFeed, error) {
	var doc opmlDocument
	if err := xml.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var feeds []opmlFeed
	var walk func(items []opmlOutline)
	walk = func(items []opmlOutline) {
		for _, item := range items {
			if len(item.Outlines) > 0 {
				walk(item.Outlines)
			}
			url := strings.TrimSpace(item.XMLURL)
			if url == "" || seen[url] {
				continue
			}
			if item.Type != "" && !strings.EqualFold(item.Type, "rss") {
				continue
			}
			seen[url] = true
			title := strings.TrimSpace(item.Title)
			if title == "" {
				title = strings.TrimSpace(item.Text)
			}
			feeds = append(feeds, opmlFeed{URL: url, Title: title})
		}
	}
	walk(doc.Body.Outlines)
	return feeds, nil
}

// opmlFeed OPML 中的一条订阅。
type opmlFeed struct {
	URL   string
	Title string
}

// handleImportResult 导入结果（部分成功：能订阅的订阅，重复与失败的逐条报告）。
type handleImportResult struct {
	Imported int
	Skipped  int
	Failed   []string
}

// handleOPMLImport 处理 OPML 上传（GET 展示表单，POST 执行导入）。
func (srv *Server) handleOPMLImport(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		srv.tmpl.Render(w, "opml_import.html", map[string]any{"CSRF": auth.CSRFValue(r)})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<20)
	file, _, err := r.FormFile("opml")
	if err != nil {
		srv.tmpl.Render(w, "opml_import.html", map[string]any{"CSRF": auth.CSRFValue(r), "Error": "请选择 .opml 或 .xml 文件"})
		return
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, 4<<20))
	if err != nil {
		srv.tmpl.Render(w, "opml_import.html", map[string]any{"CSRF": auth.CSRFValue(r), "Error": "读取文件失败：" + err.Error()})
		return
	}
	feeds, err := parseOPMLFeeds(raw)
	if err != nil {
		srv.tmpl.Render(w, "opml_import.html", map[string]any{"CSRF": auth.CSRFValue(r), "Error": "解析 OPML 失败：" + err.Error()})
		return
	}
	if len(feeds) == 0 {
		srv.tmpl.Render(w, "opml_import.html", map[string]any{"CSRF": auth.CSRFValue(r), "Error": "文件中没有可导入的订阅地址"})
		return
	}
	result := srv.importOPMLFeeds(r, feeds)
	srv.tmpl.Render(w, "opml_import.html", map[string]any{
		"CSRF": auth.CSRFValue(r), "Result": result, "Total": len(feeds),
	})
}

// importOPMLFeeds 逐条订阅：抓取 feed、写入播客与单集；重复订阅与抓取失败逐条记录。
func (srv *Server) importOPMLFeeds(r *http.Request, feeds []opmlFeed) handleImportResult {
	result := handleImportResult{}
	for _, feed := range feeds {
		if _, err := srv.store.GetPodcastByFeedURL(r.Context(), feed.URL); err == nil {
			result.Skipped++
			continue
		}
		podcast, episodes, err := srv.fetchFeed(feed.URL)
		if err != nil {
			result.Failed = append(result.Failed, feed.URL+"（"+err.Error()+"）")
			continue
		}
		if strings.TrimSpace(podcast.Title) == "" {
			podcast.Title = feed.Title
		}
		created, err := srv.store.CreatePodcast(r.Context(), feed.URL, podcast.Title, podcast.Description, podcast.ImageURL)
		if err != nil {
			result.Failed = append(result.Failed, feed.URL+"（保存订阅失败）")
			continue
		}
		if _, err := srv.store.MergeEpisodes(r.Context(), created.ID, episodes); err != nil {
			result.Failed = append(result.Failed, feed.URL+"（保存单集失败）")
			continue
		}
		_ = srv.store.UpdatePodcastFetched(r.Context(), created.ID)
		_ = srv.store.MarkPodcastOPMLPresent(r.Context(), created.ID)
		result.Imported++
	}
	return result
}
