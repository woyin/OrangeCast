package server

import (
	"context"
	"crypto/sha256"
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

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
)

// Explicit opt-in scratch DB harness; no user data, env secrets or worker loops.
func TestOfflineGoBrowserHarness(t *testing.T) {
	if os.Getenv("CWP_OFFLINE_GO_BROWSER") != "1" {
		t.Skip("interactive offline browser harness")
	}
	srv := newTestServer(t)
	claimOwnerAndLogin(t, srv, "offline-go@example.com", "password123")
	srv.bundleFor = func(provider.TaskConfig) (*provider.ProviderBundle, error) { panic("offline browser attempted AI") }
	ep := seedEpisodeWithTranscript(t, srv, "离线端到端自建原音")
	path := filepath.Join(srv.cfg.EvidenceDir, "original.wav")
	if err := writeBrowserAcceptanceWAV(path, 8); err != nil {
		t.Fatal(err)
	}
	bytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sha := fmt.Sprintf("%x", sha256.Sum256(bytes))
	if err = srv.store.UpsertEvidenceAudio(t.Context(), models.SourceEpisode, ep, "original.wav", "wav", int64(len(bytes)), sha); err != nil {
		t.Fatal(err)
	}
	snap, err := srv.store.FreezeSourceSnapshot(t.Context(), models.SourceEpisode, ep)
	if err != nil {
		t.Fatal(err)
	}
	ex, err := srv.store.CreateLearningExcerpt(t.Context(), snap.ID, []string{"seg-1", "seg-2"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = srv.store.ChangeListeningQueue(t.Context(), 0, store.ListeningQueueChange{Action: "add", Mode: "excerpt", SourceType: models.SourceEpisode, SourceID: ep, ExcerptID: ex.ID}); err != nil {
		t.Fatal(err)
	}
	shellExcerpt, err := srv.store.CreateLearningExcerpt(t.Context(), snap.ID, []string{"seg-2"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = srv.store.ChangeListeningQueue(t.Context(), 1, store.ListeningQueueChange{Action: "add", Mode: "excerpt", SourceType: models.SourceEpisode, SourceID: ep, ExcerptID: shellExcerpt.ID}); err != nil {
		t.Fatal(err)
	}
	if err = srv.store.RebuildKnowledgeSearch(t.Context()); err != nil {
		t.Fatal(err)
	}
	var loseAck atomic.Bool
	var lostAcks atomic.Int64
	router := srv.Router()
	mux := http.NewServeMux()
	mux.HandleFunc("/__offline_capability", func(w http.ResponseWriter, r *http.Request) {
		fault := r.URL.Query().Get("case")
		var script string
		switch fault {
		case "unsupported-sw":
			script = `Object.defineProperty(navigator,'serviceWorker',{value:undefined,configurable:true});`
		case "denied-sw":
			script = `navigator.serviceWorker.register=()=>Promise.reject(new DOMException('Synthetic register denied','SecurityError'));`
		case "failed-idb":
			script = `indexedDB.open=()=>{throw new DOMException('Synthetic IDB denied','UnknownError')};`
		default:
			w.WriteHeader(400)
			return
		}
		body, err := staticFS.ReadFile("static/offline-shell.html")
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		marker, _ := json.Marshal(fault)
		prefix := "<script>window.__offlineCapabilityCase=" + string(marker) + ";" + script + "</script>"
		// The actual embedded product shell and scripts execute after a scoped,
		// document-only browser API fault. Existing IndexedDB contents are untouched.
		html := strings.Replace(string(body), `<script src="/static/offline-core.js">`, prefix+`<script src="/static/offline-core.js">`, 1)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Write([]byte(html))
	})
	mux.HandleFunc("/__offline_stats", func(w http.ResponseWriter, r *http.Request) {
		counts := map[string]any{"lost_acks": lostAcks.Load()}
		for _, table := range []string{"owner_notes", "offline_organizing_drafts", "offline_operation_receipts", "listening_progress", "learning_excerpt_progress"} {
			var count int
			if err := srv.store.DB.QueryRowContext(r.Context(), "SELECT count(*) FROM "+table).Scan(&count); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			counts[table] = count
		}
		notes, e := srv.store.ListOwnerNotes(r.Context(), models.SourceEpisode, ep)
		if e != nil {
			http.Error(w, e.Error(), 500)
			return
		}
		counts["notes"] = notes
		drafts, e := srv.store.ListOfflineOrganizingDrafts(r.Context())
		if e != nil {
			http.Error(w, e.Error(), 500)
			return
		}
		counts["drafts"] = drafts
		original, _ := srv.store.GetListeningProgressMode(r.Context(), models.SourceEpisode, ep, "original")
		counts["original_progress"] = original
		excerpt, _ := srv.store.GetLearningExcerptProgress(r.Context(), models.SourceEpisode, ep, ex.ID)
		counts["excerpt_progress"] = excerpt
		shellProgress, _ := srv.store.GetLearningExcerptProgress(r.Context(), models.SourceEpisode, ep, shellExcerpt.ID)
		counts["shell_excerpt_progress"] = shellProgress
		writeJSON(w, 200, counts)
	})
	mux.HandleFunc("/__offline_control", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(405)
			return
		}
		var in struct {
			Action string `json:"action"`
		}
		if json.NewDecoder(r.Body).Decode(&in) != nil {
			w.WriteHeader(400)
			return
		}
		var e error
		switch in.Action {
		case "lose_ack":
			loseAck.Store(true)
		case "restore_ack":
			loseAck.Store(false)
		case "change_source":
			var jobID string
			e = srv.store.DB.QueryRowContext(r.Context(), `SELECT job_id FROM artifact_versions WHERE source_id=? AND kind='transcript' LIMIT 1`, ep).Scan(&jobID)
			if e == nil {
				var v int
				v, e = srv.store.CreateArtifactVersion(r.Context(), models.SourceEpisode, ep, "transcript", "test", "test", "1", jobID, `{"text":"新版本原文","segments":[{"id":"new-1","start":0,"end":1,"text":"新原文"}]}`)
				if e == nil {
					e = srv.store.SetCurrentVersion(r.Context(), models.SourceEpisode, ep, "transcript", v)
				}
			}
		case "purge":
			e = srv.store.DeleteSourceRows(r.Context(), models.SourceEpisode, ep)
		case "revoke":
			_, e = srv.store.DB.ExecContext(r.Context(), `UPDATE offline_devices SET revoked=1`)
		default:
			w.WriteHeader(400)
			return
		}
		if e != nil {
			http.Error(w, e.Error(), 500)
			return
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
	})
	mux.HandleFunc("/api/offline/sync", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && loseAck.Load() {
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, r)
			if recorder.Code == http.StatusOK {
				lostAcks.Add(1)
				conn, _, err := w.(http.Hijacker).Hijack()
				if err == nil {
					conn.Close()
					return
				}
			}
			for k, v := range recorder.Header() {
				w.Header()[k] = v
			}
			w.WriteHeader(recorder.Code)
			w.Write(recorder.Body.Bytes())
			return
		}
		router.ServeHTTP(w, r)
	})
	mux.Handle("/", router)
	ln, err := net.Listen("tcp", "127.0.0.1:18777")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	defer server.Close()
	go server.Serve(ln)
	fixture := map[string]any{"url": "http://127.0.0.1:18777", "email": "offline-go@example.com", "password": "password123", "source_id": ep, "excerpt_id": ex.ID, "shell_excerpt_id": shellExcerpt.ID, "audio_sha": sha, "data_dir": srv.cfg.DataDir}
	raw, _ := json.Marshal(fixture)
	fmt.Printf("OFFLINE_GO_READY=%s\n", raw)
	stop := os.Getenv("CWP_OFFLINE_GO_STOP")
	if stop == "" {
		stop = filepath.Join(srv.cfg.DataDir, "stop")
	}
	for {
		select {
		case <-t.Context().Done():
			return
		case <-time.After(200 * time.Millisecond):
			if _, e := os.Stat(stop); e == nil {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := server.Shutdown(ctx); err != nil {
					t.Fatal(err)
				}
				return
			}
		}
	}
}
