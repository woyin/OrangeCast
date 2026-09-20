package provider

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func ideationRequest() IdeationDiagnosisRequest {
	return IdeationDiagnosisRequest{
		Question:    "这些材料如何共同支持文章主张？",
		Constraints: "保留冲突",
		PriorRounds: []string{"先比较两集的限定条件"},
		Materials: []IdeationMaterial{
			{ID: "kp-1", SourceTitle: "第一集", Content: "低剂量与较低风险相关", Citations: []string{"seg-1"}},
			{ID: "kp-2", SourceTitle: "第二集", Content: "咖啡因会短期升压", Citations: []string{"seg-2"}},
		},
	}
}

func TestIdeationDiagnosisContract(t *testing.T) {
	req := ideationRequest()
	if err := ValidateIdeagnosisInput(req); err != nil {
		t.Fatalf("合法诊断输入被拒绝: %v", err)
	}
	if err := ValidateIdeagnosisInput(IdeationDiagnosisRequest{Question: " \n"}); err == nil {
		t.Fatal("空问题必须拒绝")
	}

	diagnosis := &IdeationDiagnosis{
		Supports:    []DiagnosisItem{{MaterialID: "kp-1", Text: "提供关联证据"}},
		Contradicts: []DiagnosisItem{{MaterialID: "kp-2", Text: "给出风险限定"}},
		Supplements: []DiagnosisItem{{MaterialID: "kp-1", Text: "补充剂量条件"}},
		Gaps:        []string{"缺少因果研究"},
		ProposedClaims: []ProposedClaimItem{{
			Claim: "咖啡影响取决于剂量和人群", MaterialIDs: []string{"kp-1", "kp-2"},
		}},
	}
	if errs := ValidateIdeationDiagnosis(diagnosis, req); len(errs) != 0 {
		t.Fatalf("合法诊断被拒绝: %v", errs)
	}
	if raw, err := diagnosis.ToJSON(); err != nil || !strings.Contains(raw, `"materialId":"kp-1"`) {
		t.Fatalf("诊断应可持久化为 JSON: %q err=%v", raw, err)
	}

	invalid := &IdeationDiagnosis{
		Supports:       []DiagnosisItem{{MaterialID: "invented-support"}},
		Contradicts:    []DiagnosisItem{{MaterialID: "invented-contradiction"}},
		Supplements:    []DiagnosisItem{{MaterialID: "invented-supplement"}},
		ProposedClaims: []ProposedClaimItem{{Claim: "越界主张", MaterialIDs: []string{"invented-claim"}}},
	}
	errs := ValidateIdeationDiagnosis(invalid, req)
	if len(errs) != 4 {
		t.Fatalf("每种越界引用都必须单独报告，实际 %d: %v", len(errs), errs)
	}
}

func TestBuildIdeationPromptCarriesFrozenContext(t *testing.T) {
	prompt := BuildIdeationPrompt(ideationRequest())
	for _, want := range []string{"Owner 的问题", "保留冲突", "前文轮次 1", "[kp-1] 第一集", "只能引用这些 ID", "proposedClaims"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("诊断提示词缺少 %q: %s", want, prompt)
		}
	}
	req := ideationRequest()
	req.Constraints = "{}"
	if prompt := BuildIdeationPrompt(req); strings.Contains(prompt, "约束：{}") {
		t.Fatalf("空 JSON 约束不应污染提示词: %s", prompt)
	}
}

func TestGroqDiagnoseIdeationReturnsStructuredResult(t *testing.T) {
	g := NewGroqProvider("key")
	g.chatCompleteFn = func(messages []map[string]string, mode string) (string, int, error) {
		if mode != "object" || len(messages) != 2 || !strings.Contains(messages[1]["content"], "kp-1") {
			t.Fatalf("诊断必须以冻结材料调用 JSON 模式: mode=%q messages=%v", mode, messages)
		}
		return `{"supports":[{"materialId":"kp-1","text":"支持"}],"gaps":["因果性未知"]}`, 200, nil
	}
	result, _, err := g.DiagnoseIdeation(context.Background(), ideationRequest())
	if err != nil || len(result.Supports) != 1 || result.Supports[0].MaterialID != "kp-1" {
		t.Fatalf("Groq 诊断解析失败: result=%+v err=%v", result, err)
	}

	if _, _, err := g.DiagnoseIdeation(context.Background(), IdeationDiagnosisRequest{}); err == nil {
		t.Fatal("非法输入不得调用模型")
	}
	g.chatCompleteFn = func([]map[string]string, string) (string, int, error) { return "", 500, context.DeadlineExceeded }
	if _, _, err := g.DiagnoseIdeation(context.Background(), ideationRequest()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("模型错误必须原样上浮: %v", err)
	}
	g.chatCompleteFn = func([]map[string]string, string) (string, int, error) { return "not-json", 200, nil }
	if _, _, err := g.DiagnoseIdeation(context.Background(), ideationRequest()); err == nil || !strings.Contains(err.Error(), "解析诊断输出") {
		t.Fatalf("非法模型响应必须明确报解析错误: %v", err)
	}
}

func TestOpenAIDiagnoseIdeationReturnsStructuredResult(t *testing.T) {
	srv := newOpenAITestServer(t, `{"supports":[{"materialId":"kp-1","text":"支持"}],"gaps":[]}`)
	defer srv.Close()
	o := NewOpenAIProvider("key").WithBaseURL(srv.URL)
	result, _, err := o.DiagnoseIdeation(context.Background(), ideationRequest())
	if err != nil || len(result.Supports) != 1 || result.Supports[0].MaterialID != "kp-1" {
		t.Fatalf("OpenAI 诊断解析失败: result=%+v err=%v", result, err)
	}
	if _, _, err := o.DiagnoseIdeation(context.Background(), IdeationDiagnosisRequest{}); err == nil {
		t.Fatal("非法输入不得调用模型")
	}

	bad := newOpenAITestServer(t, "not-json")
	defer bad.Close()
	if _, _, err := NewOpenAIProvider("key").WithBaseURL(bad.URL).DiagnoseIdeation(context.Background(), ideationRequest()); err == nil || !strings.Contains(err.Error(), "解析诊断输出") {
		t.Fatalf("非法模型响应必须明确报解析错误: %v", err)
	}
}
