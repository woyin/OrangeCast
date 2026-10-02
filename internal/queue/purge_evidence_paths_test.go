package queue

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/woyin/orangecast/internal/models"
)

func TestPurgeRecordedAudioPathAndInterruptedResume(t *testing.T) {
	s, w := newTestWorker(t)
	id := seedEpisode(t, s)
	path := filepath.Join(w.evidenceDir, "frozen", "original.wav")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("private original"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertEvidenceAudio(t.Context(), models.SourceEpisode, id, "frozen/original.wav", "wav", 16, "hash"); err != nil {
		t.Fatal(err)
	}
	// Interrupt the DB phase after the physical deletion. The durable audio row
	// remains available for restart, and a missing file must be treated as done.
	if _, err := s.DB.Exec(`CREATE TRIGGER interrupt_purge BEFORE DELETE ON episodes BEGIN SELECT RAISE(ABORT,'interrupted deletion'); END`); err != nil {
		t.Fatal(err)
	}
	if err := w.PurgeSource(t.Context(), models.SourceEpisode, id); err == nil {
		t.Fatal("expected interruption")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("recorded WAV must be deleted before DB phase", err)
	}
	if _, err := s.GetEvidenceAudio(t.Context(), models.SourceEpisode, id); err != nil {
		t.Fatal("restart must still have the recorded path", err)
	}
	if _, err := s.DB.Exec(`DROP TRIGGER interrupt_purge`); err != nil {
		t.Fatal(err)
	}
	if err := w.ResumePurges(t.Context()); err != nil {
		t.Fatal(err)
	}
	pending, err := s.ListPendingPurges(t.Context())
	if err != nil || len(pending) != 0 {
		t.Fatal(pending, err)
	}
}

func TestPurgeRecordedAudioCannotEscapeEvidenceRoot(t *testing.T) {
	for _, relative := range []string{"../private.wav", "outside/private.wav"} {
		t.Run(relative, func(t *testing.T) {
			s, w := newTestWorker(t)
			id := seedEpisode(t, s)
			outside := t.TempDir()
			path := filepath.Join(outside, "private.wav")
			if err := os.WriteFile(path, []byte("keep"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(w.evidenceDir, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, filepath.Join(w.evidenceDir, "outside")); err != nil {
				t.Fatal(err)
			}
			if err := s.UpsertEvidenceAudio(t.Context(), models.SourceEpisode, id, relative, "wav", 4, "hash"); err != nil {
				t.Fatal(err)
			}
			if err := w.PurgeSource(t.Context(), models.SourceEpisode, id); err == nil {
				t.Fatal("unsafe path must stop purge")
			}
			body, err := os.ReadFile(path)
			if err != nil || string(body) != "keep" {
				t.Fatal("file outside evidence root changed", err)
			}
			pending, err := s.ListPendingPurges(t.Context())
			if err != nil || len(pending) != 1 {
				t.Fatal("failed purge must remain resumable", pending, err)
			}
		})
	}
}
