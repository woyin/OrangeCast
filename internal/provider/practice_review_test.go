package provider

import (
	"strings"
	"testing"
)

// TestGroq_ReviewQuestions 验证问题与提示按 JSON 解析，模型身份来自响应。
func TestGroq_ReviewQuestions(t *testing.T) {
	g := NewGroqProvider("key")
	g.chatCompleteFn = func(messages []map[string]string, jsonMode string) (string, int, error) {
		if jsonMode != "object" {
			t.Errorf("练习问题需要 JSON 对象模式，实际 %q", jsonMode)
		}
		if !strings.Contains(messages[0]["content"], "自我检验") {
			t.Error("系统提示应说明自我检验问题")
		}
		return `{"items":[{"question":"为什么需要审查效率？","citations":["seg-0001"],"answerHint":"想想成本与收益的错配"}]}`, 200, nil
	}
	res, err := g.ReviewQuestions([]Segment{{ID: "seg-0001", Start: 0, End: 5, Text: "效率需要审查"}}, 3)
	if err != nil {
		t.Fatalf("ReviewQuestions: %v", err)
	}
	if len(res.Items) != 1 || res.Items[0].Question == "" || res.Items[0].AnswerHint == "" {
		t.Fatalf("应解析出一问一提示: %+v", res.Items)
	}
	if len(res.Items[0].Citations) != 1 || res.Items[0].Citations[0] != "seg-0001" {
		t.Errorf("引用应保留模型给出的片段 ID: %+v", res.Items[0].Citations)
	}
}

// TestGroq_ReviewQuestions_NoSegments 无片段时直接报错，不发起远端调用。
func TestGroq_ReviewQuestions_NoSegments(t *testing.T) {
	g := NewGroqProvider("key")
	called := false
	g.chatCompleteFn = func([]map[string]string, string) (string, int, error) {
		called = true
		return "", 200, nil
	}
	if _, err := g.ReviewQuestions(nil, 3); err == nil {
		t.Fatal("无片段应报错")
	}
	if called {
		t.Error("无片段时不应调用远端模型")
	}
}

// TestGroq_ReviewQuestions_BadJSON 非法 JSON 显式报错，不返回空集合冒充成功。
func TestGroq_ReviewQuestions_BadJSON(t *testing.T) {
	g := NewGroqProvider("key")
	g.chatCompleteFn = func([]map[string]string, string) (string, int, error) {
		return `not-json`, 200, nil
	}
	if _, err := g.ReviewQuestions([]Segment{{ID: "seg-0001", Text: "内容"}}, 3); err == nil {
		t.Fatal("非法 JSON 应报错")
	}
}

// TestOpenAI_ReviewQuestions 验证 OpenAI 路径同样解析问题与提示。
func TestOpenAI_ReviewQuestions(t *testing.T) {
	srv := newOpenAITestServer(t, `{"items":[{"question":"这段说了什么？","citations":["seg-0001"],"answerHint":"先想结论"}]}`)
	defer srv.Close()
	o := NewOpenAIProvider("key").WithBaseURL(srv.URL)
	res, err := o.ReviewQuestions([]Segment{{ID: "seg-0001", Start: 0, End: 5, Text: "内容"}}, 3)
	if err != nil {
		t.Fatalf("ReviewQuestions: %v", err)
	}
	if len(res.Items) != 1 || res.Items[0].Question == "" {
		t.Fatalf("应解析一个问题: %+v", res.Items)
	}
}

// TestOpenAI_ReviewQuestions_NoSegments 无片段时 OpenAI 路径同样不发起调用。
func TestOpenAI_ReviewQuestions_NoSegments(t *testing.T) {
	o := NewOpenAIProvider("key")
	if _, err := o.ReviewQuestions(nil, 3); err == nil {
		t.Fatal("无片段应报错")
	}
}

// TestSelector_BundleWiresPracticeReview 两个 Provider 的 bundle 都必须装配练习问题能力。
func TestSelector_BundleWiresPracticeReview(t *testing.T) {
	sel := NewSelector("groq-key", "openai-key")
	for _, name := range []string{"groq", "openai"} {
		bundle, err := sel.Bundle(name)
		if err != nil {
			t.Fatalf("%s bundle: %v", name, err)
		}
		if bundle.PracticeReview == nil {
			t.Errorf("%s bundle 缺少 PracticeReview", name)
		}
	}
}
