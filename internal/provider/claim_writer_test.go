package provider

import (
	"context"
	"strings"
	"testing"
)

func claimReq() ClaimAwareWritingRequest {
	return ClaimAwareWritingRequest{
		Title: "咖啡与健康", Audience: "大众读者", ConfirmedClaim: "咖啡的影响因人群而异",
		SourceAttribution: "轻量",
		Materials: []ArticleMaterial{
			{KeyPointID: "kp-a", SourceTitle: "健康播客", Content: "每天一到两杯咖啡的人群心血管风险略低"},
			{KeyPointID: "kp-b", SourceTitle: "医学播客", Content: "咖啡因短期升高血压"},
		},
		OwnerNotes: []ClaimOwnerNote{{ID: "note-1", Content: "外婆只肯用视频通话"}},
	}
}

func goodClaimResult() *ClaimAwareWritingResult {
	return &ClaimAwareWritingResult{
		Title:    "咖啡与健康",
		Markdown: "# 咖啡与健康\n\n研究发现每天一到两杯咖啡与更低心血管风险相关。但咖啡因短期升高血压，确诊人群应限制。",
		ClaimMap: []ClaimMapEntry{
			{Excerpt: "研究发现每天一到两杯咖啡与更低心血管风险相关", ClaimKind: ClaimSource, MaterialIDs: []string{"kp-a"}, SourceTitle: "健康播客"},
			{Excerpt: "咖啡因短期升高血压", ClaimKind: ClaimSource, MaterialIDs: []string{"kp-b"}, SourceTitle: "医学播客"},
			{Excerpt: "确诊人群应限制", ClaimKind: ClaimSynthesis, MaterialIDs: []string{"kp-a", "kp-b"}},
		},
	}
}

// TestValidateClaimMap_Contract C09：非法类型/虚构材料/片段不存在被拦截；合法通过。
func TestValidateClaimMap_Contract(t *testing.T) {
	req := claimReq()
	res := goodClaimResult()
	if errs := ValidateClaimMap(res, req); len(errs) != 0 {
		t.Fatalf("合法 ClaimMap 应通过: %v", errs)
	}
	// 虚构材料 ID
	bad := goodClaimResult()
	bad.ClaimMap[0].MaterialIDs = []string{"kp-fake"}
	if errs := ValidateClaimMap(bad, req); len(errs) == 0 {
		t.Fatal("虚构材料 ID 应被拒绝")
	}
	// 正文片段不存在
	bad2 := goodClaimResult()
	bad2.ClaimMap[0].Excerpt = "不存在的片段"
	if errs := ValidateClaimMap(bad2, req); len(errs) == 0 {
		t.Fatal("不存在的片段应被拒绝")
	}
	// 非法类型
	bad3 := goodClaimResult()
	bad3.ClaimMap[0].ClaimKind = "opinion"
	if errs := ValidateClaimMap(bad3, req); len(errs) == 0 {
		t.Fatal("非法类型应被拒绝")
	}
	// SourceClaim 缺材料
	bad4 := goodClaimResult()
	bad4.ClaimMap[0].MaterialIDs = nil
	if errs := ValidateClaimMap(bad4, req); len(errs) == 0 {
		t.Fatal("SourceClaim 缺材料应被拒绝")
	}
}

// TestWriteArticleWithClaims_Groq Groq v2 写作端到端（含校验拒绝）。
func TestWriteArticleWithClaims_Groq(t *testing.T) {
	g := NewGroqProvider("test")
	g.chatCompleteFn = func([]map[string]string, string) (string, int, error) {
		return `{"title":"咖啡","markdown":"# 咖啡\n\n低剂量关联。","claimMap":[{"excerpt":"低剂量关联","claimKind":"source_claim","materialIds":["kp-a"],"sourceTitle":"健康播客"}]}`, 200, nil
	}
	req := claimReq()
	res, _, err := g.WriteArticleWithClaims(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Markdown, "低剂量") || len(res.ClaimMap) != 1  {
		t.Fatalf("v2 写作结果不符: %+v", res)
	}
	// 非法输出 → 校验拒绝
	g.chatCompleteFn = func([]map[string]string, string) (string, int, error) {
		return `{"title":"bad","markdown":"# bad","claimMap":[{"excerpt":"x","claimKind":"opinion"}]}`, 200, nil
	}
	if _, _, err := g.WriteArticleWithClaims(context.Background(), req); err == nil || !strings.Contains(err.Error(), "ClaimMap") {
		t.Fatalf("非法 ClaimMap 应拒绝: %v", err)
	}
}
