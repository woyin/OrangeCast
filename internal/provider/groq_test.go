package provider

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

// TestGroq_Transcribe_HTTPError 验证转录服务端返回非 200 时报错。
func TestGroq_Transcribe_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad", http.StatusBadRequest)
	}))
	defer srv.Close()

	g := NewGroqProvider("key").WithBaseURL(srv.URL)
	dir := t.TempDir()
	path := filepath.Join(dir, "audio.mp3")
	if err := os.WriteFile(path, []byte("fake-audio"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Transcribe(path); err == nil {
		t.Fatal("转录 HTTP 400 应报错")
	}
}

// TestGroq_Transcribe_BadJSON 验证转录响应 JSON 非法时报错。
func TestGroq_Transcribe_BadJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`not-json`))
	}))
	defer srv.Close()

	g := NewGroqProvider("key").WithBaseURL(srv.URL)
	dir := t.TempDir()
	path := filepath.Join(dir, "audio.mp3")
	if err := os.WriteFile(path, []byte("fake-audio"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Transcribe(path); err == nil {
		t.Fatal("非法 JSON 应报错")
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

// TestGroq_ChatComplete_SchemaModeHTTP 验证 Analyze 走真实 HTTP（schema 模式）。
// 覆盖 chatComplete 中 jsonMode == "schema" 分支。
func TestGroq_ChatComplete_SchemaModeHTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("路径应为 /chat/completions，实际 %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"choices":[{"message":{"content":"{\"title\":\"通胀\",\"summary\":{\"text\":\"概览\",\"citations\":[\"seg-0001\"]},\"keyPoints\":[{\"content\":\"要点\",\"citations\":[\"seg-0001\"]}],\"chapters\":[],\"quotes\":[]}"}}]}`))
	}))
	defer srv.Close()

	g := NewGroqProvider("key").WithBaseURL(srv.URL)
	card, err := g.Analyze("", []Segment{{ID: "seg-0001", Start: 0, End: 5, Text: "通胀是物价上升"}})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if card.Title != "通胀" {
		t.Errorf("卡片标题错误: %q", card.Title)
	}
}

// TestGroq_ChatComplete_HTTPError 验证 chatComplete HTTP 非 200 时报错。
// 覆盖 chatComplete 中 code != 200 分支（经 Analyze 触发）。
func TestGroq_ChatComplete_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad request", http.StatusBadRequest)
	}))
	defer srv.Close()

	g := NewGroqProvider("key").WithBaseURL(srv.URL)
	_, err := g.Analyze("", []Segment{{ID: "seg-0001", Start: 0, End: 5, Text: "通胀"}})
	if err == nil {
		t.Fatal("HTTP 400 应报错")
	}
	if !strings.Contains(err.Error(), "groq chat 失败 HTTP 400") {
		t.Errorf("错误应含 'groq chat 失败 HTTP 400'，实际 %v", err)
	}
}

// TestGroq_ChatComplete_ParseError 验证 chatComplete JSON 解析失败时报错。
// 覆盖 chatComplete 中 json.Unmarshal 失败分支。
func TestGroq_ChatComplete_ParseError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"choices":[{"message":{"content":"..."}}]`)) // 正常 choices
	}))
	defer srv.Close()

	g := NewGroqProvider("key").WithBaseURL(srv.URL)
	_, err := g.Analyze("", []Segment{{ID: "seg-0001", Start: 0, End: 5, Text: "通胀"}})
	// Analyze 的 analyzeWindow 在 parseJSONLoose 失败时报错
	if err == nil {
		t.Fatal("无效卡片 JSON 应报错")
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

// TestGroq_Answer 验证 Groq Answer：RAG 检索 + 引用映射。
func TestGroq_Answer(t *testing.T) {
	g := NewGroqProvider("key")
	g.chatCompleteFn = func(messages []map[string]string, jsonMode string) (string, int, error) {
		return `{"answer":"通胀是物价上升","cited":[0]}`, 200, nil
	}
	segs := []Segment{
		{ID: "seg-0001", Start: 0, End: 5, Text: "通胀是物价总水平持续上升"},
		{ID: "seg-0002", Start: 5, End: 10, Text: "主权财富基金改变投资格局"},
	}
	res, err := g.Answer("通胀是什么", segs)
	if err != nil {
		t.Fatalf("Answer: %v", err)
	}
	if res.Answer != "通胀是物价上升" {
		t.Errorf("答案错误: %q", res.Answer)
	}
	if len(res.Sources) == 0 {
		t.Error("应存在引用来源")
	}
}

// TestGroq_AnswerNoChunks 验证无相关片段时返回占位回答。
func TestGroq_AnswerNoChunks(t *testing.T) {
	g := NewGroqProvider("key")
	res, err := g.Answer("完全不相关的问题", nil)
	if err != nil {
		t.Fatalf("Answer: %v", err)
	}
	if res.Answer == "" {
		t.Error("应返回占位回答")
	}
}

// TestGroq_AnswerParseFallback 验证 Answer 输出非 JSON 时退化为直接展示原文。
// 覆盖 Answer 中 parseJSONLoose 失败 → return &QAResult{Answer: content} 分支。
func TestGroq_AnswerParseFallback(t *testing.T) {
	g := NewGroqProvider("key")
	g.chatCompleteFn = func(messages []map[string]string, jsonMode string) (string, int, error) {
		return "这不是 JSON 内容", 200, nil
	}
	segs := []Segment{{ID: "seg-0001", Start: 0, End: 5, Text: "通胀"}}
	res, err := g.Answer("通胀是什么", segs)
	if err != nil {
		t.Fatalf("Answer: %v", err)
	}
	if res.Answer != "这不是 JSON 内容" {
		t.Errorf("解析失败应退化为原文，实际 %q", res.Answer)
	}
}

// TestGroq_AnswerCompleteError 验证 Answer 的 complete 失败时报错。
// 覆盖 Answer 中 complete err → return nil, err 分支。
func TestGroq_AnswerCompleteError(t *testing.T) {
	g := NewGroqProvider("key")
	g.chatCompleteFn = func(messages []map[string]string, jsonMode string) (string, int, error) {
		return "", 0, errors.New("complete 失败")
	}
	_, err := g.Answer("通胀是什么", []Segment{{ID: "seg-0001", Start: 0, End: 5, Text: "通胀"}})
	if err == nil {
		t.Fatal("complete 失败应报错")
	}
}

// TestGroq_GenerateHighlightsCompleteError 验证 GenerateHighlights 的 complete 失败时报错。
// 覆盖 GenerateHighlights 中 complete err → return nil, err 分支。
func TestGroq_GenerateHighlightsCompleteError(t *testing.T) {
	g := NewGroqProvider("key")
	g.chatCompleteFn = func(messages []map[string]string, jsonMode string) (string, int, error) {
		return "", 0, errors.New("complete 失败")
	}
	_, err := g.GenerateHighlights([]Segment{{ID: "seg-0001", Start: 0, End: 5, Text: "通胀"}})
	if err == nil {
		t.Fatal("complete 失败应报错")
	}
}

// TestGroq_ParaphraseCompleteError 验证 Paraphrase 的 complete 失败时报错。
// 覆盖 Paraphrase 中 complete err → return nil, err 分支。
func TestGroq_ParaphraseCompleteError(t *testing.T) {
	g := NewGroqProvider("key")
	g.chatCompleteFn = func(messages []map[string]string, jsonMode string) (string, int, error) {
		return "", 0, errors.New("complete 失败")
	}
	_, err := g.Paraphrase("解释", []Segment{{ID: "seg-0001", Start: 0, End: 5, Text: "通胀"}})
	if err == nil {
		t.Fatal("complete 失败应报错")
	}
}

// TestGroq_GenerateHighlightsParseError 验证高光输出非 JSON 时报错。
// 覆盖 GenerateHighlights 中 "解析高光片段失败" 错误分支。
func TestGroq_GenerateHighlightsParseError(t *testing.T) {
	g := NewGroqProvider("key")
	g.chatCompleteFn = func(messages []map[string]string, jsonMode string) (string, int, error) {
		return "bad output", 200, nil
	}
	segs := []Segment{{ID: "seg-0001", Start: 0, End: 5, Text: "通胀"}}
	_, err := g.GenerateHighlights(segs)
	if err == nil {
		t.Fatal("高光输出非 JSON 应报错")
	}
	if !strings.Contains(err.Error(), "解析高光片段失败") {
		t.Errorf("错误应含 '解析高光片段失败'，实际 %v", err)
	}
}

// TestGroq_CheckReferenceNoSegments 验证无参考片段时返回"无参考片段"。
// 覆盖 CheckReference 中 len(referenceSegments)==0 分支。
func TestGroq_CheckReferenceNoSegments(t *testing.T) {
	g := NewGroqProvider("key")
	res, err := g.CheckReference("问题", "回答", nil)
	if err != nil {
		t.Fatalf("CheckReference: %v", err)
	}
	if res.Related || res.Reason != "无参考片段" {
		t.Errorf("无参考片段应返回 related=false+无参考片段，实际 %+v", res)
	}
}

// TestGroq_CheckReferenceParseFail 验证校验输出非 JSON 时保守拒绝。
// 覆盖 CheckReference 中 parseJSONLoose 失败分支。
func TestGroq_CheckReferenceParseFail(t *testing.T) {
	g := NewGroqProvider("key")
	g.chatCompleteFn = func(messages []map[string]string, jsonMode string) (string, int, error) {
		return "not json", 200, nil
	}
	segs := []Segment{{ID: "seg-0001", Start: 0, End: 5, Text: "通胀"}}
	res, err := g.CheckReference("问题", "回答", segs)
	if err != nil {
		t.Fatalf("CheckReference: %v", err)
	}
	if res.Related || res.Reason != "校验解析失败，保守拒绝" {
		t.Errorf("解析失败应保守拒绝，实际 %+v", res)
	}
}

// TestGroq_Paraphrase 验证 Paraphrase 返回重新讲解文本与参考片段 ID。
func TestGroq_Paraphrase(t *testing.T) {
	g := NewGroqProvider("key")
	g.chatCompleteFn = func(messages []map[string]string, jsonMode string) (string, int, error) {
		return "通胀就是货币购买力下降，用买房举例…", 200, nil
	}
	res, err := g.Paraphrase("解释通胀", []Segment{{ID: "seg-0001", Start: 0, End: 5, Text: "通胀是物价上升"}})
	if err != nil {
		t.Fatalf("Paraphrase: %v", err)
	}
	if res.Text == "" {
		t.Error("应返回讲解文本")
	}
	if len(res.ReferenceSegmentIDs) != 1 || res.ReferenceSegmentIDs[0] != "seg-0001" {
		t.Errorf("参考片段 ID 错误: %v", res.ReferenceSegmentIDs)
	}
}

// TestGroq_ParaphraseNoRef 验证无参考片段报错。
func TestGroq_ParaphraseNoRef(t *testing.T) {
	g := NewGroqProvider("key")
	if _, err := g.Paraphrase("q", nil); err == nil {
		t.Error("无参考片段应报错")
	}
}

// TestGroq_StudyChatAnswer 验证学习对话返回回答与参考片段。
func TestGroq_StudyChatAnswer(t *testing.T) {
	g := NewGroqProvider("key")
	g.chatCompleteFn = func(messages []map[string]string, jsonMode string) (string, int, error) {
		return `{"answer":"通胀就是货币购买力下降","referenceSegmentIds":["seg-0001"]}`, 200, nil
	}
	res, err := g.StudyChatAnswer("通胀是啥", nil, []Segment{{ID: "seg-0001", Start: 0, End: 5, Text: "通胀是物价上升"}})
	if err != nil {
		t.Fatalf("StudyChatAnswer: %v", err)
	}
	if res.Answer == nil || res.Answer.Content != "通胀就是货币购买力下降" {
		t.Errorf("回答错误: %+v", res.Answer)
	}
}

// TestGroq_StudyChatNoCandidates 验证无候选片段时返回 scope 反馈。
func TestGroq_StudyChatNoCandidates(t *testing.T) {
	g := NewGroqProvider("key")
	res, err := g.StudyChatAnswer("q", nil, nil)
	if err != nil {
		t.Fatalf("StudyChatAnswer: %v", err)
	}
	if res.ScopeFeedback == "" {
		t.Error("应返回 scope 反馈")
	}
}

// TestGroq_ChatComplete 验证 chatComplete 实际 HTTP 路径（/chat/completions）。
func TestGroq_ChatComplete(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("路径应为 /chat/completions，实际 %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"choices":[{"message":{"content":"{\"answer\":\"通胀是物价上升\"}"}}]}`))
	}))
	defer srv.Close()

	g := NewGroqProvider("key").WithBaseURL(srv.URL)
	content, code, err := g.chatComplete([]map[string]string{{"role": "user", "content": "hi"}}, "object")
	if err != nil {
		t.Fatalf("chatComplete: %v", err)
	}
	if code != http.StatusOK {
		t.Errorf("应 200，实际 %d", code)
	}
	if content == "" {
		t.Error("应返回 message content")
	}
}

// TestGroq_ChatCompleteHTTPError 验证非 200 返回错误（4xx 不触发重试）。
func TestGroq_ChatCompleteHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad request", http.StatusBadRequest)
	}))
	defer srv.Close()

	g := NewGroqProvider("key").WithBaseURL(srv.URL)
	if _, code, err := g.chatComplete(nil, "object"); err == nil {
		t.Fatalf("429 应报错，code=%d", code)
	} else if code != http.StatusBadRequest {
		t.Errorf("应返回 400，实际 %d", code)
	}
}

// TestGroq_ChatCompleteEmptyChoices 验证空 choices 报错。
func TestGroq_ChatCompleteEmptyChoices(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"choices":[]}`))
	}))
	defer srv.Close()

	g := NewGroqProvider("key").WithBaseURL(srv.URL)
	if _, _, err := g.chatComplete(nil, ""); err == nil {
		t.Fatal("空 choices 应报错")
	}
}

// TestGroqWaitBetweenAnalysisWindows 验证 sleepFn 注入时被调用（不实际 sleep）。
func TestGroqWaitBetweenAnalysisWindows(t *testing.T) {
	called := 0
	g := NewGroqProvider("key")
	g.sleepFn = func(d time.Duration) { called++ }
	g.waitBetweenAnalysisWindows()
	if called != 1 {
		t.Errorf("sleepFn 应被调用 1 次，实际 %d", called)
	}
}

// TestGroqComplete_RealHTTPPath 验证未注入 chatCompleteFn 时 complete 走真实 HTTP 路径。
func TestGroqComplete_RealHTTPPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"choices":[{"message":{"content":"真实回答"}}]}`))
	}))
	defer srv.Close()

	g := NewGroqProvider("key").WithBaseURL(srv.URL)
	content, code, err := g.complete([]map[string]string{{"role": "user", "content": "hi"}}, "")
	if err != nil {
		t.Fatalf("complete 真实路径: %v", err)
	}
	if code != http.StatusOK {
		t.Errorf("应 200，实际 %d", code)
	}
	if content != "真实回答" {
		t.Errorf("应返回消息内容，实际 %q", content)
	}
}

// TestGroq_AnalyzeEmptySegments 验证空 Segment 列表时报错。
// 覆盖 Analyze 中 "无法分析空转录稿" 分支。
func TestGroq_AnalyzeEmptySegments(t *testing.T) {
	g := NewGroqProvider("key")
	_, err := g.Analyze("", nil)
	if err == nil {
		t.Fatal("空 segments 应报错")
	}
	if !strings.Contains(err.Error(), "无法分析空转录稿") {
		t.Errorf("错误应含 '无法分析空转录稿'，实际 %v", err)
	}
}

// TestGroq_SplitAnalysisWindows_ZeroBudget 验证 charBudget<=0 时返回 nil。
// 覆盖 splitAnalysisWindows 中 charBudget <= 0 → return nil 分支。
func TestGroq_SplitAnalysisWindows_ZeroBudget(t *testing.T) {
	segs := []Segment{{ID: "s1", Start: 0, End: 5, Text: "x"}}
	if got := splitAnalysisWindows(segs, 0); got != nil {
		t.Errorf("charBudget<=0 应返回 nil，实际 %+v", got)
	}
}

// TestGroq_AnalyzeWindowParseError 验证 analyzeWindow 输出非 JSON 时报错。
// 覆盖 analyzeWindow 中 "解析 KnowledgeCard 失败" 分支。
func TestGroq_AnalyzeWindowParseError(t *testing.T) {
	g := NewGroqProvider("key")
	g.chatCompleteFn = func(messages []map[string]string, jsonMode string) (string, int, error) {
		return "not json", 200, nil
	}
	_, err := g.analyzeWindow([]Segment{{ID: "s1", Start: 0, End: 5, Text: "x"}})
	if err == nil {
		t.Fatal("非 JSON 输出应报错")
	}
	if !strings.Contains(err.Error(), "解析 KnowledgeCard 失败") {
		t.Errorf("错误应含 '解析 KnowledgeCard 失败'，实际 %v", err)
	}
}

// TestGroq_StudyChat_AssistantHistory 验证历史中含 assistant 消息时角色映射。
// 覆盖 StudyChatAnswer 中 m.Role == "assistant" → role = "assistant" 分支。
func TestGroq_StudyChat_AssistantHistory(t *testing.T) {
	g := NewGroqProvider("key")
	g.chatCompleteFn = func(messages []map[string]string, jsonMode string) (string, int, error) {
		return `{"answer":"回答","referenceSegmentIds":["seg-0001"]}`, 200, nil
	}
	history := []StudyChatMessage{
		{Role: "user", Content: "问题"},
		{Role: "assistant", Content: "回答"},
	}
	res, err := g.StudyChatAnswer("追问", history, []Segment{{ID: "seg-0001", Start: 0, End: 5, Text: "通胀"}})
	if err != nil {
		t.Fatalf("StudyChatAnswer: %v", err)
	}
	if res.Answer == nil {
		t.Error("应生成回答")
	}
}

// TestGroq_StudyChat_CompleteError 验证 complete 失败时报错。
// 覆盖 StudyChatAnswer 中 complete err → return nil, err 分支。
func TestGroq_StudyChat_CompleteError(t *testing.T) {
	g := NewGroqProvider("key")
	g.chatCompleteFn = func(messages []map[string]string, jsonMode string) (string, int, error) {
		return "", 0, errors.New("complete 失败")
	}
	_, err := g.StudyChatAnswer("通胀是啥", nil, []Segment{{ID: "seg-0001", Start: 0, End: 5, Text: "通胀"}})
	if err == nil {
		t.Fatal("complete 失败应报错")
	}
}

// TestGroq_StudyChat_ParseError 验证输出非 JSON 时报错。
// 覆盖 StudyChatAnswer 中 "解析 StudyChat 输出失败" 分支。
func TestGroq_StudyChat_ParseError(t *testing.T) {
	g := NewGroqProvider("key")
	g.chatCompleteFn = func(messages []map[string]string, jsonMode string) (string, int, error) {
		return "not json", 200, nil
	}
	_, err := g.StudyChatAnswer("通胀是啥", nil, []Segment{{ID: "seg-0001", Start: 0, End: 5, Text: "通胀"}})
	if err == nil {
		t.Fatal("非 JSON 输出应报错")
	}
	if !strings.Contains(err.Error(), "解析 StudyChat 输出失败") {
		t.Errorf("错误应含 '解析 StudyChat 输出失败'，实际 %v", err)
	}
}

// TestGroq_ChatComplete_PostJSONError 验证 chatComplete 的 postJSON 网络错误时报错。
// 覆盖 chatComplete 中 postJSON err → return "", code, err 分支。
func TestGroq_ChatComplete_PostJSONError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close() // 关闭服务器 → 连接失败

	g := NewGroqProvider("key").WithBaseURL(url)
	_, _, err := g.chatComplete([]map[string]string{{"role": "user", "content": "hi"}}, "object")
	if err == nil {
		t.Fatal("连接失败应报错")
	}
}

// TestGroq_CheckReference_CompleteError 验证 CheckReference 的 complete 失败时报错。
// 覆盖 CheckReference 中 complete err → return ReferenceCheckResult{}, err 分支。
func TestGroq_CheckReference_CompleteError(t *testing.T) {
	g := NewGroqProvider("key")
	g.chatCompleteFn = func(messages []map[string]string, jsonMode string) (string, int, error) {
		return "", 0, errors.New("complete 失败")
	}
	_, err := g.CheckReference("问题", "回答", []Segment{{ID: "seg-0001", Start: 0, End: 5, Text: "通胀"}})
	if err == nil {
		t.Fatal("complete 失败应报错")
	}
}

// TestGroq_ChatComplete_SchemaMode 验证 chatComplete 的 schema 模式分支。
// 覆盖 chatComplete 中 jsonMode == "schema" 分支（直接调用私有方法）。
func TestGroq_ChatComplete_SchemaMode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 验证请求带 response_format json_schema
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), "json_schema") {
			t.Errorf("schema 模式应带 json_schema response_format，实际 %s", string(body))
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer srv.Close()

	g := NewGroqProvider("key").WithBaseURL(srv.URL)
	content, _, err := g.chatComplete([]map[string]string{{"role": "user", "content": "hi"}}, "schema")
	if err != nil {
		t.Fatalf("chatComplete(schema): %v", err)
	}
	if content != "ok" {
		t.Errorf("content 应为 ok，实际 %q", content)
	}
}

// TestGroq_Transcribe_NetworkError 验证转录网络错误时报错。
// 覆盖 Transcribe 中 "groq 转录请求" 分支（连接失败）。
func TestGroq_Transcribe_NetworkError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close() // 连接失败

	g := NewGroqProvider("key").WithBaseURL(url)
	dir := t.TempDir()
	path := filepath.Join(dir, "audio.mp3")
	os.WriteFile(path, []byte("fake-audio"), 0o644)
	_, err := g.Transcribe(path)
	if err == nil {
		t.Fatal("连接失败应报错")
	}
}
