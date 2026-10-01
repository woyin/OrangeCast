package store

import (
	"errors"
	"github.com/woyin/orangecast/internal/models"
	"testing"
)

func TestListeningProgressBindsAudioAndRejectsOldUnfrozenClients(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	ep := seedEpisodeForArtifact(t, s)
	if err := s.UpsertEvidenceAudio(ctx, models.SourceEpisode, ep, "identity.mp3", "mp3", 100, "first"); err != nil {
		t.Fatal(err)
	}
	p := &models.ListeningProgress{SourceType: models.SourceEpisode, SourceID: ep, Mode: "original", AudioSHA256: "first", ItemOffsetSeconds: 12, Speed: 1, Seq: 1}
	saved, err := s.SaveListeningProgressCAS(ctx, p, 0)
	if err != nil || saved.AudioSHA256 != "first" {
		t.Fatal(saved, err)
	}
	old := *p
	old.AudioSHA256 = ""
	old.Seq = 200
	old.ItemOffsetSeconds = 999
	if _, err := s.SaveListeningProgressCAS(ctx, &old, saved.Revision); !errors.Is(err, ErrConflict) {
		t.Fatal("old CAS client overwrote bound progress", err)
	}
	if err := s.SaveListeningProgress(ctx, &old); !errors.Is(err, ErrConflict) {
		t.Fatal("old seq client overwrote bound progress", err)
	}
	if err := s.UpsertEvidenceAudio(ctx, models.SourceEpisode, ep, "identity-new.mp3", "mp3", 100, "second"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveListeningProgressCAS(ctx, p, saved.Revision); !errors.Is(err, ErrConflict) {
		t.Fatal("old audio assigned to replacement", err)
	}
	p.AudioSHA256 = "second"
	p.ItemOffsetSeconds = 0
	p.Seq++
	saved, err = s.SaveListeningProgressCAS(ctx, p, saved.Revision)
	if err != nil || saved.ItemOffsetSeconds != 0 || saved.AudioSHA256 != "second" {
		t.Fatal(saved, err)
	}
	if err := s.MarkEvidenceMissing(ctx, models.SourceEpisode, ep); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveListeningProgressCAS(ctx, p, saved.Revision); !errors.Is(err, ErrConflict) {
		t.Fatal("missing audio accepted", err)
	}
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM evidence_audio WHERE source_id=?`, ep); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveListeningProgressCAS(ctx, p, saved.Revision); !errors.Is(err, ErrConflict) {
		t.Fatal("purged audio accepted", err)
	}
}
