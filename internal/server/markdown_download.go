// Package server 实现 CloudWisePod 的 HTTP 层：路由、handler、模板渲染与 REST API。
// 按职责拆分到多个文件（本文件：KnowledgeNote Markdown 下载（信息分层标注））。
package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/woyin/orangecast/internal/markdown"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
)

// handleDownloadMarkdown 下载单 Source 的 KnowledgeNote Markdown（Roadmap Phase 5）。
// 确定性渲染：frontmatter + 摘要/要点/章节/金句（全部带 Citation 链接）+ 标签。
func (srv *Server) handleDownloadMarkdown(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/sources/"), "/")
	if len(parts) < 3 || parts[2] != "download" {
		http.NotFound(w, r)
		return
	}
	sourceType := models.SourceType(parts[0])
	sourceID := parts[1]

	card, err := srv.store.GetCurrentVersion(r.Context(), sourceType, sourceID, store.KindKnowledgeCard)
	if err != nil {
		http.Error(w, "尚无知识卡片", http.StatusNotFound)
		return
	}
	tr, err := srv.store.GetCurrentVersion(r.Context(), sourceType, sourceID, store.KindTranscript)
	if err != nil {
		http.Error(w, "尚无转录稿", http.StatusNotFound)
		return
	}
	var cardData provider.KnowledgeCard
	if err := json.Unmarshal([]byte(card.Payload), &cardData); err != nil {
		http.Error(w, "卡片数据损坏", http.StatusInternalServerError)
		return
	}
	var tp provider.TranscriptPayload
	if err := json.Unmarshal([]byte(tr.Payload), &tp); err != nil {
		http.Error(w, "转录数据损坏", http.StatusInternalServerError)
		return
	}
	title := cardData.Title
	// ADR-0018 R4：Owner 可选下沉 GeneratedDerivative 块（Paraphrase + 手选 StudyChat 回答）。
	// 默认不包含（遵守"禁止自动写入 KnowledgeNote"）；通过 ?with_generated=1 显式开启。
	// 下沉的块一律标为 AI 讲解·非原文，挂 Reference（?ref=），与 CitedDerivative（?t=）区分。
	var genBlocks []markdown.GeneratedBlock
	if r.URL.Query().Get("with_generated") == "1" {
		// Paraphrase：Owner 明确触发的重讲，全部纳入（每锚点最近 3 条已由存储淘汰）。
		paras, _ := srv.store.ListParaphrasesForSource(r.Context(), sourceType, sourceID)
		for _, pr := range paras {
			genBlocks = append(genBlocks, markdown.GeneratedBlock{
				Kind: "Paraphrase", Body: pr.Body,
				References: parseSegmentIDs(pr.SegmentIDs),
			})
		}
		// StudyChat：仅纳入未被抑制的 assistant 回答（抑制的是 ReferenceCheck 失败，不应下沉）。
		sessions, _ := srv.store.ListStudySessions(r.Context(), sourceType, sourceID)
		for _, sess := range sessions {
			msgs, _ := srv.store.ListStudyMessages(r.Context(), sess.ID, false)
			for _, m := range msgs {
				if m.Role != "assistant" || m.Suppressed {
					continue
				}
				genBlocks = append(genBlocks, markdown.GeneratedBlock{
					Kind: "StudyChat", Body: m.Content,
					References: m.ReferenceSegmentIDs,
				})
			}
		}
	}
	md, err := markdown.Render(markdown.Input{
		Card: &cardData, Segments: tp.Segments,
		SourceType: string(sourceType), SourceID: sourceID,
		Title: title, BaseURL: srv.cfg.PublicURL,
		GeneratedAt:     time.Now().UTC().Format(time.RFC3339),
		GeneratedBlocks: genBlocks,
	})
	if err != nil {
		http.Error(w, "渲染失败", http.StatusInternalServerError)
		return
	}
	filename := sanitizeFilename(title) + ".md"
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+filename+"\"")
	_, _ = w.Write([]byte(md))
}
