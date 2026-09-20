package provider

import (
	"context"
	"strings"
	"testing"
)

func TestValidateClaimReviewResult(t *testing.T) {
	for _, ok := range []*ClaimReviewResult{
		{Status: ClaimReviewPassed},
		{Status: ClaimReviewFailed, Findings: []ClaimReviewFinding{{Excerpt: "x", IssueKind: "misattributed", Detail: "d"}}},
	} {
		if err := ValidateClaimReviewResult(ok); err != nil {
			t.Fatalf("合法结论被拒: %+v %v", ok, err)
		}
	}
	for _, bad := range []*ClaimReviewResult{
		{Status: "pass"},
		{Status: "fail"},
		{Status: "advisory"},
		{Status: ClaimReviewPassed, Findings: []ClaimReviewFinding{{Excerpt: "x", IssueKind: "misattributed", Detail: "d"}}},
		{Status: ClaimReviewFailed},
		{Status: ClaimReviewFailed, Findings: []ClaimReviewFinding{{Excerpt: "x", IssueKind: "made_up", Detail: "d"}}},
		{Status: ClaimReviewFailed, Findings: []ClaimReviewFinding{{IssueKind: "misattributed", Detail: "d"}}},
		{Status: ClaimReviewFailed, Findings: []ClaimReviewFinding{{Excerpt: "x", IssueKind: "misattributed"}}},
	} {
		if err := ValidateClaimReviewResult(bad); err == nil {
			t.Fatalf("非法结论应拒绝: %+v", bad)
		}
	}
}

func reviewTestRequest() ClaimReviewRequest {
	return ClaimReviewRequest{
		Markdown:       "全文内容，来源说过这句话。",
		ConfirmedClaim: "确认主张",
		AuthorizedIDs:  []string{"kp-1"},
		Materials:      []ArticleMaterial{{KeyPointID: "kp-1", SourceType: "episode", SourceID: "ep-1", SourceTitle: "访谈原文", Content: "材料原文", Citations: []string{"seg-1"}}},
		ClaimMap:       []ClaimMapEntry{{Excerpt: "来源说过这句话。", ClaimKind: ClaimSource, MaterialIDs: []string{"kp-1"}}},
	}
}

func TestBuildClaimReviewPrompt(t *testing.T) {
	req := reviewTestRequest()
	prompt := BuildClaimReviewPrompt(req)
	for _, want := range []string{"确认主张", "ClaimMap", "来源说过这句话", "kp-1", "访谈原文", "材料原文", "seg-1", "全文内容"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt 缺少 %q", want)
		}
	}
}

func TestValidateClaimReviewAgainstInput(t *testing.T) {
	req := reviewTestRequest()
	if err := ValidateClaimReviewAgainstInput(&ClaimReviewResult{Status: ClaimReviewPassed}, &req); err != nil {
		t.Fatal(err)
	}
	if err := ValidateClaimReviewAgainstInput(&ClaimReviewResult{Status: ClaimReviewFailed, Findings: []ClaimReviewFinding{{Excerpt: "来源说过这句话。", IssueKind: "misattributed", Detail: "d"}}}, &req); err != nil {
		t.Fatal(err)
	}
	// findings excerpt 越界。
	if err := ValidateClaimReviewAgainstInput(&ClaimReviewResult{Status: ClaimReviewFailed, Findings: []ClaimReviewFinding{{Excerpt: "不在正文", IssueKind: "misattributed", Detail: "d"}}}, &req); err == nil {
		t.Fatal("excerpt 越界应拒绝")
	}
	// ClaimMap excerpt 越界。
	bad := reviewTestRequest()
	bad.ClaimMap[0].Excerpt = "不在正文"
	if err := ValidateClaimReviewAgainstInput(&ClaimReviewResult{Status: ClaimReviewPassed}, &bad); err == nil {
		t.Fatal("ClaimMap excerpt 越界应拒绝")
	}
	// ClaimMap 引用授权之外材料。
	unauthorized := reviewTestRequest()
	unauthorized.ClaimMap[0].MaterialIDs = []string{"kp-x"}
	if err := ValidateClaimReviewAgainstInput(&ClaimReviewResult{Status: ClaimReviewPassed}, &unauthorized); err == nil {
		t.Fatal("越权材料引用应拒绝")
	}
	// ClaimMap 引用授权集合内但不在冻结材料中。
	unknown := reviewTestRequest()
	unknown.ClaimMap[0].MaterialIDs = []string{"kp-2"}
	unknown.AuthorizedIDs = []string{"kp-1", "kp-2"}
	if err := ValidateClaimReviewAgainstInput(&ClaimReviewResult{Status: ClaimReviewPassed}, &unknown); err == nil {
		t.Fatal("不在冻结材料中的引用应拒绝")
	}
	// 冻结材料集合夹带授权之外 ID。
	smuggled := reviewTestRequest()
	smuggled.Materials = append(smuggled.Materials, ArticleMaterial{KeyPointID: "kp-ghost", SourceID: "ep-2"})
	if err := ValidateClaimReviewAgainstInput(&ClaimReviewResult{Status: ClaimReviewPassed}, &smuggled); err == nil {
		t.Fatal("材料夹带未授权 ID 应拒绝")
	}
}

func TestParseClaimReviewResult(t *testing.T) {
	r, err := parseClaimReviewResult(`{"status":"failed","findings":[{"excerpt":"x","issueKind":"misattributed","detail":"d"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != ClaimReviewFailed || len(r.Findings) != 1 || r.Findings[0].IssueKind != "misattributed" {
		t.Fatalf("解析不符: %+v", r)
	}
	if _, err := parseClaimReviewResult(`{"status":"pass","findings":[]}`); err == nil {
		t.Fatal("旧 pass 状态必须拒绝（统一 passed|failed）")
	}
}

func TestReviewClaimsGroq(t *testing.T) {
	g := NewGroqProvider("k")
	g.chatCompleteFn = func([]map[string]string, string) (string, int, error) {
		return `{"status":"passed","findings":[]}`, 200, nil
	}
	req := reviewTestRequest()
	r, usage, err := g.ReviewClaims(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != ClaimReviewPassed || usage.InputUnits != 0 {
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
