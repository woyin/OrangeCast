package server

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/store"
)

func knowledgeQuery(r *http.Request) store.KnowledgeSearchQuery {
	q := r.URL.Query()
	if source := strings.SplitN(q.Get("source"), ":", 2); len(source) == 2 {
		q.Set("source_type", source[0])
		q.Set("source_id", source[1])
	}
	page, _ := strconv.Atoi(q.Get("page"))
	return store.KnowledgeSearchQuery{Text: strings.TrimSpace(q.Get("q")), Kind: q.Get("kind"), SourceType: q.Get("source_type"), SourceID: q.Get("source_id"), PodcastID: q.Get("podcast_id"), Theme: q.Get("theme"), From: q.Get("from"), Until: q.Get("until"), Page: page, PerPage: 20, IncludeDrafts: q.Get("drafts") == "1", IncludeHistory: q.Get("history") == "1"}
}

type knowledgeSearchView struct {
	Hit         store.KnowledgeSearchHit
	Label, Href string
}

func knowledgeSearchViews(hits []store.KnowledgeSearchHit) []knowledgeSearchView {
	labels := map[string]string{"original": "原文", "document": "文档原文", "keypoint": "来源重点", "source_note": "来源笔记", "owner_reflection": "我的理解", "article": "知识文章"}
	out := make([]knowledgeSearchView, 0, len(hits))
	for _, hit := range hits {
		href := sourceHref(models.SourceType(hit.SourceType), hit.SourceID, hit.Position)
		switch hit.Kind {
		case "article":
			href = fmt.Sprintf("/knowledge-articles/%s?revision=%d#paragraph-%.0f", url.PathEscape(hit.ObjectID), hit.Revision, hit.Position)
		case "document":
			href = "/documents/" + url.PathEscape(hit.ObjectID) + "#" + url.PathEscape(hit.SegmentID)
		case "source_note", "owner_reflection":
			href = "/sources/" + url.PathEscape(hit.SourceType) + "/" + url.PathEscape(hit.SourceID) + "#note-" + url.PathEscape(hit.ObjectID)
			if hit.SourceType == "document" {
				href = "/documents/" + url.PathEscape(hit.SourceID) + "#note-" + url.PathEscape(hit.ObjectID)
			}
		case "keypoint":
			if hit.SourceType == "document" && hit.SegmentID != "" {
				href = "/documents/" + url.PathEscape(hit.SourceID) + "#" + url.PathEscape(hit.SegmentID)
			}
		}
		out = append(out, knowledgeSearchView{Hit: hit, Label: labels[hit.Kind], Href: href})
	}
	return out
}
func knowledgePageURL(r *http.Request, page int) string {
	q := r.URL.Query()
	q.Set("page", strconv.Itoa(page))
	return r.URL.Path + "?" + q.Encode()
}
