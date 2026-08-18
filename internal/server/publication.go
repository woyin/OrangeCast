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
	sources, err := srv.publicationSources(r, id, draft.EditorialProfileID)
	if err != nil {
		http.Error(w, "当前证据或素材授权已失效，不能生成内容包："+err.Error(), http.StatusConflict)
		return
	}
	if r.URL.Query().Get("format") == "markdown" {
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		w.Header().Set("Content-Disposition", "attachment; filename=\""+sanitizeFilename(revision.Title)+".md\"")
		_, _ = w.Write([]byte(revision.Markdown + "\n\n---\n\n## 来源\n" + markdownSourceList(sources)))
		return
	}
	packageData := buildPublicationPackage(revision.Title, revision.Markdown)
	switch r.URL.Query().Get("format") {
	case "plain":
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Content-Disposition", "attachment; filename=\""+sanitizeFilename(revision.Title)+".txt\"")
		_, _ = w.Write([]byte(packageData.PlainText + "\n\n来源\n" + plainSourceList(sources)))
		return
	case "cover-svg":
		w.Header().Set("Content-Type", "image/svg+xml; charset=utf-8")
		w.Header().Set("Content-Disposition", "attachment; filename=\""+sanitizeFilename(revision.Title)+"-cover.svg\"")
		_, _ = w.Write([]byte(coverSVG(packageData.CoverTitle, packageData.CoverSubtitle)))
		return
	}
	srv.tmpl.Render(w, "publication_package.html", map[string]any{
		"Revision": revision, "Sources": sources, "RichHTML": template.HTML(wechatRichText(revision.Markdown)), "Package": packageData,
	})
}

type publicationPackage struct {
	PlainText, Summary, Recommendation, CoverTitle, CoverSubtitle string
	CandidateTitles                                               []string
}

func buildPublicationPackage(title, markdown string) publicationPackage {
	plain := markdownPlainText(markdown)
	summary := ""
	var headings []string
	for _, line := range strings.Split(markdown, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			heading := strings.TrimSpace(strings.TrimLeft(trimmed, "#"))
			if heading != "" && heading != title {
				headings = append(headings, heading)
			}
			continue
		}
		if summary == "" && trimmed != "" && !strings.HasPrefix(trimmed, "-") {
			summary = truncateRunes(trimmed, 120)
		}
	}
	if summary == "" {
		summary = truncateRunes(plain, 120)
	}
	titles := []string{title}
	for _, heading := range headings {
		candidate := title + "｜" + heading
		if len(titles) == 3 {
			break
		}
		titles = append(titles, candidate)
	}
	return publicationPackage{PlainText: plain, Summary: summary, Recommendation: "推荐阅读：" + summary, CoverTitle: title, CoverSubtitle: summary, CandidateTitles: titles}
}

func markdownPlainText(markdown string) string {
	var lines []string
	for _, line := range strings.Split(markdown, "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimSpace(strings.TrimLeft(line, "#"))
		line = strings.TrimSpace(strings.TrimPrefix(line, "- "))
		if line != "" {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}

func truncateRunes(value string, limit int) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) <= limit {
		return string(runes)
	}
	return string(runes[:limit]) + "…"
}

func plainSourceList(sources []string) string {
	if len(sources) == 0 {
		return "本文未使用外部来源。\n"
	}
	return strings.Join(sources, "\n") + "\n"
}

func coverSVG(title, subtitle string) string {
	return fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="900" height="383" viewBox="0 0 900 383"><rect width="900" height="383" fill="#111827"/><rect x="48" y="48" width="8" height="287" rx="4" fill="#f59e0b"/><text x="88" y="150" fill="white" font-family="sans-serif" font-size="42" font-weight="700">%s</text><text x="88" y="215" fill="#d1d5db" font-family="sans-serif" font-size="22">%s</text><text x="88" y="315" fill="#9ca3af" font-family="sans-serif" font-size="18">CloudWisePod · Evidence-grounded</text></svg>`, html.EscapeString(truncateRunes(title, 22)), html.EscapeString(truncateRunes(subtitle, 36)))
}

func (srv *Server) publicationSources(r *http.Request, revisionID, profileID string) ([]string, error) {
	maps, err := srv.store.ListEvidenceMaps(r.Context(), revisionID)
	if err != nil {
		return nil, err
	}
	if len(maps) == 0 {
		return nil, fmt.Errorf("Revision 没有 EvidenceMap")
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
			usable, err := srv.store.CanUseSourceForPublication(r.Context(), profileID, keyPoint.SourceType, keyPoint.SourceID)
			if err != nil || !usable {
				return nil, fmt.Errorf("Source %s/%s 已归档或不可用", keyPoint.SourceType, keyPoint.SourceID)
			}
			var citations []string
			if err := json.Unmarshal([]byte(keyPoint.CitationsJSON), &citations); err != nil {
				return nil, err
			}
			valid, err := srv.store.ValidateSourceCitations(r.Context(), keyPoint.SourceType, keyPoint.SourceID, citations)
			if err != nil || !valid {
				return nil, fmt.Errorf("KeyPoint %s 的 Citation 已失效", keyPoint.ID)
			}
			title := strings.TrimSpace(keyPoint.SourceTitle)
			if title == "" {
				title = fmt.Sprintf("%s · %s", keyPoint.SourceType, keyPoint.SourceID)
			}
			sourceKey := string(keyPoint.SourceType) + "\x00" + keyPoint.SourceID
			if !seen[sourceKey] {
				seen[sourceKey] = true
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
