package queue

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/woyin/orangecast/internal/filehash"
	"github.com/woyin/orangecast/internal/models"
)

func TestEvidenceReuseRecordedPathAndHash(t *testing.T) {
	s, w := newTestWorker(t)
	id := seedEpisode(t, s)
	rel := "restored/original.wav"
	path := filepath.Join(w.evidenceDir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("restored evidence bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	hash, err := filehash.SHA256(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertEvidenceAudio(t.Context(), models.SourceEpisode, id, rel, "wav", 23, hash); err != nil {
		t.Fatal(err)
	}
	job := &models.ProcessingJob{SourceType: models.SourceEpisode, SourceID: id}
	got, ok := w.reusableEvidencePath(t.Context(), job, filepath.Join(w.evidenceDir, "wrong.mp3"))
	expected := path
	if !ok || got != expected {
		t.Fatalf("recorded evidence not reused: %q %v", got, ok)
	}
	if err := os.WriteFile(path, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, ok := w.reusableEvidencePath(t.Context(), job, path); ok {
		t.Fatal("changed bytes reused")
	}
}

func TestEvidenceReuseRejectsEscapingSymlink(t *testing.T) {
	s, w := newTestWorker(t)
	id := seedEpisode(t, s)
	outside := filepath.Join(t.TempDir(), "private.wav")
	if err := os.WriteFile(outside, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(w.evidenceDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(w.evidenceDir, "escape.wav")); err != nil {
		t.Fatal(err)
	}
	hash, err := filehash.SHA256(outside)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertEvidenceAudio(t.Context(), models.SourceEpisode, id, "escape.wav", "wav", 7, hash); err != nil {
		t.Fatal(err)
	}
	job := &models.ProcessingJob{SourceType: models.SourceEpisode, SourceID: id}
	if _, ok := w.reusableEvidencePath(t.Context(), job, outside); ok {
		t.Fatal("escaped evidence reused")
	}
}

func TestEvidenceReuseUnavailableRecordedFiles(t *testing.T) {
	for _, scenario := range []string{"no_record", "not_ready", "unsafe_relative", "missing_root", "missing_file", "directory", "empty"} {
		t.Run(scenario, func(t *testing.T) {
			s, w := newTestWorker(t)
			id := seedEpisode(t, s)
			job := &models.ProcessingJob{SourceType: models.SourceEpisode, SourceID: id}
			rel := "original.wav"
			if scenario == "unsafe_relative" {
				rel = "../private.wav"
			}
			if scenario != "no_record" {
				if err := s.UpsertEvidenceAudio(t.Context(), models.SourceEpisode, id, rel, "wav", 1, "hash"); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "not_ready" {
				if _, err := s.DB.Exec(`UPDATE evidence_audio SET status='missing' WHERE source_id=?`, id); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "missing_file" || scenario == "directory" || scenario == "empty" {
				if err := os.MkdirAll(w.evidenceDir, 0700); err != nil {
					t.Fatal(err)
				}
				p := filepath.Join(w.evidenceDir, rel)
				if scenario == "directory" {
					if err := os.Mkdir(p, 0700); err != nil {
						t.Fatal(err)
					}
				}
				if scenario == "empty" {
					if err := os.WriteFile(p, nil, 0600); err != nil {
						t.Fatal(err)
					}
				}
			}
			if _, ok := w.reusableEvidencePath(t.Context(), job, "ignored.mp3"); ok {
				t.Fatal("unavailable evidence reused")
			}
		})
	}
}
