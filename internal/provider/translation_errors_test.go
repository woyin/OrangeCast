package provider

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestOpenAI_Translate_ErrorPaths OpenAI 路径的响应与 JSON 错误必须显式返回，
// 不能返回空译文冒充成功。
func TestOpenAI_Translate_ErrorPaths(t *testing.T) {
	srv := newOpenAITestServer(t, "not-json")
	defer srv.Close()
	o := NewOpenAIProvider("key").WithBaseURL(srv.URL)
	if _, err := o.Translate([]Segment{{ID: "seg-0001", Text: "a"}}, "中文"); err == nil {
		t.Fatal("非 JSON 输出应报错")
	}

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer bad.Close()
	o2 := NewOpenAIProvider("key").WithBaseURL(bad.URL)
	if _, err := o2.Translate([]Segment{{ID: "seg-0001", Text: "a"}}, "中文"); err == nil {
		t.Fatal("远端 500 应报错")
	}
}

// TestOpenAI_Translate_AllInvalid OpenAI 返回的片段全部不存在时显式报错。
func TestOpenAI_Translate_AllInvalid(t *testing.T) {
	srv := newOpenAITestServer(t, `{"segments":[{"id":"ghost","text":"编造"}]}`)
	defer srv.Close()
	o := NewOpenAIProvider("key").WithBaseURL(srv.URL)
	if _, err := o.Translate([]Segment{{ID: "seg-0001", Text: "a"}}, "中文"); err == nil {
		t.Fatal("全部无效应报错")
	}
}
