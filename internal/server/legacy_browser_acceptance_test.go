package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/woyin/orangecast/internal/provider"
)

// Opt-in loopback harness uses the real player/router/queue and isolated SQLite
// fixture. It never reads .env or calls an external supplier.
func TestLegacyStudyBrowserAcceptanceHarness(t *testing.T) {
	if os.Getenv("CWP_LEGACY_BROWSER_ACCEPTANCE") != "1" {
		t.Skip("opt-in legacy StudyChat browser harness")
	}
	srv := newTestServer(t)
	srv.cfg.NarrationDir = filepath.Join(srv.cfg.DataDir, "narrations")
	session := claimOwnerAndLogin(t, srv, browserAcceptanceEmail, browserAcceptancePassword)
	for _, dir := range []string{srv.cfg.EvidenceDir, srv.cfg.NarrationDir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	fixture := seedBrowserAcceptanceFixture(t, srv, session)
	var calls atomic.Int32
	supplier := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		var request struct {
			Model    string                          `json:"model"`
			Messages []provider.QuestionStudyMessage `json:"messages"`
		}
		if json.NewDecoder(r.Body).Decode(&request) != nil {
			http.Error(w, "invalid", 400)
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-time.After(time.Second):
		}
		content := `{"answer":"这里需要先核对证据，再用自己的话解释。","referenceSegmentIds":["seg-0001"]}`
		if len(request.Messages) > 0 && strings.Contains(request.Messages[0].Content, "related") {
			content = `{"related":true,"reason":"围绕本集证据"}`
		}
		if len(request.Messages) > 1 && strings.Contains(request.Messages[1].Content, "模拟未知") {
			w.Write([]byte(`{`))
			return
		}
		raw, _ := json.Marshal(map[string]any{"model": request.Model, "choices": []any{map[string]any{"message": map[string]any{"content": content}}}, "usage": map[string]int{"prompt_tokens": 30 + int(n), "completion_tokens": 12}})
		w.Write(raw)
	}))
	t.Cleanup(supplier.Close)
	srv.selector.ApplySettings("test-key", supplier.URL, "", "")
	workerCtx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go srv.worker.Run(workerCtx)
	listener, err := net.Listen("tcp", "127.0.0.1:18130")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: srv.Router(), ReadHeaderTimeout: 5 * time.Second}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	fixture.URL = "http://127.0.0.1:18130"
	fixture.DataDir = srv.cfg.DataDir
	raw, _ := json.Marshal(fixture)
	fmt.Printf("LEGACY_BROWSER_READY=%s\n", raw)
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case err := <-done:
			if err != nil && err != http.ErrServerClosed {
				t.Fatal(err)
			}
			return
		case <-ticker.C:
			if _, err = os.Stat("/tmp/c09-legacy.stop"); err == nil {
				stop, release := context.WithTimeout(context.Background(), 5*time.Second)
				defer release()
				if err = server.Shutdown(stop); err != nil {
					t.Fatal(err)
				}
				fmt.Printf("LEGACY_BROWSER_SUPPLIER_CALLS=%d\n", calls.Load())
				if err = <-done; err != nil && err != http.ErrServerClosed {
					t.Fatal(err)
				}
				return
			} else if !os.IsNotExist(err) {
				t.Fatal(err)
			}
		}
	}
}
