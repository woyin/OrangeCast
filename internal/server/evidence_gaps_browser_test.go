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

	"github.com/woyin/orangecast/internal/filehash"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/store"
)

// TestEvidenceGapBrowserHarness is an opt-in isolated, real-router browser
// fixture. It never starts the worker or calls an external provider.
func TestEvidenceGapBrowserHarness(t *testing.T) {
	if os.Getenv("CWP_GAP_BROWSER") != "1" {
		t.Skip("set CWP_GAP_BROWSER=1 for isolated interactive validation")
	}
	srv, _, ep := seedSnapshotSource(t)
	ctx := t.Context()
	raw := `{"language":"zh","text":"自建补听材料","segments":[{"id":"gap-first","start":20,"end":45,"text":"主动回忆实践：用自己的例子解释知识。"},{"id":"gap-second","start":60,"end":90,"text":"主动回忆反例：只背术语不能说明适用条件。"}]}`
	if _, err := srv.store.DB.Exec(`UPDATE artifact_versions SET payload=? WHERE source_type='episode' AND source_id=? AND kind='transcript' AND version=1`, raw, ep); err != nil {
		t.Fatal(err)
	}
	audioPath := filepath.Join(srv.cfg.EvidenceDir, "gap-browser-original.wav")
	if err := writeBrowserAcceptanceWAV(audioPath, 120); err != nil {
		t.Fatal(err)
	}
	sha, err := filehash.SHA256(audioPath)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(audioPath)
	if err != nil {
		t.Fatal(err)
	}
	if err = srv.store.UpsertEvidenceAudio(ctx, models.SourceEpisode, ep, "gap-browser-original.wav", "wav", info.Size(), sha); err != nil {
		t.Fatal(err)
	}
	snapshot, err := srv.store.FreezeSourceSnapshot(ctx, models.SourceEpisode, ep)
	if err != nil {
		t.Fatal(err)
	}
	if err = srv.store.RebuildKnowledgeSearch(ctx); err != nil {
		t.Fatal(err)
	}
	doc, err := srv.store.CreatePastedDocument(ctx, "自建主动回忆文档", "主动回忆实践：验证自己的解释是否包含条件。")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = srv.store.FreezeSourceSnapshot(ctx, models.SourceDocument, doc.ID); err != nil {
		t.Fatal(err)
	}
	question, err := srv.store.CreateLearningQuestion(ctx, store.LearningQuestion{Body: "自建验收：主动回忆怎样实践？", Goal: "核对补听链路，不作真实质量评分"})
	if err != nil {
		t.Fatal(err)
	}

	var djID string
	if os.Getenv("CWP_GAP_DJ") == "1" {
		plan, e := srv.store.CreateDJPlan(ctx, &models.DJPlan{SourceType: models.SourceEpisode, SourceID: ep, HighlightVersion: 1, TargetSeconds: 55, TotalSeconds: 55, Items: []models.DJPlanItem{{Kind: models.DJItemEvidence, HighlightID: "self-first", SegmentIDs: []string{"gap-first"}, Start: 20, End: 45, EstSeconds: 25}, {Kind: models.DJItemEvidence, HighlightID: "self-second", SegmentIDs: []string{"gap-second"}, Start: 60, End: 90, EstSeconds: 30}}})
		if e != nil {
			t.Fatal(e)
		}
		djID = plan.ID
		for _, segment := range []string{"gap-first", "gap-second"} {
			ex, e := srv.store.CreateLearningExcerpt(ctx, snapshot.ID, []string{segment})
			if e != nil {
				t.Fatal(e)
			}
			q, e := srv.store.GetListeningQueue(ctx)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = srv.store.ChangeListeningQueue(ctx, q.Revision, store.ListeningQueueChange{Action: "add", SourceType: models.SourceEpisode, SourceID: ep, Mode: "excerpt", ExcerptID: ex.ID}); e != nil {
				t.Fatal(e)
			}
		}
	}
	fixture := map[string]any{"dj_plan_id": djID, "source_id": ep, "snapshot_id": snapshot.ID, "question_id": question.ID, "document_id": doc.ID, "email": "snap@t.local", "password": "password123", "db_path": srv.cfg.DBPath, "audio_path": audioPath}
	mux := http.NewServeMux()
	mux.Handle("/", srv.Router())
	mux.HandleFunc("/__gap_stats", func(w http.ResponseWriter, r *http.Request) {
		counts := map[string]int{}
		for _, table := range []string{"processing_jobs", "artifact_versions", "dj_plans", "usage_records", "learning_excerpts", "owner_notes", "listening_reflections", "evidence_gaps", "evidence_gap_operations"} {
			var n int
			if err := srv.store.DB.QueryRowContext(r.Context(), "SELECT count(*) FROM "+table).Scan(&n); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			counts[table] = n
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(counts)
	})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	httpServer := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go httpServer.Serve(listener)
	fixture["url"] = "http://" + listener.Addr().String()
	payload, _ := json.Marshal(fixture)
	fmt.Printf("GAP_BROWSER_READY=%s\n", payload)
	stop := os.Getenv("CWP_GAP_BROWSER_STOP")
	if stop == "" {
		stop = filepath.Join(srv.cfg.DataDir, "gap-browser.stop")
	}
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for range ticker.C {
		if _, err := os.Stat(stop); err == nil {
			shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err = httpServer.Shutdown(shutdown); err != nil {
				t.Fatal(err)
			}
			return
		}
	}
}
