package evalset

import "testing"

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
