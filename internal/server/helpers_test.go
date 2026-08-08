package server

import (
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
