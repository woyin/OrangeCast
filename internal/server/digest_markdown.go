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

	// G05：交付导出时重新检查就绪状态（不只相信生成时状态）；
	// 草稿允许导出但显式携带草稿标记；导出不创建发布历史。
	readiness, err := srv.store.EvaluateDigestReadiness(r.Context(), d.ID)
	if err != nil {
		http.Error(w, "就绪检查失败", http.StatusInternalServerError)
		return
	}

	var sb strings.Builder
	sb.WriteString("# " + d.Title + "\n\n")
	sb.WriteString(fmt.Sprintf("> 来源：%s/%s · 修订 v%d · %s\n", d.SourceType, d.SourceID, d.Version, d.CreatedAt))
	sb.WriteString("> 状态：" + readiness.Summary() + "\n\n")

	// G04：共享引用解析逻辑——导出与页面一致显示来源名称与冻结快照定位。
	views := map[string][]digestCitationView{}
	for _, b := range blocks {
		views[b.ID] = srv.digestCitationViews(r.Context(), d, b.Citations)
	}

	for _, b := range blocks {
		switch b.Type {
		case models.DigestBlockParaphrase:
			sb.WriteString(b.Text + "\n\n")
		case models.DigestBlockAIExpansion:
			sb.WriteString("> [!ai-generated] " + strings.ReplaceAll(b.Text, "\n", "\n> ") + "\n\n")
		case models.DigestBlockNote:
			sb.WriteString("> [!note] Owner 笔记\n> " + strings.ReplaceAll(b.Text, "\n", "\n> ") + "\n\n")
		case models.DigestBlockCitedFact:
			sb.WriteString(b.Text + "\n\n")
		}
		if cvs := views[b.ID]; len(cvs) > 0 {
			var links []string
			for _, cv := range cvs {
				if cv.Frozen {
					links = append(links, fmt.Sprintf("[%s](%s)", cv.Label, cv.URL))
				} else {
					links = append(links, cv.Label)
				}
			}
			sb.WriteString("> 依据：" + strings.Join(links, " · ") + "\n\n")
		}
	}

	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"digest-%s.md\"", d.ID))
	w.Write([]byte(sb.String()))
}
