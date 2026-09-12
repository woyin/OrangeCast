package server

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/woyin/orangecast/internal/models"
)

// TestDigestMarkdown_Download 验证 /digest/{id}/markdown 下载 Markdown 文件功能。
func TestDigestMarkdown_Download(t *testing.T) {
	srv, session, _, epID := seedDigestEpisode(t)
	ctx := t.Context()

	d, err := srv.store.CreateEpisodeDigest(ctx, &models.EpisodeDigest{
		SourceType: models.SourceEpisode,
		SourceID:   epID,
		Title:      "深度解读测试单集",
		Provider:   "fake",
		Model:      "m",
	}, []models.DigestBlock{
		{Type: models.DigestBlockParaphrase, Text: "这是本集转述内容。"},
		{Type: models.DigestBlockAIExpansion, Text: "这是 AI 展开的背景知识。"},
		{Type: models.DigestBlockNote, Text: "这是 Owner 添加的个人笔记。"},
		{Type: models.DigestBlockCitedFact, Text: "这是引用的客观事实。"},
	})
	if err != nil {
		t.Fatalf("创建精读文失败: %v", err)
	}

	// 1. 请求下载 Markdown
	rec := doWithCookie(srv, session, http.MethodGet, "/digest/"+d.ID+"/markdown")
	if rec.Code != http.StatusOK {
		t.Fatalf("下载 Markdown 应返回 200，实际: %d", rec.Code)
	}

	contentType := rec.Header().Get("Content-Type")
	if !strings.Contains(contentType, "text/markdown") {
		t.Errorf("Content-Type 应为 text/markdown，实际: %s", contentType)
	}

	expectedDisposition := fmt.Sprintf("attachment; filename=\"digest-%s.md\"", d.ID)
	if disposition := rec.Header().Get("Content-Disposition"); disposition != expectedDisposition {
		t.Errorf("Content-Disposition 期望 %q，实际: %q", expectedDisposition, disposition)
	}

	body := rec.Body.String()
	// 验证标题与来源信息
	if !strings.Contains(body, "# "+d.Title) {
		t.Errorf("正文应包含标题 # %s，实际正文:\n%s", d.Title, body)
	}
	if !strings.Contains(body, fmt.Sprintf("> 来源：%s/%s", d.SourceType, d.SourceID)) {
		t.Errorf("正文应包含来源元数据，实际正文:\n%s", body)
	}

	// 验证各种块的格式
	if !strings.Contains(body, "这是本集转述内容。") {
		t.Errorf("正文应包含本集转述内容")
	}
	if !strings.Contains(body, "> [!ai-generated] 这是 AI 展开的背景知识。") {
		t.Errorf("正文应包含 AI 展开 callout 标注: > [!ai-generated]")
	}
	if !strings.Contains(body, "> [!note] Owner 笔记\n> 这是 Owner 添加的个人笔记。") {
		t.Errorf("正文应包含 note callout 标注: > [!note] Owner 笔记")
	}
	if !strings.Contains(body, "这是引用的客观事实。") {
		t.Errorf("正文应包含引用的客观事实")
	}

	// 2. 404 测试：不存在的 digest ID
	rec404 := doWithCookie(srv, session, http.MethodGet, "/digest/nonexistent-id/markdown")
	if rec404.Code != http.StatusNotFound {
		t.Errorf("不存在的 digest ID 应返回 404，实际: %d", rec404.Code)
	}

	// 3. 404 测试：空 ID (/digest/markdown)
	recEmpty := doWithCookie(srv, session, http.MethodGet, "/digest/markdown")
	if recEmpty.Code != http.StatusNotFound {
		t.Errorf("空 digest ID 应返回 404，实际: %d", recEmpty.Code)
	}
}
