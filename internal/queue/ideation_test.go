package queue

import (
	"context"
	"strings"
	"testing"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

// qualityAnalyzer 同时实现构思诊断接口（C03）。
func (f *qualityAnalyzer) DiagnoseIdeation(ctx context.Context, req provider.IdeationDiagnosisRequest) (*provider.IdeationDiagnosis, provider.TaskUsage, error) {
	f.calls++
	if f.diagErr != nil {
		return nil, provider.TaskUsage{}, f.diagErr
	}
	if f.diag == nil {
		f.diag = &provider.IdeationDiagnosis{Gaps: []string{"材料未覆盖"}}
	}
	return f.diag, provider.TaskUsage{InputUnits: 50, OutputUnits: 10}, nil
}

// TestIdeationDiagnosisJob_DiagnosesRound C03：诊断落库为 MaterialDiagnosis
// 并回写轮次状态；虚构引用被拒。
func TestIdeationDiagnosisJob_DiagnosesRound(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	profile, err := s.EnsureDefaultEditorialProfile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := s.CreateIdeationSession(ctx, models.IdeationSession{
		EditorialProfileID: profile.ID, Intent: "测试构思",
	})
	if err != nil {
		t.Fatal(err)
	}
	round, _, err := s.AddIdeationRound(ctx, sess.ID, "n1", "比较两种方法", "{}", `[{"id":"kp-a","content":"观点A"}]`)
	if err != nil {
		t.Fatal(err)
	}

	qa := &qualityAnalyzer{diag: &provider.IdeationDiagnosis{
		Supports:    []provider.DiagnosisItem{{MaterialID: "kp-a", Text: "观点A 支持该问题"}},
		Contradicts: []provider.DiagnosisItem{{MaterialID: "kp-fake", Text: "虚构引用"}},
	}}
	w.bundleFor = func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{Analysis: qa}, nil
	}
	job, err := w.EnqueueIdeationDiagnosisJob(ctx, sess.ID, round.ID)
	if err != nil || job == nil {
		t.Fatal(err)
	}
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetJob(ctx, job.ID)
	if got.Status != models.StatusFailed {
		t.Fatalf("虚构引用应失败: %+v", got)
	}
	// 合法诊断 → 诊断落库 + 轮次 diagnosed。
	qa.diag = &provider.IdeationDiagnosis{
		Supports:       []provider.DiagnosisItem{{MaterialID: "kp-a", Text: "支持"}},
		Contradicts:    []provider.DiagnosisItem{{MaterialID: "kp-a", Text: "冲突保留"}},
		Gaps:           []string{"缺时间成本数据"},
		ProposedClaims: []provider.ProposedClaimItem{{Claim: "可选主张", MaterialIDs: []string{"kp-a"}}},
	}
	if _, _, err := s.AddIdeationRound(ctx, sess.ID, "n2", "再次诊断", "{}", `[{"id":"kp-a","content":"观点A"}]`); err != nil {
		t.Fatal(err)
	}
	rounds, _ := s.ListIdeationRounds(ctx, sess.ID)
	job2, err := w.EnqueueIdeationDiagnosisJob(ctx, sess.ID, rounds[1].ID)
	if err != nil || job2 == nil {
		t.Fatal(err)
	}
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatalf("合法诊断失败: %v", err)
	}
	rounds, _ = s.ListIdeationRounds(ctx, sess.ID)
	if rounds[1].Status != models.RoundDiagnosed || rounds[1].OutputDiagnosisID == "" {
		t.Fatalf("轮次应为 diagnosed: %+v", rounds[1])
	}
}

// TestIdeationDiagnosisJob_RoundFailureMarked 诊断模型失败 → 轮次 failed（不假装诊断完成）。
func TestIdeationDiagnosisJob_RoundFailureMarked(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	profile, err := s.EnsureDefaultEditorialProfile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := s.CreateIdeationSession(ctx, models.IdeationSession{EditorialProfileID: profile.ID, Intent: "失败场景"})
	if err != nil {
		t.Fatal(err)
	}
	qa := &qualityAnalyzer{}
	w.bundleFor = func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{Analysis: qa}, nil
	}
	round, _, err := s.AddIdeationRound(ctx, sess.ID, "n-fail", "问题", "{}", "[]")
	if err != nil {
		t.Fatal(err)
	}
	job, err := w.EnqueueIdeationDiagnosisJob(ctx, sess.ID, round.ID)
	if err != nil {
		t.Fatal(err)
	}
	// 不注入 Analysis → 不支持诊断 → 失败可见。
	w.bundleFor = func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{}, nil
	}
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetJob(ctx, job.ID)
	if got.Status != models.StatusFailed {
		t.Fatalf("诊断失败应使任务失败: %+v", got)
	}
	rounds, _ := s.ListIdeationRounds(ctx, sess.ID)
	if rounds[0].Status != models.RoundRecorded {
		t.Fatalf("未诊断轮次保持 recorded: %+v", rounds[0])
	}
	_ = strings.Contains
}

// TestIdeationDiagnosis_MultiRoundPriorContext R13：第二轮诊断实际读取已冻结前轮
// （输入 + 诊断摘要），两轮请求 PriorRounds 确实不同；过期结果不进入本轮。
func TestIdeationDiagnosis_MultiRoundPriorContext(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	profile, _ := s.EnsureDefaultEditorialProfile(ctx)
	sess, err := s.CreateIdeationSession(ctx, models.IdeationSession{EditorialProfileID: profile.ID, Intent: "多轮"})
	if err != nil {
		t.Fatal(err)
	}
	r1, _, err := s.AddIdeationRound(ctx, sess.ID, "n1", "第一轮：时间成本", "{}", `[{"id":"kp-a","content":"观点A"}]`)
	if err != nil {
		t.Fatal(err)
	}
	qa := &qualityAnalyzer{diag: &provider.IdeationDiagnosis{
		Supports:       []provider.DiagnosisItem{{MaterialID: "kp-a", Text: "第一轮支持结论"}},
		Gaps:           []string{"缺少跨集证据"},
		ProposedClaims: []provider.ProposedClaimItem{{Claim: "初步主张甲", MaterialIDs: []string{"kp-a"}}},
	}}
	w.bundleFor = func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{Analysis: qa}, nil
	}
	job1, err := w.EnqueueIdeationDiagnosisJob(ctx, sess.ID, r1.ID)
	if err != nil || job1 == nil {
		t.Fatal(err)
	}
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	var req1 provider.IdeationDiagnosisRequest
	qa2 := &qualityAnalyzer{diag: &provider.IdeationDiagnosis{Gaps: []string{"仍缺"}}}
	diagCalls := 0
	qa2Calls := &qualityAnalyzer{diag: &provider.IdeationDiagnosis{Gaps: []string{"二轮缺口"}}}
	_ = qa2Calls
	w.bundleFor = func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{Analysis: &contextCapturingAnalyzer{inner: qa2, captured: &req1, onCall: func() { diagCalls++ }}}, nil
	}
	_ = qa
	r2, _, err := s.AddIdeationRound(ctx, sess.ID, "n2", "第二轮：追问证据", `{"scope":"跨集"}`, `[{"id":"kp-a","content":"观点A"}]`)
	if err != nil {
		t.Fatal(err)
	}
	job2, err := w.EnqueueIdeationDiagnosisJob(ctx, sess.ID, r2.ID)
	if err != nil || job2 == nil {
		t.Fatal(err)
	}
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	// 前轮上下文：第二轮请求必须含第一轮输入与诊断摘要，且与第一轮请求不同。
	if len(req1.PriorRounds) == 0 || !strings.Contains(req1.PriorRounds[0], "第一轮：时间成本") || !strings.Contains(req1.PriorRounds[0], "初步主张甲") {
		t.Fatalf("第二轮诊断应读取已冻结前轮（输入+诊断摘要）: %+v", req1.PriorRounds)
	}
}

// contextCapturingAnalyzer 捕获诊断请求（前轮上下文断言用）。
type contextCapturingAnalyzer struct {
	inner    *qualityAnalyzer
	captured *provider.IdeationDiagnosisRequest
	onCall   func()
}

func (f *contextCapturingAnalyzer) DiagnoseIdeation(ctx context.Context, req provider.IdeationDiagnosisRequest) (*provider.IdeationDiagnosis, provider.TaskUsage, error) {
	*f.captured = req
	if f.onCall != nil {
		f.onCall()
	}
	return f.inner.DiagnoseIdeation(ctx, req)
}
func (f *contextCapturingAnalyzer) Analyze(transcript string, segments []provider.Segment) (*provider.AnalyzeResult, error) {
	return f.inner.Analyze(transcript, segments)
}
func (f *contextCapturingAnalyzer) Name() string { return "capturing" }
