package server

import (
	"encoding/json"
	"github.com/woyin/orangecast/internal/models"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLearningExcerptsHTTPFrozenAudioProgressAndPrivacy(t *testing.T) {
	srv, session, ep := seedSnapshotSource(t)
	ctx := t.Context()
	snap, err := srv.store.FreezeSourceSnapshot(ctx, models.SourceEpisode, ep)
	if err != nil {
		t.Fatal(err)
	}
	body := `{"snapshot_id":"` + snap.ID + `","segment_ids":["seg-0001"]}`
	if rec := queueJSONRequest(t, srv, nil, "POST", "/api/learning-excerpts", body, true); rec.Code != 401 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	if rec := queueJSONRequest(t, srv, session, "POST", "/api/learning-excerpts", body, false); rec.Code != 403 {
		t.Fatal(rec.Code)
	}
	rec := queueJSONRequest(t, srv, session, "POST", "/api/learning-excerpts", body, true)
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	var excerpt models.LearningExcerpt
	if json.Unmarshal(rec.Body.Bytes(), &excerpt) != nil || excerpt.StartSeconds != 3 || excerpt.EndSeconds != 9 {
		t.Fatal(rec.Body.String())
	}
	query := url.Values{"source_type": {"episode"}, "source_id": {ep}, "mode": {"excerpt"}, "excerpt_id": {excerpt.ID}, "audio_sha256": {excerpt.AudioSHA256}}
	path := "/api/listening-session?" + query.Encode()
	rec = queueJSONRequest(t, srv, session, "GET", path, "", false)
	var item models.ListeningQueueItem
	if json.Unmarshal(rec.Body.Bytes(), &item) != nil || item.Available || !strings.Contains(item.Reason, "缺失") {
		t.Fatal("missing audio playable", rec.Code, rec.Body.String())
	}
	audioPath := filepath.Join(srv.cfg.EvidenceDir, "evidence/s.mp3")
	if err = os.MkdirAll(filepath.Dir(audioPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(audioPath, []byte("local test audio"), 0600); err != nil {
		t.Fatal(err)
	}
	rec = queueJSONRequest(t, srv, session, "GET", path, "", false)
	if json.Unmarshal(rec.Body.Bytes(), &item) != nil || !item.Available || item.SnapshotID != snap.ID || item.SnapshotVersion != 1 || item.StartSeconds != 3 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	progress := `{"source_type":"episode","source_id":"` + ep + `","mode":"excerpt","excerpt_id":"` + excerpt.ID + `","snapshot_id":"` + snap.ID + `","audio_sha256":"sha-api","item_offset_seconds":5,"speed":1,"expected_revision":0}`
	rec = queueJSONRequest(t, srv, session, "POST", "/api/listening-progress", progress, true)
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	rec = queueJSONRequest(t, srv, session, "GET", "/api/listening-progress?"+query.Encode(), "", false)
	var saved models.ListeningProgress
	if json.Unmarshal(rec.Body.Bytes(), &saved) != nil || saved.ItemOffsetSeconds != 5 || saved.ExcerptID != excerpt.ID || saved.SnapshotID != snap.ID {
		t.Fatal(rec.Code, rec.Body.String())
	}
	bad := strings.Replace(progress, `"item_offset_seconds":5`, `"item_offset_seconds":2`, 1)
	if rec = queueJSONRequest(t, srv, session, "POST", "/api/listening-progress", bad, true); rec.Code != 400 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	if err = srv.store.UpsertEvidenceAudio(ctx, models.SourceEpisode, ep, "evidence/s.mp3", "mp3", 88, "sha-new"); err != nil {
		t.Fatal(err)
	}
	rec = queueJSONRequest(t, srv, session, "GET", path, "", false)
	if json.Unmarshal(rec.Body.Bytes(), &item) != nil || item.Available {
		t.Fatal("replaced audio playable", rec.Body.String())
	}
}
