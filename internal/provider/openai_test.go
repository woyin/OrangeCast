package provider

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

// TestOpenAI_Transcribe_HTTPError 验证转录 HTTP 非 200 时报错。
// 覆盖 Transcribe 中 "openai 转录失败 HTTP" 分支。
func TestOpenAI_Transcribe_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad request", http.StatusBadRequest)
	}))
	defer srv.Close()

	o := NewOpenAIProvider("key").WithBaseURL(srv.URL)
	dir := t.TempDir()
	path := filepath.Join(dir, "a.mp3")
	os.WriteFile(path, []byte("audio"), 0o644)
	_, err := o.Transcribe(path)
	if err == nil {
		t.Fatal("HTTP 400 应报错")
	}
	if !strings.Contains(err.Error(), "转录失败 HTTP 400") {
		t.Errorf("错误应含 '转录失败 HTTP 400'，实际 %v", err)
	}
}

// TestOpenAI_Transcribe_NetworkError 验证转录网络错误时报错。
// 覆盖 Transcribe 中 "openai 转录请求" 分支（连接失败）。
func TestOpenAI_Transcribe_NetworkError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close()

	o := NewOpenAIProvider("key").WithBaseURL(url)
	dir := t.TempDir()
	path := filepath.Join(dir, "a.mp3")
	os.WriteFile(path, []byte("audio"), 0o644)
	if _, err := o.Transcribe(path); err == nil {
		t.Fatal("连接失败应报错")
	}
}

// TestOpenAI_Transcribe_OpenFileError 验证源文件不存在时报错。
// 覆盖 Transcribe 中 os.Open 失败分支。
func TestOpenAI_Transcribe_OpenFileError(t *testing.T) {
	o := NewOpenAIProvider("key")
	if _, err := o.Transcribe(filepath.Join(t.TempDir(), "missing.mp3")); err == nil {
		t.Fatal("文件不存在应报错")
	}
}

// TestOpenAI_Transcribe_ParseError 验证转录响应 JSON 解析失败时报错。
// 覆盖 Transcribe 中 "解析 openai 转录响应" 分支。
func TestOpenAI_Transcribe_ParseError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`not json`))
	}))
	defer srv.Close()

	o := NewOpenAIProvider("key").WithBaseURL(srv.URL)
	dir := t.TempDir()
	path := filepath.Join(dir, "a.mp3")
	os.WriteFile(path, []byte("audio"), 0o644)
	_, err := o.Transcribe(path)
	if err == nil {
		t.Fatal("非 JSON 响应应报错")
	}
	if !strings.Contains(err.Error(), "解析 openai 转录响应") {
		t.Errorf("错误应含 '解析 openai 转录响应'，实际 %v", err)
	}
}

// TestOpenAI_DoResponses_NetworkError 验证 doResponses 网络错误时报错。
// 覆盖 doResponses 中 "openai %s 请求" 分支（经 Answer 触发）。
func TestOpenAI_DoResponses_NetworkError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close()

	o := NewOpenAIProvider("key").WithBaseURL(url)
	_, err := o.Answer("问题", []Segment{{ID: "seg-0001", Start: 0, End: 5, Text: "通胀"}})
	if err == nil {
		t.Fatal("连接失败应报错")
	}
	if !strings.Contains(err.Error(), "openai QA 请求") {
		t.Errorf("错误应含 'openai QA 请求'，实际 %v", err)
	}
}

// TestOpenAI_DoResponses_HTTPError 验证 doResponses 非 200 时报错。
// 覆盖 doResponses 中 "openai %s 失败 HTTP" 分支（经 Answer 触发）。
func TestOpenAI_DoResponses_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "server error", http.StatusInternalServerError)
	}))
	defer srv.Close()

	o := NewOpenAIProvider("key").WithBaseURL(srv.URL)
	_, err := o.Answer("问题", []Segment{{ID: "seg-0001", Start: 0, End: 5, Text: "通胀"}})
	if err == nil {
		t.Fatal("HTTP 500 应报错")
	}
	if !strings.Contains(err.Error(), "openai QA 失败 HTTP 500") {
		t.Errorf("错误应含 'openai QA 失败 HTTP 500'，实际 %v", err)
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

// TestOpenAI_StudyChat_HistoryTruncation 验证超过 6 轮的对话历史被折叠进输入。
func TestOpenAI_StudyChat_HistoryTruncation(t *testing.T) {
	srv := newOpenAITestServer(t, `{"answer":"最近回答","referenceSegmentIds":["seg-0001"]}`)
	defer srv.Close()
	o := NewOpenAIProvider("key").WithBaseURL(srv.URL)
	history := make([]StudyChatMessage, 8)
	for i := range history {
		history[i] = StudyChatMessage{Role: "user", Content: "历史消息"}
	}
	res, err := o.StudyChatAnswer("解释通胀", history, []Segment{{ID: "seg-0001", Start: 0, End: 5, Text: "x"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Answer == nil || res.Answer.Content != "最近回答" {
		t.Errorf("应基于最近 6 轮历史生成回答，实际 %+v", res.Answer)
	}
}

// TestOpenAI_StudyChat_NoCandidates 验证无候选片段时返回范围外反馈。
func TestOpenAI_StudyChat_NoCandidates(t *testing.T) {
	o := NewOpenAIProvider("key")
	res, err := o.StudyChatAnswer("通胀是啥", nil, nil)
	if err != nil {
		t.Fatalf("StudyChatAnswer: %v", err)
	}
	if res.Answer != nil || res.ScopeFeedback == "" {
		t.Errorf("应返回范围外反馈，实际 %+v", res)
	}
}

// TestOpenAI_StudyChat_AssistantHistory 验证历史中含 assistant 消息时 role 映射。
// 覆盖 StudyChatAnswer 中 m.Role == "assistant" → role = "助手" 分支。
func TestOpenAI_StudyChat_AssistantHistory(t *testing.T) {
	srv := newOpenAITestServer(t, `{"answer":"回答","referenceSegmentIds":["seg-0001"]}`)
	defer srv.Close()
	o := NewOpenAIProvider("key").WithBaseURL(srv.URL)
	history := []StudyChatMessage{
		{Role: "user", Content: "问题"},
		{Role: "assistant", Content: "回答"},
	}
	res, err := o.StudyChatAnswer("追问", history, []Segment{{ID: "seg-0001", Start: 0, End: 5, Text: "通胀"}})
	if err != nil {
		t.Fatalf("StudyChatAnswer: %v", err)
	}
	if res.Answer == nil {
		t.Error("应生成回答")
	}
}

// TestOpenAI_CheckReference_DoResponsesError 验证校验 doResponses 失败时报错。
// 覆盖 CheckReference 中 doResponses err → return ReferenceCheckResult{}, err 分支。
func TestOpenAI_CheckReference_DoResponsesError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "error", http.StatusInternalServerError)
	}))
	defer srv.Close()
	o := NewOpenAIProvider("key").WithBaseURL(srv.URL)
	_, err := o.CheckReference("问题", "回答", []Segment{{ID: "seg-0001", Start: 0, End: 5, Text: "通胀"}})
	if err == nil {
		t.Fatal("HTTP 500 应报错")
	}
}

// TestOpenAI_CheckReference_JSONUnmarshalError 验证响应体非法 JSON 时保守拒绝。
// 覆盖 CheckReference 中 json.Unmarshal(data) 失败 → 保守拒绝分支。
func TestOpenAI_CheckReference_JSONUnmarshalError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`不是 JSON`)) // 非法 JSON 响应体
	}))
	defer srv.Close()
	o := NewOpenAIProvider("key").WithBaseURL(srv.URL)
	res, err := o.CheckReference("问题", "回答", []Segment{{ID: "seg-0001", Start: 0, End: 5, Text: "通胀"}})
	if err != nil {
		t.Fatalf("CheckReference: %v", err)
	}
	if res.Related || res.Reason != "校验解析失败，保守拒绝" {
		t.Errorf("响应解析失败应保守拒绝，实际 %+v", res)
	}
}

// TestOpenAI_StudyChat_EmptyReferenceIDs 验证返回空参考片段时给出范围外反馈（不请求服务器）。
func TestOpenAI_StudyChat_EmptyReferenceIDs(t *testing.T) {
	srv := newOpenAITestServer(t, `{"answer":"","referenceSegmentIds":[]}`)
	defer srv.Close()
	o := NewOpenAIProvider("key").WithBaseURL(srv.URL)
	res, err := o.StudyChatAnswer("通胀是啥", nil, []Segment{{ID: "seg-0001", Start: 0, End: 5, Text: "通胀是物价上升"}})
	if err != nil {
		t.Fatalf("StudyChatAnswer: %v", err)
	}
	if res.Answer != nil {
		t.Errorf("空参考应返回范围外反馈，实际 %+v", res)
	}
}

// TestOpenAI_StudyChat_ParseFail 验证输出非 JSON 时报错。
// 覆盖 StudyChatAnswer 中 "解析 StudyChat 输出失败" 错误分支。
func TestOpenAI_StudyChat_ParseFail(t *testing.T) {
	srv := newOpenAITestServer(t, `不是 JSON`)
	defer srv.Close()
	o := NewOpenAIProvider("key").WithBaseURL(srv.URL)
	_, err := o.StudyChatAnswer("通胀是啥", nil, []Segment{{ID: "seg-0001", Start: 0, End: 5, Text: "通胀是物价上升"}})
	if err == nil {
		t.Fatal("非 JSON 输出应报错")
	}
	if !strings.Contains(err.Error(), "解析 StudyChat 输出失败") {
		t.Errorf("错误应含 '解析 StudyChat 输出失败'，实际 %v", err)
	}
}

// TestOpenAI_CheckReference_ParseFail 验证校验输出非 JSON 时保守拒绝。
// 覆盖 CheckReference 中 parseJSONLoose 失败分支。
func TestOpenAI_CheckReference_ParseFail(t *testing.T) {
	srv := newOpenAITestServer(t, `不是 JSON`)
	defer srv.Close()
	o := NewOpenAIProvider("key").WithBaseURL(srv.URL)
	res, err := o.CheckReference("问题", "回答", []Segment{{ID: "seg-0001", Start: 0, End: 5, Text: "文本"}})
	if err != nil {
		t.Fatalf("CheckReference: %v", err)
	}
	if res.Related || res.Reason != "校验解析失败，保守拒绝" {
		t.Errorf("解析失败应保守拒绝，实际 %+v", res)
	}
}

// TestOpenAI_CheckReference_NoSegments 验证无参考片段时判定不相关。
func TestOpenAI_CheckReference_NoSegments(t *testing.T) {
	o := NewOpenAIProvider("key")
	res, err := o.CheckReference("q", "a", nil)
	if err != nil {
		t.Fatalf("CheckReference: %v", err)
	}
	if res.Related {
		t.Errorf("无参考片段应判定不相关，实际 %+v", res)
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

// TestOpenAI_Answer_NoValidCited 验证 cited 全无效时兜底第一个 chunk。
// 覆盖 Answer 中 len(result.Sources)==0 → 追加 chunks[0] 分支。
func TestOpenAI_Answer_NoValidCited(t *testing.T) {
	srv := newOpenAITestServer(t, `{"answer":"回答","cited":[99]}`) // cited 越界
	defer srv.Close()
	o := NewOpenAIProvider("key").WithBaseURL(srv.URL)
	res, err := o.Answer("通胀是啥", []Segment{{ID: "seg-0001", Start: 0, End: 5, Text: "通胀是物价上升"}})
	if err != nil {
		t.Fatalf("Answer: %v", err)
	}
	if len(res.Sources) == 0 {
		t.Fatal("cited 无效时应兜底第一个 chunk")
	}
	if res.Sources[0].Content != "通胀是物价上升" {
		t.Errorf("兜底来源应为第一个 chunk，实际 %q", res.Sources[0].Content)
	}
}

// TestOpenAI_Analyze_ParseError 验证 Analyze 输出非 JSON 时报错。
// 覆盖 Analyze 中 "解析 openai KnowledgeCard" 分支。
func TestOpenAI_Analyze_ParseError(t *testing.T) {
	srv := newOpenAITestServer(t, `不是 JSON`)
	defer srv.Close()
	o := NewOpenAIProvider("key").WithBaseURL(srv.URL)
	_, err := o.Analyze("", []Segment{{ID: "seg-0001", Start: 0, End: 5, Text: "通胀"}})
	if err == nil {
		t.Fatal("非 JSON 输出应报错")
	}
	if !strings.Contains(err.Error(), "解析 openai KnowledgeCard") {
		t.Errorf("错误应含 '解析 openai KnowledgeCard'，实际 %v", err)
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

// TestOpenAI_GenerateHighlights_EmptySegments 验证空 Segment 列表直接报错（不经 HTTP）。
// 覆盖 GenerateHighlights 中 "无 Segment 可供生成高光" 提前返回分支。
func TestOpenAI_GenerateHighlights_EmptySegments(t *testing.T) {
	o := NewOpenAIProvider("key")
	_, err := o.GenerateHighlights(nil)
	if err == nil {
		t.Fatal("空 Segment 列表应报错")
	}
	if !strings.Contains(err.Error(), "无 Segment") {
		t.Errorf("错误应含 '无 Segment'，实际 %v", err)
	}
}

// TestOpenAI_Paraphrase_NoReference 验证无参考片段时直接报错（不经 HTTP）。
// 覆盖 Paraphrase 中 "复述讲解至少需要一个参考片段" 提前返回分支。
func TestOpenAI_Paraphrase_NoReference(t *testing.T) {
	o := NewOpenAIProvider("key")
	_, err := o.Paraphrase("问题", nil)
	if err == nil {
		t.Fatal("无参考片段应报错")
	}
	if !strings.Contains(err.Error(), "至少需要一个参考片段") {
		t.Errorf("错误应含 '至少需要一个参考片段'，实际 %v", err)
	}
}

// TestOpenAI_Answer_NoSegments 验证无可用片段时 Answer 返回拒答（不经 HTTP）。
// 覆盖 Answer 中 "暂无转录稿可用于回答。" 提前返回分支。
func TestOpenAI_Answer_NoSegments(t *testing.T) {
	o := NewOpenAIProvider("key")
	res, err := o.Answer("任意问题", nil)
	if err != nil {
		t.Fatalf("无片段不应返回 error，实际 %v", err)
	}
	if !strings.Contains(res.Answer, "暂无转录稿") {
		t.Errorf("应返回拒答文本，实际 %q", res.Answer)
	}
}

// TestOpenAI_GenerateHighlights_ParseError 验证高光输出非 JSON 时报错。
// 覆盖 GenerateHighlights 中 "解析 openai 高光" 分支。
func TestOpenAI_GenerateHighlights_ParseError(t *testing.T) {
	srv := newOpenAITestServer(t, `不是 JSON`)
	defer srv.Close()
	o := NewOpenAIProvider("key").WithBaseURL(srv.URL)
	_, err := o.GenerateHighlights([]Segment{{ID: "seg-0001", Start: 0, End: 5, Text: "通胀"}})
	if err == nil {
		t.Fatal("非 JSON 输出应报错")
	}
	if !strings.Contains(err.Error(), "解析 openai 高光") {
		t.Errorf("错误应含 '解析 openai 高光'，实际 %v", err)
	}
}

// TestOpenAI_Paraphrase_DoResponsesError 验证 Paraphrase 的 doResponses 失败时报错。
// 覆盖 Paraphrase 中 doResponses err → return nil, err 分支。
func TestOpenAI_Paraphrase_DoResponsesError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "error", http.StatusInternalServerError)
	}))
	defer srv.Close()
	o := NewOpenAIProvider("key").WithBaseURL(srv.URL)
	_, err := o.Paraphrase("解释", []Segment{{ID: "seg-0001", Start: 0, End: 5, Text: "通胀"}})
	if err == nil {
		t.Fatal("HTTP 500 应报错")
	}
}

// TestOpenAI_Paraphrase_ParseError 验证 Paraphrase 响应 JSON 解析失败时报错。
// 覆盖 Paraphrase 中 "解析 openai 复述讲解响应" 分支。
func TestOpenAI_Paraphrase_ParseError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`不是 JSON`)) // 非法 JSON 响应体
	}))
	defer srv.Close()
	o := NewOpenAIProvider("key").WithBaseURL(srv.URL)
	_, err := o.Paraphrase("解释", []Segment{{ID: "seg-0001", Start: 0, End: 5, Text: "通胀"}})
	if err == nil {
		t.Fatal("非 JSON 响应应报错")
	}
	if !strings.Contains(err.Error(), "解析 openai 复述讲解响应") {
		t.Errorf("错误应含 '解析 openai 复述讲解响应'，实际 %v", err)
	}
}

// TestOpenAI_StudyChat_DoResponsesError 验证 StudyChatAnswer 的 doResponses 失败时报错。
// 覆盖 StudyChatAnswer 中 doResponses err → return nil, err 分支。
func TestOpenAI_StudyChat_DoResponsesError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "error", http.StatusInternalServerError)
	}))
	defer srv.Close()
	o := NewOpenAIProvider("key").WithBaseURL(srv.URL)
	_, err := o.StudyChatAnswer("通胀是啥", nil, []Segment{{ID: "seg-0001", Start: 0, End: 5, Text: "通胀"}})
	if err == nil {
		t.Fatal("HTTP 500 应报错")
	}
}

// TestOpenAI_StudyChat_ResponseUnmarshalError 验证响应体非法 JSON 时报错。
// 覆盖 StudyChatAnswer 中 json.Unmarshal(data) 失败 → "解析 openai 学习对话响应" 分支。
func TestOpenAI_StudyChat_ResponseUnmarshalError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`不是 JSON`))
	}))
	defer srv.Close()
	o := NewOpenAIProvider("key").WithBaseURL(srv.URL)
	_, err := o.StudyChatAnswer("通胀是啥", nil, []Segment{{ID: "seg-0001", Start: 0, End: 5, Text: "通胀"}})
	if err == nil {
		t.Fatal("非 JSON 响应应报错")
	}
	if !strings.Contains(err.Error(), "解析 openai 学习对话响应") {
		t.Errorf("错误应含 '解析 openai 学习对话响应'，实际 %v", err)
	}
}

// TestOpenAI_StudyChat_InvalidReferenceIDs 验证返回的参考片段全部无效时范围外反馈。
// 覆盖 StudyChatAnswer 中 validIDs 为空 → 范围外反馈分支。
func TestOpenAI_StudyChat_InvalidReferenceIDs(t *testing.T) {
	srv := newOpenAITestServer(t, `{"answer":"回答","referenceSegmentIds":["seg-9999"]}`) // 无效 ID
	defer srv.Close()
	o := NewOpenAIProvider("key").WithBaseURL(srv.URL)
	res, err := o.StudyChatAnswer("通胀是啥", nil, []Segment{{ID: "seg-0001", Start: 0, End: 5, Text: "通胀"}})
	if err != nil {
		t.Fatalf("StudyChatAnswer: %v", err)
	}
	if res.Answer != nil || res.ScopeFeedback == "" {
		t.Errorf("参考片段无效应返回范围外反馈，实际 %+v", res)
	}
}
