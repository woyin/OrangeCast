// Package server 实现 OrangeCast 的 HTTP 层：路由、handler、模板渲染与 REST API。
// 按职责拆分到多个文件（本文件：KnowledgeNote Markdown 下载（信息分层标注））。
package server

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/woyin/orangecast/internal/markdown"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
)

// handleDownloadMarkdown 下载单 Source 的 KnowledgeNote Markdown（Roadmap Phase 5）。
// 确定性渲染：frontmatter + 摘要/要点/章节/金句（全部带 Citation 链接）+ 标签。
// sourceExport 是一个来源导出所需的全部渲染结果，供 Markdown 下载与外部同步共用。
type sourceExport struct {
	Title    string
	Card     provider.KnowledgeCard
	Segments []provider.Segment
	Markdown string
}

// exportError 携带应返回给浏览器的状态码与文案。
type exportError struct {
	Status int
	Msg    string
}

func (e *exportError) Error() string { return e.Msg }

// buildSourceExport 读取当前知识卡片与转录，渲染 Markdown。
func (srv *Server) buildSourceExport(ctx context.Context, sourceType models.SourceType, sourceID string, withGenerated bool) (*sourceExport, *exportError) {
	card, err := srv.store.GetCurrentVersion(ctx, sourceType, sourceID, store.KindKnowledgeCard)
	if err != nil {
		return nil, &exportError{http.StatusNotFound, "尚无知识卡片"}
	}
	tr, err := srv.store.GetCurrentVersion(ctx, sourceType, sourceID, store.KindTranscript)
	if err != nil {
		return nil, &exportError{http.StatusNotFound, "尚无转录稿"}
	}
	var cardData provider.KnowledgeCard
	if err := json.Unmarshal([]byte(card.Payload), &cardData); err != nil {
		return nil, &exportError{http.StatusInternalServerError, "卡片数据损坏"}
	}
	var tp provider.TranscriptPayload
	if err := json.Unmarshal([]byte(tr.Payload), &tp); err != nil {
		return nil, &exportError{http.StatusInternalServerError, "转录数据损坏"}
	}
	title := cardData.Title
	// ADR-0018 R4：Owner 可选下沉 GeneratedDerivative 块（Paraphrase + 手选 StudyChat 回答）。
	// 默认不包含（遵守"禁止自动写入 KnowledgeNote"）；通过 ?with_generated=1 显式开启。
	// 下沉的块一律标为 AI 讲解·非原文，挂 Reference（?ref=），与 CitedDerivative（?t=）区分。
	// K04：导出保留两类 Owner 笔记（来源笔记/个人理解）及引用、参考跳转。
	noteBlocks, _ := srv.ownerNoteBlocks(ctx, sourceType, sourceID, tp.Segments)
	var genBlocks []markdown.GeneratedBlock
	if withGenerated {
		// Paraphrase：Owner 明确触发的重讲，全部纳入（每锚点最近 3 条已由存储淘汰）。
		paras, _ := srv.store.ListParaphrasesForSource(ctx, sourceType, sourceID)
		for _, pr := range paras {
			genBlocks = append(genBlocks, markdown.GeneratedBlock{
				Kind: "Paraphrase", Body: pr.Body,
				References: parseSegmentIDs(pr.SegmentIDs),
			})
		}
		// StudyChat：仅纳入未被抑制的 assistant 回答（抑制的是 ReferenceCheck 失败，不应下沉）。
		sessions, _ := srv.store.ListStudySessions(ctx, sourceType, sourceID)
		for _, sess := range sessions {
			msgs, _ := srv.store.ListStudyMessages(ctx, sess.ID, false)
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
		OwnerNoteBlocks: noteBlocks,
	})
	if err != nil {
		return nil, &exportError{http.StatusInternalServerError, "渲染失败"}
	}
	return &sourceExport{Title: title, Card: cardData, Segments: tp.Segments, Markdown: md}, nil
}

func (srv *Server) handleDownloadMarkdown(w http.ResponseWriter, r *http.Request) {
	sourceType, sourceID, rest, ok := parseSourcePath(r)
	if !ok || len(rest) < 1 || rest[0] != "download" {
		http.NotFound(w, r)
		return
	}
	exp, eerr := srv.buildSourceExport(r.Context(), sourceType, sourceID, r.URL.Query().Get("with_generated") == "1")
	if eerr != nil {
		http.Error(w, eerr.Msg, eerr.Status)
		return
	}
	filename := sanitizeFilename(exp.Title) + ".md"
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+filename+"\"")
	_, _ = w.Write([]byte(exp.Markdown))
}

// ownerNoteBlocks 把两类 Owner 笔记转换为导出块（K04）。
func (srv *Server) ownerNoteBlocks(ctx context.Context, sourceType models.SourceType, sourceID string, segments []provider.Segment) ([]markdown.OwnerNoteBlock, []provider.Segment) {
	notes, err := srv.store.ListOwnerNotes(ctx, sourceType, sourceID)
	if err != nil {
		return nil, segments
	}
	blocks := make([]markdown.OwnerNoteBlock, 0, len(notes))
	for _, n := range notes {
		var citations, references []string
		_ = json.Unmarshal([]byte(n.CitationsJSON), &citations)
		_ = json.Unmarshal([]byte(n.ReferencesJSON), &references)
		var anchor models.NoteAnchor
		_ = json.Unmarshal([]byte(n.AnchorJSON), &anchor)
		anchorURL := ""
		if anchor.SnapshotID != "" {
			anchorURL = noteHref(n)
		}
		blocks = append(blocks, markdown.OwnerNoteBlock{
			AnchorURL: anchorURL, AnchorVersion: anchor.Version,
			Kind: n.Kind, Body: n.Content,
			Citations: citations, References: references, Revision: n.Revision,
		})
	}
	return blocks, segments
}
