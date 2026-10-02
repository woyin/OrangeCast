package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
)

type writingPurposeQueueFake struct {
	requests      []provider.KnowledgeArticleRequest
	badStructure  bool
	outputOverrun bool
}

func (f *writingPurposeQueueFake) Name() string { return "pod" }
func (f *writingPurposeQueueFake) KnowledgeArticleStep(_ context.Context, req provider.KnowledgeArticleRequest) (*provider.KnowledgeArticleResult, provider.TaskUsage, error) {
	f.requests = append(f.requests, req)
	usage := provider.TaskUsage{InputUnits: 100, OutputUnits: 50}
	if f.outputOverrun && req.Stage == "write" {
		usage.OutputUnits = req.Estimate.OutputTokens + 1
	}
	ids := []string{}
	for _, m := range req.Materials {
		ids = append(ids, m.ID)
	}
	switch req.Stage {
	case "discover":
		return &provider.KnowledgeArticleResult{Topics: []provider.KnowledgeTopic{{Title: "怎样区分学习理解与来源表达", Question: "如何核对学习理解？", Thesis: "核对来源和记录理解边界", Rationale: "两份来源支持这个问题", Outline: "问题、依据、边界", MaterialIDs: ids, Score: 90, Sufficient: true}}}, usage, nil
	case "select":
		topic := *req.Topic
		topic.MaterialIDs = ids
		topic.Selection = nil
		for _, id := range ids {
			topic.Selection = append(topic.Selection, provider.KnowledgeSelection{MaterialID: id, Role: "support", Selected: true, Reason: "提供来源表达或个人理解"})
		}
		return &provider.KnowledgeArticleResult{Topics: []provider.KnowledgeTopic{topic}}, usage, nil
	case "write":
		sections := []string{"", ""}
		for _, mode := range provider.WritingModes() {
			if mode.ID == req.WritingPurpose.Mode && len(mode.Sections) > 0 {
				sections = mode.Sections
			}
		}
		blocks := []provider.KnowledgeBlock{}
		sourceIDs := []string{}
		for _, m := range req.Materials {
			if m.Kind != "owner_reflection" {
				sourceIDs = append(sourceIDs, m.ID)
			}
		}
		for _, section := range sections {
			blocks = append(blocks, provider.KnowledgeBlock{PurposeSection: section, Kind: "synthesis", Text: "依据来源核对自己的理解。缺例子或实践证据时明确材料不足，不推断效果。", MaterialIDs: sourceIDs})
		}
		if f.badStructure {
			blocks[0].PurposeSection = "invented"
		}
		return &provider.KnowledgeArticleResult{Title: req.Topic.Title, Blocks: blocks}, usage, nil
	default:
		yes := true
		return &provider.KnowledgeArticleResult{Passed: &yes}, usage, nil
	}
}

// This self-authored fixture proves routing/receipt mechanics, not model quality.
func seedWritingPurposeQueue(t *testing.T, mode, version string, batch bool) (*store.Store, *Worker, *store.KnowledgeArticleRecord, *writingPurposeQueueFake) {
	t.Helper()
	s, w := newTestWorker(t)
	ctx := t.Context()
	podcast, err := s.CreatePodcast(ctx, "https://writing-modes.test/feed", "自建来源", "", "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.MergeEpisodes(ctx, podcast.ID, []models.Episode{{GUID: "a", Title: "上下文核对", AudioURL: "https://writing-modes.test/a"}, {GUID: "b", Title: "个人理解边界", AudioURL: "https://writing-modes.test/b"}})
	if err != nil {
		t.Fatal(err)
	}
	eps, err := s.ListEpisodes(ctx, podcast.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, ep := range eps {
		job, err := s.EnqueueJob(ctx, models.SourceEpisode, ep.ID, models.JobTranscribe)
		if err != nil {
			t.Fatal(err)
		}
		v, err := s.CreateArtifactVersion(ctx, models.SourceEpisode, ep.ID, store.KindTranscript, "test", "test", "1", job.ID, `{"segments":[{"id":"seg-1","start":1,"end":8,"text":"核对来源上下文，区分个人理解与来源表达。"}]}`)
		if err != nil {
			t.Fatal(err)
		}
		if err = s.SetCurrentVersion(ctx, models.SourceEpisode, ep.ID, store.KindTranscript, v); err != nil {
			t.Fatal(err)
		}
		kp, err := s.CreateManualKeyPoint(ctx, store.KeyPointRow{SourceType: models.SourceEpisode, SourceID: ep.ID, SourceTitle: ep.Title, TimeStart: 1, TimeEnd: 8, Content: "核对来源上下文，区分个人理解与来源表达。", CitationsJSON: `["seg-1"]`})
		if err != nil {
			t.Fatal(err)
		}
		if err = s.SetKeyPointQualityStatus(ctx, kp.ID, models.KeyPointOwnerConfirmed); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = s.DB.ExecContext(ctx, `UPDATE processing_jobs SET status='succeeded'`); err != nil {
		t.Fatal(err)
	}
	prefs, err := s.GetKnowledgeArticleSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	prefs.WritingMode = mode
	if err = s.SetKnowledgeArticleSettings(ctx, prefs); err != nil {
		t.Fatal(err)
	}
	profile, err := s.EnsureDefaultEditorialProfile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	req, _, err := s.BuildKnowledgeArticleRequest(ctx, profile.ID, "pod")
	if err != nil {
		t.Fatal(err)
	}
	if version == provider.KnowledgeArticlePromptVersion {
		req.PromptVersion = version
		req.WritingPurpose = nil
	}
	if batch {
		b, err := s.EnsureKnowledgeDiscoveryBatch(ctx, profile.ID, "pod", "model", req, 0, false)
		if err != nil {
			t.Fatal(err)
		}
		req.DiscoveryBatchID = b.ID
	}
	article, _, err := s.ReserveKnowledgeArticle(ctx, profile.ID, "pod", "model", req, false)
	if err != nil {
		t.Fatal(err)
	}
	fake := &writingPurposeQueueFake{}
	w.WithBundleResolver(func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{KnowledgeArticle: fake}, nil
	})
	return s, w, article, fake
}

func TestWritingPurposeQueueFourModesFreezeActualMessagesAndEstimates(t *testing.T) {
	for _, mode := range provider.WritingModes() {
		t.Run(mode.ID, func(t *testing.T) {
			s, w, a, f := seedWritingPurposeQueue(t, mode.ID, provider.KnowledgeArticlePurposePromptVersion, true)
			for i := 0; i < 4; i++ {
				if err := w.ProcessOne(t.Context()); err != nil {
					t.Fatal(err)
				}
			}
			fresh, err := s.GetKnowledgeArticle(t.Context(), a.ID)
			if err != nil || fresh.Status != "ready" || len(f.requests) != 4 {
				t.Fatalf("journey: %+v requests=%d err=%v", fresh, len(f.requests), err)
			}
			frozen, _ := provider.FreezeWritingPurpose(mode.ID)
			for i, stage := range []string{"discover", "select", "write", "review"} {
				req := f.requests[i]
				if req.Stage != stage || req.PromptVersion != provider.KnowledgeArticlePurposePromptVersion || req.WritingPurpose == nil || *req.WritingPurpose != frozen {
					t.Fatalf("unfrozen stage %s: %+v", stage, req)
				}
				estimate, err := provider.EstimateKnowledgeRequest(req, "model")
				if err != nil || req.Estimate == nil || estimate.InputFingerprint != req.Estimate.InputFingerprint || estimate.InputTokens != req.Estimate.InputTokens {
					t.Fatal("wrong actual-message estimate", err)
				}
				system, input, err := provider.KnowledgeArticleMessages(req)
				if err != nil || !strings.Contains(system, mode.Rules) || !strings.Contains(input, frozen.Fingerprint) {
					t.Fatal("missing real mode instructions", err)
				}
			}
		})
	}
}

func TestWritingPurposeBadStructureAndOutputOverrunKeepPaidResponse(t *testing.T) {
	for _, kind := range []string{"structure", "output-overrun"} {
		t.Run(kind, func(t *testing.T) {
			s, w, a, f := seedWritingPurposeQueue(t, "explanation", provider.KnowledgeArticlePurposePromptVersion, false)
			if err := w.ProcessOne(t.Context()); err != nil {
				t.Fatal(err)
			}
			f.badStructure = kind == "structure"
			f.outputOverrun = kind == "output-overrun"
			jobs, err := s.ListQueuedOrRunning(t.Context())
			if err != nil || len(jobs) != 1 {
				t.Fatal(jobs, err)
			}
			writeJob := jobs[0]
			if err = w.ProcessOne(t.Context()); err != nil {
				t.Fatal(err)
			}
			fresh, err := s.GetKnowledgeArticle(t.Context(), a.ID)
			if err != nil || fresh.Status != "failed" || fresh.Stage != "write" || len(f.requests) != 2 {
				t.Fatal(fresh, err)
			}
			exec, err := s.GetJobExecution(t.Context(), writeJob.ID)
			if err != nil || exec.CheckpointJSON == "" {
				t.Fatal("paid response lost", err)
			}
			var cp knowledgeCheckpoint
			if err = json.Unmarshal([]byte(exec.CheckpointJSON), &cp); err != nil || cp.Result == nil || cp.Usage.OutputUnits == 0 {
				t.Fatal("checkpoint lacks result/usage", err)
			}
			var receipts, units int
			if err = s.DB.QueryRowContext(t.Context(), `SELECT COUNT(*),COALESCE(SUM(output_units),0) FROM usage_records WHERE operation='knowledge_article_write'`).Scan(&receipts, &units); err != nil || receipts != 1 || units != cp.Usage.OutputUnits {
				t.Fatal("receipt lost", receipts, units, err)
			}
			queued, err := s.ListQueuedOrRunning(t.Context())
			if err != nil || len(queued) != 0 {
				t.Fatal("invalid response advanced", queued, err)
			}
			if err = w.doKnowledgeArticle(t.Context(), writeJob, &provider.ProviderBundle{KnowledgeArticle: f}); err == nil || len(f.requests) != 2 {
				t.Fatal("bad paid response replayed", err, len(f.requests))
			}
		})
	}
}

func TestWritingPurposeLegacyV4KnownCheckpointRecoveryNeverRecalls(t *testing.T) {
	s, w, a, f := seedWritingPurposeQueue(t, "synthesis", provider.KnowledgeArticlePromptVersion, false)
	if _, err := s.DB.ExecContext(t.Context(), `CREATE TRIGGER fail_legacy_purpose_successor BEFORE INSERT ON processing_jobs WHEN NEW.intent_id LIKE '%:write' BEGIN SELECT RAISE(FAIL,'interrupted'); END`); err != nil {
		t.Fatal(err)
	}
	if err := w.ProcessOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	fresh, err := s.GetKnowledgeArticle(t.Context(), a.ID)
	if err != nil || fresh.Status != "failed" || len(f.requests) != 1 {
		t.Fatal(fresh, err)
	}
	if f.requests[0].WritingPurpose != nil || f.requests[0].PromptVersion != provider.KnowledgeArticlePromptVersion {
		t.Fatal("v4 changed")
	}
	if _, err = s.DB.ExecContext(t.Context(), `DROP TRIGGER fail_legacy_purpose_successor`); err != nil {
		t.Fatal(err)
	}
	if err = s.RetryKnowledgeArticle(t.Context(), a.ID); err != nil {
		t.Fatal(err)
	}
	if err = w.ProcessOne(t.Context()); err != nil || len(f.requests) != 1 {
		t.Fatal("known v4 response recalled", err)
	}
	var receipts int
	if err = s.DB.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM usage_records WHERE operation='knowledge_article_discover'`).Scan(&receipts); err != nil || receipts != 1 {
		t.Fatal(fmt.Sprint("duplicate receipt", receipts, err))
	}
}
