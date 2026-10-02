package queue

import (
	"archive/zip"
	"errors"
	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLearningExportRealZIPWithoutModelAndRestartCleanup(t *testing.T) {
	s, w := newTestWorker(t)
	ep := seedEpisode(t, s)
	q, e := s.CreateLearningQuestion(t.Context(), store.LearningQuestion{Body: "怎样读懂中文节目？", Goal: "保留出处"})
	if e != nil {
		t.Fatal(e)
	}
	q, e = s.ChangeLearningQuestion(t.Context(), q.ID, q.Revision, store.LearningQuestionChange{Action: "link", Link: provider.LearningQuestionLink{Kind: "source", SourceType: "episode", SourceID: ep}})
	if e != nil {
		t.Fatal(e)
	}
	p, e := s.PreviewLearningExport(t.Context(), store.LearningExportScope{Kind: "question", ID: q.ID})
	if e != nil {
		t.Fatal(e)
	}
	v, e := s.CreateLearningExport(t.Context(), p.ID, p.Hash, uuid.NewString(), "owner")
	if e != nil {
		t.Fatal(e)
	}
	w.bundleFor = func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		t.Fatal("export attempted AI")
		return nil, nil
	}
	w.taskConfigFor = func(*models.ProcessingJob) (provider.TaskConfig, error) {
		t.Fatal("export resolved model config")
		return provider.TaskConfig{}, nil
	}
	dir := filepath.Join(t.TempDir(), "learning-exports")
	ConfigureLearningExports(w, s, dir)
	job, e := s.GetJob(t.Context(), v.JobID)
	if e != nil {
		t.Fatal(e)
	}
	if e = w.processJob(t.Context(), job); e != nil {
		t.Fatal(e)
	}
	v, e = s.ValidLearningExportDownload(t.Context(), v.ID)
	if e != nil {
		t.Fatal(e)
	}
	info, e := os.Stat(v.Path)
	if e != nil || info.Mode().Perm() != 0600 {
		t.Fatal(info, e)
	}
	zr, e := zip.OpenReader(v.Path)
	if e != nil {
		t.Fatal(e)
	}
	seen := map[string]string{}
	for _, f := range zr.File {
		r, e := f.Open()
		if e != nil {
			t.Fatal(e)
		}
		b, e := io.ReadAll(r)
		r.Close()
		if e != nil {
			t.Fatal(e)
		}
		seen[f.Name] = string(b)
	}
	zr.Close()
	if len(seen) < 4 || !strings.Contains(seen["questions/"+q.ID+"-r2.md"], "中文") || seen["manifest.json"] == "" {
		t.Fatal(seen)
	}
	if _, e = s.DB.Exec(`DELETE FROM episodes WHERE id=?`, ep); e != nil {
		t.Fatal(e)
	}
	if _, e = s.ValidLearningExportDownload(t.Context(), v.ID); !errors.Is(e, store.ErrConflict) {
		t.Fatal(e)
	}
	if _, e = s.DB.Exec(`UPDATE learning_exports SET expires_at='2000-01-01T00:00:00Z'; UPDATE learning_export_previews SET expires_at='2000-01-01T00:00:00Z'`); e != nil {
		t.Fatal(e)
	}
	if e = CleanupLearningExportFiles(t.Context(), s, dir); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(v.Path); !os.IsNotExist(e) {
		t.Fatal(e)
	}
	// Independent copied DB restores organization, not temporary ZIP; regeneration stays local.
	copyPath := filepath.Join(t.TempDir(), "restored.db")
	if e = store.ConsistencyBackup(t.Context(), s.DB, copyPath); e != nil {
		t.Fatal(e)
	}
	restored, e := store.Open(copyPath)
	if e != nil {
		t.Fatal(e)
	}
	defer restored.Close()
	fresh, e := restored.PreviewLearningExport(t.Context(), store.LearningExportScope{Kind: "question", ID: q.ID})
	if e != nil || fresh.Count != 1 {
		t.Fatal(fresh, e)
	}
	restoredJob, e := restored.CreateLearningExport(t.Context(), fresh.ID, fresh.Hash, uuid.NewString(), "owner")
	if e != nil {
		t.Fatal(e)
	}
	rw := NewWorker(restored, nil, t.TempDir(), t.TempDir(), t.TempDir())
	ConfigureLearningExports(rw, restored, t.TempDir())
	j, e := restored.GetJob(t.Context(), restoredJob.JobID)
	if e != nil {
		t.Fatal(e)
	}
	if e = rw.processJob(t.Context(), j); e != nil {
		t.Fatal(e)
	}
}
