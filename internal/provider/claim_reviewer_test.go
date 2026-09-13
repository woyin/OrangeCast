package provider

import (
	"context"
	"strings"
	"testing"
)

func TestValidateClaimReviewResult(t *testing.T) {
	if err := ValidateClaimReviewResult(&ClaimReviewResult{Status: "pass"}); err != nil {
		t.Fatal(err)
	}
	if err := ValidateClaimReviewResult(&ClaimReviewResult{Status: "fail"}); err != nil {
		t.Fatal(err)
	}
	if err := ValidateClaimReviewResult(&ClaimReviewResult{Status: "bogus"}); err == nil {
		t.Fatal("非法状态应拒绝")
	}
}

func TestBuildClaimReviewPrompt(t *testing.T) {
	req := ClaimReviewRequest{
		Markdown: "全文内容", ConfirmedClaim: "确认主张",
		ClaimMap: []ClaimMapEntry{{Excerpt: "片段", ClaimKind: ClaimSource, MaterialIDs: []string{"kp-1"}}},
	}
	prompt := BuildClaimReviewPrompt(req)
	for _, want := range []string{"确认主张", "ClaimMap", "片段", "kp-1", "全文内容"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt 缺少 %q", want)
		}
	}
}

func TestParseClaimReviewResult(t *testing.T) {
	r, err := parseClaimReviewResult(`{"status":"fail","findings":[{"excerpt":"x","issueKind":"misattributed","detail":"d"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != "fail" || len(r.Findings) != 1 || r.Findings[0].IssueKind != "misattributed" {
		t.Fatalf("解析不符: %+v", r)
	}
}

func TestReviewClaimsGroq(t *testing.T) {
	g := NewGroqProvider("k")
	g.chatCompleteFn = func([]map[string]string, string) (string, int, error) {
		return `{"status":"pass","findings":[]}`, 200, nil
	}
	req := ClaimReviewRequest{
		Markdown: "全文", ConfirmedClaim: "主张",
		ClaimMap: []ClaimMapEntry{{Excerpt: "片段", ClaimKind: ClaimSource, MaterialIDs: []string{"kp-1"}}},
	}
	r, usage, err := g.ReviewClaims(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != "pass" || usage.InputUnits != 0 {
		t.Fatalf("审校结果: %+v %+v", r, usage)
	}
	g.chatCompleteFn = func([]map[string]string, string) (string, int, error) {
		return "", 0, context.DeadlineExceeded
	}
	if _, _, err := g.ReviewClaims(context.Background(), req); err == nil {
		t.Fatal("模型不可用应报错")
	}
}

func TestWriteArticleWithClaimsOpenAI(t *testing.T) {
	srv := newOpenAITestServer(t, `{"title":"T","markdown":"# T\n\n正文","claimMap":[{"excerpt":"正文","claimKind":"owner_claim","materialIds":[]}]}`)
	defer srv.Close()
	o := NewOpenAIProvider("k").WithBaseURL(srv.URL)
	req := ClaimAwareWritingRequest{ConfirmedClaim: "主张", Materials: []ArticleMaterial{}}
	res, _, err := o.WriteArticleWithClaims(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if res.Title != "T" || len(res.ClaimMap) != 1 {
		t.Fatalf("结果不符: %+v", res)
	}
}
