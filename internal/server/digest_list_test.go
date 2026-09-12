package server

import (
	"net/http"
	"strings"
	"testing"

	"github.com/woyin/orangecast/internal/models"
)

// TestDigestList_Renders 验证 /digests 列表页正常渲染（空列表、已有精读文、卡片跳转及 404 处理）。
func TestDigestList_Renders(t *testing.T) {
	srv, session, _, epID := seedDigestEpisode(t)
	ctx := t.Context()

	// 1. 空列表渲染
	rec := doWithCookie(srv, session, http.MethodGet, "/digests")
	if rec.Code != http.StatusOK {
		t.Fatalf("空精读文列表页应返回 200，实际: %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "单集精读文") || !strings.Contains(body, "暂无已生成的精读文") {
		t.Fatalf("空列表渲染内容不符: %s", body)
	}

	// 2. 创建精读文后渲染列表
	d, err := srv.store.CreateEpisodeDigest(ctx, &models.EpisodeDigest{
		SourceType:    models.SourceEpisode,
		SourceID:      epID,
		Title:         "深度解读科技前沿",
		Provider:      "openai",
		Model:         "gpt-4o",
		PromptVersion: "v1",
		Degraded:      false,
	}, []models.DigestBlock{
		{Type: models.DigestBlockParaphrase, Text: "本期核心内容讲解。"},
	})
	if err != nil {
		t.Fatalf("创建精读文失败: %v", err)
	}

	rec = doWithCookie(srv, session, http.MethodGet, "/digests")
	if rec.Code != http.StatusOK {
		t.Fatalf("精读文列表页应返回 200，实际: %d", rec.Code)
	}
	body = rec.Body.String()
	for _, want := range []string{
		"深度解读科技前沿",
		"/digest/" + d.ID,
		"查看草稿",
		"版本 v1",
		"openai",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("精读文列表缺少预期内容 %q", want)
		}
	}

	// 3. 非 /digests 子路径应 404
	rec = doWithCookie(srv, session, http.MethodGet, "/digests/invalid")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("子路径应返回 404，实际: %d", rec.Code)
	}

	// 4. 验证仪表盘页面包含单集精读入口卡片
	rec = doWithCookie(srv, session, http.MethodGet, "/dashboard")
	if rec.Code != http.StatusOK {
		t.Fatalf("仪表盘页应返回 200，实际: %d", rec.Code)
	}
	dashBody := rec.Body.String()
	if !strings.Contains(dashBody, `href="/digests"`) || !strings.Contains(dashBody, "单集精读") {
		t.Fatalf("仪表盘页缺少单集精读卡片: %s", dashBody)
	}
}
