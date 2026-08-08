package provider

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// TestGroq_Transcribe 验证 Groq 转录：multipart 上传 + 稳定 Segment ID 分配。
func TestGroq_Transcribe(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/audio/transcriptions" {
			t.Errorf("路径应为 /audio/transcriptions，实际 %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"text":"主权财富基金改变全球投资","language":"zh","segments":[{"start":0,"end":5,"text":"主权财富基金改变全球投资"}]}`))
	}))
	defer srv.Close()

	g := NewGroqProvider("key").WithBaseURL(srv.URL)
	dir := t.TempDir()
	path := filepath.Join(dir, "audio.mp3")
	if err := os.WriteFile(path, []byte("fake-audio"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := g.Transcribe(path)
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if res.Text != "主权财富基金改变全球投资" {
		t.Errorf("文本错误: %q", res.Text)
	}
	if len(res.Segments) != 1 || res.Segments[0].ID != "seg-0001" {
		t.Errorf("应分配稳定 Segment ID seg-0001，实际 %+v", res.Segments)
	}
}

// TestGroq_Analyze 验证 Analyze 用假 chatCompleteFn 解析知识卡片。
func TestGroq_Analyze(t *testing.T) {
	g := NewGroqProvider("key")
	g.chatCompleteFn = func(messages []map[string]string, jsonMode string) (string, int, error) {
		return `{"title":"通胀","summary":{"text":"概览","citations":["seg-0001"]},"keyPoints":[{"content":"要点","description":"d","citations":["seg-0001"]}],"chapters":[],"quotes":[],"tags":["经济"],"suggestedQuestions":[]}`, 200, nil
	}
	card, err := g.Analyze("", []Segment{{ID: "seg-0001", Start: 0, End: 5, Text: "通胀是物价上升"}})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if card.Title != "通胀" || len(card.KeyPoints) != 1 {
		t.Errorf("卡片解析错误: %+v", card)
	}
}

// TestGroq_GenerateHighlights 验证高光生成解析 HighlightSet。
func TestGroq_GenerateHighlights(t *testing.T) {
	g := NewGroqProvider("key")
	g.chatCompleteFn = func(messages []map[string]string, jsonMode string) (string, int, error) {
		return `{"highlights":[{"gist":"最值得听","citations":["seg-0001"]}]}`, 200, nil
	}
	hs, err := g.GenerateHighlights([]Segment{{ID: "seg-0001", Start: 0, End: 5, Text: "通胀"}})
	if err != nil {
		t.Fatalf("GenerateHighlights: %v", err)
	}
	if len(hs.Highlights) != 1 || hs.Highlights[0].Gist != "最值得听" {
		t.Errorf("高光解析错误: %+v", hs)
	}
}

// TestGroq_Getters 验证 Name/WithModel/WithBaseURL。
func TestGroq_Getters(t *testing.T) {
	g := NewGroqProvider("key")
	if g.Name() != "groq" {
		t.Errorf("Name() = %q", g.Name())
	}
	m := g.WithModel("custom-model")
	if m.model != "custom-model" {
		t.Errorf("WithModel 未设置 model: %q", m.model)
	}
	b := g.WithBaseURL("http://localhost:9999")
	if b.base() != "http://localhost:9999" {
		t.Errorf("WithBaseURL 未生效: %q", b.base())
	}
	// 默认 base
	if g.base() != groqBaseURL {
		t.Errorf("默认 base 应 groqBaseURL，实际 %q", g.base())
	}
}

// TestGroq_EmptyTranscribe 验证空/无 segment 的高光生成报错。
func TestGroq_EmptyGenerateHighlights(t *testing.T) {
	g := NewGroqProvider("key")
	if _, err := g.GenerateHighlights(nil); err == nil {
		t.Error("空 segments 应报错")
	}
}
