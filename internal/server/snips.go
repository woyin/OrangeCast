package server

import (
	"fmt"
	"net/http"
	"strings"
)

// snipView 是片段页的一行：来源、时间范围与逐字原文（取自冻结的转录快照，不含 AI 文本）。
type snipView struct {
	ID, Title, Text, Link, Range, Created string
}

// handleSnips 列出一键保存的片段。片段即 learning_excerpts：创建时冻结了真实音频区间。
func (srv *Server) handleSnips(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}
	list, err := srv.store.ListLearningExcerpts(r.Context(), 100)
	if err != nil {
		http.Error(w, "读取片段失败", http.StatusInternalServerError)
		return
	}
	segText := map[string]map[string]string{} // snapshot -> segment id -> text
	views := make([]snipView, 0, len(list))
	for _, e := range list {
		texts, ok := segText[e.SnapshotID]
		if !ok {
			texts = map[string]string{}
			if _, segs, _, err := srv.store.SnapshotContent(r.Context(), e.SnapshotID); err == nil {
				for _, s := range segs {
					texts[s.ID] = s.Text
				}
			}
			segText[e.SnapshotID] = texts
		}
		var parts []string
		for _, id := range e.SegmentIDs {
			if t := strings.TrimSpace(texts[id]); t != "" {
				parts = append(parts, t)
			}
		}
		title := e.SourceTitle
		if title == "" {
			title = "（来源已删除）"
		}
		views = append(views, snipView{
			ID: e.ID, Title: title, Text: strings.Join(parts, " "), Created: e.CreatedAt,
			Link:  fmt.Sprintf("/sources/%s/%s?t=%.1f", e.SourceType, e.SourceID, e.StartSeconds),
			Range: clock(e.StartSeconds) + " – " + clock(e.EndSeconds),
		})
	}
	srv.tmpl.Render(w, "snips.html", map[string]any{"Snips": views})
}

func clock(sec float64) string {
	s := int(sec)
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s%3600/60, s%60)
	}
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}
