package server

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

type purposeBrowserProvider struct{}

func (purposeBrowserProvider) Name() string { return "pod" }
func (purposeBrowserProvider) KnowledgeArticleStep(_ context.Context, req provider.KnowledgeArticleRequest) (*provider.KnowledgeArticleResult, provider.TaskUsage, error) {
	result := knowledgeStepResult(req, false, false, false)
	if req.Stage == "discover" {
		// Different writing purposes may answer distinct self-authored questions in
		// this fixture; this does not claim that changing style is knowledge gain.
		result.Topics[0].Question += "（" + req.WritingPurpose.Mode + "用途）"
		result.Topics[0].Thesis += "，遵循" + req.WritingPurpose.Mode + "结构"
		result.Topics[0].Title += " · " + req.WritingPurpose.Mode
	}
	if req.Stage == "write" || req.Stage == "revise" {
		for _, mode := range provider.WritingModes() {
			if mode.ID != req.WritingPurpose.Mode || len(mode.Sections) == 0 {
				continue
			}
			var ids []string
			for _, m := range req.Materials {
				if m.Kind == "source_note" || m.Kind == "keypoint" {
					ids = append(ids, m.ID)
				}
			}
			result.Blocks = nil
			for _, section := range mode.Sections {
				result.Blocks = append(result.Blocks, provider.KnowledgeBlock{PurposeSection: section, Kind: "synthesis", Text: "## " + section + "\n\n自建夹具：来源与个人理解应分开，材料不足时明确边界，不能编造例子或效果。", MaterialIDs: ids})
			}
		}
	}
	return result, provider.TaskUsage{InputUnits: 100, OutputUnits: 50}, nil
}

// TestWritingPurposeBrowserHarness is opt-in: scratch database + deterministic
// provider through the real router, queue and model-result validation.
func TestWritingPurposeBrowserHarness(t *testing.T) {
	if os.Getenv("CWP_WRITING_BROWSER") != "1" {
		t.Skip("explicit interactive writing fixture")
	}
	stop := os.Getenv("CWP_WRITING_BROWSER_STOP")
	if strings.TrimSpace(stop) == "" {
		t.Fatal("stop file required")
	}
	srv := newTestServer(t)
	seedKnowledgeLearning(t, srv)
	if _, err := srv.store.ClaimOwner(t.Context(), "writing-browser@example.com", mustBrowserHash(t)); err != nil {
		t.Fatal(err)
	}
	srv.cfg.PodBaseURL, srv.cfg.PodAPIKey, srv.cfg.PodModel = "https://fixture.invalid/v1", "fixture-key", "writing-browser-fixture"
	srv.worker.WithBundleResolver(func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{KnowledgeArticle: purposeBrowserProvider{}}, nil
	})

	historyURL := ""
	if os.Getenv("CWP_WRITING_BROWSER_STAGE") == "history" {
		profile, err := srv.store.EnsureDefaultEditorialProfile(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		article, _, err := srv.enqueueKnowledgeArticle(t.Context(), profile.ID, false)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 4; i++ {
			if err = srv.worker.ProcessOne(t.Context()); err != nil {
				t.Fatal(err)
			}
		}
		if err = srv.store.QueueKnowledgePurposeRevision(t.Context(), article.ID, 1, "practice", "", "writing-browser-fixture"); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 2; i++ {
			if err = srv.worker.ProcessOne(t.Context()); err != nil {
				t.Fatal(err)
			}
		}
		historyURL = "http://127.0.0.1:18089/knowledge-articles/" + article.ID + "?revision=1"
	}
	listener, err := net.Listen("tcp", "127.0.0.1:18089")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: srv.Router(), ReadHeaderTimeout: 5 * time.Second}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); srv.worker.Run(ctx) }()
	go server.Serve(listener)
	defer func() {
		cancel()
		<-done
		shutdown, end := context.WithTimeout(context.Background(), 5*time.Second)
		defer end()
		server.Shutdown(shutdown)
	}()
	fmt.Println("WRITING_BROWSER_READY http://127.0.0.1:18089/login")
	if historyURL != "" {
		fmt.Println("WRITING_BROWSER_HISTORY", historyURL)
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(10 * time.Minute)
	defer deadline.Stop()
	for {
		select {
		case <-deadline.C:
			t.Fatal("writing browser timeout")
		case <-ticker.C:
			if _, err := os.Stat(stop); err == nil {
				return
			}
		}
	}
}
