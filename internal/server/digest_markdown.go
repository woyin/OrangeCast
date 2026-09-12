package server

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/woyin/orangecast/internal/models"
)

// handleDigestMarkdown 输出确定性的 Markdown 格式精读文（带 Obsidian Callout 分层标注）。
func (srv *Server) handleDigestMarkdown(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/digest/")
	id := strings.TrimSuffix(path, "/markdown")
	if id == "" {
		http.NotFound(w, r)
		return
	}
	d, err := srv.store.GetEpisodeDigest(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	blocks, err := srv.store.ListDigestBlocks(r.Context(), d.ID)
	if err != nil {
		http.Error(w, "加载内容块失败", http.StatusInternalServerError)
		return
	}

	var sb strings.Builder
	sb.WriteString("# " + d.Title + "\n\n")
	sb.WriteString(fmt.Sprintf("> 来源：%s/%s · 修订 v%d · %s\n\n", d.SourceType, d.SourceID, d.Version, d.CreatedAt))

	for _, b := range blocks {
		switch b.Type {
		case models.DigestBlockParaphrase:
			sb.WriteString(b.Text + "\n\n")
		case models.DigestBlockAIExpansion:
			sb.WriteString("> [!ai-generated] " + b.Text + "\n\n")
		case models.DigestBlockNote:
			sb.WriteString("> [!note] Owner 笔记\n> " + b.Text + "\n\n")
		case models.DigestBlockCitedFact:
			sb.WriteString(b.Text + "\n\n")
		}
	}

	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"digest-%s.md\"", d.ID))
	w.Write([]byte(sb.String()))
}
