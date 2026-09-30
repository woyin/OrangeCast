package server

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/woyin/orangecast/internal/auth"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

func seedKnowledgeLearning(t *testing.T, srv *Server) (string, string) {
	t.Helper()
	ctx := t.Context()
	ep := seedEpisodeWithTranscript(t, srv, "automatic-knowledge")
	if _, err := srv.store.DB.ExecContext(ctx, `UPDATE processing_jobs SET status='succeeded'`); err != nil {
		t.Fatal(err)
	}
	source, err := srv.store.CreateOwnerNote(ctx, models.OwnerNote{SourceType: "episode", SourceID: ep, Kind: "source_note", Content: "来源讲了素材与观点。", CitationsJSON: `["seg-1","seg-2"]`})
	if err != nil {
		t.Fatal(err)
	}
	reflection, err := srv.store.CreateOwnerNote(ctx, models.OwnerNote{SourceType: "episode", SourceID: ep, Kind: "owner_reflection", Content: "我希望先理清来源，再形成自己的理解。", ReferencesJSON: `["seg-1"]`})
	if err != nil {
		t.Fatal(err)
	}
	_ = source
	return ep, reflection.ID
}

func knowledgeStepResult(req provider.KnowledgeArticleRequest, reviewFail, finalFail, insufficient bool) *provider.KnowledgeArticleResult {
	switch req.Stage {
	case "discover":
		if insufficient {
			return &provider.KnowledgeArticleResult{Reason: "当前材料缺少具体案例"}
		}
		ids := []string{}
		for _, m := range req.Materials {
			ids = append(ids, m.ID)
		}
		return &provider.KnowledgeArticleResult{Topics: []provider.KnowledgeTopic{{Title: "怎样区分来源与个人理解", Question: "怎样避免把理解混同为原文？", Thesis: "先辨认来源，再清楚标识综合解释", Rationale: "已有来源整理与个人笔记", Outline: "来源、个人理解、综合应用", Score: 92, Sufficient: true, MaterialIDs: ids}}}
	case "select":
		topic := *req.Topic
		for _, m := range req.Materials {
			topic.Selection = append(topic.Selection, provider.KnowledgeSelection{MaterialID: m.ID, Selected: true, Role: "support", Reason: "与问题相关"})
		}
		return &provider.KnowledgeArticleResult{Topics: []provider.KnowledgeTopic{topic}}
	case "write", "revise":
		var source, note string
		for _, m := range req.Materials {
			if m.Kind == "owner_reflection" {
				note = m.ID
			} else {
				source = m.ID
			}
		}
		return &provider.KnowledgeArticleResult{Title: req.Topic.Title, Blocks: []provider.KnowledgeBlock{{Kind: "source", Text: "## 来源说了什么\n\n来源提供了素材与观点。", MaterialIDs: []string{source}}, {Kind: "reflection", Text: "个人笔记提出先理清来源，再形成理解。", MaterialIDs: []string{note}}, {Kind: "synthesis", Text: "综合这两项材料，可以先辨认原文和理解的边界，再用自己的话解释；这属于AI综合。", MaterialIDs: []string{source, note}}}}
	case "review", "review_final":
		pass := !(req.Stage == "review" && reviewFail || req.Stage == "review_final" && finalFail)
		issues := []string{}
		if !pass {
			issues = []string{"应把个人理解与来源表达进一步区分"}
		}
		return &provider.KnowledgeArticleResult{Passed: &pass, Issues: issues}
	}
	return nil
}

func TestKnowledgeArticleRealTransportJourney(t *testing.T) {
	for _, tc := range []struct {
		name                                string
		reviewFail, finalFail, insufficient bool
		status                              string
		calls                               int
	}{
		{"ready", false, false, false, "ready", 4}, {"bounded-revision", true, false, false, "ready", 6}, {"needs-review", true, true, false, "needs_review", 6}, {"insufficient", false, false, true, "insufficient", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := newTestServer(t)
			session := claimOwnerAndLogin(t, srv, "knowledge@example.com", "password123")
			ep, noteID := seedKnowledgeLearning(t, srv)
			var mu sync.Mutex
			stages := []string{}
			service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer dedicated-test-key" {
					t.Error("POD credential missing")
				}
				var body struct {
					Model    string `json:"model"`
					Messages []struct {
						Content string `json:"content"`
					} `json:"messages"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Messages) < 2 {
					t.Errorf("request: %v", err)
					w.WriteHeader(400)
					return
				}
				if body.Model != "test-text-model" {
					t.Error("wrong model")
				}
				var req provider.KnowledgeArticleRequest
				if err := json.NewDecoder(strings.NewReader(body.Messages[1].Content)).Decode(&req); err != nil {
					t.Errorf("frozen request: %v", err)
					w.WriteHeader(400)
					return
				}
				mu.Lock()
				stages = append(stages, req.Stage)
				mu.Unlock()
				if len(req.Materials) != 2 {
					t.Errorf("notes not selected: %d", len(req.Materials))
				}
				content, _ := json.Marshal(knowledgeStepResult(req, tc.reviewFail, tc.finalFail, tc.insufficient))
				json.NewEncoder(w).Encode(map[string]any{"model": "test-text-model", "choices": []any{map[string]any{"message": map[string]any{"content": string(content)}}}, "usage": map[string]int{"prompt_tokens": 100, "completion_tokens": 50}})
			}))
			defer service.Close()
			srv.cfg.PodBaseURL, srv.cfg.PodAPIKey, srv.cfg.PodModel = service.URL+"/v1", "dedicated-test-key", "test-text-model"
			srv.selector.WithPod(srv.cfg.PodAPIKey, srv.cfg.PodBaseURL, srv.cfg.PodModel)
			rec := postForm(t, srv, session, "/knowledge-articles/generate", "")
			if rec.Code != 303 {
				t.Fatalf("generate: %d %s", rec.Code, rec.Body.String())
			}
			path := rec.Header().Get("Location")
			if pending := doWithCookie(srv, session, "GET", path+"/download"); pending.Code != 409 {
				t.Fatal("unreviewed download accepted")
			}
			for i := 0; i < tc.calls; i++ {
				if err := srv.worker.ProcessOne(t.Context()); err != nil {
					t.Fatal(err)
				}
			}
			records, err := srv.store.ListKnowledgeArticles(t.Context())
			if err != nil || len(records) != 1 || records[0].Status != tc.status {
				t.Fatalf("article: %v %+v", err, records)
			}
			mu.Lock()
			callCount := len(stages)
			mu.Unlock()
			if callCount != tc.calls {
				t.Fatalf("calls=%d expected=%d", callCount, tc.calls)
			}
			if page := doWithCookie(srv, session, "GET", path); page.Code != 200 || !strings.Contains(page.Body.String(), "全部知识文章") {
				t.Fatalf("detail: %d %s", page.Code, page.Body.String())
			}
			rec = postForm(t, srv, session, "/knowledge-articles/generate", "")
			if rec.Code != 303 || rec.Header().Get("Location") != path {
				t.Fatal("unchanged inputs must reuse the article")
			}
			if err := srv.worker.ProcessOne(t.Context()); err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			if len(stages) != tc.calls {
				t.Error("duplicate click called model")
			}
			mu.Unlock()
			var claims int
			if err := srv.store.DB.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM creation_proposals`).Scan(&claims); err != nil || claims != 0 {
				t.Fatal("automatic pipeline fabricated Owner confirmation")
			}
			if tc.status == "ready" {
				download := doWithCookie(srv, session, "GET", path+"/download")
				if download.Code != 200 || !strings.Contains(download.Body.String(), "AI 综合") || !strings.Contains(download.Body.String(), "?t=") {
					t.Fatalf("grounded export: %d %s", download.Code, download.Body.String())
				}
				n, _ := srv.store.GetOwnerNote(t.Context(), noteID)
				if _, err := srv.store.UpdateOwnerNote(t.Context(), noteID, "修订后的理解", "[]", n.ReferencesJSON, n.Revision); err != nil {
					t.Fatal(err)
				}
				if stale := doWithCookie(srv, session, "GET", path+"/download"); stale.Code != 409 {
					t.Fatal("changed note did not invalidate export")
				}
			}
			_ = ep
		})
	}
}

type knowledgeStageFake struct {
	calls int
	fail  bool
}

func (f *knowledgeStageFake) Name() string { return "pod" }
func (f *knowledgeStageFake) KnowledgeArticleStep(_ context.Context, req provider.KnowledgeArticleRequest) (*provider.KnowledgeArticleResult, provider.TaskUsage, error) {
	f.calls++
	if f.fail {
		return nil, provider.TaskUsage{}, fmt.Errorf("测试连接失败")
	}
	return knowledgeStepResult(req, false, false, false), provider.TaskUsage{InputUnits: 20, OutputUnits: 10}, nil
}

func TestKnowledgeArticleSchedulerFailureAndRetry(t *testing.T) {
	srv := newTestServer(t)
	session := claimOwnerAndLogin(t, srv, "scheduler@example.com", "password123")
	seedKnowledgeLearning(t, srv)
	fake := &knowledgeStageFake{fail: true}
	srv.worker.WithBundleResolver(func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{KnowledgeArticle: fake}, nil
	})
	if err := srv.RunKnowledgeArticles(t.Context()); err != nil || fake.calls != 0 {
		t.Fatal("default disabled automation called model")
	}
	form := url.Values{"enabled": {"on"}, "daily_limit": {"1"}, "debounce_minutes": {"0"}, "audience": {"个人读者"}, "style": {"清楚具体"}}
	if rec := postForm(t, srv, session, "/knowledge-articles/settings", form.Encode()); rec.Code != 400 {
		t.Fatal("enabled without config")
	}
	srv.cfg.PodBaseURL, srv.cfg.PodAPIKey, srv.cfg.PodModel = "https://configured.example/v1", "test", "model"
	if rec := postForm(t, srv, session, "/knowledge-articles/settings", form.Encode()); rec.Code != 303 {
		t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
	}
	for i := 0; i < 2; i++ {
		if err := srv.RunKnowledgeArticles(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	jobs, _ := srv.store.ListQueuedOrRunning(t.Context())
	if len(jobs) != 1 {
		t.Fatal("scheduler duplicated initial existing-library scan")
	}
	if err := srv.worker.ProcessOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	records, _ := srv.store.ListKnowledgeArticles(t.Context())
	if records[0].Status != "failed" || !strings.Contains(records[0].Reason, "连接失败") {
		t.Fatalf("failure invisible: %+v", records[0])
	}
	fake.fail = false
	if rec := postForm(t, srv, session, "/knowledge-articles/retry", "article_id="+records[0].ID); rec.Code != 303 {
		t.Fatalf("retry: %d %s", rec.Code, rec.Body.String())
	}
	for i := 0; i < 4; i++ {
		srv.worker.ProcessOne(t.Context())
	}
	records, _ = srv.store.ListKnowledgeArticles(t.Context())
	if records[0].Status != "ready" || fake.calls != 5 {
		t.Fatalf("retry journey %+v calls=%d", records[0], fake.calls)
	}
	if rec := doWithCookie(srv, session, "GET", "/knowledge-articles"); rec.Code != 200 || !strings.Contains(rec.Body.String(), "开启后台自动生成") {
		t.Fatal("list/settings missing")
	}
	if rec := doWithCookie(srv, session, "GET", "/knowledge-articles/missing"); rec.Code != 404 {
		t.Fatal("missing article")
	}
	for _, path := range []string{"/knowledge-articles/generate", "/knowledge-articles/settings", "/knowledge-articles/retry"} {
		if rec := doWithCookie(srv, session, "GET", path); rec.Code != 405 {
			t.Fatal("GET mutation accepted")
		}
		if rec := doWithCookie(srv, nil, "POST", path); rec.Code != 303 || rec.Header().Get("Location") != "/login" {
			t.Fatal("unauthenticated mutation accepted")
		}
		if rec := doWithCookie(srv, session, "POST", path); rec.Code != 403 {
			t.Fatal("CSRF bypass")
		}
	}
}

// TestKnowledgeArticleBrowserHarness exposes only a scratch, opt-in UI fixture.
func TestKnowledgeArticleBrowserHarness(t *testing.T) {
	if os.Getenv("CWP_KNOWLEDGE_BROWSER") != "1" {
		t.Skip("explicit browser fixture only")
	}
	srv := newTestServer(t)
	seedKnowledgeLearning(t, srv)
	if _, err := srv.store.ClaimOwner(t.Context(), "knowledge-browser@example.com", mustBrowserHash(t)); err != nil {
		t.Fatal(err)
	}
	srv.cfg.PodBaseURL, srv.cfg.PodAPIKey, srv.cfg.PodModel = "https://fixture.invalid/v1", "fixture-key", "浏览器测试模型"
	fake := &knowledgeStageFake{}
	srv.worker.WithBundleResolver(func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{KnowledgeArticle: fake}, nil
	})
	listener, err := net.Listen("tcp", "127.0.0.1:18086")
	if err != nil {
		t.Fatal(err)
	}
	httpServer := &http.Server{Handler: srv.Router()}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	workerDone := make(chan struct{})
	go func() { defer close(workerDone); srv.worker.Run(ctx) }()
	go httpServer.Serve(listener)
	fmt.Println("KNOWLEDGE_BROWSER_READY http://127.0.0.1:18086/login")
	stop := os.Getenv("CWP_KNOWLEDGE_BROWSER_STOP")
	if stop == "" {
		t.Fatal("stop file must be provided")
	}
	deadline := time.NewTimer(10 * time.Minute)
	defer deadline.Stop()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-deadline.C:
			t.Fatal("browser fixture timeout")
		case <-ticker.C:
			if _, err := os.Stat(stop); err == nil {
				cancel()
				<-workerDone
				shutdown, done := context.WithTimeout(context.Background(), 5*time.Second)
				defer done()
				httpServer.Shutdown(shutdown)
				return
			}
		}
	}
}

func mustBrowserHash(t *testing.T) string {
	t.Helper()
	hash, err := auth.HashPassword("browser-test-password")
	if err != nil {
		t.Fatal(err)
	}
	return hash
}
