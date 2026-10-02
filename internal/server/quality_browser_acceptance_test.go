package server

import (
	"context"
	"fmt"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"net"
	"net/http"
	"os"
	"testing"
	"time"
)

// TestArticleQualityBrowserHarness is isolated self-authored content, with no live provider or default database.
func TestArticleQualityBrowserHarness(t *testing.T) {
	if os.Getenv("CWP_QUALITY_BROWSER") != "1" {
		t.Skip("explicit interactive quality fixture")
	}
	stop := os.Getenv("CWP_QUALITY_BROWSER_STOP")
	if stop == "" {
		t.Fatal("stop file required")
	}
	srv := newTestServer(t)
	seedKnowledgeLearning(t, srv)
	if _, err := srv.store.ClaimOwner(t.Context(), "quality-browser@example.com", mustBrowserHash(t)); err != nil {
		t.Fatal(err)
	}
	srv.cfg.PodBaseURL, srv.cfg.PodAPIKey, srv.cfg.PodModel = "https://fixture.invalid/v1", "fixture-key", "quality-browser-fixture"
	srv.worker.WithBundleResolver(func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{KnowledgeArticle: purposeBrowserProvider{}}, nil
	})
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
	if err = srv.store.QueueKnowledgePurposeRevision(t.Context(), article.ID, 1, "practice", "", "quality-browser-fixture"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err = srv.worker.ProcessOne(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	listener, err := net.Listen("tcp", "127.0.0.1:18096")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: srv.Router(), ReadHeaderTimeout: 5 * time.Second}
	go server.Serve(listener)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		server.Shutdown(ctx)
	}()
	fmt.Printf("QUALITY_BROWSER_READY=http://127.0.0.1:18096/login\nQUALITY_BROWSER_ARTICLE=http://127.0.0.1:18096/knowledge-articles/%s?revision=1\nQUALITY_BROWSER_DB=%s\n", article.ID, srv.cfg.DBPath)
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(10 * time.Minute)
	defer deadline.Stop()
	for {
		select {
		case <-deadline.C:
			t.Fatal("quality browser timeout")
		case <-ticker.C:
			if _, err := os.Stat(stop); err == nil {
				return
			}
		}
	}
}
