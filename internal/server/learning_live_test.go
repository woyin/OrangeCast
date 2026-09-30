package server

import (
	"encoding/json"
	"fmt"
	"github.com/woyin/orangecast/internal/config"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/queue"
	"github.com/woyin/orangecast/internal/store"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestPersonalLearningV2Live validates current queue contracts with self-authored data only.
func TestPersonalLearningV2Live(t *testing.T) {
	if os.Getenv("CWP_LEARNING_V2_LIVE") != "1" {
		t.Skip("explicit live POD opt-in required")
	}
	if err := config.LoadEnvironmentFile("../../.env"); err != nil {
		t.Fatal("local configuration unavailable")
	}
	srv := newTestServer(t)
	srv.cfg.PodBaseURL, srv.cfg.PodAPIKey, srv.cfg.PodModel, srv.cfg.PodReviewModel = os.Getenv("POD_BASE_URL"), os.Getenv("POD_API_KEY"), os.Getenv("POD_MODEL"), os.Getenv("POD_REVIEW_MODEL")
	if !srv.cfg.PodAvailable() {
		t.Fatal("POD connection is incomplete")
	}
	srv.selector.WithPod(srv.cfg.PodAPIKey, srv.cfg.PodBaseURL, srv.cfg.PodModel)
	if os.Getenv("CWP_LEARNING_EVAL_SAVE") == "1" {
		defer func() {
			dir := filepath.Join("..", "..", "data", "eval", "personal-learning-v2")
			_ = os.MkdirAll(dir, 0700)
			_ = store.ConsistencyBackup(t.Context(), srv.store.DB, filepath.Join(dir, fmt.Sprintf("attempt-%s.db", time.Now().UTC().Format("20060102T150405"))))
		}()
	}
	originals := []string{"合上笔记先解释一个要点，可以发现自己没有说清楚的地方。几天后再次解释，再回原文核对遗漏。换术语也需要保留原来的适用条件。", "集中整理资料有助于建立整体结构；几天后回看有助于发现遗漏。这些材料没有比较两种方式的效果数字。不同场景的信息不能直接推出所有人应该用同一个方法。", "任务太大时先写一个具体问题，检查现有材料能支持哪些部分。写作前列出缺口，比追求好听的标题更有用。材料不足时先记录缺口，后续有新笔记再重新检查。"}
	var episodes []string
	groups := [][]string{{}, {}}
	p, err := srv.store.CreatePodcast(t.Context(), "https://example.test/v2-eval.xml", "自建评测材料（不是实际节目）", "", "")
	if err != nil {
		t.Fatal(err)
	}
	for i, text := range originals {
		if _, err = srv.store.MergeEpisodes(t.Context(), p.ID, []models.Episode{{GUID: fmt.Sprintf("eval-v2-%d", i), Title: fmt.Sprintf("自建材料%d", i+1), AudioURL: "https://example.test/audio.wav"}}); err != nil {
			t.Fatal(err)
		}
		eps, e := srv.store.ListEpisodes(t.Context(), p.ID)
		if e != nil {
			t.Fatal(e)
		}
		id := ""
		for _, ep := range eps {
			if ep.GUID == fmt.Sprintf("eval-v2-%d", i) {
				id = ep.ID
			}
		}
		episodes = append(episodes, id)
		job, e := srv.store.EnqueueJob(t.Context(), models.SourceEpisode, id, models.JobTranscribe)
		if e != nil {
			t.Fatal(e)
		}
		raw, _ := json.Marshal(provider.TranscriptPayload{Segments: []provider.Segment{{ID: "seg-1", Start: 12, End: 42, Text: text}}})
		version, e := srv.store.CreateArtifactVersion(t.Context(), models.SourceEpisode, id, store.KindTranscript, "self-authored", "fixture", "v2", job.ID, string(raw))
		if e != nil {
			t.Fatal(e)
		}
		if e = srv.store.SetCurrentVersion(t.Context(), models.SourceEpisode, id, store.KindTranscript, version); e != nil {
			t.Fatal(e)
		}
		_ = srv.store.MarkJobSucceeded(t.Context(), job.ID)
	}
	for i := 0; i < 20; i++ {
		group := 0
		source := i % 2
		content := []string{"先用自己的话解释，再检查遗漏，不能把听懂的感觉当成实际能解释。", "集中整理和几天后回看对应不同目的，材料没有提供量化比较。"}[source]
		if i >= 10 {
			group = 1
			source = 2
			content = "先明确具体问题，再按材料可支持的部分写作；没有证据时记录缺口。"
		}
		kind := "source_note"
		citations := "[\"seg-1\"]"
		if i%4 == 3 {
			kind = "owner_reflection"
			citations = "[]"
			content = "我的实践理解：" + content
		}
		n, e := srv.store.CreateOwnerNote(t.Context(), models.OwnerNote{SourceType: "episode", SourceID: episodes[source], Kind: kind, Content: content, CitationsJSON: citations})
		if e != nil {
			t.Fatal(e)
		}
		groups[group] = append(groups[group], n.ID)
	}
	profile, err := srv.store.EnsureDefaultEditorialProfile(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	// The input artifacts above are self-authored fixtures, not a request to
	// fetch placeholder audio or run the unrelated transcription pipeline.
	if _, err = srv.store.DB.ExecContext(t.Context(), `UPDATE processing_jobs SET status='succeeded' WHERE status='queued'`); err != nil {
		t.Fatal(err)
	}
	for i, ids := range groups {
		article, _, e := srv.enqueueKnowledgeArticleScope(t.Context(), profile.ID, false, store.KnowledgeScope{MaterialIDs: ids})
		if e != nil {
			t.Fatal("live admission failed; details retained locally")
		}
		for step := 0; step < 6; step++ {
			if e = srv.worker.ProcessOne(t.Context()); e != nil {
				t.Fatal("live worker boundary failed; details retained locally")
			}
			article, e = srv.store.GetKnowledgeArticle(t.Context(), article.ID)
			if e != nil {
				t.Fatal(e)
			}
			if article.Status == "ready" || article.Status == "needs_review" || article.Status == "insufficient" || article.Status == "failed" {
				break
			}
		}
		runs, e := srv.store.ListKnowledgeExecutions(t.Context(), article.ID)
		if e != nil {
			t.Fatal(e)
		}
		for _, run := range runs {
			t.Logf("topic=%d stage=%s model=%s prompt=%s input=%d output=%d cost_cents=%s status=%s", i+1, run.Stage, run.Model, run.PromptVersion, run.InputUnits, run.OutputUnits, run.Cost, run.Status)
		}
		if article.Status == "failed" || article.Status == "insufficient" {
			t.Fatalf("topic=%d status=%s (private details retained locally)", i+1, article.Status)
		}
		if article.Status != "ready" && article.Status != "needs_review" {
			t.Fatal("bounded pipeline did not terminate")
		}
		if article.PassedRevision > 0 {
			rev, e := srv.store.GetKnowledgeRevision(t.Context(), article.ID, article.PassedRevision)
			if e != nil {
				t.Fatal(e)
			}
			state, _, e := srv.store.KnowledgeEvidenceState(t.Context(), article, rev)
			if e != nil || state != "valid" {
				t.Fatal("passed evidence invalid")
			}
		}
	}
	batch, _, err := srv.store.ReserveLearningReview(t.Context(), profile.ID, "pod", srv.cfg.PodModel, time.Now(), false)
	if err != nil || batch == nil {
		t.Fatal("live weekly admission failed")
	}
	if err = srv.worker.ProcessOne(t.Context()); err != nil {
		t.Fatal("live weekly worker failed")
	}
	batch, err = srv.store.GetLearningReviewBatch(t.Context(), batch.ID)
	if err != nil || batch.Status != "ready" {
		t.Fatal("live weekly contract failed; details retained locally")
	}
	items, err := srv.store.ListLearningReviewItems(t.Context(), batch.ID)
	if err != nil || len(items) > 5 || len(items) == 0 {
		t.Fatal("invalid live question count")
	}
	t.Logf("weekly_questions=%d model=%s prompt=%s", len(items), batch.Model, batch.PromptVersion)
	if os.Getenv("CWP_LEARNING_EVAL_SAVE") == "1" {
		dir := filepath.Join("..", "..", "data", "eval", "personal-learning-v2")
		if err = os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err = store.ConsistencyBackup(t.Context(), srv.store.DB, filepath.Join(dir, fmt.Sprintf("live-%s.db", time.Now().UTC().Format("20060102T150405")))); err != nil {
			t.Fatal(err)
		}
		t.Log("self-authored inputs, generated output, exact usage and audit saved under ignored data/eval")
	}
	t.Log("real endpoint compatibility passed; real podcast samples, human quality and physical phone acceptance remain separate")
}

// TestPersonalLearningV2LiveRevision uses a copy of the self-authored live fixture.
func TestPersonalLearningV2LiveRevision(t *testing.T) {
	if os.Getenv("CWP_LEARNING_V2_LIVE_REVISION") != "1" {
		t.Skip("explicit live revision opt-in required")
	}
	if err := config.LoadEnvironmentFile("../../.env"); err != nil {
		t.Fatal("local configuration unavailable")
	}
	paths, err := filepath.Glob("../../data/eval/personal-learning-v2/live-*.db")
	if err != nil || len(paths) == 0 {
		t.Fatal("self-authored live fixture unavailable")
	}
	raw, err := os.ReadFile(paths[len(paths)-1])
	if err != nil {
		t.Fatal(err)
	}
	restoredPath := filepath.Join(t.TempDir(), "live-revision.db")
	if err = os.WriteFile(restoredPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	restored, err := store.Open(restoredPath)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	srv := newTestServer(t)
	srv.store = restored
	srv.cfg.PodBaseURL, srv.cfg.PodAPIKey, srv.cfg.PodModel, srv.cfg.PodReviewModel = os.Getenv("POD_BASE_URL"), os.Getenv("POD_API_KEY"), os.Getenv("POD_MODEL"), os.Getenv("POD_REVIEW_MODEL")
	if !srv.cfg.PodAvailable() {
		t.Fatal("POD connection incomplete")
	}
	srv.selector.WithPod(srv.cfg.PodAPIKey, srv.cfg.PodBaseURL, srv.cfg.PodModel)
	srv.worker = queue.NewWorker(restored, srv.selector, srv.cfg.TempDir, srv.cfg.EvidenceDir, srv.cfg.NarrationDir)
	defer func() {
		_ = store.ConsistencyBackup(t.Context(), restored.DB, fmt.Sprintf("../../data/eval/personal-learning-v2/revision-%s.db", time.Now().UTC().Format("20060102T150405")))
	}()
	articles, err := restored.ListKnowledgeArticles(t.Context())
	if err != nil || len(articles) != 2 {
		t.Fatal(err)
	}
	article := articles[0]
	previous, err := restored.GetKnowledgeRevision(t.Context(), article.ID, article.PassedRevision)
	if err != nil {
		t.Fatal(err)
	}
	var req provider.KnowledgeArticleRequest
	var blocks []provider.KnowledgeBlock
	if json.Unmarshal([]byte(previous.InputJSON), &req) != nil || json.Unmarshal([]byte(previous.BlocksJSON), &blocks) != nil {
		t.Fatal("frozen revision invalid")
	}
	blocks[0].Text += "\n\n本文以提供的来源和个人笔记为使用范围。"
	draft, err := restored.SaveKnowledgeDraft(t.Context(), article.ID, article.WorkingRevision, previous.Title, blocks, req)
	if err != nil {
		t.Fatal(err)
	}
	current, err := restored.GetKnowledgeArticle(t.Context(), article.ID)
	if err != nil || current.PassedRevision != previous.Revision || current.Status != "needs_review" {
		t.Fatal("manual draft displaced passed revision", err)
	}
	if err = restored.QueueKnowledgeRevision(t.Context(), article.ID, draft.Revision, "按冻结材料核对编辑后的正文，减少重复，说明应用边界；不要补充机制或量化效果。", srv.cfg.KnowledgeReviewModel(), true); err != nil {
		t.Fatal(err)
	}
	for step := 0; step < 2; step++ {
		if err = srv.worker.ProcessOne(t.Context()); err != nil {
			t.Fatal("live revision worker failed")
		}
	}
	current, err = restored.GetKnowledgeArticle(t.Context(), article.ID)
	if err != nil || (current.Status != "ready" && current.Status != "needs_review") || current.WorkingRevision != draft.Revision+1 {
		t.Fatal("live revision contract failed; details retained locally")
	}
	runs, err := restored.ListKnowledgeExecutions(t.Context(), article.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, run := range runs {
		if run.Stage == "revise" || run.Stage == "review_final" {
			t.Logf("stage=%s model=%s prompt=%s input=%d output=%d cost_cents=%s status=%s", run.Stage, run.Model, run.PromptVersion, run.InputUnits, run.OutputUnits, run.Cost, run.Status)
		}
	}
	t.Logf("working_revision=%d passed_revision=%d status=%s; source fixture unchanged", current.WorkingRevision, current.PassedRevision, current.Status)
}
