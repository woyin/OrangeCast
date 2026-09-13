package provider

import (
	"context"
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
