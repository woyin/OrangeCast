package evalset

import (
	"errors"
	"strings"
	"testing"

	"github.com/woyin/orangecast/internal/provider"
)

// TestReferenceCheckSamples_Integrity 验证样本集自身的完整性：
// 3 放行 + 3 挡住，每个样本都有 Segments/Question/Answer/ExpectRelated。
// 这是 ReferenceCheck 评测的"红测试"基底（ADR-0018 R3）；校验器实现需通过这些样本。
func TestReferenceCheckSamples_Integrity(t *testing.T) {
	if len(ReferenceCheckSamples) != 6 {
		t.Fatalf("应有 6 个 ReferenceCheck 样本（3 放行 + 3 挡住），实际 %d", len(ReferenceCheckSamples))
	}
	var pass, block int
	for _, smp := range ReferenceCheckSamples {
		if len(smp.Segments) == 0 {
			t.Errorf("%s 缺少参考片段", smp.ID)
		}
		if smp.Question == "" || smp.Answer == "" {
			t.Errorf("%s 缺少 question/answer", smp.ID)
		}
		if smp.ExpectRelated {
			pass++
		} else {
			block++
		}
	}
	if pass != 3 || block != 3 {
		t.Errorf("应有 3 放行 + 3 挡住，实际 %d 放行 + %d 挡住", pass, block)
	}
}

// alwaysMatch 校验器：总是返回 Related=true（不区分样本），用于制造与挡住样本的差异。
type alwaysMatch struct{}

func (alwaysMatch) Name() string { return "always-match" }
func (alwaysMatch) CheckReference(question, answer string, segs []provider.Segment) (provider.ReferenceCheckResult, error) {
	return provider.ReferenceCheckResult{Related: true}, nil
}

// TestCheckReferenceSamples_AllMatch 验证：校验器全部正确时返回空 issue 列表。
func TestCheckReferenceSamples_AllMatch(t *testing.T) {
	issues := CheckReferenceSamples(exactChecker{})
	if len(issues) != 0 {
		t.Fatalf("全对校验器应无 issue，实际 %d 条: %+v", len(issues), issues)
	}
}

// TestCheckReferenceSamples_ReportsDifferences 验证：校验器与期望不符时逐条报告。
func TestCheckReferenceSamples_ReportsDifferences(t *testing.T) {
	issues := CheckReferenceSamples(alwaysMatch{})
	if len(issues) == 0 {
		t.Fatal("恒真校验器应报告挡住样本的差异")
	}
	// alwaysMatch 恒返回 Related=true；3 个挡住样本期望 false → 应报告这 3 条。
	if len(issues) != 3 {
		t.Fatalf("应报告 3 条挡住样本差异，实际 %d", len(issues))
	}
	for _, iss := range issues {
		if iss.Got != true || iss.Want != false {
			t.Errorf("差异应 Got=true Want=false，实际 %+v", iss)
		}
	}
}

// TestCheckReferenceSamples_NilChecker 验证：nil 校验器返回错误 issue 而非 panic。
func TestCheckReferenceSamples_NilChecker(t *testing.T) {
	issues := CheckReferenceSamples(nil)
	if len(issues) != 1 || issues[0].SampleID != "(nil-checker)" {
		t.Fatalf("nil 校验器应报告 (nil-checker)，实际 %+v", issues)
	}
}

// TestCheckReferenceSamples_CheckerError 验证：校验器返回错误时逐条报告"校验报错"。
func TestCheckReferenceSamples_CheckerError(t *testing.T) {
	issues := CheckReferenceSamples(errChecker{})
	if len(issues) == 0 {
		t.Fatal("报错校验器应报告 issue")
	}
	wantCount := len(ReferenceCheckSamples)
	if len(issues) != wantCount {
		t.Fatalf("报错校验器应对每个样本报告一条，期望 %d 实际 %d", wantCount, len(issues))
	}
	for _, iss := range issues {
		if iss.Got != false {
			t.Errorf("报错 issue 应 Got=false，实际 %+v", iss)
		}
		if !strings.Contains(iss.Reason, "校验报错") {
			t.Errorf("报错 issue 应含原因，实际 %+v", iss)
		}
	}
}

// errChecker 恒返回错误，用于覆盖 CheckReferenceSamples 的报错分支。
type errChecker struct{}

func (errChecker) Name() string { return "err" }
func (errChecker) CheckReference(question, answer string, segs []provider.Segment) (provider.ReferenceCheckResult, error) {
	return provider.ReferenceCheckResult{}, errReferenceCheck
}

var errReferenceCheck = errors.New("reference check failed")

// exactChecker 严格按样本期望返回相关性（总是正确）。
type exactChecker struct{}

func (exactChecker) Name() string { return "exact" }
func (exactChecker) CheckReference(question, answer string, segs []provider.Segment) (provider.ReferenceCheckResult, error) {
	// 按 answer 匹配样本并返回其期望相关性（接口不传 ID，故用 answer 关联）。
	for _, smp := range ReferenceCheckSamples {
		if smp.Answer == answer {
			return provider.ReferenceCheckResult{Related: smp.ExpectRelated}, nil
		}
	}
	return provider.ReferenceCheckResult{Related: true}, nil
}
