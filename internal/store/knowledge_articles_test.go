package store

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

func knowledgeStoreFixture(t *testing.T) (*Store, string, string, string) {
	t.Helper()
	s := newTestStore(t)
	ctx := t.Context()
	ep := seedEpisodeForArtifact(t, s)
	job, err := s.EnqueueJob(ctx, models.SourceEpisode, ep, models.JobTranscribe)
	if err != nil {
		t.Fatal(err)
	}
	version, err := s.CreateArtifactVersion(ctx, models.SourceEpisode, ep, KindTranscript, "test", "test", "1", job.ID, `{"segments":[{"id":"seg-1","start":12,"end":20,"text":"保留来源上下文可以避免误读。"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetCurrentVersion(ctx, models.SourceEpisode, ep, KindTranscript, version); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE processing_jobs SET status='succeeded'`); err != nil {
		t.Fatal(err)
	}
	kp, err := s.CreateManualKeyPoint(ctx, KeyPointRow{SourceType: models.SourceEpisode, SourceID: ep, SourceTitle: "来源", TimeStart: 12, TimeEnd: 20, Content: "保留上下文避免误读", Description: "理解观点前先检查来源", CitationsJSON: `["seg-1"]`})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetKeyPointQualityStatus(ctx, kp.ID, models.KeyPointOwnerConfirmed); err != nil {
		t.Fatal(err)
	}
	note, err := s.CreateOwnerNote(ctx, models.OwnerNote{SourceType: "episode", SourceID: ep, Kind: "owner_reflection", Content: "我想把自己的理解与来源表达区分。"})
	if err != nil {
		t.Fatal(err)
	}
	profile, err := s.EnsureDefaultEditorialProfile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return s, profile.ID, ep, note.ID
}

func TestKnowledgeLibraryGroundingPoliciesAndFreshness(t *testing.T) {
	s, profile, ep, noteID := knowledgeStoreFixture(t)
	ctx := t.Context()
	req, latest, err := s.BuildKnowledgeArticleRequest(ctx, profile, "pod")
	if err != nil || len(req.Materials) != 2 || latest == "" {
		t.Fatalf("library: %v %+v", err, req)
	}
	var source provider.KnowledgeMaterial
	for _, m := range req.Materials {
		if m.Kind == "keypoint" {
			source = m
		}
	}
	if source.Evidence == "" || source.SnapshotID == "" || source.Position != 12 {
		t.Fatalf("actual evidence/position missing: %+v", source)
	}
	if err := s.CheckKnowledgeMaterials(ctx, profile, "pod", req.Materials); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSourceProductionPolicy(ctx, models.SourceEpisode, ep, "internal", models.ModelDataLocalOnly); err != nil {
		t.Fatal(err)
	}
	filtered, _, err := s.BuildKnowledgeArticleRequest(ctx, profile, "pod")
	if err != nil || len(filtered.Materials) != 0 {
		t.Fatalf("policy leak: %v %+v", err, filtered)
	}
	if s.CheckKnowledgeMaterials(ctx, profile, "pod", req.Materials) == nil {
		t.Fatal("frozen input bypassed policy")
	}
	if err := s.SetSourceProductionPolicy(ctx, models.SourceEpisode, ep, "internal", models.ModelDataExternalAllowed); err != nil {
		t.Fatal(err)
	}
	n, err := s.GetOwnerNote(ctx, noteID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateOwnerNote(ctx, noteID, "更新后的个人理解", "[]", "[]", n.Revision); err != nil {
		t.Fatal(err)
	}
	if s.CheckKnowledgeMaterials(ctx, profile, "pod", req.Materials) == nil {
		t.Fatal("changed note accepted")
	}
	if err := s.SetKeyPointQualityStatus(ctx, source.ID, models.KeyPointQualityDismissed); err != nil {
		t.Fatal(err)
	}
	filtered, _, err = s.BuildKnowledgeArticleRequest(ctx, profile, "pod")
	if err != nil || len(filtered.Materials) != 1 || filtered.Materials[0].Kind != "owner_reflection" {
		t.Fatalf("dismissed keypoint leaked: %v %+v", err, filtered)
	}
}

func TestKnowledgeAdmissionConcurrentIdempotenceAndBackup(t *testing.T) {
	s, profile, ep, _ := knowledgeStoreFixture(t)
	ctx := t.Context()
	req, _, err := s.BuildKnowledgeArticleRequest(ctx, profile, "pod")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	created := 0
	var id string
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, new, err := s.ReserveKnowledgeArticle(ctx, profile, "pod", "model", req, false)
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			defer mu.Unlock()
			if id != "" && id != v.ID {
				t.Error("multiple articles")
			}
			id = v.ID
			if new {
				created++
			}
		}()
	}
	wg.Wait()
	if created != 1 {
		t.Fatalf("created=%d", created)
	}
	other := req
	other.Style = "new style"
	if _, _, err := s.ReserveKnowledgeArticle(ctx, profile, "pod", "model", other, false); err == nil {
		t.Fatal("in-flight backpressure bypassed")
	}
	settings, err := s.GetKnowledgeArticleSettings(ctx)
	if err != nil || settings.Enabled {
		t.Fatal("automation must start disabled")
	}
	settings.Enabled = true
	settings.DebounceMinutes = 0
	if err := s.SetKnowledgeArticleSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "restored.db")
	if err := ConsistencyBackup(ctx, s.DB, path); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	v, err := restored.GetKnowledgeArticle(ctx, id)
	if err != nil || v.InputJSON == "" {
		t.Fatalf("restore article: %v", err)
	}
	rs, err := restored.GetKnowledgeArticleSettings(ctx)
	if err != nil || !rs.Enabled {
		t.Fatal("restore settings")
	}
	jobs, err := restored.ListQueuedOrRunning(ctx)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("restored stages: %v %d", err, len(jobs))
	}
	if err := s.DeleteSourceRows(ctx, models.SourceEpisode, ep); err != nil {
		t.Fatal(err)
	}
	articles, err := s.ListKnowledgeArticles(ctx)
	if err != nil || len(articles) != 0 {
		t.Fatal("purged source left derivative text")
	}
	jobs, err = s.ListQueuedOrRunning(ctx)
	if err != nil || len(jobs) != 0 {
		t.Fatal("purged source left runnable article")
	}
}

func TestKnowledgeStageCommitAndExplicitRetry(t *testing.T) {
	s, profile, _, _ := knowledgeStoreFixture(t)
	ctx := t.Context()
	req, _, err := s.BuildKnowledgeArticleRequest(ctx, profile, "pod")
	if err != nil {
		t.Fatal(err)
	}
	article, _, err := s.ReserveKnowledgeArticle(ctx, profile, "pod", "model", req, false)
	if err != nil {
		t.Fatal(err)
	}
	jobs, err := s.ListQueuedOrRunning(ctx)
	if err != nil || len(jobs) != 1 {
		t.Fatal(err)
	}
	job := jobs[0]
	if ok, err := s.MarkJobRunning(ctx, job.ID); err != nil || !ok {
		t.Fatal(err)
	}
	exec, _ := s.GetJobExecution(ctx, job.ID)
	var input KnowledgeStageInput
	if err := json.Unmarshal([]byte(exec.InputSnapshotJSON), &input); err != nil {
		t.Fatal(err)
	}
	result := &provider.KnowledgeArticleResult{Topics: []provider.KnowledgeTopic{{Title: "来源与个人理解", Thesis: "先厘清来源再综合", Question: "怎样形成可靠理解？", Outline: "来源、理解、综合", Rationale: "已有来源和笔记", Sufficient: true, Score: 90, MaterialIDs: []string{req.Materials[0].ID, req.Materials[1].ID}}}}
	if err := s.CommitKnowledgeStage(ctx, job, input, result); err != nil {
		t.Fatal(err)
	}
	if err := s.CommitKnowledgeStage(ctx, job, input, result); err != nil {
		t.Fatal("completed result must be idempotent", err)
	}
	if err := s.MarkJobSucceeded(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	v, _ := s.GetKnowledgeArticle(ctx, article.ID)
	if v.Status != "write" {
		t.Fatalf("next state=%s", v.Status)
	}
	jobs, _ = s.ListQueuedOrRunning(ctx)
	if len(jobs) != 1 {
		t.Fatalf("successor count %d", len(jobs))
	}
	next := jobs[0]
	s.MarkJobRunning(ctx, next.ID)
	s.MarkJobFailed(ctx, next.ID, "timeout")
	s.FailKnowledgeArticle(ctx, article.ID, "timeout")
	if err := s.RetryKnowledgeArticle(ctx, article.ID); err != nil {
		t.Fatal(err)
	}
	old, _ := s.GetJob(ctx, next.ID)
	if old.Status != models.StatusFailed {
		t.Fatal("retry overwrote audit")
	}
	jobs, _ = s.ListQueuedOrRunning(ctx)
	if len(jobs) != 1 || jobs[0].ID == next.ID {
		t.Fatal("retry must create new attempt")
	}
	if err := s.RetryKnowledgeArticle(ctx, article.ID); err == nil {
		t.Fatal("active retry duplicated")
	}
}

func TestKnowledgeSettingsAndDebounce(t *testing.T) {
	s := newTestStore(t)
	settings, err := s.GetKnowledgeArticleSettings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*KnowledgeArticleSettings){func(v *KnowledgeArticleSettings) { v.DailyLimit = 0 }, func(v *KnowledgeArticleSettings) { v.DailyLimit = 11 }, func(v *KnowledgeArticleSettings) { v.DebounceMinutes = -1 }, func(v *KnowledgeArticleSettings) { v.DebounceMinutes = 1441 }, func(v *KnowledgeArticleSettings) { v.Audience = "" }, func(v *KnowledgeArticleSettings) { v.Style = "" }, func(v *KnowledgeArticleSettings) { v.Style = strings.Repeat("长", 2001) }} {
		v := settings
		mutate(&v)
		if s.SetKnowledgeArticleSettings(t.Context(), v) == nil {
			t.Fatal("invalid settings accepted")
		}
	}
	now := time.Now().UTC()
	if !KnowledgeDebounceReady("", 30, now) || !KnowledgeDebounceReady("invalid", 0, now) || KnowledgeDebounceReady("invalid", 30, now) || KnowledgeDebounceReady(now.Format("2006-01-02 15:04:05"), 30, now) || !KnowledgeDebounceReady(now.Add(-time.Hour).Format("2006-01-02 15:04:05"), 30, now) {
		t.Fatal("debounce boundary")
	}
}

func TestKnowledgeStageChainAndDailyAdmission(t *testing.T) {
	for _, mode := range []string{"ready", "needs_review", "insufficient"} {
		t.Run(mode, func(t *testing.T) {
			s, profile, _, _ := knowledgeStoreFixture(t)
			ctx := t.Context()
			req, _, err := s.BuildKnowledgeArticleRequest(ctx, profile, "pod")
			if err != nil {
				t.Fatal(err)
			}
			article, _, err := s.ReserveKnowledgeArticle(ctx, profile, "pod", "model", req, true)
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 5; i++ {
				job, err := s.ClaimNextJob(ctx, "60 seconds")
				if err != nil {
					t.Fatal(err)
				}
				if job == nil {
					break
				}
				exec, _ := s.GetJobExecution(ctx, job.ID)
				var input KnowledgeStageInput
				if err := json.Unmarshal([]byte(exec.InputSnapshotJSON), &input); err != nil {
					t.Fatal(err)
				}
				result := &provider.KnowledgeArticleResult{}
				switch input.Stage {
				case "discover":
					if mode == "insufficient" {
						result.Reason = "缺少案例"
					} else {
						result.Topics = []provider.KnowledgeTopic{{Title: "可靠学习的来源边界", Thesis: "检查来源再综合解释", Question: "怎样避免误读？", Outline: "来源、解释", Score: 90, Sufficient: true, MaterialIDs: []string{req.Materials[0].ID, req.Materials[1].ID}}}
					}
				case "write", "revise":
					result.Title = "可靠学习的来源边界"
					result.Blocks = []provider.KnowledgeBlock{{Kind: "synthesis", Text: "基于来源的综合理解", MaterialIDs: []string{req.Materials[0].ID}}, {Kind: "synthesis", Text: "把个人笔记连接到学习", MaterialIDs: []string{req.Materials[1].ID}}}
				case "review", "review_final":
					passed := mode == "ready"
					result.Passed = &passed
					if !passed {
						result.Issues = []string{"说明综合边界"}
					}
				}
				if err := s.CommitKnowledgeStage(ctx, job, input, result); err != nil {
					t.Fatal(err)
				}
				if err := s.MarkJobSucceeded(ctx, job.ID); err != nil {
					t.Fatal(err)
				}
			}
			got, _ := s.GetKnowledgeArticle(ctx, article.ID)
			if got.Status != mode {
				t.Fatalf("mode=%s status=%s", mode, got.Status)
			}
			changed := req
			changed.Style = "另一种风格"
			if _, _, err := s.ReserveKnowledgeArticle(ctx, profile, "pod", "model", changed, true); err == nil {
				t.Fatal("daily automation cap bypassed")
			}
			if _, _, err := s.ReserveKnowledgeArticle(ctx, profile, "pod", "model", changed, false); err != nil {
				t.Fatal("manual action incorrectly shares automatic daily cap", err)
			}
		})
	}
}
