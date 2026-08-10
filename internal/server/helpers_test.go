package server

import (
	"context"
	"bytes"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

// TestSanitizeFilename 验证文件名清理：保留字母数字/中文/常见符号，替换分隔符，空串回退。
func TestSanitizeFilename(t *testing.T) {
	cases := map[string]string{
		"通胀与经济 2026.mp3":        "通胀与经济 2026.mp3",
		"a/b\\c:d*e?f\"g<h>i|j": "a-b-c-d-e-f-g-h-i-j",
		"   ":                   "cloudwisepod-note",
		"":                      "cloudwisepod-note",
		"Hello_World-1.2":       "Hello_World-1.2",
	}
	for in, want := range cases {
		if got := sanitizeFilename(in); got != want {
			t.Errorf("sanitizeFilename(%q)=%q want %q", in, got, want)
		}
	}
}

// TestTitleForStatus 验证各处理状态的占位标题。
func TestTitleForStatus(t *testing.T) {
	if got := titleForStatus(models.StatusFailedEp); got != "处理失败" {
		t.Errorf("failed 应=处理失败，实际 %q", got)
	}
	if got := titleForStatus(models.StatusProcessed); got != "" {
		t.Errorf("processed 应=空串，实际 %q", got)
	}
	if got := titleForStatus(models.StatusUnprocessed); got != "尚未处理" {
		t.Errorf("unprocessed 应=尚未处理，实际 %q", got)
	}
	if got := titleForStatus(models.StatusQueuedEp); got != "处理中…" {
		t.Errorf("queued 应=处理中…，实际 %q", got)
	}
}

// TestStageLabel 验证进度页各阶段标签（含未知回退）。
func TestStageLabel(t *testing.T) {
	cases := map[string]string{
		"transcribing": "正在转录音频",
		"analyzing":    "正在生成知识卡片",
		"queued":       "等待处理",
		"processed":    "处理完成",
		"failed":       "处理失败",
		"unknown":      "等待处理",
	}
	for in, want := range cases {
		if got := stageLabel(in); got != want {
			t.Errorf("stageLabel(%q)=%q want %q", in, got, want)
		}
	}
}

// TestIsAllowedAudio 验证音频类型判定：支持的扩展名（大小写不敏感）或 audio/* 类型。
func TestIsAllowedAudio(t *testing.T) {
	if !isAllowedAudio("a.mp3", "") {
		t.Error(".mp3 应允许")
	}
	if !isAllowedAudio("a.M4A", "") {
		t.Error(".M4A 大写应允许")
	}
	if !isAllowedAudio("a.wav", "") {
		t.Error(".wav 应允许")
	}
	if isAllowedAudio("a.pdf", "application/pdf") {
		t.Error(".pdf 应拒绝")
	}
	if !isAllowedAudio("noext", "audio/mpeg") {
		t.Error("audio/* contentType 应允许")
	}
	if isAllowedAudio("noext", "text/plain") {
		t.Error("非音频 contentType 应拒绝")
	}
}

// TestCardView 验证 KnowledgeCard 到模板结构的转换（含无 Citation 项被跳过）。
func TestCardView(t *testing.T) {
	segments := []provider.Segment{
		{ID: "s1", Start: 0, End: 5, Text: "a"},
		{ID: "s2", Start: 5, End: 10, Text: "b"},
	}
	card := provider.KnowledgeCard{
		Title:   "通胀",
		Summary: provider.CitedText{Text: "概览"},
		Chapters: []provider.Chapter{
			{Title: "第一章", Gist: "g", Citations: []string{"s1", "s2"}},
			{Title: "无引用章节", Gist: "x", Citations: nil}, // 无 Citation → 跳过
		},
		Quotes: []provider.Quote{
			{Text: "金句", Citations: []string{"s1"}},
			{Text: "无引用金句", Citations: nil}, // 跳过
		},
		KeyPoints: []provider.KeyPoint{
			{Content: "要点一", Description: "描述"},
		},
		Tags: []string{"经济"},
	}
	view := cardView(card, segments)
	if view["title"] != "通胀" || view["summary"] != "概览" {
		t.Errorf("title/summary 映射错误: %+v", view)
	}
	chapters := view["chapters"].([]map[string]any)
	if len(chapters) != 1 || chapters[0]["title"] != "第一章" {
		t.Errorf("应有 1 个章节（无引用被跳过），实际 %d", len(chapters))
	}
	if chapters[0]["startTime"] != float64(0) || chapters[0]["endTime"] != float64(10) {
		t.Errorf("章节时间范围解析错误: %+v", chapters[0])
	}
	quotes := view["quotes"].([]map[string]any)
	if len(quotes) != 1 || quotes[0]["text"] != "金句" {
		t.Errorf("应有 1 个金句（无引用被跳过），实际 %d", len(quotes))
	}
	keyPoints := view["keyPoints"].([]map[string]any)
	if len(keyPoints) != 1 || keyPoints[0]["content"] != "要点一" {
		t.Errorf("KeyPoint 映射错误: %+v", keyPoints)
	}
	if tags, ok := view["tags"].([]string); !ok || len(tags) != 1 || tags[0] != "经济" {
		t.Errorf("tags 映射错误: %+v", view["tags"])
	}
}

// TestStrPtr 验证 strPtr 与 ptrStr 互为逆操作：非空串转指针，空串返回 nil。
func TestStrPtr(t *testing.T) {
	if p := strPtr(""); p != nil {
		t.Errorf("空串应返回 nil，实际 %v", p)
	}
	p := strPtr("openai")
	if p == nil || *p != "openai" {
		t.Errorf("非空串应返回指针，实际 %v", p)
	}
	// 与 ptrStr 互逆
	if got := ptrStr(strPtr("gpt-4o")); got != "gpt-4o" {
		t.Errorf("ptrStr(strPtr(x)) 应还原 x，实际 %q", got)
	}
	if got := ptrStr(strPtr("")); got != "" {
		t.Errorf("ptrStr(strPtr(\"\")) 应为空串，实际 %q", got)
	}
}

// TestSaveUploadFile 验证 saveUploadFile 把 reader 内容写入 tempDir/uploads/<id>。
func TestSaveUploadFile(t *testing.T) {
	dir := t.TempDir()
	content := "fake-audio-bytes-fiction"
	if err := saveUploadFile(dir, "up-1", strings.NewReader(content)); err != nil {
		t.Fatalf("saveUploadFile: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "uploads", "up-1"))
	if err != nil {
		t.Fatalf("读取文件: %v", err)
	}
	if string(got) != content {
		t.Errorf("文件内容 = %q, want %q", got, content)
	}
}

// TestSaveUploadFile_CreateError 验证 uploads 目录不可创建时报错。
func TestSaveUploadFile_CreateError(t *testing.T) {
	dir := t.TempDir()
	// 用文件占用 uploads 目录路径 → MkdirAll 失败
	blk := filepath.Join(dir, "uploads")
	if err := os.WriteFile(blk, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := saveUploadFile(dir, "up-1", strings.NewReader("data")); err == nil {
		t.Fatal("uploads 目录不可创建应报错")
	}
}

// TestSaveUploadFile_OsCreateError 验证 os.Create 失败（目标路径为目录）时报错。
// 覆盖 saveUploadFile 中 os.Create 失败分支。
func TestSaveUploadFile_OsCreateError(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "uploads"), 0o755)
	// 用目录占用 uploads/up-1 路径 → os.Create 失败
	os.MkdirAll(filepath.Join(dir, "uploads", "up-1"), 0o755)
	if err := saveUploadFile(dir, "up-1", strings.NewReader("data")); err == nil {
		t.Fatal("目标路径为目录时 os.Create 应报错")
	}
}

// TestSaveUploadFile_WriteError 验证写入失败时报错。
// 覆盖 saveUploadFile 中 dst.Write 失败分支（预先创建只读目标文件）。
func TestSaveUploadFile_WriteError(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root 用户不受文件权限限制")
	}
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "uploads"), 0o755)
	// 预先创建只读目标文件 → os.Create 打开（写入）失败或写入失败
	target := filepath.Join(dir, "uploads", "up-1")
	os.WriteFile(target, []byte("x"), 0o444)
	if err := saveUploadFile(dir, "up-1", strings.NewReader("data")); err == nil {
		t.Fatal("目标文件不可写应报错")
	}
}

// TestSourceStatusAndError 验证 sourceStatusAndError：正常状态、失败状态取 last_error、未知 source。
func TestSourceStatusAndError(t *testing.T) {
	srv := newTestServer(t)
	ctx := context.Background()

	p, _ := srv.store.CreatePodcast(ctx, "https://f.xml", "Pod", "", "")
	srv.store.MergeEpisodes(ctx, p.ID, []models.Episode{{GUID: "g1", Title: "Ep", AudioURL: "https://a.mp3"}})
	eps, _ := srv.store.ListEpisodes(ctx, p.ID)
	sourceID := eps[0].ID

	// 未处理 → Unprocessed + 无错误
	status, errMsg := srv.sourceStatusAndError(ctx, models.SourceEpisode, sourceID)
	if status != models.StatusUnprocessed || errMsg != "" {
		t.Errorf("未处理应 Unprocessed+空，实际 %q %q", status, errMsg)
	}

	// 失败状态 → 取 last_error
	job, _ := srv.store.EnqueueJob(ctx, models.SourceEpisode, sourceID, models.JobTranscribe)
	srv.store.MarkJobRunning(ctx, job.ID)
	srv.store.MarkJobFailed(ctx, job.ID, "429 限流")
	srv.store.UpdateEpisodeStatus(ctx, sourceID, models.StatusFailedEp)

	status, errMsg = srv.sourceStatusAndError(ctx, models.SourceEpisode, sourceID)
	if status != models.StatusFailedEp || errMsg != "429 限流" {
		t.Errorf("失败应取 last_error，实际 %q %q", status, errMsg)
	}

	// 未知 source → Unprocessed
	status, _ = srv.sourceStatusAndError(ctx, models.SourceEpisode, "nonexistent")
	if status != models.StatusUnprocessed {
		t.Errorf("未知 source 应 Unprocessed，实际 %q", status)
	}

	// upload 未知 source → Unprocessed（覆盖 upload 分支 GetUploadByID 错误）
	status, _ = srv.sourceStatusAndError(ctx, models.SourceUpload, "nonexistent-upload")
	if status != models.StatusUnprocessed {
		t.Errorf("未知 upload source 应 Unprocessed，实际 %q", status)
	}
}

// TestParseSourcePath 验证 /sources/{type}/{id}[/rest] 路径解析。
func TestParseSourcePath(t *testing.T) {
	// 合法：/sources/episode/{id}/download
	req := httptest.NewRequest(http.MethodGet, "/sources/episode/ep-1/download", nil)
	st, id, rest, ok := parseSourcePath(req)
	if !ok || st != models.SourceEpisode || id != "ep-1" || len(rest) != 1 || rest[0] != "download" {
		t.Errorf("解析失败: type=%v id=%v rest=%v ok=%v", st, id, rest, ok)
	}

	// 合法：/sources/upload/{id}
	req = httptest.NewRequest(http.MethodGet, "/sources/upload/up-1", nil)
	st, id, rest, ok = parseSourcePath(req)
	if !ok || st != models.SourceUpload || id != "up-1" || len(rest) != 0 {
		t.Errorf("解析失败: type=%v id=%v rest=%v ok=%v", st, id, rest, ok)
	}

	// 非法：缺 id
	req = httptest.NewRequest(http.MethodGet, "/sources/episode", nil)
	if _, _, _, ok := parseSourcePath(req); ok {
		t.Error("缺 id 应解析失败")
	}
	// 非法：非 /sources/ 前缀
	req = httptest.NewRequest(http.MethodGet, "/uploads", nil)
	if _, _, _, ok := parseSourcePath(req); ok {
		t.Error("非 /sources/ 前缀应解析失败")
	}
}

// TestParseSegmentIDs 验证 segment_ids 解析：JSON 数组、分隔字符串、空串。
func TestParseSegmentIDs(t *testing.T) {
	// JSON 数组
	if got := parseSegmentIDs(`["seg-0001","seg-0002"]`); len(got) != 2 || got[0] != "seg-0001" {
		t.Errorf("JSON 数组解析错误: %v", got)
	}
	// 逗号分隔
	if got := parseSegmentIDs("seg-1,seg-2,seg-3"); len(got) != 3 || got[2] != "seg-3" {
		t.Errorf("逗号分隔解析错误: %v", got)
	}
	// 空格/换行分隔
	if got := parseSegmentIDs("seg-1 seg-2\nseg-3"); len(got) != 3 {
		t.Errorf("空白分隔解析错误: %v", got)
	}
	// 空串 → nil
	if got := parseSegmentIDs("   "); got != nil {
		t.Errorf("空串应返回 nil，实际 %v", got)
	}
}

// TestStaticFS 验证静态文件系统可读取已知静态资源。
// 覆盖 StaticFS 正常返回路径（registerStaticRoutes 依赖）。
func TestStaticFS(t *testing.T) {
	f, err := StaticFS()
	if err != nil {
		t.Fatalf("StaticFS: %v", err)
	}
	// 列出根目录，至少应存在可服务的文件
	entries, err := fs.ReadDir(f, ".")
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) == 0 {
		t.Error("静态文件系统应为非空")
	}
}

// TestRender_AllPages 验证所有页面模板可被渲染（不 panic）。
// 覆盖 NewTemplates 内每个页面 template set 的构建路径。
func TestRender_AllPages(t *testing.T) {
	tmpl, err := NewTemplates()
	if err != nil {
		t.Fatalf("NewTemplates: %v", err)
	}
	if len(tmpl.pages) == 0 {
		t.Fatal("应至少加载一个页面模板")
	}
	// 各页面用最小 data 渲染（部分字段缺失由模板零值兜底）
	for name := range tmpl.pages {
		var buf bytes.Buffer
		if err := tmpl.Render(&buf, name, map[string]any{}); err != nil {
			t.Errorf("渲染 %s 失败: %v", name, err)
		}
	}
}
