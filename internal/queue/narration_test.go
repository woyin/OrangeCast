package queue

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
)

// fakeNarration 一个可注入的 NarrationProvider（ADR-0019 测试）。
type fakeNarration struct {
	available bool
	synthErr  error
	written   []string // 捕获每次合成写盘路径
}

func (f *fakeNarration) Synthesize(text, voice, outPath string) (*provider.NarrationResult, error) {
	if f.synthErr != nil {
		return nil, f.synthErr
	}
	if err := os.WriteFile(outPath, []byte("fake-wav"), 0o644); err != nil {
		return nil, err
	}
	f.written = append(f.written, outPath)
	return &provider.NarrationResult{AudioPath: outPath, CharCount: len([]rune(text)), Voice: "af_heart", Model: "kokoro-82m"}, nil
}
func (f *fakeNarration) Available() bool { return f.available }
func (f *fakeNarration) Name() string    { return "kokoro" }

// seedSeedHighlight 直接写入一个含 2 个 Highlight 的 HighlightSet 作为当前版本。
func seedCurrentHighlight(t *testing.T, s *store.Store, sourceType models.SourceType, sourceID string, hs *provider.HighlightSet) {
	t.Helper()
	// 需要一个 job 满足 artifact_versions.job_id 外键。
	jobRow, err := s.EnqueueJob(context.Background(), sourceType, sourceID, models.JobAnalyze)
	if err != nil {
		t.Fatalf("入队 job: %v", err)
	}
	payload, _ := json.Marshal(hs)
	v, err := s.CreateArtifactVersion(context.Background(), sourceType, sourceID, store.KindHighlight,
		"groq", "llama-3.3-70b-versatile", "1", jobRow.ID, string(payload))
	if err != nil {
		t.Fatalf("创建 highlight 版本: %v", err)
	}
	if err := s.SetCurrentVersion(context.Background(), sourceType, sourceID, store.KindHighlight, v); err != nil {
		t.Fatalf("设置 highlight current: %v", err)
	}
}

// TestDoNarration_ProviderUnavailable_Skips (ADR-0019 R1)
// Narration Provider 不可用 → 不合成、不报错、narrations 表为空。
func TestDoNarration_ProviderUnavailable_Skips(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	up, _ := s.CreateUpload(ctx, "a.wav", "audio/wav", 10)
	seedCurrentHighlight(t, s, models.SourceUpload, up.ID, &provider.HighlightSet{
		Highlights: []provider.Highlight{{ID: "hl-a", Gist: "gist a", Citations: []string{"seg-0001"}}},
	})

	job := &models.ProcessingJob{ID: "j1", SourceType: models.SourceUpload, SourceID: up.ID}
	bundle := &provider.ProviderBundle{Narration: &fakeNarration{available: false}}

	if err := w.doNarration(ctx, job, bundle); err != nil {
		t.Fatalf("Provider 不可用应跳过且不报错，实际 %v", err)
	}
	all, _ := s.ListCurrentNarrationsForSource(ctx, models.SourceUpload, up.ID)
	if len(all) != 0 {
		t.Errorf("Provider 不可用时 narrations 应为空，实际 %d", len(all))
	}
}

// TestDoNarration_Succeeds_WritesTableAndFile (ADR-0019)
// Provider 可用 → 每个 Highlight 的 Gist 各合成一段 Narration，写文件 + 写表。
func TestDoNarration_Succeeds_WritesTableAndFile(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	up, _ := s.CreateUpload(ctx, "a.wav", "audio/wav", 10)
	hs := &provider.HighlightSet{Highlights: []provider.Highlight{
		{ID: "hl-a", Gist: "第一个高光解说", Citations: []string{"seg-0001"}},
		{ID: "hl-b", Gist: "第二个高光解说", Citations: []string{"seg-0002"}},
	}}
	seedCurrentHighlight(t, s, models.SourceUpload, up.ID, hs)

	fn := &fakeNarration{available: true}
	job := &models.ProcessingJob{ID: "j1", SourceType: models.SourceUpload, SourceID: up.ID}
	bundle := &provider.ProviderBundle{Narration: fn}

	if err := w.doNarration(ctx, job, bundle); err != nil {
		t.Fatalf("doNarration: %v", err)
	}
	all, err := s.ListCurrentNarrationsForSource(ctx, models.SourceUpload, up.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("应为 2 段 Narration，实际 %d", len(all))
	}
	// 每段都写了 wav 文件
	if len(fn.written) != 2 {
		t.Errorf("应合成 2 个 wav，实际 %d", len(fn.written))
	}
	for _, p := range fn.written {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("wav 文件应存在: %v", err)
		}
	}
	// 文件应落在 narrations 目录
	rel := filepath.Base(fn.written[0])
	if !strings.Contains(rel, "hl-a") && !strings.Contains(rel, "hl-b") {
		t.Errorf("文件名应含 highlight_id: %s", rel)
	}
}

// TestDoNarration_Idempotent_SkipsExisting (ADR-0019)
// 同 highlight_id 已有同 provider 的 Narration → 跳过，不重复合成。
func TestDoNarration_Idempotent_SkipsExisting(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	up, _ := s.CreateUpload(ctx, "a.wav", "audio/wav", 10)
	hs := &provider.HighlightSet{Highlights: []provider.Highlight{
		{ID: "hl-a", Gist: "gist", Citations: []string{"seg-0001"}},
	}}
	seedCurrentHighlight(t, s, models.SourceUpload, up.ID, hs)
	// 预置一条已存在的 Narration。
	if _, err := s.CreateNarration(ctx, models.SourceUpload, up.ID, "hl-a", "af_heart", "kokoro-82m", "x.wav", 1, 5, "kokoro"); err != nil {
		t.Fatal(err)
	}

	fn := &fakeNarration{available: true}
	job := &models.ProcessingJob{ID: "j1", SourceType: models.SourceUpload, SourceID: up.ID}
	bundle := &provider.ProviderBundle{Narration: fn}

	if err := w.doNarration(ctx, job, bundle); err != nil {
		t.Fatalf("doNarration: %v", err)
	}
	if len(fn.written) != 0 {
		t.Errorf("已有 Narration 应跳过合成，实际写盘 %d 个", len(fn.written))
	}
	all, _ := s.ListCurrentNarrationsForSource(ctx, models.SourceUpload, up.ID)
	if len(all) != 1 {
		t.Errorf("应保持 1 条 Narration，实际 %d", len(all))
	}
}

// TestDoNarration_PerSegmentFailure_Continues (ADR-0019 R1)
// 某段合成失败 → 跳过该段、继续其他段（不阻塞）。
func TestDoNarration_PerSegmentFailure_Continues(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	up, _ := s.CreateUpload(ctx, "a.wav", "audio/wav", 10)
	hs := &provider.HighlightSet{Highlights: []provider.Highlight{
		{ID: "hl-a", Gist: "gist a", Citations: []string{"seg-0001"}},
		{ID: "hl-b", Gist: "gist b", Citations: []string{"seg-0002"}},
	}}
	seedCurrentHighlight(t, s, models.SourceUpload, up.ID, hs)

	// 让第二个 highlight 合成失败：用 synthErr 但只对 hl-b 触发。
	fn := &fakeNarration{available: true, synthErr: nil}
	orig := fn.Synthesize
	_ = orig
	// 子类化：用一个计数版本，第二次调用报错。
	failOnSecond := &countingNarration{ninner: fn}
	job := &models.ProcessingJob{ID: "j1", SourceType: models.SourceUpload, SourceID: up.ID}
	bundle := &provider.ProviderBundle{Narration: failOnSecond}

	if err := w.doNarration(ctx, job, bundle); err != nil {
		t.Fatalf("doNarration 不应因单段失败而报错，实际 %v", err)
	}
	all, _ := s.ListCurrentNarrationsForSource(ctx, models.SourceUpload, up.ID)
	if len(all) != 1 {
		t.Errorf("应成功 1 段（另一段失败跳过），实际 %d", len(all))
	}
}

// countingNarration 包装 fakeNarration，第二次合成调用失败（模拟某段失败）。
type countingNarration struct {
	ninner *fakeNarration
	calls  int
}

func (c *countingNarration) Synthesize(text, voice, outPath string) (*provider.NarrationResult, error) {
	c.calls++
	if c.calls == 2 {
		return nil, os.ErrNotExist
	}
	return c.ninner.Synthesize(text, voice, outPath)
}
func (c *countingNarration) Available() bool { return c.ninner.Available() }
func (c *countingNarration) Name() string    { return c.ninner.Name() }
