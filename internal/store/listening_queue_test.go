package store

import (
	"errors"
	"github.com/woyin/orangecast/internal/models"
	"strings"
	"testing"
)

func queueChange(t *testing.T, s *Store, q *models.ListeningQueue, c ListeningQueueChange) *models.ListeningQueue {
	t.Helper()
	result, err := s.ChangeListeningQueue(t.Context(), q.Revision, c)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestListeningQueueIdentityOrderingAndConflictingWindows(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	ep := seedEpisodeForArtifact(t, s)
	up, err := s.CreateUpload(ctx, "queue.mp3", "audio/mpeg", 100)
	if err != nil {
		t.Fatal(err)
	}
	q, err := s.GetListeningQueue(ctx)
	if err != nil || q.Autoplay || q.Revision != 0 || len(q.Items) != 0 {
		t.Fatal(q, err)
	}
	add := ListeningQueueChange{Action: "add", SourceType: models.SourceEpisode, SourceID: ep, Mode: "original"}
	q = queueChange(t, s, q, add)
	id := q.Items[0].ID
	if !q.Items[0].Available || !q.Items[0].Unfrozen || q.Items[0].Reason == "" {
		t.Fatal(q.Items[0])
	}
	duplicate, err := s.ChangeListeningQueue(ctx, 0, add)
	if err != nil || duplicate.Revision != q.Revision || len(duplicate.Items) != 1 || duplicate.Items[0].ID != id {
		t.Fatal("duplicate add moved/renamed", duplicate, err)
	}
	if _, err := s.ChangeListeningQueue(ctx, 0, ListeningQueueChange{Action: "clear"}); !errors.Is(err, ErrConflict) {
		t.Fatal("stale edit overwrote queue", err)
	}
	q = queueChange(t, s, q, ListeningQueueChange{Action: "add", SourceType: models.SourceUpload, SourceID: up.ID, Mode: "original"})
	uploadID := q.Items[1].ID
	if q.Items[1].Available || !strings.Contains(q.Items[1].Reason, "音频") {
		t.Fatal(q.Items[1])
	}
	q = queueChange(t, s, q, ListeningQueueChange{Action: "reorder", Order: []string{uploadID, id}})
	if q.Items[0].ID != uploadID || q.Items[1].ID != id {
		t.Fatal(q.Items)
	}
	q = queueChange(t, s, q, ListeningQueueChange{Action: "move_last", ItemID: uploadID})
	if q.Items[1].ID != uploadID {
		t.Fatal(q.Items)
	}
	q = queueChange(t, s, q, ListeningQueueChange{Action: "play", ItemID: id})
	if q.CurrentItemID != id {
		t.Fatal(q)
	}
	enabled := true
	q = queueChange(t, s, q, ListeningQueueChange{Action: "autoplay", Autoplay: &enabled})
	if !q.Autoplay {
		t.Fatal(q)
	}
	q = queueChange(t, s, q, ListeningQueueChange{Action: "stop"})
	if q.CurrentItemID != "" {
		t.Fatal(q)
	}
	q = queueChange(t, s, q, ListeningQueueChange{Action: "remove", ItemID: uploadID})
	q = queueChange(t, s, q, ListeningQueueChange{Action: "play", ItemID: id})
	q = queueChange(t, s, q, ListeningQueueChange{Action: "remove", ItemID: id})
	if q.CurrentItemID != "" || len(q.Items) != 0 {
		t.Fatal(q)
	}
	if exists, err := s.sourceExists(ctx, models.SourceUpload, up.ID); err != nil || !exists {
		t.Fatal("queue removal deleted source", err)
	}
	q = queueChange(t, s, q, add)
	q = queueChange(t, s, q, ListeningQueueChange{Action: "clear"})
	if len(q.Items) != 0 || q.CurrentItemID != "" {
		t.Fatal(q)
	}
}

func TestListeningQueueAudioIdentityAndPurgeInvalidateCurrent(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	ep := seedEpisodeForArtifact(t, s)
	if err := s.UpsertEvidenceAudio(ctx, models.SourceEpisode, ep, "queue.mp3", "mp3", 100, "old-sha"); err != nil {
		t.Fatal(err)
	}
	q, err := s.GetListeningQueue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	q = queueChange(t, s, q, ListeningQueueChange{Action: "add", SourceType: models.SourceEpisode, SourceID: ep, Mode: "original"})
	id := q.Items[0].ID
	if q.Items[0].Unfrozen || q.Items[0].AudioSHA256 != "old-sha" {
		t.Fatal(q.Items[0])
	}
	q = queueChange(t, s, q, ListeningQueueChange{Action: "play", ItemID: id})
	if _, err := s.DB.ExecContext(ctx, `UPDATE episodes SET title='改名后的节目' WHERE id=?`, ep); err != nil {
		t.Fatal(err)
	}
	q, err = s.GetListeningQueue(ctx)
	if err != nil || q.Items[0].ID != id || q.Items[0].Title != "改名后的节目" {
		t.Fatal(q, err)
	}
	if err := s.UpsertEvidenceAudio(ctx, models.SourceEpisode, ep, "queue-new.mp3", "mp3", 100, "new-sha"); err != nil {
		t.Fatal(err)
	}
	q, err = s.GetListeningQueue(ctx)
	if err != nil || q.Items[0].Available || !strings.Contains(q.Items[0].Reason, "已变化") {
		t.Fatal(q, err)
	}
	if _, err := s.ChangeListeningQueue(ctx, q.Revision, ListeningQueueChange{Action: "play", ItemID: id}); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatal(err)
	}
	if err := s.UpsertEvidenceAudio(ctx, models.SourceEpisode, ep, "queue.mp3", "mp3", 100, "old-sha"); err != nil {
		t.Fatal(err)
	}
	if err := s.ArchiveSource(ctx, models.SourceEpisode, ep, true); err != nil {
		t.Fatal(err)
	}
	q, err = s.GetListeningQueue(ctx)
	if err != nil || q.Items[0].Available || !strings.Contains(q.Items[0].Reason, "归档") {
		t.Fatal(q, err)
	}
	if err := s.ArchiveSource(ctx, models.SourceEpisode, ep, false); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkEvidenceMissing(ctx, models.SourceEpisode, ep); err != nil {
		t.Fatal(err)
	}
	q, err = s.GetListeningQueue(ctx)
	if err != nil || q.Items[0].Available || q.Items[0].Reason != "原音文件不可用" {
		t.Fatal(q, err)
	}
	before := q.Revision
	if err := s.DeleteSourceRows(ctx, models.SourceEpisode, ep); err != nil {
		t.Fatal(err)
	}
	q, err = s.GetListeningQueue(ctx)
	if err != nil || q.CurrentItemID != "" || q.Revision != before+1 || q.Items[0].Available || q.Items[0].Title != "已删除来源" || q.Items[0].Reason != "来源已删除" {
		t.Fatal(q, err)
	}
}

func TestListeningQueueDJIsDistinctAndRequiresExactPlan(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	ep := seedEpisodeForArtifact(t, s)
	if err := s.UpsertEvidenceAudio(ctx, models.SourceEpisode, ep, "queue.mp3", "mp3", 100, "sha"); err != nil {
		t.Fatal(err)
	}
	plan, err := s.CreateDJPlan(ctx, &models.DJPlan{SourceType: models.SourceEpisode, SourceID: ep, HighlightVersion: 1, Items: []models.DJPlanItem{{Kind: models.DJItemEvidence, Start: 0, End: 10}}})
	if err != nil {
		t.Fatal(err)
	}
	q, err := s.GetListeningQueue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	q = queueChange(t, s, q, ListeningQueueChange{Action: "add", SourceType: models.SourceEpisode, SourceID: ep, Mode: "original"})
	add := ListeningQueueChange{Action: "add", SourceType: models.SourceEpisode, SourceID: ep, Mode: "dj", PlanID: plan.ID, PlanVersion: plan.Version}
	q = queueChange(t, s, q, add)
	if len(q.Items) != 2 || !q.Items[1].Available || q.Items[1].ID == q.Items[0].ID {
		t.Fatal(q)
	}
	bad := add
	bad.PlanVersion++
	if _, err := s.ChangeListeningQueue(ctx, q.Revision, bad); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM dj_plans WHERE id=?`, plan.ID); err != nil {
		t.Fatal(err)
	}
	q, err = s.GetListeningQueue(ctx)
	if err != nil || q.Items[1].Available || q.Items[1].Reason != "冻结DJ清单不可用" {
		t.Fatal(q, err)
	}
}

func TestListeningQueueRejectsInvalidEditsAtomically(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	ep := seedEpisodeForArtifact(t, s)
	q, err := s.GetListeningQueue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	q = queueChange(t, s, q, ListeningQueueChange{Action: "add", SourceType: models.SourceEpisode, SourceID: ep, Mode: "original"})
	id := q.Items[0].ID
	for _, c := range []ListeningQueueChange{{Action: "unknown"}, {Action: "autoplay"}, {Action: "add", SourceType: "document", SourceID: ep, Mode: "original"}, {Action: "add", SourceType: "episode", SourceID: ep, Mode: "invalid"}, {Action: "add", SourceType: "episode", SourceID: ep, Mode: "original", PlanID: "unexpected"}, {Action: "add", SourceType: "episode", SourceID: ep, Mode: "dj"}, {Action: "add", SourceType: "episode", SourceID: "missing", Mode: "original"}, {Action: "play", ItemID: "missing"}, {Action: "remove", ItemID: "missing"}, {Action: "reorder", Order: []string{id, id}}, {Action: "reorder", Order: []string{"unknown"}}, {Action: "reorder", Order: []string{}}, {Action: "remove", ItemID: strings.Repeat("x", 201)}} {
		if _, err := s.ChangeListeningQueue(ctx, q.Revision, c); err == nil {
			t.Fatal("invalid edit accepted", c)
		}
		fresh, err := s.GetListeningQueue(ctx)
		if err != nil || fresh.Revision != q.Revision || len(fresh.Items) != 1 || fresh.Items[0].ID != id {
			t.Fatal("failed edit partially changed queue", fresh, err)
		}
	}
	if _, err := s.ChangeListeningQueue(ctx, -1, ListeningQueueChange{Action: "clear"}); err == nil {
		t.Fatal("negative revision")
	}
	if err := s.ArchiveSource(ctx, models.SourceEpisode, ep, true); err != nil {
		t.Fatal(err)
	}
	up, err := s.CreateUpload(ctx, "old.mp3", "audio/mpeg", 100)
	if err != nil {
		t.Fatal(err)
	}
	uploadQ := queueChange(t, s, q, ListeningQueueChange{Action: "add", SourceType: models.SourceUpload, SourceID: up.ID, Mode: "original"})
	if err := s.DeleteSourceRows(ctx, models.SourceUpload, up.ID); err != nil {
		t.Fatal(err)
	}
	fresh, err := s.GetListeningQueue(ctx)
	if err != nil || fresh.Revision != uploadQ.Revision+1 || fresh.Items[1].Available {
		t.Fatal(fresh, err)
	}
	_ = s.Close()
	if _, err := s.GetListeningQueue(ctx); err == nil {
		t.Fatal("closed queue read accepted")
	}
	if _, err := s.ChangeListeningQueue(ctx, fresh.Revision, ListeningQueueChange{Action: "clear"}); err == nil {
		t.Fatal("closed queue edit accepted")
	}
}
