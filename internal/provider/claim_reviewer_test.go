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

// TestBuildClaimRevisionPrompt R21：修订模式请求必须携带现有正文、审校反馈与原
// ClaimMap，且 revision prompt version 与 v2 写作不同。
func TestBuildClaimRevisionPrompt(t *testing.T) {
	req := ClaimAwareWritingRequest{
		ConfirmedClaim: "Owner 主张", Audience: "读者",
		ExistingMarkdown: "# 现有正文\n\n来源说过这句话。",
		RevisionFeedback: []string{"片段X： misattributed — 归因错误"},
		ExistingClaimMap: []ClaimMapEntry{{Excerpt: "来源说过这句话。", ClaimKind: ClaimSource, MaterialIDs: []string{"kp-1"}}},
	}
	prompt := BuildClaimPrompt(req)
	for _, want := range []string{"修订任务", "审校反馈", "归因错误", "现有正文", "来源说过这句话", "kp-1", "完整映射"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("修订 prompt 缺少 %q", want)
		}
	}
	if ClaimRevisionWriterPromptVersion == ClaimWriterPromptVersion {
		t.Fatal("修订写作必须有独立 prompt version")
	}
}

// TestValidateClaimRevisionMap R21：重复句、身份篡改、重复映射、base 歧义拒绝。
func TestValidateClaimRevisionMap(t *testing.T) {
	base := ClaimAwareWritingRequest{
		ConfirmedClaim:   "Owner 主张",
		Materials:        []ArticleMaterial{{KeyPointID: "kp-1", SourceType: "episode", SourceID: "ep-1", SourceTitle: "来源", Content: "材料", Citations: []string{"seg-1"}}},
		ExistingMarkdown: "# 旧\n\n来源说过这句话。Owner 的判断在此。",
		ExistingClaimMap: []ClaimMapEntry{
			{Excerpt: "来源说过这句话。", ClaimKind: ClaimSource, MaterialIDs: []string{"kp-1"}, SourceTitle: "来源", CitationRefs: []string{"seg-1"}},
			{Excerpt: "Owner 的判断在此。", ClaimKind: ClaimOwner},
		},
	}
	valid := func() *ClaimAwareWritingResult {
		return &ClaimAwareWritingResult{
			Title:    "新",
			Markdown: "# 新\n\n来源说过这句话。（改）Owner 的判断在此。",
			ClaimMap: []ClaimMapEntry{
				{Excerpt: "来源说过这句话。", ClaimKind: ClaimSource, MaterialIDs: []string{"kp-1"}, SourceTitle: "来源", CitationRefs: []string{"seg-1"}},
				{Excerpt: "Owner 的判断在此。", ClaimKind: ClaimOwner},
			},
		}
	}
	if err := ValidateClaimRevisionMap(valid(), base); err != nil {
		t.Fatalf("合法修订被拒: %v", err)
	}
	// 重复句：保留 excerpt 在新正文出现两次。
	dup := valid()
	dup.Markdown = "# 新\n\n来源说过这句话。来源说过这句话。Owner 的判断在此。"
	if err := ValidateClaimRevisionMap(dup, base); err == nil || !strings.Contains(err.Error(), "恰好出现一次") {
		t.Fatalf("重复句必须拒绝: %v", err)
	}
	// 身份篡改：excerpt 保留但 kind 改变。
	tampered := valid()
	for i := range tampered.ClaimMap {
		if tampered.ClaimMap[i].Excerpt == "来源说过这句话。" {
			tampered.ClaimMap[i].ClaimKind = ClaimOwner
		}
	}
	if err := ValidateClaimRevisionMap(tampered, base); err == nil || !strings.Contains(err.Error(), "身份被篡改") {
		t.Fatalf("身份篡改必须拒绝: %v", err)
	}
	// 身份篡改：material IDs 变化。
	tamperedIDs := valid()
	for i := range tamperedIDs.ClaimMap {
		if tamperedIDs.ClaimMap[i].Excerpt == "来源说过这句话。" {
			tamperedIDs.ClaimMap[i].MaterialIDs = nil
		}
	}
	if err := ValidateClaimRevisionMap(tamperedIDs, base); err == nil {
		t.Fatal("材料身份篡改必须拒绝")
	}
	// 输出映射重复 excerpt。
	dupMap := valid()
	dupMap.ClaimMap = append(dupMap.ClaimMap, dupMap.ClaimMap[1])
	if err := ValidateClaimRevisionMap(dupMap, base); err == nil || !strings.Contains(err.Error(), "重复 excerpt") {
		t.Fatalf("重复映射必须拒绝: %v", err)
	}
	// base 映射自身重复（身份歧义）→ 直接拒绝。
	ambiguous := base
	ambiguous.ExistingClaimMap = append(ambiguous.ExistingClaimMap, base.ExistingClaimMap[0])
	if err := ValidateClaimRevisionMap(valid(), ambiguous); err == nil || !strings.Contains(err.Error(), "身份歧义") {
		t.Fatalf("base 歧义必须拒绝: %v", err)
	}
}

// TestValidateClaimRevisionMap_OmittedKeptExcerpt R21：正文复制旧句（新正文唯一）
// 但输出省略其映射 → 必须拒绝；新正文重复出现旧句 → 无论是否映射都必须拒绝；
// excerpt 删除后输出映射仍保留 → 拒绝。
func TestValidateClaimRevisionMap_OmittedKeptExcerpt(t *testing.T) {
	base := ClaimAwareWritingRequest{
		ConfirmedClaim:   "Owner 主张",
		Materials:        []ArticleMaterial{{KeyPointID: "kp-1", SourceType: "episode", SourceID: "ep-1", SourceTitle: "来源", Content: "材料", Citations: []string{"seg-1"}}},
		ExistingMarkdown: "# 旧\n\n来源说过这句话。Owner 的判断在此。",
		ExistingClaimMap: []ClaimMapEntry{
			{Excerpt: "来源说过这句话。", ClaimKind: ClaimSource, MaterialIDs: []string{"kp-1"}, SourceTitle: "来源", CitationRefs: []string{"seg-1"}},
			{Excerpt: "Owner 的判断在此。", ClaimKind: ClaimOwner},
		},
	}
	// 正文保留旧句但输出省略其映射（owner 段完整）。
	omit := &ClaimAwareWritingResult{
		Title:    "新",
		Markdown: "# 新\n\n来源说过这句话。Owner 的判断在此。",
		ClaimMap: []ClaimMapEntry{
			{Excerpt: "Owner 的判断在此。", ClaimKind: ClaimOwner},
		},
	}
	if err := ValidateClaimRevisionMap(omit, base); err == nil || !strings.Contains(err.Error(), "省略") {
		t.Fatalf("保留片段省略映射必须拒绝: %v", err)
	}
	// 新正文重复出现旧句（两次）且输出完全省略该 excerpt → 仍必须拒绝。
	duplicated := &ClaimAwareWritingResult{
		Title:    "新",
		Markdown: "# 新\n\n来源说过这句话。来源说过这句话。Owner 的判断在此。",
		ClaimMap: []ClaimMapEntry{
			{Excerpt: "Owner 的判断在此。", ClaimKind: ClaimOwner},
		},
	}
	if err := ValidateClaimRevisionMap(duplicated, base); err == nil || !strings.Contains(err.Error(), "重复句") {
		t.Fatalf("新正文重复旧句必须无条件拒绝: %v", err)
	}
	// excerpt 删除但输出映射仍保留 → 拒绝。
	ghost := &ClaimAwareWritingResult{
		Title:    "新",
		Markdown: "# 新\n\n全新内容。Owner 的判断在此。",
		ClaimMap: []ClaimMapEntry{
			{Excerpt: "来源说过这句话。", ClaimKind: ClaimSource, MaterialIDs: []string{"kp-1"}, SourceTitle: "来源", CitationRefs: []string{"seg-1"}},
			{Excerpt: "Owner 的判断在此。", ClaimKind: ClaimOwner},
		},
	}
	if err := ValidateClaimRevisionMap(ghost, base); err == nil {
		t.Fatal("已删除 excerpt 的输出映射必须拒绝")
	}
}
