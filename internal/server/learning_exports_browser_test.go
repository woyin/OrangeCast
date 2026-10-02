package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

// TestLearningExportThemeBrowserHarness is opt-in and owns an isolated scratch
// database, real Theme relations, fake local audio and explicit control files.
func TestLearningExportThemeBrowserHarness(t *testing.T) {
	if os.Getenv("CWP_EXPORT_THEME_BROWSER") != "1" {
		t.Skip("explicit isolated export browser")
	}
	control := os.Getenv("CWP_EXPORT_THEME_CONTROL")
	if control == "" {
		t.Fatal("explicit control directory required")
	}
	srv, _, fixture, questionCalls, articleCalls := personalV4Seed(t)
	profile, err := srv.store.EnsureDefaultEditorialProfile(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	theme, err := srv.store.CreateTheme(t.Context(), models.Theme{EditorialProfileID: profile.ID, Name: "跨集主动回忆：来源与理解", Status: "confirmed"})
	if err != nil {
		t.Fatal(err)
	}
	for i, source := range fixture.Sources[:2] {
		_, err = srv.store.IndexKeyPoints(t.Context(), models.SourceEpisode, source, "导出主题来源", 1, &provider.KnowledgeCard{KeyPoints: []provider.KeyPoint{{Content: fmt.Sprintf("主题观点%d：主动回忆需核对来源条件", i+1), Citations: []string{"seg-1"}}}}, []provider.Segment{{ID: "seg-1", Start: 20, End: 40}})
		if err != nil {
			t.Fatal(err)
		}
	}
	approveThemeTestKeyPoints(t, srv)
	points, _, err := srv.store.ListKeyPoints(t.Context(), 1, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, point := range points {
		if point.SourceID == fixture.Sources[0] || point.SourceID == fixture.Sources[1] {
			if err := srv.store.AddKeyPointToTheme(t.Context(), theme.ID, point.ID, "supports"); err != nil {
				t.Fatal(err)
			}
		}
	}
	relations, err := srv.store.ListThemeKeyPoints(t.Context(), theme.ID)
	if err != nil || len(relations) < 2 {
		t.Fatal("real Theme requires two source relations", err, len(relations))
	}
	listener, err := net.Listen("tcp", "127.0.0.1:18135")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: srv.Router(), ReadHeaderTimeout: 5 * time.Second}
	go server.Serve(listener)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); srv.worker.Run(ctx) }()
	defer func() {
		cancel()
		<-done
		shutdown, end := context.WithTimeout(context.Background(), 5*time.Second)
		defer end()
		server.Shutdown(shutdown)
	}()
	payload, _ := json.Marshal(map[string]any{"url": "http://" + listener.Addr().String(), "theme_id": theme.ID, "source_audio": fixture.Sources[2], "withdraw_source": fixture.Sources[0], "db": srv.cfg.DBPath, "relations": len(relations)})
	fmt.Printf("EXPORT_THEME_BROWSER_READY=%s\n", payload)
	timer := time.NewTicker(200 * time.Millisecond)
	defer timer.Stop()
	for {
		select {
		case <-t.Context().Done():
			t.Fatal("harness timeout")
		case <-timer.C:
			if _, err := os.Stat(filepath.Join(control, "stop")); err == nil {
				fmt.Printf("EXPORT_THEME_MODEL_COUNTS question=%d article=%d\n", questionCalls.Load(), articleCalls.Load())
				return
			}
			if _, err := os.Stat(filepath.Join(control, "withdraw")); err == nil {
				if err := srv.store.ArchiveSource(t.Context(), models.SourceEpisode, fixture.Sources[0], true); err != nil {
					t.Fatal(err)
				}
				os.Remove(filepath.Join(control, "withdraw"))
				os.WriteFile(filepath.Join(control, "withdraw-done"), []byte("yes"), 0600)
			}
			if b, err := os.ReadFile(filepath.Join(control, "missing")); err == nil {
				v, err := srv.store.GetLearningExport(t.Context(), strings.TrimSpace(string(b)))
				if err != nil {
					t.Fatal(err)
				}
				if err = os.Remove(v.Path); err != nil {
					t.Fatal(err)
				}
				os.Remove(filepath.Join(control, "missing"))
				os.WriteFile(filepath.Join(control, "missing-done"), []byte("yes"), 0600)
			}
		}
	}
}
