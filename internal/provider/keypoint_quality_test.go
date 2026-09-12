package provider

import (
	"context"

	"github.com/woyin/orangecast/internal/models"
	"strings"
	"testing"
)

func qualityInput() KeypointQualityInput {
	return KeypointQualityInput{
		KeyPointID: "kp-1",
		Content:    "主讲人认为低剂量咖啡与更低心血管风险相关",
		Citations:  []string{"seg-0001"},
		Segments:   []Segment{{ID: "seg-0001", Start: 0, End: 9, Text: "每天一到两杯咖啡的人群心血管疾病风险略低"}},
	}
}

// TestPrecheckKeypointQuality_Blocks 空引用/背景标题/完全重复被程序拦截。
func TestPrecheckKeypointQuality_Blocks(t *testing.T) {
	cases := []struct {
		name  string
		mut   func(i *KeypointQualityInput)
		right string
	}{
		{"引用不存在", func(i *KeypointQualityInput) { i.Citations = []string{"seg-9999"} }, "seg-9999"},
		{"完全重复", func(i *KeypointQualityInput) { i.SameSourceTexts = []string{i.Content} }, "完全重复"},
	}
	for _, tc := range cases {
		i := qualityInput()
		tc.mut(&i)
		v, final := PrecheckKeypointQuality(i)
		if !final || v.Decision != models.KPQualityInvalid {
			t.Errorf("%s: 应程序终审为 invalid: %+v final=%v", tc.name, v, final)
			continue
		}
		if len(v.Reasons) == 0 || !strings.Contains(strings.Join(v.Reasons, ","), tc.right) {
			t.Errorf("%s: 原因应含 %q: %v", tc.name, tc.right, v.Reasons)
		}
	}
	// 空内容
	i := qualityInput()
	i.Content = "  "
	if v, final := PrecheckKeypointQuality(i); !final || v.Decision != models.KPQualityInvalid {
		t.Errorf("空内容应终审 invalid: %+v %v", v, final)
	}
	// 背景标题启发式：过短且无说明
	i2 := qualityInput()
	i2.Content = "第一章"
	if v, final := PrecheckKeypointQuality(i2); !final || v.Decision != models.KPQualityInvalid {
		t.Errorf("背景标题应终审 invalid: %+v %v", v, final)
	}
	// 具体且有依据的观点：不终审，交给独立判定
	if _, final := PrecheckKeypointQuality(qualityInput()); final {
		t.Error("有依据的具体观点不应被程序拦截")
	}
}

// TestGroqAssessKeypointQuality 独立判定解析与错误透传（模型不可用不伪装通过）。
func TestGroqAssessKeypointQuality(t *testing.T) {
	g := NewGroqProvider("k")
	g.chatCompleteFn = func([]map[string]string, string) (string, int, error) {
		return `{"decision":"needs_review","reasons":["原文只提到相关性，观点写成因果"]}`, 200, nil
	}
	v, usage, err := g.AssessKeypointQuality(context.Background(), qualityInput())
	if err != nil {
		t.Fatal(err)
	}
	if v.Decision != models.KPQualityNeedsReview || len(v.Reasons) != 1 || usage.InputUnits != 0 {
		t.Fatalf("判定解析不符: %+v %+v", v, usage)
	}
	g.chatCompleteFn = func([]map[string]string, string) (string, int, error) {
		return "", 0, context.DeadlineExceeded
	}
	if _, _, err := g.AssessKeypointQuality(context.Background(), qualityInput()); err == nil {
		t.Fatal("模型不可用应返回错误（不伪装通过）")
	}
	g.chatCompleteFn = func([]map[string]string, string) (string, int, error) {
		return `{"decision":"bogus"}`, 200, nil
	}
	if _, _, err := g.AssessKeypointQuality(context.Background(), qualityInput()); err == nil {
		t.Fatal("非法结论应报错")
	}
}
