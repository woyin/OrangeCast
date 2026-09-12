package queue

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
)

// countingHighlight 统计远端生成调用次数（验证断点复用不重复调用）。
type countingHighlight struct {
	set   provider.HighlightSet
	err   error
	calls int
}

func (f *countingHighlight) GenerateHighlights(segments []provider.Segment) (*provider.HighlightSet, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	out := f.set
	return &out, nil
}
func (f *countingHighlight) Name() string { return "fake" }

func validHighlightSet(segments []provider.Segment) provider.HighlightSet {
	hs := provider.HighlightSet{}
	if len(segments) > 0 {
		hs.Highlights = []provider.Highlight{{
			ID:        "hl-1",
			Gist:      "值得听的开场",
			Citations: []string{segments[0].ID},
		}}
	}
	return hs
}

// TestHighlightJob_EndToEnd 独立高光任务：冻结版本素材 → 生成校验 → 不可变版本 →
// 结果落库；产物记录实际模型。
func TestHighlightJob_EndToEnd(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)
	seedDigestTranscript(t, s, models.SourceEpisode, sourceID)

	hl := &countingHighlight{set: validHighlightSet([]provider.Segment{
		{ID: "seg-0001", Start: 0, End: 30, Text: "本期讨论主权基金的配置变化"},
	})}
	w.bundleFor = func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{Highlight: hl}, nil
	}
	job, err := s.EnqueueHighlightJob(ctx, models.SourceEpisode, sourceID)
	if err != nil || job == nil {
		t.Fatalf("入队失败: %v %v", job, err)
	}
	// 快照冻结版本 1
	exec, _ := s.GetJobExecution(ctx, job.ID)
	if exec.InputSnapshotJSON == "" || !contains(exec.InputSnapshotJSON, `"transcript_version":1`) {
		t.Fatalf("入队应冻结转录版本: %s", exec.InputSnapshotJSON)
	}
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatalf("高光任务失败: %v", err)
	}
	if hl.calls != 1 {
		t.Fatalf("远端生成应恰好调用一次: %d", hl.calls)
	}
	version, err := s.GetCurrentVersion(ctx, models.SourceEpisode, sourceID, store.KindHighlight)
	if err != nil || version.Version != 1 {
		t.Fatalf("高光版本未落库: %v", err)
	}
	// 结果已持久化（恢复可复用）
	exec, _ = s.GetJobExecution(ctx, job.ID)
	if exec.ResultState != models.JobResultComplete {
		t.Fatalf("结果应 complete: %+v", exec)
	}
	// 同意图重复入队 → 活跃期已结束（成功），重新入队生成新任务（显式重试语义）
	job2, err := s.EnqueueHighlightJob(ctx, models.SourceEpisode, sourceID)
	if err != nil || job2 == nil || job2.ID == job.ID {
		t.Fatalf("成功后同版本重试应可再次入队: %v %v", job2, err)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (haystack == needle || len(needle) == 0 ||
		(len(haystack) > 0 && indexOf(haystack, needle) >= 0))
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// TestHighlightJob_CheckpointReusesGeneration 生成已落断点、持久化前失败：
// 重试从断点复用候选集，不重复调用远端模型。
func TestHighlightJob_CheckpointReusesGeneration(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)
	seedDigestTranscript(t, s, models.SourceEpisode, sourceID)

	hl := &countingHighlight{set: validHighlightSet([]provider.Segment{
		{ID: "seg-0001", Start: 0, End: 30, Text: "本期讨论主权基金的配置变化"},
	})}
	w.bundleFor = func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{Highlight: hl}, nil
	}
	job, err := s.EnqueueHighlightJob(ctx, models.SourceEpisode, sourceID)
	if err != nil || job == nil {
		t.Fatal(err)
	}
	// 领取并注入"已生成"断点，模拟生成后、持久化前进程中断。
	if _, err := s.MarkJobRunning(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	generated := validHighlightSet([]provider.Segment{{ID: "seg-0001", Start: 0, End: 30, Text: "本期讨论主权基金的配置变化"}})
	cpData, _ := json.Marshal(highlightCheckpoint{Stage: "generated", GeneratedSet: &generated})
	if err := s.SaveJobCheckpoint(ctx, job.ID, string(cpData)); err != nil {
		t.Fatal(err)
	}
	if err := s.ResetRunningOnStartup(ctx); err != nil {
		t.Fatal(err)
	}
	// 重试：候选集来自断点，远端零调用。
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatalf("断点重试失败: %v", err)
	}
	if hl.calls != 0 {
		t.Fatalf("断点复用不得重复调用远端: %d", hl.calls)
	}
	if _, err := s.GetCurrentVersion(ctx, models.SourceEpisode, sourceID, store.KindHighlight); err != nil {
		t.Fatalf("断点重试应完成持久化: %v", err)
	}
}

// TestHighlightJob_FailureDoesNotTouchCard 高光任务失败不回抹已完成的卡片。
func TestHighlightJob_FailureDoesNotTouchCard(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)
	seedDigestTranscript(t, s, models.SourceEpisode, sourceID)

	w.bundleFor = func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{Highlight: &countingHighlight{err: errors.New("tts down")}}, nil
	}
	job, err := s.EnqueueHighlightJob(ctx, models.SourceEpisode, sourceID)
	if err != nil {
		t.Fatal(err)
	}
	// 预置一个已完成的卡片版本（版本 1；job_id 外键借用本任务行）。
	cardPayload := `{"title":"T","summary":{"text":"S","citations":["seg-0001"]},"keyPoints":[],"chapters":[],"quotes":[],"tags":[]}`
	if _, err := s.CreateArtifactVersion(ctx, models.SourceEpisode, sourceID, store.KindKnowledgeCard, "fake", "m", "1", job.ID, cardPayload); err != nil {
		t.Fatal(err)
	}
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatalf("失败任务不应报周期错误: %v", err)
	}
	jobs, _ := s.ListRecentCompleted(ctx, 5)
	failed := false
	for _, j := range jobs {
		if j.ID == job.ID && j.Status == models.StatusFailed {
			failed = true
		}
	}
	if !failed {
		t.Fatalf("高光任务应失败: %+v", jobs)
	}
	// 卡片版本原样保留（不新增、不删除）。
	var n int
	_ = s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM artifact_versions WHERE source_id=? AND kind='knowledge_card'`, sourceID).Scan(&n)
	if n != 1 {
		t.Fatalf("卡片版本不应受高光失败影响: %d", n)
	}
}

// TestEnqueueHighlightJob_RequiresTranscript 未转录显式拒绝。
func TestEnqueueHighlightJob_RequiresTranscript(t *testing.T) {
	s, _ := newTestWorker(t)
	sourceID := seedEpisode(t, s)
	if _, err := s.EnqueueHighlightJob(t.Context(), models.SourceEpisode, sourceID); !errors.Is(err, store.ErrInvalidEditorialState) {
		t.Fatalf("未转录应显式拒绝: %v", err)
	}
}
