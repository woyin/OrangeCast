// Package server 实现 CloudWisePod 的 HTTP 层：路由、handler、模板渲染与 REST API。
// handler 按职责拆分到多个文件：
//   - auth_handlers.go    认领/登录/登出/Dashboard
//   - podcasts.go         Podcast 订阅与 Upload 管理
//   - sources.go          Source 详情、音频、版本、AI DJ、搜索、进度、设置、处理、Purge
//   - ai_handlers.go      证据问答 / 复述讲解 / 学习对话
//   - knowledge.go        KeyPoint 全局视图、知识图谱、Annotation/Pin/Collection
//   - markdown_download.go KnowledgeNote Markdown 下载
//
// 本文件只保留跨职责共享的工具函数。
package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

// parseSegmentIDs 把存储里的 segment_ids JSON 数组字符串解析为 []string。
// parseSegmentIDs 把存储里的 segment_ids JSON 数组字符串、或逗号/空格/换行分隔的字符串，统一解析为 []string。
// 兼容两种来源：DB 列（JSON 数组）与表单输入（可能为分隔字符串）。
func parseSegmentIDs(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	if strings.HasPrefix(raw, "[") {
		var ids []string
		if err := json.Unmarshal([]byte(raw), &ids); err == nil {
			return ids
		}
	}
	parts := strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' })
	return parts
}

// sanitizeFilename 清理文件名中的不安全字符。
func sanitizeFilename(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '-', r == '_', r == '.', r == ' ', r >= 0x4E00 && r <= 0x9FFF:
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	out := strings.TrimSpace(b.String())
	if out == "" {
		return "cloudwisepod-note"
	}
	return out
}

// pageParam 解析 ?page= 查询参数，非法或缺省回退 1。
func pageParam(r *http.Request) int {
	if v := r.URL.Query().Get("page"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return 1
}

// totalPages 由总数与每页数量计算总页数（向上取整）。
func totalPages(total, perPage int) int {
	if perPage <= 0 {
		return 0
	}
	return (total + perPage - 1) / perPage
}

// parseSourcePath 从 /sources/{sourceType}/{sourceID}[/...] 路径解析出 sourceType 与 sourceID。
// 返回剩余路径段（如 ["download"]、["dj"]、["versions"]）。路径不合法时 ok=false。
func parseSourcePath(r *http.Request) (models.SourceType, string, []string, bool) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/sources/"), "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", "", nil, false
	}
	return models.SourceType(parts[0]), parts[1], parts[2:], true
}

// sourceStatusAndError 返回 source 的处理状态与最近一次失败原因。
func (srv *Server) sourceStatusAndError(ctx context.Context, sourceType models.SourceType, sourceID string) (models.EpisodeProcessingStatus, string) {
	var status models.EpisodeProcessingStatus
	if sourceType == models.SourceEpisode {
		ep, err := srv.store.GetEpisodeByID(ctx, sourceID)
		if err != nil {
			return models.StatusUnprocessed, ""
		}
		status = ep.ProcessingStatus
	} else {
		up, err := srv.store.GetUploadByID(ctx, sourceID)
		if err != nil {
			return models.StatusUnprocessed, ""
		}
		status = up.ProcessingStatus
	}
	// 失败时取最近一次失败 job 的错误
	var lastError string
	if status == models.StatusFailedEp {
		row := srv.store.DB.QueryRowContext(ctx,
			`SELECT COALESCE(last_error,'') FROM processing_jobs
			 WHERE source_type=? AND source_id=? AND status='failed'
			 ORDER BY updated_at DESC LIMIT 1`,
			string(sourceType), sourceID)
		_ = row.Scan(&lastError)
	}
	return status, lastError
}

// titleForStatus 处理中/失败时的占位标题。
func titleForStatus(status models.EpisodeProcessingStatus) string {
	switch status {
	case models.StatusFailedEp:
		return "处理失败"
	case models.StatusProcessed:
		return ""
	default:
		if status == models.StatusUnprocessed {
			return "尚未处理"
		}
		return "处理中…"
	}
}

// cardView 把验证过的 KnowledgeCard 转换成模板友好结构（时间范围由程序从 Citation 解析）。
func cardView(c provider.KnowledgeCard, segments []provider.Segment) map[string]any {
	chapterView := make([]map[string]any, 0, len(c.Chapters))
	for _, ch := range c.Chapters {
		start, end, ok := provider.ResolveCitationSpan(ch.Citations, segments)
		if !ok {
			continue
		}
		chapterView = append(chapterView, map[string]any{
			"title": ch.Title, "gist": ch.Gist, "startTime": start, "endTime": end,
		})
	}
	quoteView := make([]map[string]any, 0, len(c.Quotes))
	for _, q := range c.Quotes {
		start, end, ok := provider.ResolveCitationSpan(q.Citations, segments)
		if !ok {
			continue
		}
		quoteView = append(quoteView, map[string]any{"text": q.Text, "startTime": start, "endTime": end})
	}
	keyPointView := make([]map[string]any, 0, len(c.KeyPoints))
	for _, kp := range c.KeyPoints {
		keyPointView = append(keyPointView, map[string]any{"content": kp.Content, "description": kp.Description})
	}
	return map[string]any{
		"title":     c.Title,
		"summary":   c.Summary.Text,
		"chapters":  chapterView,
		"keyPoints": keyPointView,
		"quotes":    quoteView,
		"tags":      c.Tags,
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

// saveUploadFile 将上传的音频保存到临时目录，供 worker 后续读取。
// 约定路径：<tempDir>/uploads/<uploadID>
func saveUploadFile(tempDir, uploadID string, src interface{ Read([]byte) (int, error) }) error {
	dir := filepath.Join(tempDir, "uploads")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	dst, err := os.Create(filepath.Join(dir, uploadID))
	if err != nil {
		return err
	}
	defer dst.Close()
	buf := make([]byte, 32<<20) // 32MB buffer
	for {
		n, err := src.Read(buf)
		if n > 0 {
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return werr
			}
		}
		if err != nil {
			break
		}
	}
	return nil
}
