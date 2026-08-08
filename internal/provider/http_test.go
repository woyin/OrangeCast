package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseJSONLoose_PlainJSON(t *testing.T) {
	raw := `{"title":"T","summary":{"text":"S","citations":["seg-0001"]},"keyPoints":[],"chapters":[],"quotes":[],"tags":[],"suggestedQuestions":[]}`
	var c KnowledgeCard
	if err := parseJSONLoose(raw, &c); err != nil {
		t.Fatal(err)
	}
	if c.Title != "T" {
		t.Errorf("title=%q want T", c.Title)
	}
	if c.Summary.Text != "S" || len(c.Summary.Citations) != 1 {
		t.Errorf("summary 解析错误: %+v", c.Summary)
	}
}

func TestParseJSONLoose_StripsCodeBlock(t *testing.T) {
	// Groq 常见的 markdown 代码块包裹（第 10 题：容错剥离脏标记）
	raw := "```json\n{\"title\":\"X\",\"summary\":{\"text\":\"\",\"citations\":[]},\"keyPoints\":[],\"chapters\":[],\"quotes\":[],\"tags\":[],\"suggestedQuestions\":[]}\n```"
	var c KnowledgeCard
	if err := parseJSONLoose(raw, &c); err != nil {
		t.Fatalf("应剥离代码块标记: %v", err)
	}
	if c.Title != "X" {
		t.Errorf("title=%q want X", c.Title)
	}
}

func TestParseJSONLoose_StripsSurroundingNoise(t *testing.T) {
	// LLM 偶尔在 JSON 前后加解释文本
	raw := `这是知识卡片：{"title":"Y","summary":{"text":"","citations":[]},"keyPoints":[],"chapters":[],"quotes":[],"tags":[],"suggestedQuestions":[]} 希望对你有帮助`
	var c KnowledgeCard
	if err := parseJSONLoose(raw, &c); err != nil {
		t.Fatalf("应剥离前后噪声: %v", err)
	}
	if c.Title != "Y" {
		t.Errorf("title=%q want Y", c.Title)
	}
}

func TestParseJSONLoose_IgnoresUnknownField(t *testing.T) {
	// json_object 模式不强制 schema，LLM 可能输出额外字段，应忽略而非拒绝（容错）
	raw := `{"title":"Z","summary":{"text":"","citations":[]},"keyPoints":[],"chapters":[],"quotes":[],"tags":[],"suggestedQuestions":[],"bogusField":1}`
	var c KnowledgeCard
	if err := parseJSONLoose(raw, &c); err != nil {
		t.Errorf("应忽略未知字段，却报错: %v", err)
	}
	if c.Title != "Z" {
		t.Errorf("title=%q want Z", c.Title)
	}
}

func TestParseJSONLoose_RejectsMalformed(t *testing.T) {
	raw := `not json at all`
	var c KnowledgeCard
	if err := parseJSONLoose(raw, &c); err == nil {
		t.Error("非 JSON 应解析失败")
	}
}

func TestGroqAnalyze_SplitsWindowsAndMergesCitations(t *testing.T) {
	segments := []Segment{
		{ID: "seg-0001", Text: strings.Repeat("a", 13000)},
		{ID: "seg-0002", Text: strings.Repeat("b", 13000)},
	}
	calls := 0
	g := NewGroqProvider("test")
	waits := 0
	g.sleepFn = func(time.Duration) { waits++ }
	g.chatCompleteFn = func(_ []map[string]string, _ string) (string, int, error) {
		calls++
		if calls == 1 {
			return `{"title":"First","summary":{"text":"summary one","citations":["seg-0001"]},"keyPoints":[],"chapters":[],"quotes":[],"tags":["python"],"suggestedQuestions":[]}`, 200, nil
		}
		return `{"title":"Second","summary":{"text":"summary two","citations":["seg-0002"]},"keyPoints":[],"chapters":[],"quotes":[],"tags":["python","ai"],"suggestedQuestions":[]}`, 200, nil
	}
	card, err := g.Analyze("ignored", segments)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("expected 2 analysis windows, got %d", calls)
	}
	if waits != 1 {
		t.Fatalf("analysis windows should be rate paced, got %d waits", waits)
	}
	if card.Title != "First" || card.Summary.Text != "summary one\n\nsummary two" {
		t.Errorf("merged card mismatch: %+v", card)
	}
	if len(card.Summary.Citations) != 2 || card.Summary.Citations[0] != "seg-0001" || card.Summary.Citations[1] != "seg-0002" {
		t.Errorf("summary citations must preserve both windows: %+v", card.Summary.Citations)
	}
	if len(card.Tags) != 2 || card.Tags[0] != "python" || card.Tags[1] != "ai" {
		t.Errorf("tags should be stable and deduplicated: %+v", card.Tags)
	}
}

func TestSplitAnalysisWindows_DoesNotSplitSegment(t *testing.T) {
	segments := []Segment{{ID: "a", Text: "12345"}, {ID: "b", Text: "67890"}}
	windows := splitAnalysisWindows(segments, 10)
	if len(windows) != 2 || len(windows[0]) != 1 || windows[0][0].ID != "a" || windows[1][0].ID != "b" {
		t.Fatalf("segments must remain whole across windows: %+v", windows)
	}
}

// TestDoWithRetry_Success 验证 200 成功立即返回，不重试。
func TestDoWithRetry_Success(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	resp, err := doWithRetry(context.Background(), req)
	if err != nil {
		t.Fatalf("成功请求不应报错: %v", err)
	}
	defer resp.Body.Close()
	if calls != 1 {
		t.Errorf("成功请求应只调用 1 次，实际 %d", calls)
	}
}

// TestDoWithRetry_NonRetryableError 验证 4xx 错误立即返回不重试。
func TestDoWithRetry_NonRetryableError(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.Error(w, "bad request", http.StatusBadRequest)
	}))
	defer srv.Close()

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	resp, err := doWithRetry(context.Background(), req)
	if err != nil {
		t.Fatalf("4xx 应返回响应而非错误: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("应返回 400，实际 %d", resp.StatusCode)
	}
	if calls != 1 {
		t.Errorf("4xx 不应重试，实际 %d 次", calls)
	}
}

// TestDoWithRetry_RetryAfterSucceeds 验证 429 带 Retry-After 后重试成功。
func TestDoWithRetry_RetryAfterSucceeds(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Retry-After", "1") // 1 秒后重试
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	resp, err := doWithRetry(context.Background(), req)
	if err != nil {
		t.Fatalf("重试后应成功: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("重试后应 200，实际 %d", resp.StatusCode)
	}
	if calls != 2 {
		t.Errorf("应重试 1 次共 2 次调用，实际 %d", calls)
	}
}

// TestDoWithRetry_ContextCancel 验证 context 取消时立即返回错误，不重试。
func TestDoWithRetry_ContextCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 立即取消
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	if _, err := doWithRetry(ctx, req); err == nil {
		t.Fatal("context 取消后应返回错误")
	}
}

// TestDoWithRetry_Exhaustion 验证所有重试均失败（持续 500）时返回“重试耗尽”错误。
// 用 Retry-After: 0 使退避立即完成，避免真实等待。
func TestDoWithRetry_Exhaustion(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	_, err := doWithRetry(context.Background(), req)
	if err == nil {
		t.Fatal("所有重试失败后应返回错误")
	}
	if !strings.Contains(err.Error(), "请求失败（已重试 3 次）") {
		t.Errorf("应包含重试计数提示，实际: %v", err)
	}
	if calls != maxRetries+1 {
		t.Errorf("应调用 %d 次（含重试），实际 %d", maxRetries+1, calls)
	}
}

// TestPostJSON 验证 postJSON 发送 JSON 请求并携带 Authorization 头。
func TestPostJSON(t *testing.T) {
	var gotAuth string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotBody)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	data, code, err := postJSON(context.Background(), srv.URL, "secret-key", map[string]any{"q": "hello"})
	if err != nil {
		t.Fatalf("postJSON: %v", err)
	}
	if code != http.StatusOK {
		t.Errorf("应 200，实际 %d", code)
	}
	if gotAuth != "Bearer secret-key" {
		t.Errorf("Authorization 头错误: %q", gotAuth)
	}
	if gotBody["q"] != "hello" {
		t.Errorf("请求体错误: %+v", gotBody)
	}
	if !bytes.Contains(data, []byte(`"ok":true`)) {
		t.Errorf("应返回响应体，实际 %s", data)
	}
}

// TestPostJSON_RetryError 验证服务端持续 5xx 时 postJSON 返回重试失败错误。
func TestPostJSON_RetryError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	if _, code, err := postJSON(context.Background(), srv.URL, "k", map[string]any{"q": 1}); err == nil || code != 0 {
		t.Fatalf("应返回错误且 code=0，实际 code=%d err=%v", code, err)
	}
}

// TestPostJSON_BadURL 验证非法 URL 时 postJSON 报错。
func TestPostJSON_BadURL(t *testing.T) {
	if _, _, err := postJSON(context.Background(), "://bad-url", "k", map[string]any{"q": 1}); err == nil {
		t.Fatal("非法 URL 应报错")
	}
}

// TestUploadFileAsMultipart 验证 multipart 文件上传携带字段与认证头。
func TestUploadFileAsMultipart(t *testing.T) {
	var gotAuth string
	var gotField string
	var gotFilePart bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if err := r.ParseMultipartForm(1 << 20); err == nil {
			gotField = r.FormValue("model")
			_, gotFilePart = r.MultipartForm.File["audio"]
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// 建临时文件
	dir := t.TempDir()
	path := filepath.Join(dir, "test.mp3")
	if err := os.WriteFile(path, []byte("fake-audio"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, code, err := uploadFileAsMultipart(context.Background(), srv.URL, "key", "audio", path,
		map[string]string{"model": "whisper"})
	if err != nil {
		t.Fatalf("uploadFileAsMultipart: %v", err)
	}
	if code != http.StatusOK {
		t.Errorf("应 200，实际 %d", code)
	}
	if gotAuth != "Bearer key" {
		t.Errorf("Authorization 头错误: %q", gotAuth)
	}
	if gotField != "whisper" {
		t.Errorf("额外字段 model 应传，实际 %q", gotField)
	}
	if !gotFilePart {
		t.Error("应包含 audio 文件部分")
	}
}

// TestUploadFileAsMultipart_MissingFile 验证文件不存在时上传报错。
func TestUploadFileAsMultipart_MissingFile(t *testing.T) {
	_, code, err := uploadFileAsMultipart(context.Background(), "http://127.0.0.1:1/upload", "k", "file",
		"/nonexistent-file-xyz.mp3", nil)
	if err == nil {
		t.Fatal("文件不存在应报错")
	}
	if code != 0 {
		t.Errorf("code 应为 0，实际 %d", code)
	}
}

// TestUploadFileAsMultipart_RetryError 验证服务端持续 5xx 时上传返回重试失败错误。
func TestUploadFileAsMultipart_RetryError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	dir := t.TempDir()
	path := filepath.Join(dir, "test.mp3")
	if err := os.WriteFile(path, []byte("fake-audio"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, code, err := uploadFileAsMultipart(context.Background(), srv.URL, "k", "file", path, nil); err == nil || code != 0 {
		t.Fatalf("应返回错误且 code=0，实际 code=%d err=%v", code, err)
	}
}
