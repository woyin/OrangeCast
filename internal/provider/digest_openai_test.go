package provider

import (
	"context"
	"strings"
	"testing"
)

// TestOpenAIDigest_ComposeDigest OpenAI 精读写作（C03 依赖）。
func TestOpenAIDigest_ComposeDigest(t *testing.T) {
	srv := newOpenAITestServer(t, `{"title":"OpenAI 精读","markdown":"# 正文","claimMap":[],"blocks":[{"type":"paraphrase","text":"转述内容","citations":["seg-0001"]}]}`)
	defer srv.Close()
	o := NewOpenAIProvider("k").WithBaseURL(srv.URL)
	result, err := o.ComposeDigest(context.Background(), DigestWritingRequest{
		SourceTitle: "T", Segments: []Segment{{ID: "seg-0001", Start: 0, End: 5, Text: "内容"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Title != "OpenAI 精读" || len(result.Blocks) == 0 {
		t.Fatalf("结果不符: %+v", result)
	}
}

// TestOpenAIDigest_OpenAIDigestCall_Direct 直接测试底层调用。
func TestOpenAIDigest_OpenAIDigestCall_Direct(t *testing.T) {
	srv := newOpenAITestServer(t, "直接调用内容")
	defer srv.Close()
	o := NewOpenAIProvider("k").WithBaseURL(srv.URL)
	text, usage, err := o.openaiDigestCall(context.Background(), "system prompt", "user input")
	if err != nil {
		t.Fatal(err)
	}
	if text != "直接调用内容" {
		t.Fatalf("内容不符: %q", text)
	}
	_ = usage
}

func TestOpenAIDigest_RejectsInvalidComposeResponses(t *testing.T) {
	o := NewOpenAIProvider("k")
	if _, err := o.ComposeDigest(context.Background(), DigestWritingRequest{}); err == nil || !strings.Contains(err.Error(), "无本集 Segment") {
		t.Fatalf("空材料必须在远程调用前拒绝: %v", err)
	}

	request := DigestWritingRequest{
		SourceTitle:   "节目",
		SourceSummary: "摘要",
		Segments:      []Segment{{ID: "seg-1", Text: "原文"}},
		KeyPoints:     []DigestKeyPoint{{Content: "要点", Citations: []string{"seg-1"}}},
		Notes:         []DigestOwnerNote{{NoteID: "note-1", Kind: "source_note", Text: "笔记"}},
	}
	for _, output := range []string{"not-json", `{"title":"","blocks":[]}`} {
		srv := newOpenAITestServer(t, output)
		_, err := NewOpenAIProvider("k").WithBaseURL(srv.URL).ComposeDigest(context.Background(), request)
		srv.Close()
		if err == nil {
			t.Fatalf("非法精读文初稿必须拒绝: %q", output)
		}
	}
}

func TestOpenAIDigest_WeavesOnlyWhenGapsAndDocumentsExist(t *testing.T) {
	o := NewOpenAIProvider("k")
	for _, request := range []DigestWeaveRequest{
		{FactGaps: []string{"缺口"}},
		{Documents: []DigestDocument{{DocumentID: "doc-1"}}},
	} {
		result, err := o.WeaveDigestFacts(context.Background(), request)
		if err != nil || len(result.Blocks) != 0 {
			t.Fatalf("缺少任一输入时应直接返回空补织: result=%+v err=%v", result, err)
		}
	}

	request := DigestWeaveRequest{
		FactGaps: []string{"还缺什么证据"},
		Blocks:   []DigestBlockDraft{{Type: DigestBlockParaphraseStr, Text: "已有内容"}},
		Documents: []DigestDocument{{
			DocumentID: "doc-1", Title: "落源文档", Segments: []Segment{{ID: "doc-seg-1", Text: "事实原文"}},
		}},
	}
	srv := newOpenAITestServer(t, `{"blocks":[{"type":"cited_fact","text":"补充事实","citations":["doc-seg-1"],"targetSourceId":"doc-1"}]}`)
	result, err := NewOpenAIProvider("k").WithBaseURL(srv.URL).WeaveDigestFacts(context.Background(), request)
	srv.Close()
	if err != nil || len(result.Blocks) != 1 || result.Blocks[0].TargetSourceID != "doc-1" {
		t.Fatalf("补织结果解析失败: result=%+v err=%v", result, err)
	}

	bad := newOpenAITestServer(t, "not-json")
	defer bad.Close()
	if _, err := NewOpenAIProvider("k").WithBaseURL(bad.URL).WeaveDigestFacts(context.Background(), request); err == nil || !strings.Contains(err.Error(), "解析补织事实块") {
		t.Fatalf("非法补织响应必须明确报错: %v", err)
	}
}

func TestOpenAIDigest_RewriteEnforcesInputsAndOutput(t *testing.T) {
	if _, err := NewOpenAIProvider("k").RewriteDigest(context.Background(), DigestRewriteRequest{}); err == nil || !strings.Contains(err.Error(), "无内容块") {
		t.Fatalf("空正文必须在远程调用前拒绝: %v", err)
	}
	request := DigestRewriteRequest{Blocks: []DigestBlockDraft{{Type: DigestBlockParaphraseStr, Text: "正文"}}}
	srv := newOpenAITestServer(t, `{"text":"改写正文 #播客"}`)
	result, err := NewOpenAIProvider("k").WithBaseURL(srv.URL).RewriteDigest(context.Background(), request)
	srv.Close()
	if err != nil || result.Text != "改写正文 #播客" {
		t.Fatalf("渠道改写解析失败: result=%+v err=%v", result, err)
	}
	for _, output := range []string{"not-json", `{"text":"  "}`} {
		bad := newOpenAITestServer(t, output)
		_, err := NewOpenAIProvider("k").WithBaseURL(bad.URL).RewriteDigest(context.Background(), request)
		bad.Close()
		if err == nil {
			t.Fatalf("非法渠道改写必须拒绝: %q", output)
		}
	}
	if _, err := NewOpenAIProvider("k").Search(context.Background(), "query"); err == nil || !strings.Contains(err.Error(), "不支持 SourceSearch") {
		t.Fatalf("OpenAI 搜索必须显式声明降级: %v", err)
	}
}
