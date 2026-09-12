package provider

import (
	"context"
	"testing"

	"github.com/woyin/orangecast/internal/models"
)

// TestFingerprintKeypoint 指纹确定性：同输入同指纹，内容/引用不同则不同。
func TestFingerprintKeypoint(t *testing.T) {
	a := FingerprintKeypoint("内容", "说明", []string{"seg-0001"})
	b := FingerprintKeypoint("内容", "说明", []string{"seg-0001"})
	c := FingerprintKeypoint("内容2", "说明", []string{"seg-0001"})
	d := FingerprintKeypoint("内容", "说明", []string{"seg-0002"})
	if a != b || a == c || a == d {
		t.Fatalf("指纹应稳定且可区分: %s %s %s %s", a, b, c, d)
	}
}

// TestOpenAIAssessKeypointQuality OpenAI 路径同契约（HTTP 假服务）。
func TestOpenAIAssessKeypointQuality(t *testing.T) {
	srv := newOpenAITestServer(t, `{"decision":"needs_review","reasons":["限定条件不足"]}`)
	defer srv.Close()
	o := NewOpenAIProvider("k").WithBaseURL(srv.URL)
	v, usage, err := o.AssessKeypointQuality(context.Background(), qualityInput())
	if err != nil {
		t.Fatal(err)
	}
	if v.Decision != models.KPQualityNeedsReview || len(v.Reasons) != 1 {
		t.Fatalf("判定不符: %+v", v)
	}
	_ = usage // 用量统计由 chatUsage 既有测试覆盖
}
