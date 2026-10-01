package queue

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
)

type knowledgeQueueFake struct {
	calls       int
	bad         bool
	failure     error
	empty       bool
	outputUnits int
}

func (f *knowledgeQueueFake) Name() string { return "pod" }
func (f *knowledgeQueueFake) KnowledgeArticleStep(_ context.Context, req provider.KnowledgeArticleRequest) (*provider.KnowledgeArticleResult, provider.TaskUsage, error) {
	f.calls++
	usage := provider.TaskUsage{InputUnits: 100, OutputUnits: 50}
	if f.outputUnits > 0 {
		usage.OutputUnits = f.outputUnits
	}
	if f.failure != nil || f.empty {
		return nil, usage, f.failure
	}
	if req.Stage == "discover" {
		ids := []string{}
		for _, m := range req.Materials {
			ids = append(ids, m.ID)
		}
		if f.bad {
			ids[0] = "invented"
		}
		return &provider.KnowledgeArticleResult{Topics: []provider.KnowledgeTopic{{Title: "个人学习如何留下理解", Thesis: "主动表达让听过的内容变成自己的理解", Question: "怎样从听过变成理解？", Outline: "记录、表达、回顾", Rationale: "个人学习笔记", Score: 90, Sufficient: true, MaterialIDs: ids}}}, usage, nil
	}
	if req.Stage == "write" {
		return &provider.KnowledgeArticleResult{Title: req.Topic.Title, Blocks: []provider.KnowledgeBlock{{Kind: "reflection", Text: "个人笔记记下了听后理解。", MaterialIDs: []string{req.Materials[0].ID}}, {Kind: "synthesis", Text: "可以把记录与回顾连接，这是基于笔记的综合建议。", MaterialIDs: []string{req.Materials[0].ID, req.Materials[1].ID}}}}, usage, nil
	}
	yes := true
	return &provider.KnowledgeArticleResult{Passed: &yes}, usage, nil
}

func TestKnowledgeArticleFailedResponseStillAccountsForUsage(t *testing.T) {
	for _, empty := range []bool{false, true} {
		t.Run(fmt.Sprint(empty), func(t *testing.T) {
			s, w, article, fake, _ := seedKnowledgeQueue(t, func(s *store.Store) {
				if err := s.SetModelPrice(t.Context(), models.ModelPrice{Provider: "pod", Model: "model", InputCentsPerMillion: 1_000_000, OutputCentsPerMillion: 1_000_000}); err != nil {
					t.Fatal(err)
				}
			})
			ctx := t.Context()
			fake.empty = empty
			if !empty {
				fake.failure = fmt.Errorf("malformed remote JSON")
			}
			if err := s.SetModelPrice(ctx, models.ModelPrice{Provider: "pod", Model: "model", InputCentsPerMillion: 2_000_000, OutputCentsPerMillion: 2_000_000}); err != nil {
				t.Fatal(err)
			}
			if err := w.ProcessOne(ctx); err != nil {
				t.Fatal(err)
			}
			v, _ := s.GetKnowledgeArticle(ctx, article.ID)
			if v.Status != "failed" || fake.calls != 1 {
				t.Fatal("failed remote response was accepted")
			}
			var n int
			var cost int64
			if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(estimated_cost),0) FROM usage_records WHERE operation='knowledge_article_discover'`).Scan(&n, &cost); err != nil {
				t.Fatal(err)
			}
			// Even an empty successful result can carry billable usage.
			if n != 1 || cost != 150 {
				t.Fatalf("lost billable usage: receipts=%d cost=%d", n, cost)
			}
		})
	}
}

func TestKnowledgeArticleCheckpointPersistenceFailureStopsReplay(t *testing.T) {
	s, w, article, fake, _ := seedKnowledgeQueue(t)
	ctx := t.Context()
	if _, err := s.DB.ExecContext(ctx, `CREATE TRIGGER fail_knowledge_checkpoint BEFORE UPDATE OF checkpoint_json ON processing_jobs BEGIN SELECT RAISE(FAIL,'checkpoint unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	v, _ := s.GetKnowledgeArticle(ctx, article.ID)
	if v.Status != "failed" || fake.calls != 1 {
		t.Fatal("unpersisted response did not fail visibly")
	}
	jobs, _ := s.ListRecentCompleted(ctx, 100)
	if len(jobs) != 1 {
		t.Fatalf("jobs=%d", len(jobs))
	}
	exec, _ := s.GetJobExecution(ctx, jobs[0].ID)
	if !exec.RemoteCallStarted || exec.CheckpointJSON != "" {
		t.Fatal("call boundary or checkpoint incorrect")
	}
	if _, err := s.DB.ExecContext(ctx, `DROP TRIGGER fail_knowledge_checkpoint`); err != nil {
		t.Fatal(err)
	}
	if err := w.doKnowledgeArticle(ctx, jobs[0], &provider.ProviderBundle{KnowledgeArticle: fake}); err == nil || fake.calls != 1 {
		t.Fatal("unpersisted remote response was automatically replayed")
	}
}

func TestKnowledgeArticleMissingDurableIdentityPreventsCall(t *testing.T) {
	for _, kind := range []string{"missing-job", "missing-article", "unknown-result"} {
		t.Run(kind, func(t *testing.T) {
			s, w, article, fake, _ := seedKnowledgeQueue(t)
			ctx := t.Context()
			jobs, _ := s.ListQueuedOrRunning(ctx)
			job := jobs[0]
			if kind == "unknown-result" {
				if err := s.SaveJobResult(ctx, job.ID, "", models.JobResultUnknown); err != nil {
					t.Fatal(err)
				}
				if err := w.ProcessOne(ctx); err != nil {
					t.Fatal(err)
				}
				v, _ := s.GetKnowledgeArticle(ctx, article.ID)
				if v.Status != "failed" || fake.calls != 0 {
					t.Fatal("unknown result not exposed")
				}
				return
			}
			if kind == "missing-job" {
				job.ID = "missing"
			} else {
				if _, err := s.DB.ExecContext(ctx, `DELETE FROM knowledge_articles WHERE id=?`, article.ID); err != nil {
					t.Fatal(err)
				}
			}
			if err := w.doKnowledgeArticle(ctx, job, &provider.ProviderBundle{KnowledgeArticle: fake}); err == nil || fake.calls != 0 {
				t.Fatal("missing durable identity triggered a remote call")
			}
		})
	}
}

func TestKnowledgeArticleUsagePersistenceRecovery(t *testing.T) {
	for _, kind := range []string{"price-unavailable", "receipt-unavailable", "failed-response-receipt-unavailable"} {
		t.Run(kind, func(t *testing.T) {
			s, w, _, fake, _ := seedKnowledgeQueue(t)
			ctx := t.Context()
			jobs, _ := s.ListQueuedOrRunning(ctx)
			job := jobs[0]
			var breakSQL, restoreSQL string
			if kind == "price-unavailable" {
				breakSQL, restoreSQL = `ALTER TABLE model_prices RENAME TO unavailable_model_prices`, `ALTER TABLE unavailable_model_prices RENAME TO model_prices`
			} else {
				breakSQL, restoreSQL = `CREATE TRIGGER fail_knowledge_receipt BEFORE INSERT ON usage_records BEGIN SELECT RAISE(FAIL,'receipt unavailable'); END`, `DROP TRIGGER fail_knowledge_receipt`
			}
			if kind == "failed-response-receipt-unavailable" {
				fake.failure = fmt.Errorf("bad remote JSON")
			}
			if _, err := s.DB.ExecContext(ctx, breakSQL); err != nil {
				t.Fatal(err)
			}
			bundle := &provider.ProviderBundle{KnowledgeArticle: fake}
			if err := w.doKnowledgeArticle(ctx, job, bundle); err == nil || fake.calls != 1 {
				t.Fatal("missing usage persistence did not block progression")
			}
			if _, err := s.DB.ExecContext(ctx, restoreSQL); err != nil {
				t.Fatal(err)
			}
			exec, _ := s.GetJobExecution(ctx, job.ID)
			if fake.failure != nil {
				if exec.CheckpointJSON != "" {
					t.Fatal("invalid response cannot become a reusable checkpoint")
				}
				return
			}
			if exec.CheckpointJSON == "" {
				t.Fatal("known response lost while accounting was unavailable")
			}
			if err := w.doKnowledgeArticle(ctx, job, bundle); err != nil || fake.calls != 1 {
				t.Fatalf("accounting recovery replayed model: %v calls=%d", err, fake.calls)
			}
			var receipts int
			if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM usage_records WHERE operation='knowledge_article_discover'`).Scan(&receipts); err != nil || receipts != 1 {
				t.Fatalf("usage recovery: %v receipts=%d", err, receipts)
			}
		})
	}
}

func seedKnowledgeQueue(t *testing.T, configure ...func(*store.Store)) (*store.Store, *Worker, *store.KnowledgeArticleRecord, *knowledgeQueueFake, string) {
	t.Helper()
	s, w := newTestWorker(t)
	ep := seedEpisode(t, s)
	ctx := t.Context()
	for _, setup := range configure {
		setup(s)
	}
	for _, text := range []string{"每次听完先写一条自己的理解。", "几天后回看笔记，检查还能不能解释。"} {
		if _, err := s.CreateOwnerNote(ctx, models.OwnerNote{SourceType: "episode", SourceID: ep, Kind: "owner_reflection", Content: text}); err != nil {
			t.Fatal(err)
		}
	}
	profile, err := s.EnsureDefaultEditorialProfile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	req, _, err := s.BuildKnowledgeArticleRequest(ctx, profile.ID, "pod")
	if err != nil {
		t.Fatal(err)
	}
	v, _, err := s.ReserveKnowledgeArticle(ctx, profile.ID, "pod", "model", req, false)
	if err != nil {
		t.Fatal(err)
	}
	fake := &knowledgeQueueFake{}
	w.WithBundleResolver(func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{KnowledgeArticle: fake}, nil
	})
	return s, w, v, fake, ep
}

func TestKnowledgeArticleCheckpointRecoveryKeepsUsageAudit(t *testing.T) {
	s, w, article, fake, _ := seedKnowledgeQueue(t)
	ctx := t.Context()
	if _, err := s.DB.ExecContext(ctx, `CREATE TRIGGER fail_knowledge_successor BEFORE INSERT ON processing_jobs WHEN NEW.intent_id LIKE '%:write' BEGIN SELECT RAISE(FAIL,'test interruption'); END`); err != nil {
		t.Fatal(err)
	}
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	v, _ := s.GetKnowledgeArticle(ctx, article.ID)
	if v.Status != "failed" || fake.calls != 1 {
		t.Fatalf("failure state: %+v calls=%d", v, fake.calls)
	}
	if _, err := s.DB.ExecContext(ctx, `DROP TRIGGER fail_knowledge_successor`); err != nil {
		t.Fatal(err)
	}
	if err := s.RetryKnowledgeArticle(ctx, article.ID); err != nil {
		t.Fatal(err)
	}
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	if fake.calls != 1 {
		t.Fatal("known discovery response was billed/called again")
	}
	for i := 0; i < 2; i++ {
		if err := w.ProcessOne(ctx); err != nil {
			t.Fatal(err)
		}
	}
	v, _ = s.GetKnowledgeArticle(ctx, article.ID)
	if v.Status != "ready" || fake.calls != 3 {
		t.Fatalf("recovered journey: %+v calls=%d", v, fake.calls)
	}
	var receipts int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM usage_records WHERE operation LIKE 'knowledge_article_%'`).Scan(&receipts); err != nil || receipts != 3 {
		t.Fatalf("usage duplicates: %v count=%d", err, receipts)
	}
}

func TestKnowledgeArticleRecoveryBlocksUnknownRemoteResult(t *testing.T) {
	s, w, article, fake, _ := seedKnowledgeQueue(t)
	ctx := t.Context()
	jobs, _ := s.ListQueuedOrRunning(ctx)
	job := jobs[0]
	s.MarkJobRunning(ctx, job.ID)
	s.MarkJobRemoteCallStarted(ctx, job.ID)
	if err := s.ResetRunningOnStartup(ctx); err != nil {
		t.Fatal(err)
	}
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	v, _ := s.GetKnowledgeArticle(ctx, article.ID)
	exec, _ := s.GetJobExecution(ctx, job.ID)
	if v.Status != "failed" || !strings.Contains(v.Reason, "结果未知") || fake.calls != 0 || exec.ResultState != models.JobResultUnknown {
		t.Fatalf("unsafe replay: %+v %+v calls=%d", v, exec, fake.calls)
	}
	if err := s.RetryKnowledgeArticle(ctx, article.ID); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		w.ProcessOne(ctx)
	}
	v, _ = s.GetKnowledgeArticle(ctx, article.ID)
	if v.Status != "ready" || fake.calls != 3 {
		t.Fatal("explicit retry did not work")
	}
}

func TestKnowledgeArticleGatesBeforeRemoteCall(t *testing.T) {
	for _, name := range []string{"policy", "note-changed", "budget", "corrupt-checkpoint", "corrupt-input", "prompt-version"} {
		t.Run(name, func(t *testing.T) {
			s, w, article, fake, ep := seedKnowledgeQueue(t)
			ctx := t.Context()
			jobs, _ := s.ListQueuedOrRunning(ctx)
			job := jobs[0]
			switch name {
			case "policy":
				s.SetSourceProductionPolicy(ctx, models.SourceEpisode, ep, "internal", models.ModelDataLocalOnly)
			case "note-changed":
				notes, _ := s.ListOwnerNotes(ctx, models.SourceEpisode, ep)
				s.UpdateOwnerNote(ctx, notes[0].ID, "更新理解", "[]", "[]", notes[0].Revision)
			case "budget":
				zero := int64(0)
				s.SetOwnerMonthlyBudget(ctx, &zero)
				s.SetModelPrice(ctx, models.ModelPrice{Provider: "pod", Model: "model", InputCentsPerMillion: 100, OutputCentsPerMillion: 100})
			case "corrupt-checkpoint":
				s.DB.ExecContext(ctx, `UPDATE processing_jobs SET checkpoint_json='{}' WHERE id=?`, job.ID)
			case "corrupt-input":
				s.DB.ExecContext(ctx, `UPDATE processing_jobs SET input_snapshot_json='invalid' WHERE id=?`, job.ID)
			case "prompt-version":
				s.DB.ExecContext(ctx, `UPDATE processing_jobs SET config_version='unknown' WHERE id=?`, job.ID)
			}
			if err := w.ProcessOne(ctx); err != nil {
				t.Fatal(err)
			}
			v, _ := s.GetKnowledgeArticle(ctx, article.ID)
			if fake.calls != 0 || v.Status != "failed" {
				t.Fatalf("gate %s failed: %+v calls=%d", name, v, fake.calls)
			}
		})
	}
}

func TestKnowledgeArticleInvalidOutputCanBeRetried(t *testing.T) {
	s, w, article, fake, _ := seedKnowledgeQueue(t)
	fake.bad = true
	ctx := t.Context()
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	v, _ := s.GetKnowledgeArticle(ctx, article.ID)
	if v.Status != "failed" {
		t.Fatalf("invalid IDs accepted: %+v", v)
	}
	fake.bad = false
	if err := s.RetryKnowledgeArticle(ctx, article.ID); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := w.ProcessOne(ctx); err != nil {
			t.Fatal(err)
		}
	}
	v, _ = s.GetKnowledgeArticle(ctx, article.ID)
	if v.Status != "ready" || fake.calls != 4 {
		t.Fatalf("invalid checkpoint incorrectly reused: %+v calls=%d", v, fake.calls)
	}
	var receipts int
	s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM usage_records WHERE operation LIKE 'knowledge_article_%'`).Scan(&receipts)
	if receipts != 4 {
		t.Fatal(fmt.Sprintf("all actual calls must be audited: %d", receipts))
	}
}

func TestKnowledgeArticleRejectsTamperedRecoveryAndUnavailableProvider(t *testing.T) {
	for _, kind := range []string{"malformed-checkpoint", "unavailable-provider", "wrong-identity"} {
		t.Run(kind, func(t *testing.T) {
			s, w, article, fake, _ := seedKnowledgeQueue(t)
			ctx := t.Context()
			jobs, _ := s.ListQueuedOrRunning(ctx)
			job := jobs[0]
			switch kind {
			case "malformed-checkpoint":
				s.DB.ExecContext(ctx, `UPDATE processing_jobs SET checkpoint_json='invalid' WHERE id=?`, job.ID)
			case "unavailable-provider":
				w.WithBundleResolver(func(*models.ProcessingJob) (*provider.ProviderBundle, error) { return nil, nil })
			case "wrong-identity":
				s.DB.ExecContext(ctx, `UPDATE processing_jobs SET input_snapshot_json=json_set(input_snapshot_json,'$.article_id','other') WHERE id=?`, job.ID)
			}
			w.ProcessOne(ctx)
			v, _ := s.GetKnowledgeArticle(ctx, article.ID)
			if v.Status != "failed" || fake.calls != 0 {
				t.Fatal("invalid recovery called model")
			}
		})
	}
}
