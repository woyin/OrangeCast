package queue

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
)

// completeSeedJobs 终结 seedCurrentHighlight 为满足外键而遗留的排队任务，
// 避免其被 ProcessOne 先行领取。
func completeSeedJobs(t *testing.T, s *store.Store) {
	t.Helper()
	jobs, err := s.ListQueuedOrRunning(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, j := range jobs {
		if _, err := s.MarkJobRunning(context.Background(), j.ID); err != nil {
			t.Fatal(err)
		}
		if err := s.MarkJobSucceeded(context.Background(), j.ID); err != nil {
			t.Fatal(err)
		}
	}
}

// writeTinyWav 写一个真实可解析的极小 WAV（0.05s 单声道 8kHz PCM16），
// 供 ffprobe 校验时长（B07 发布前校验）。
func writeTinyWav(t *testing.T, path string) {
	t.Helper()
	dataSize := uint32(800) // 800 字节 ≈ 0.05s @8kHz 16bit 单声道
	buf := make([]byte, 0, 44+int(dataSize))
	buf = append(buf, "RIFF"...)
	buf = append(buf, byte(36+dataSize), 0, 0, 0)
	buf = append(buf, "WAVE"...)
	buf = append(buf, "fmt "...)
	buf = append(buf, 16, 0, 0, 0)      // fmt chunk 大小
	buf = append(buf, 1, 0)             // PCM
	buf = append(buf, 1, 0)             // 单声道
	buf = append(buf, 0x40, 0x1f, 0, 0) // 8000Hz
	buf = append(buf, 0, 0x7d, 0, 0)    // 字节率 16000
	buf = append(buf, 2, 0)             // 块对齐
	buf = append(buf, 16, 0)            // 位深
	buf = append(buf, "data"...)
	buf = append(buf, byte(dataSize), byte(dataSize>>8), byte(dataSize>>16), byte(dataSize>>24))
	buf = append(buf, make([]byte, dataSize)...)
	if err := os.WriteFile(path, buf, 0o644); err != nil {
		t.Fatal(err)
	}
}

// taskNarration 计数型 fake：可指定写入真实 WAV 或垃圾字节。
type taskNarration struct {
	available bool
	synthErr  error
	garbage   bool
	calls     int
}

func (f *taskNarration) Synthesize(text, voice, outPath string) (*provider.NarrationResult, error) {
	f.calls++
	if f.synthErr != nil {
		return nil, f.synthErr
	}
	if f.garbage {
		_ = os.WriteFile(outPath, []byte("half-written"), 0o644)
		return &provider.NarrationResult{AudioPath: outPath, CharCount: 5, Voice: "v", Model: "kokoro-82m"}, nil
	}
	writeTinyWav(&testing.T{}, outPath)
	return &provider.NarrationResult{AudioPath: outPath, CharCount: len([]rune(text)), Voice: "af_heart", Model: "kokoro-82m"}, nil
}
func (f *taskNarration) Available() bool { return f.available }
func (f *taskNarration) Name() string    { return "kokoro" }

func twoHighlights() *provider.HighlightSet {
	return &provider.HighlightSet{Highlights: []provider.Highlight{
		{ID: "hl-a", Gist: "开场值得听", Citations: []string{"seg-0001"}},
		{ID: "hl-b", Gist: "结论在这", Citations: []string{"seg-0002"}},
	}}
}

// TestNarrationTask_EndToEnd 冻结高光版本 → 每段合成 → 缓存 key 落库 → 结果完整。
func TestNarrationTask_EndToEnd(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)
	seedCurrentHighlight(t, s, models.SourceEpisode, sourceID, twoHighlights())
	completeSeedJobs(t, s)

	nar := &taskNarration{available: true}
	w.bundleFor = func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{Narration: nar}, nil
	}
	job, err := w.EnqueueNarrationJob(ctx, models.SourceEpisode, sourceID, "", "zh")
	if err != nil || job == nil {
		t.Fatalf("入队失败: %v %v", job, err)
	}
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatalf("解说任务失败: %v", err)
	}
	rows, err := s.ListCurrentNarrationsForSource(ctx, models.SourceEpisode, sourceID)
	if err != nil || len(rows) != 2 {
		t.Fatalf("应产出 2 段解说: %v %+v", err, rows)
	}
	for _, r := range rows {
		if r.CacheKey == "" {
			t.Fatalf("解说行应有缓存身份: %+v", r)
		}
	}
	exec, _ := s.GetJobExecution(ctx, job.ID)
	if exec.ResultState != models.JobResultComplete || !strings.Contains(exec.ResultJSON, `"narrated":2`) {
		t.Fatalf("结果应完整: %+v", exec)
	}
	// 无残留临时文件
	matches, _ := filepath.Glob(filepath.Join(w.narrationDir, "*.tmp"))
	if len(matches) != 0 {
		t.Fatalf("不应残留临时文件: %v", matches)
	}
}

// TestNarrationTask_CacheReuse 相同输入复用缓存；文本/音色变化生成新版本。
func TestNarrationTask_CacheReuse(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)
	seedCurrentHighlight(t, s, models.SourceEpisode, sourceID, twoHighlights())
	completeSeedJobs(t, s)

	nar := &taskNarration{available: true}
	w.bundleFor = func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{Narration: nar}, nil
	}
	if job, err := w.EnqueueNarrationJob(ctx, models.SourceEpisode, sourceID, "", "zh"); err != nil || job == nil {
		t.Fatal(err)
	} else if err := w.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	firstCalls := nar.calls
	if firstCalls != 2 {
		t.Fatalf("首次应合成 2 段: %d", firstCalls)
	}
	// 相同输入再次入队执行：全部缓存命中，零合成。
	job2, err := w.EnqueueNarrationJob(ctx, models.SourceEpisode, sourceID, "", "zh")
	if err != nil || job2 == nil {
		t.Fatal(err)
	}
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	if nar.calls != firstCalls {
		t.Fatalf("相同输入应命中缓存零合成: %d → %d", firstCalls, nar.calls)
	}
	// 音色变化：缓存 key 变化，重新合成新版本。
	job3, err := w.EnqueueNarrationJob(ctx, models.SourceEpisode, sourceID, "zh_female", "zh")
	if err != nil || job3 == nil {
		t.Fatal(err)
	}
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	if nar.calls != firstCalls+2 {
		t.Fatalf("音色变化应重新合成: %d", nar.calls)
	}
	var n int
	_ = s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM narrations WHERE source_id=?`, sourceID).Scan(&n)
	if n != 4 {
		t.Fatalf("应有 4 行（2 段 × 2 音色）: %d", n)
	}
}

// TestNarrationTask_RejectsHalfWrittenAudio 半写文件不可播放：校验失败、清理临时文件、不落库。
func TestNarrationTask_RejectsHalfWrittenAudio(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)
	seedCurrentHighlight(t, s, models.SourceEpisode, sourceID, twoHighlights())
	completeSeedJobs(t, s)

	nar := &taskNarration{available: true, garbage: true}
	w.bundleFor = func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{Narration: nar}, nil
	}
	job, _ := w.EnqueueNarrationJob(ctx, models.SourceEpisode, sourceID, "", "zh")
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM narrations WHERE source_id=?`, sourceID).Scan(&n)
	if n != 0 {
		t.Fatalf("半写音频不得落库: %d", n)
	}
	matches, _ := filepath.Glob(filepath.Join(w.narrationDir, "*.tmp"))
	if len(matches) != 0 {
		t.Fatalf("半写临时文件应被清理: %v", matches)
	}
	exec, _ := s.GetJobExecution(ctx, job.ID)
	if !strings.Contains(exec.CheckpointJSON, "partial_failure") {
		t.Fatalf("部分失败原因应可见: %+v", exec)
	}
}

// TestNarrationTask_EngineUnavailable 引擎缺失显式失败，原因可见；不产生行。
func TestNarrationTask_EngineUnavailable(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)
	seedCurrentHighlight(t, s, models.SourceEpisode, sourceID, twoHighlights())
	completeSeedJobs(t, s)

	w.bundleFor = func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{Narration: &taskNarration{available: false}}, nil
	}
	job, _ := w.EnqueueNarrationJob(ctx, models.SourceEpisode, sourceID, "", "zh")
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetJob(ctx, job.ID)
	if got.Status != models.StatusFailed || got.LastError == nil || !strings.Contains(*got.LastError, "解说引擎不可用") {
		t.Fatalf("引擎缺失应显式失败: %+v", got)
	}
}

// TestNarrationTask_DoesNotRerunHighlights 解说重试不重跑高光：高光版本数不变。
func TestNarrationTask_DoesNotRerunHighlights(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)
	seedCurrentHighlight(t, s, models.SourceEpisode, sourceID, twoHighlights())
	completeSeedJobs(t, s)

	nar := &taskNarration{available: true}
	w.bundleFor = func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{Narration: nar}, nil
	}
	for i := 0; i < 2; i++ {
		job, err := w.EnqueueNarrationJob(ctx, models.SourceEpisode, sourceID, "", "zh")
		if err != nil {
			t.Fatal(err)
		}
		if job == nil {
			continue
		}
		if err := w.ProcessOne(ctx); err != nil {
			t.Fatal(err)
		}
	}
	var n int
	_ = s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM artifact_versions WHERE source_id=? AND kind='highlight'`, sourceID).Scan(&n)
	if n != 1 {
		t.Fatalf("解说执行不得重跑高光: %d", n)
	}
}
