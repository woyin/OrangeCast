package server

import (
	"encoding/json"
	"fmt"
	"html"
	"html/template"
	"net/http"
	"strings"
)

// handlePublicationPackage renders or downloads a package only after the exact revision passes evidence review.
func (srv *Server) handlePublicationPackage(w http.ResponseWriter, r *http.Request) {
	id, ok := strings.CutPrefix(r.URL.Path, "/workbench/revisions/")
	if !ok || !strings.HasSuffix(id, "/package") {
		http.NotFound(w, r)
		return
	}
	id = strings.TrimSuffix(id, "/package")
	if id == "" || strings.Contains(id, "/") {
		http.NotFound(w, r)
		return
	}
	revision, err := srv.store.GetArticleRevision(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	draft, err := srv.store.GetArticleDraft(r.Context(), revision.DraftID)
	if err != nil {
		http.Error(w, "读取文章草稿失败", http.StatusInternalServerError)
		return
	}
	// A passed historic snapshot remains auditable, but cannot be published
	// after a newer revision becomes current. This prevents an old approval
	// from bypassing the re-review requirement created by any edit.
	if draft.CurrentRevisionID == nil || *draft.CurrentRevisionID != revision.ID {
		http.Error(w, "只能为当前修订生成内容包；请先审校当前版本", http.StatusConflict)
		return
	}
	ready, err := srv.store.IsRevisionReadyForPublication(r.Context(), id)
	if err != nil {
		http.Error(w, "检查证据门禁失败", http.StatusInternalServerError)
		return
	}
	if !ready {
		http.Error(w, "当前修订尚未通过证据审校，不能生成内容包", http.StatusConflict)
		return
	}
	sources, err := srv.publicationSources(r, id)
	if err != nil {
		http.Error(w, "整理来源清单失败", http.StatusInternalServerError)
		return
	}
	if r.URL.Query().Get("format") == "markdown" {
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		w.Header().Set("Content-Disposition", "attachment; filename=\""+sanitizeFilename(revision.Title)+".md\"")
		_, _ = w.Write([]byte(revision.Markdown + "\n\n---\n\n## 来源\n" + markdownSourceList(sources)))
		return
	}
	srv.tmpl.Render(w, "publication_package.html", map[string]any{
		"Revision": revision, "Sources": sources, "RichHTML": template.HTML(wechatRichText(revision.Markdown)),
	})
}

func (srv *Server) publicationSources(r *http.Request, revisionID string) ([]string, error) {
	maps, err := srv.store.ListEvidenceMaps(r.Context(), revisionID)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var sources []string
	for _, mapping := range maps {
		var ids []string
		if err := json.Unmarshal([]byte(mapping.KeyPointIDs), &ids); err != nil {
			return nil, err
		}
		for _, id := range ids {
			keyPoint, err := srv.store.GetKeyPoint(r.Context(), id)
			if err != nil {
				return nil, err
			}
			title := strings.TrimSpace(keyPoint.SourceTitle)
			if title == "" {
				title = fmt.Sprintf("%s · %s", keyPoint.SourceType, keyPoint.SourceID)
			}
			if !seen[title] {
				seen[title] = true
				sources = append(sources, title)
			}
		}
	}
	return sources, nil
}

func markdownSourceList(sources []string) string {
	if len(sources) == 0 {
		return "- 本文未使用外部来源。\n"
	}
	var b strings.Builder
	for _, source := range sources {
		b.WriteString("- ")
		b.WriteString(source)
		b.WriteByte('\n')
	}
	return b.String()
}

// wechatRichText deliberately supports the conservative Markdown subset emitted by Writer and Owner edits.
// User text is escaped before assembly; no raw HTML from a revision is trusted.
func wechatRichText(markdown string) string {
	var b strings.Builder
	inList := false
	closeList := func() {
		if inList {
			b.WriteString("</ul>")
			inList = false
		}
	}
	for _, line := range strings.Split(markdown, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "":
			closeList()
		case strings.HasPrefix(trimmed, "### "):
			closeList()
			b.WriteString("<h3>")
			b.WriteString(html.EscapeString(strings.TrimSpace(strings.TrimPrefix(trimmed, "### "))))
			b.WriteString("</h3>")
		case strings.HasPrefix(trimmed, "## "):
			closeList()
			b.WriteString("<h2>")
			b.WriteString(html.EscapeString(strings.TrimSpace(strings.TrimPrefix(trimmed, "## "))))
			b.WriteString("</h2>")
		case strings.HasPrefix(trimmed, "# "):
			closeList()
			b.WriteString("<h1>")
			b.WriteString(html.EscapeString(strings.TrimSpace(strings.TrimPrefix(trimmed, "# "))))
			b.WriteString("</h1>")
		case strings.HasPrefix(trimmed, "- "):
			if !inList {
				b.WriteString("<ul>")
				inList = true
			}
			b.WriteString("<li>")
			b.WriteString(html.EscapeString(strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))))
			b.WriteString("</li>")
		default:
			closeList()
			b.WriteString("<p>")
			b.WriteString(html.EscapeString(trimmed))
			b.WriteString("</p>")
		}
	}
	closeList()
	return b.String()
}
