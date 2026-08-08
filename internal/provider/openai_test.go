package provider

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// TestOpenAI_Analyze 验证 OpenAI Analyze 走 /responses 并解析 output_text。
func TestOpenAI_Analyze(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Errorf("路径应为 /responses，实际 %s", r.URL.Path)
		}
		// 验证请求体含 instructions
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["instructions"] == nil {
			t.Error("请求应含 instructions")
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"output_text":"{\"title\":\"通胀\",\"summary\":{\"text\":\"概览\",\"citations\":[\"seg-0001\"]},\"keyPoints\":[{\"content\":\"要点\",\"description\":\"d\",\"citations\":[\"seg-0001\"]}],\"chapters\":[],\"quotes\":[],\"tags\":[\"经济\"],\"suggestedQuestions\":[]}"}`))
	}))
	defer srv.Close()

	o := NewOpenAIProvider("key").WithBaseURL(srv.URL)
	card, err := o.Analyze("", []Segment{{ID: "seg-0001", Start: 0, End: 5, Text: "通胀"}})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if card.Title != "通胀" || len(card.KeyPoints) != 1 {
		t.Errorf("卡片解析错误: %+v", card)
	}
}

// TestOpenAI_Transcribe 验证 OpenAI 转录：multipart 上传 + 稳定 Segment ID。
func TestOpenAI_Transcribe(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/audio/transcriptions" {
			t.Errorf("路径应为 /audio/transcriptions，实际 %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"text":"hello world","language":"en","segments":[{"start":0,"end":3,"text":"hello"},{"start":3,"end":6,"text":"world"}]}`))
	}))
	defer srv.Close()

	o := NewOpenAIProvider("key").WithBaseURL(srv.URL)
	dir := t.TempDir()
	path := filepath.Join(dir, "a.mp3")
	if err := os.WriteFile(path, []byte("audio"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := o.Transcribe(path)
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if res.Text != "hello world" {
		t.Errorf("文本错误: %q", res.Text)
	}
	if len(res.Segments) != 2 || res.Segments[0].ID != "seg-0001" || res.Segments[1].ID != "seg-0002" {
		t.Errorf("应分配稳定 Segment ID，实际 %+v", res.Segments)
	}
}

// TestOpenAI_Getters 验证 Name/WithModel/WithBaseURL。
func TestOpenAI_Getters(t *testing.T) {
	o := NewOpenAIProvider("key")
	if o.Name() != "openai" {
		t.Errorf("Name() = %q", o.Name())
	}
	m := o.WithModel("gpt-4o")
	if m.analysisModel != "gpt-4o" {
		t.Errorf("WithModel 未设置 model: %q", m.analysisModel)
	}
	b := o.WithBaseURL("http://localhost:9999")
	if b.baseURL != "http://localhost:9999" {
		t.Errorf("WithBaseURL 未生效: %q", b.baseURL)
	}
}
