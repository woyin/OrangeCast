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

// TestOpenAI_GenerateHighlights 验证 OpenAI 高光解析。
func TestOpenAI_GenerateHighlights(t *testing.T) {
	srv := newOpenAITestServer(t, `{"highlights":[{"gist":"最值得听","citations":["seg-0001"]}]}`)
	defer srv.Close()
	o := NewOpenAIProvider("key").WithBaseURL(srv.URL)
	hs, err := o.GenerateHighlights([]Segment{{ID: "seg-0001", Start: 0, End: 5, Text: "通胀"}})
	if err != nil {
		t.Fatalf("GenerateHighlights: %v", err)
	}
	if len(hs.Highlights) != 1 || hs.Highlights[0].Gist != "最值得听" {
		t.Errorf("高光解析错误: %+v", hs)
	}
}

// TestOpenAI_Paraphrase 验证 OpenAI 复述讲解返回文本与参考片段。
func TestOpenAI_Paraphrase(t *testing.T) {
	srv := newOpenAITestServer(t, "通胀就是货币购买力下降")
	defer srv.Close()
	o := NewOpenAIProvider("key").WithBaseURL(srv.URL)
	res, err := o.Paraphrase("解释通胀", []Segment{{ID: "seg-0001", Start: 0, End: 5, Text: "通胀"}})
	if err != nil {
		t.Fatalf("Paraphrase: %v", err)
	}
	if res.Text == "" || len(res.ReferenceSegmentIDs) != 1 {
		t.Errorf("复述讲解错误: %+v", res)
	}
}

// TestOpenAI_StudyChatAnswer 验证 OpenAI 学习对话返回回答与参考片段。
func TestOpenAI_StudyChatAnswer(t *testing.T) {
	srv := newOpenAITestServer(t, `{"answer":"通胀就是货币购买力下降","referenceSegmentIds":["seg-0001"]}`)
	defer srv.Close()
	o := NewOpenAIProvider("key").WithBaseURL(srv.URL)
	res, err := o.StudyChatAnswer("通胀是啥", nil, []Segment{{ID: "seg-0001", Start: 0, End: 5, Text: "通胀是物价上升"}})
	if err != nil {
		t.Fatalf("StudyChatAnswer: %v", err)
	}
	if res.Answer == nil || res.Answer.Content != "通胀就是货币购买力下降" {
		t.Errorf("回答错误: %+v", res.Answer)
	}
}

// TestOpenAI_CheckReference 验证 OpenAI 主题锚定校验。
func TestOpenAI_CheckReference(t *testing.T) {
	srv := newOpenAITestServer(t, `{"related":true,"reason":"主题扎根"}`)
	defer srv.Close()
	o := NewOpenAIProvider("key").WithBaseURL(srv.URL)
	res, err := o.CheckReference("q", "a", []Segment{{ID: "seg-0001", Start: 0, End: 5, Text: "通胀"}})
	if err != nil {
		t.Fatalf("CheckReference: %v", err)
	}
	if !res.Related {
		t.Errorf("应判定相关，实际 %+v", res)
	}
}

// TestOpenAI_Answer 验证 OpenAI 问答返回答案。
func TestOpenAI_Answer(t *testing.T) {
	srv := newOpenAITestServer(t, `{"answer":"通胀是物价上升","cited":[0]}`)
	defer srv.Close()
	o := NewOpenAIProvider("key").WithBaseURL(srv.URL)
	res, err := o.Answer("通胀是啥", []Segment{{ID: "seg-0001", Start: 0, End: 5, Text: "通胀是物价上升"}})
	if err != nil {
		t.Fatalf("Answer: %v", err)
	}
	if res.Answer != "通胀是物价上升" {
		t.Errorf("答案错误: %q", res.Answer)
	}
}

// newOpenAITestServer 构造返回固定 output_text 的 /responses 测试服务器。
func newOpenAITestServer(t *testing.T, outputText string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Errorf("路径应为 /responses，实际 %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"output_text":` + mustJSON(outputText) + `}`))
	}))
}

// mustJSON 把原始 JSON 字符串包成 JSON 字符串字面量。
func mustJSON(raw string) string {
	b, _ := json.Marshal(raw)
	return string(b)
}
