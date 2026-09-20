package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

const (
	browserAcceptanceEmail    = "browser-acceptance@example.com"
	browserAcceptancePassword = "password123"
)

// TestBrowserAcceptanceHarness starts an explicitly enabled, test-only HTTP
// server backed by scratch SQLite data and deterministic providers. It keeps
// the production router, templates, queue and persistence boundaries intact
// so agent-browser can exercise the real UI without calling external models.
func TestBrowserAcceptanceHarness(t *testing.T) {
	if os.Getenv("CWP_BROWSER_ACCEPTANCE") != "1" {
		t.Skip("set CWP_BROWSER_ACCEPTANCE=1 to start the interactive harness")
	}

	srv := newTestServer(t)
	srv.cfg.NarrationDir = filepath.Join(srv.cfg.DataDir, "narrations")
	for _, dir := range []string{srv.cfg.EvidenceDir, srv.cfg.NarrationDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	session := claimOwnerAndLogin(t, srv, browserAcceptanceEmail, browserAcceptancePassword)
	fixture := seedBrowserAcceptanceFixture(t, srv, session)

	addr := os.Getenv("CWP_BROWSER_ADDR")
	if addr == "" {
		addr = "127.0.0.1:18085"
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("listen %s: %v", addr, err)
	}
	httpServer := &http.Server{Handler: srv.Router(), ReadHeaderTimeout: 5 * time.Second}
	serveErr := make(chan error, 1)
	go func() {
		serveErr <- httpServer.Serve(listener)
	}()

	fixture.URL = "http://" + listener.Addr().String()
	fixture.DataDir = srv.cfg.DataDir
	payload, err := json.Marshal(fixture)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("BROWSER_ACCEPTANCE_READY=%s\n", payload)

	stopFile := os.Getenv("CWP_BROWSER_STOP_FILE")
	if stopFile == "" {
		stopFile = filepath.Join(srv.cfg.DataDir, "browser-acceptance.stop")
	}
	for {
		select {
		case err := <-serveErr:
			if err != nil && err != http.ErrServerClosed {
				t.Fatalf("acceptance server: %v", err)
			}
			return
		case <-time.After(200 * time.Millisecond):
			if _, err := os.Stat(stopFile); err == nil {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := httpServer.Shutdown(ctx); err != nil {
					t.Fatalf("shutdown acceptance server: %v", err)
				}
				if err := <-serveErr; err != nil && err != http.ErrServerClosed {
					t.Fatalf("acceptance server: %v", err)
				}
				return
			} else if !os.IsNotExist(err) {
				t.Fatalf("stat stop file: %v", err)
			}
		}
	}
}

type browserAcceptanceFixture struct {
	URL              string `json:"url"`
	DataDir          string `json:"data_dir"`
	Email            string `json:"email"`
	Password         string `json:"password"`
	LearningSourceID string `json:"learning_source_id"`
	DigestID         string `json:"digest_id"`
	EditableBriefID  string `json:"editable_brief_id"`
	BlockedDraftID   string `json:"blocked_draft_id"`
	BlockedRevision  string `json:"blocked_revision_id"`
	ReadyDraftID     string `json:"ready_draft_id"`
	ReadyRevision    string `json:"ready_revision_id"`
}

func seedBrowserAcceptanceFixture(t *testing.T, srv *Server, session *http.Cookie) browserAcceptanceFixture {
	t.Helper()
	ctx := t.Context()

	podcast, err := srv.store.CreatePodcast(ctx, "https://feed.example.com/r25-learning.xml", "R25 学习与创作", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.store.MergeEpisodes(ctx, podcast.ID, []models.Episode{{
		GUID: "r25-learning", Title: "从收听到可审校作品", AudioURL: "https://media.example.com/r25-learning.mp3",
	}}); err != nil {
		t.Fatal(err)
	}
	episodes, err := srv.store.ListEpisodes(ctx, podcast.ID)
	if err != nil || len(episodes) != 1 {
		t.Fatalf("learning episode: %v %+v", err, episodes)
	}
	learningSourceID := episodes[0].ID
	rawAudio := filepath.Join(srv.cfg.DataDir, "r25-learning.wav")
	if err := writeBrowserAcceptanceWAV(rawAudio, 12); err != nil {
		t.Fatal(err)
	}
	bundle := journeyLearningBundle()
	srv.worker.WithBundleResolver(func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return bundle, nil
	}).WithRawAudioResolver(func(context.Context, *models.ProcessingJob) (string, func(), error) {
		return rawAudio, func() {}, nil
	})
	if rec := postForm(t, srv, session, "/api/process", "source_type=episode&source_id="+learningSourceID); rec.Code != http.StatusSeeOther {
		t.Fatalf("process learning fixture: %d %s", rec.Code, rec.Body.String())
	}
	drainJourneyJobs(t, srv, 12)
	if rec := postForm(t, srv, session, "/api/owner-notes", "source_type=episode&source_id="+learningSourceID+"&kind=owner_reflection&content=夹具笔记：证据与个人判断需要分层&citations_json=[]&references_json=[\"seg-0001\"]"); rec.Code != http.StatusSeeOther {
		t.Fatalf("learning note fixture: %d %s", rec.Code, rec.Body.String())
	}
	if rec := postForm(t, srv, session, "/api/digest", "source_type=episode&source_id="+learningSourceID); rec.Code != http.StatusSeeOther {
		t.Fatalf("digest fixture: %d %s", rec.Code, rec.Body.String())
	}
	drainJourneyJobs(t, srv, 3)
	digest, err := srv.store.GetCurrentEpisodeDigest(ctx, models.SourceEpisode, learningSourceID)
	if err != nil {
		t.Fatal(err)
	}

	editableBrief, _ := seedR19ConfirmedBrief(t, srv, "R25-方案编辑")
	if _, err := srv.store.CreateCreationBriefRevisionCAS(ctx, editableBrief.ID, editableBrief.CurrentVersion, models.CreationBriefRevision{
		OwnerClaim: editableBrief.OwnerClaim, Outline: "一、保留证据\n二、形成判断\n三、写成作品",
		MaterialPlanJSON: editableBrief.MaterialPlanJSON, Style: "清晰、克制", Notes: "等待浏览器确认",
	}); err != nil {
		t.Fatal(err)
	}

	blockedRevision, blockedDraftID, _ := seedNewContractRevision(t, srv, "R25-审校门禁")
	readyRevision, readyDraftID := readyNewContractFixture(t, srv, "R25-可交付文章")

	return browserAcceptanceFixture{
		Email: browserAcceptanceEmail, Password: browserAcceptancePassword,
		LearningSourceID: learningSourceID, DigestID: digest.ID, EditableBriefID: editableBrief.ID,
		BlockedDraftID: blockedDraftID, BlockedRevision: blockedRevision.ID,
		ReadyDraftID: readyDraftID, ReadyRevision: readyRevision.ID,
	}
}

func writeBrowserAcceptanceWAV(path string, seconds int) error {
	const sampleRate = 8000
	dataSize := uint32(sampleRate * 2 * seconds)
	riffSize := uint32(36) + dataSize
	buf := make([]byte, 0, 44+int(dataSize))
	buf = append(buf, "RIFF"...)
	buf = append(buf, byte(riffSize), byte(riffSize>>8), byte(riffSize>>16), byte(riffSize>>24))
	buf = append(buf, "WAVEfmt "...)
	buf = append(buf, 16, 0, 0, 0, 1, 0, 1, 0)
	buf = append(buf, 0x40, 0x1f, 0, 0, 0x80, 0x3e, 0, 0, 2, 0, 16, 0)
	buf = append(buf, "data"...)
	buf = append(buf, byte(dataSize), byte(dataSize>>8), byte(dataSize>>16), byte(dataSize>>24))
	buf = append(buf, make([]byte, dataSize)...)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, buf, 0o644)
}
