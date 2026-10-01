package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/woyin/orangecast/internal/config"
	"github.com/woyin/orangecast/internal/evalset"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/queue"
	"github.com/woyin/orangecast/internal/store"
)

func learningV3SelfAuthoredCorpus() evalset.LearningEvaluationCorpus {
	c := evalset.LearningEvaluationCorpus{Version: 1, Kind: "self-authored"}
	for _, v := range evalset.LearningEpisodes()[:3] {
		c.Episodes = append(c.Episodes, evalset.LearningEvaluationEpisode{ID: v.ID, Title: v.Description, Source: v.Source, ModelDataPolicy: "external_allowed", Segments: v.Segments})
	}
	for i := 0; i < 20; i++ {
		ep := c.Episodes[0]
		group := 0
		if i >= 10 {
			ep = c.Episodes[1+i%2]
			group = 1
		}
		kind := "source_note"
		content := ep.Segments[0].Text
		if i%4 == 3 {
			kind = "owner_reflection"
			content = "我的理解：先保留原文的适用条件，再尝试用自己的话解释。"
		}
		n := evalset.LearningEvaluationNote{ID: fmt.Sprintf("note-%02d", i), EpisodeID: ep.ID, Kind: kind, Content: content, Segments: []string{ep.Segments[0].ID}}
		c.Notes = append(c.Notes, n)
		if len(c.Cases) <= group {
			c.Cases = append(c.Cases, evalset.LearningEvaluationCase{ID: fmt.Sprintf("question-%d", group), Question: []string{"这些访谈中的建议有哪些适用条件？", "怎样区分所提供材料的来源表达与个人理解？"}[group], Expected: []string{"保留来源和条件", "个人理解分别归因"}, Forbidden: []string{"补充未给出的效果数字", "把个人理解当原话"}})
		}
		c.Cases[group].NoteIDs = append(c.Cases[group].NoteIDs, n.ID)
	}
	return c
}

// TestPersonalLearningV3Evaluation is the reproducible corpus entry; real calls require an explicit live flag.
func TestPersonalLearningV3Evaluation(t *testing.T) {
	corpus := learningV3SelfAuthoredCorpus()
	if path := os.Getenv("CWP_LEARNING_V3_MANIFEST"); path != "" {
		f, err := os.Open(path)
		if err != nil {
			t.Fatal("evaluation manifest unavailable")
		}
		defer f.Close()
		corpus, err = evalset.LoadLearningEvaluation(f)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := corpus.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, gap := range corpus.Gaps() {
		t.Log(gap)
	}
	if len(corpus.Cases) == 0 {
		t.Skip("no evaluation cases; human/real acceptance remains pending")
	}
	sourceFingerprint := learningEvaluationSourceFingerprint(t)
	live := os.Getenv("CWP_LEARNING_V3_LIVE") == "1"
	if live {
		if err := config.LoadEnvironmentFile("../../.env"); err != nil {
			t.Fatal("local configuration unavailable")
		}
	}
	base := newTestServer(t)
	podcast, err := base.store.CreatePodcast(t.Context(), "https://example.test/v3-frozen-corpus.xml", "本地冻结评测材料", "", "")
	if err != nil {
		t.Fatal(err)
	}
	sourceIDs := map[string]string{}
	for _, ep := range corpus.Episodes {
		if _, err = base.store.MergeEpisodes(t.Context(), podcast.ID, []models.Episode{{GUID: ep.ID, Title: ep.Title, AudioURL: "https://example.test/frozen-not-fetched.wav"}}); err != nil {
			t.Fatal(err)
		}
		eps, e := base.store.ListEpisodes(t.Context(), podcast.ID)
		if e != nil {
			t.Fatal(e)
		}
		for _, v := range eps {
			if v.GUID == ep.ID {
				sourceIDs[ep.ID] = v.ID
			}
		}
		id := sourceIDs[ep.ID]
		job, e := base.store.EnqueueJob(t.Context(), models.SourceEpisode, id, models.JobTranscribe)
		if e != nil {
			t.Fatal(e)
		}
		raw, _ := json.Marshal(provider.TranscriptPayload{Segments: ep.Segments})
		v, e := base.store.CreateArtifactVersion(t.Context(), models.SourceEpisode, id, store.KindTranscript, "local-evaluation", "frozen", "evaluation-v1", job.ID, string(raw))
		if e != nil {
			t.Fatal(e)
		}
		if e = base.store.SetCurrentVersion(t.Context(), models.SourceEpisode, id, store.KindTranscript, v); e != nil {
			t.Fatal(e)
		}
		policy := models.ModelDataLocalOnly
		if ep.ModelDataPolicy == "external_allowed" {
			policy = models.ModelDataExternalAllowed
		}
		if e = base.store.SetSourceProductionPolicy(t.Context(), models.SourceEpisode, id, "internal", policy); e != nil {
			t.Fatal(e)
		}
	}
	noteIDs := map[string]string{}
	for _, n := range corpus.Notes {
		refs, _ := json.Marshal(n.Segments)
		note := models.OwnerNote{SourceType: "episode", SourceID: sourceIDs[n.EpisodeID], Kind: n.Kind, Content: n.Content}
		if n.Kind == "source_note" {
			note.CitationsJSON = string(refs)
		} else {
			note.ReferencesJSON = string(refs)
		}
		saved, e := base.store.CreateOwnerNote(t.Context(), note)
		if e != nil {
			t.Fatal(e)
		}
		noteIDs[n.ID] = saved.ID
	}
	if _, err = base.store.DB.ExecContext(t.Context(), `UPDATE processing_jobs SET status='succeeded' WHERE status='queued'`); err != nil {
		t.Fatal(err)
	}
	frozenPath := filepath.Join(t.TempDir(), "corpus.db")
	if err = store.ConsistencyBackup(t.Context(), base.store.DB, frozenPath); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(frozenPath)
	if err != nil {
		t.Fatal(err)
	}
	modelNames := []string{"test-evaluation-model"}
	if live {
		modelNames = []string{os.Getenv("POD_MODEL")}
		if value := os.Getenv("CWP_LEARNING_V3_COMPARE_MODELS"); value != "" {
			modelNames = strings.Split(value, ",")
		}
	}
	seenModels := map[string]bool{}
	for _, model := range modelNames {
		model = strings.TrimSpace(model)
		if model == "" || seenModels[model] {
			t.Fatal("comparison requires distinct configured model names")
		}
		seenModels[model] = true
		for _, sample := range corpus.Cases {
			t.Run(model+"/"+sample.ID, func(t *testing.T) {
				srv := newTestServer(t)
				path := filepath.Join(t.TempDir(), "isolated.db")
				if e := os.WriteFile(path, raw, 0600); e != nil {
					t.Fatal(e)
				}
				isolated, e := store.Open(path)
				if e != nil {
					t.Fatal(e)
				}
				t.Cleanup(func() { isolated.Close() })
				srv.store = isolated
				srv.cfg.PodBaseURL, srv.cfg.PodAPIKey, srv.cfg.PodModel = "https://example.test/v1", "evaluation-test-key", model
				if live {
					srv.cfg.PodBaseURL, srv.cfg.PodAPIKey, srv.cfg.PodReviewModel = os.Getenv("POD_BASE_URL"), os.Getenv("POD_API_KEY"), os.Getenv("POD_REVIEW_MODEL")
				}
				if !srv.cfg.PodAvailable() {
					t.Fatal("POD configuration incomplete")
				}
				srv.selector.WithPod(srv.cfg.PodAPIKey, srv.cfg.PodBaseURL, model)
				srv.worker = queue.NewWorker(isolated, srv.selector, srv.cfg.TempDir, srv.cfg.EvidenceDir, srv.cfg.NarrationDir)
				if !live {
					srv.worker.WithBundleResolver(func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
						return &provider.ProviderBundle{KnowledgeArticle: &learningV3EvaluationProvider{}}, nil
					})
				}
				configuration, _ := json.Marshal([]string{srv.cfg.PodBaseURL, model, srv.cfg.KnowledgeReviewModel(), provider.KnowledgeArticlePromptVersion})
				report := evalset.LearningEvaluationRun{SourceFingerprint: sourceFingerprint, ConfigurationFingerprint: fmt.Sprintf("%x", sha256.Sum256(configuration)), CaseID: sample.ID, CorpusHash: corpus.Fingerprint(), Baseline: "9757702", Model: model, ReviewModel: srv.cfg.KnowledgeReviewModel(), PromptVersion: provider.KnowledgeArticlePromptVersion, Status: "pending"}
				var executions []store.KnowledgeExecution
				if os.Getenv("CWP_LEARNING_EVAL_SAVE") == "1" {
					dir := filepath.Join("..", "..", "data", "eval", "personal-learning-v3", fmt.Sprintf("run-%d", time.Now().UnixNano()))
					if e = os.MkdirAll(dir, 0700); e != nil {
						t.Fatal(e)
					}
					t.Cleanup(func() {
						output := struct {
							Run        evalset.LearningEvaluationRun    `json:"run"`
							Executions []store.KnowledgeExecution       `json:"executions"`
							Corpus     evalset.LearningEvaluationCorpus `json:"corpus"`
							Platform   string                           `json:"platform"`
							Live       bool                             `json:"live_endpoint"`
							Gaps       []string                         `json:"gaps"`
						}{report, executions, corpus, runtime.GOOS + "/" + runtime.GOARCH + " " + runtime.Version(), live, append(corpus.Gaps(), "human scores pending", "physical phone acceptance pending", "incremental update evaluation pending")}
						b, _ := json.MarshalIndent(output, "", "  ")
						if e := os.WriteFile(filepath.Join(dir, "report.json"), append(b, '\n'), 0600); e != nil {
							t.Error(e)
						}
						if e := store.ConsistencyBackup(context.Background(), isolated.DB, filepath.Join(dir, "frozen-run.db")); e != nil {
							t.Error(e)
						}
					})
					// Cleanup functions execute in reverse registration order.
					t.Cleanup(func() {})
				}
				profile, e := isolated.EnsureDefaultEditorialProfile(t.Context())
				if e != nil {
					t.Fatal(e)
				}
				ids := []string{}
				for _, id := range sample.NoteIDs {
					ids = append(ids, noteIDs[id])
				}
				req, last, _, e := isolated.BuildKnowledgeDiscoveryRequest(t.Context(), profile.ID, "pod", store.KnowledgeScope{MaterialIDs: ids}, false)
				if e != nil {
					t.Fatal(e)
				}
				req.Instructions = "围绕以下问题发现文章方向：" + sample.Question
				req.ReviewModel = srv.cfg.KnowledgeReviewModel()
				batch, e := isolated.EnsureKnowledgeDiscoveryBatch(t.Context(), profile.ID, "pod", model, req, last, false)
				if e != nil {
					t.Fatal(e)
				}
				req.DiscoveryBatchID = batch.ID
				article, _, e := isolated.ReserveKnowledgeArticle(t.Context(), profile.ID, "pod", model, req, false)
				if e != nil {
					report.Status = "admission_blocked"
					t.Fatal("evaluation admission blocked; source policy/budget/configuration retained")
				}
				report.ArticleID, report.InputHash = article.ID, article.InputHash
				for i := 0; i < 6; i++ {
					if e = srv.worker.ProcessOne(t.Context()); e != nil {
						report.Status = "worker_failed"
						t.Fatal("evaluation worker failed; private details retained")
					}
					article, e = isolated.GetKnowledgeArticle(t.Context(), article.ID)
					if e != nil {
						t.Fatal(e)
					}
					if article.Status == "ready" || article.Status == "needs_review" || article.Status == "insufficient" || article.Status == "failed" {
						break
					}
				}
				report.Status = article.Status
				executions, e = isolated.ListKnowledgeExecutions(t.Context(), article.ID)
				if e != nil {
					t.Fatal(e)
				}
				if article.PassedRevision > 0 {
					revision, e := isolated.GetKnowledgeRevision(t.Context(), article.ID, article.PassedRevision)
					if e != nil {
						t.Fatal(e)
					}
					state, reason, e := isolated.KnowledgeEvidenceState(t.Context(), article, revision)
					if e != nil || state != "valid" {
						report.StructuralIssues = append(report.StructuralIssues, reason)
						t.Fatal("passed revision evidence invalid")
					}
				}
				if article.Status != "ready" && article.Status != "needs_review" && article.Status != "insufficient" {
					t.Fatal("bounded evaluation did not terminate")
				}
				t.Logf("case=%s model=%s status=%s stages=%d human=pending live=%t", sample.ID, model, article.Status, len(executions), live)
			})
		}
	}
}

// learningV3EvaluationProvider models structural contracts; it does not score prose quality.
type learningV3EvaluationProvider struct{}

func (*learningV3EvaluationProvider) Name() string { return "pod" }
func (*learningV3EvaluationProvider) KnowledgeArticleStep(_ context.Context, req provider.KnowledgeArticleRequest) (*provider.KnowledgeArticleResult, provider.TaskUsage, error) {
	if req.Stage == "select" {
		topic := *req.Topic
		topic.Selection = nil
		topic.MaterialIDs = nil
		for _, m := range req.Materials {
			topic.MaterialIDs = append(topic.MaterialIDs, m.ID)
		}
		req.Topic = &topic
	}
	return knowledgeStepResult(req, false, false, false), provider.TaskUsage{InputUnits: 20, OutputUnits: 10}, nil
}

func learningEvaluationSourceFingerprint(t *testing.T) string {
	t.Helper()
	hash := sha256.New()
	root := filepath.Join("..", "..")
	add := func(path string) {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(hash, "%s:%d:", filepath.ToSlash(rel), len(data))
		hash.Write(data)
	}
	for _, name := range []string{"go.mod", "go.sum"} {
		add(filepath.Join(root, name))
	}
	for _, name := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(filepath.Join(root, name), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			ext := filepath.Ext(path)
			if (ext == ".go" && !strings.HasSuffix(path, "_test.go")) || ext == ".sql" || ext == ".html" || ext == ".js" {
				add(path)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return fmt.Sprintf("%x", hash.Sum(nil))
}
