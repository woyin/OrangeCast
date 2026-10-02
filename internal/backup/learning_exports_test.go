package backup

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/queue"
	"github.com/woyin/orangecast/internal/store"
)

// Real v2 archive restore must exclude temporary bundles and permit a fresh zero-model export.
func TestLearningExportsV2BackupExcludesZIPAndRestoresRebuild(t *testing.T) {
	ctx := t.Context()
	src := t.TempDir()
	evidence := filepath.Join(src, "evidence")
	if e := os.MkdirAll(evidence, 0700); e != nil {
		t.Fatal(e)
	}
	s, e := store.Open(filepath.Join(src, dbFileName))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	q, e := s.CreateLearningQuestion(ctx, store.LearningQuestion{Body: "实际备份后重建成果包", Goal: "通过版与Owner文字可读"})
	if e != nil {
		t.Fatal(e)
	}
	dir := filepath.Join(src, "learning-exports")
	w := queue.NewWorker(s, nil, filepath.Join(src, "tmp"), evidence, filepath.Join(src, "narrations"))
	queue.ConfigureLearningExports(w, s, dir)
	w.WithBundleResolver(func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		t.Fatal("export invoked model")
		return nil, nil
	})
	p, e := s.PreviewLearningExport(ctx, store.LearningExportScope{Kind: "question", ID: q.ID})
	if e != nil {
		t.Fatal(e)
	}
	v, e := s.CreateLearningExport(ctx, p.ID, p.Hash, uuid.NewString(), "owner")
	if e != nil {
		t.Fatal(e)
	}
	if e = w.ProcessOne(ctx); e != nil {
		t.Fatal(e)
	}
	v, e = s.ValidLearningExportDownload(ctx, v.ID)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(v.Path); e != nil {
		t.Fatal(e)
	}
	archive := filepath.Join(t.TempDir(), "actual-v2.tar.gz")
	m, e := Create(ctx, s, evidence, archive)
	if e != nil || m.Version != 2 {
		t.Fatal(m, e)
	}
	f, e := os.Open(archive)
	if e != nil {
		t.Fatal(e)
	}
	gz, e := gzip.NewReader(f)
	if e != nil {
		t.Fatal(e)
	}
	tr := tar.NewReader(gz)
	for {
		h, e := tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			t.Fatal(e)
		}
		if strings.Contains(h.Name, "learning-exports") || strings.HasSuffix(h.Name, ".zip") {
			t.Fatal("temporary bundle in instance backup", h.Name)
		}
	}
	gz.Close()
	f.Close()
	dst := filepath.Join(t.TempDir(), "independent")
	if _, e = Restore(ctx, archive, dst, false); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(filepath.Join(dst, "learning-exports")); !os.IsNotExist(e) {
		t.Fatal(e)
	}
	restored, e := store.Open(filepath.Join(dst, dbFileName))
	if e != nil {
		t.Fatal(e)
	}
	defer restored.Close()
	fresh, e := restored.PreviewLearningExport(ctx, store.LearningExportScope{Kind: "question", ID: q.ID})
	if e != nil {
		t.Fatal(e)
	}
	rebuilt, e := restored.CreateLearningExport(ctx, fresh.ID, fresh.Hash, uuid.NewString(), "owner")
	if e != nil {
		t.Fatal(e)
	}
	rw := queue.NewWorker(restored, nil, filepath.Join(dst, "tmp"), filepath.Join(dst, "evidence"), filepath.Join(dst, "narrations"))
	queue.ConfigureLearningExports(rw, restored, filepath.Join(dst, "learning-exports"))
	rw.WithBundleResolver(func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		t.Fatal("restored export invoked model")
		return nil, nil
	})
	if e = rw.ProcessOne(ctx); e != nil {
		t.Fatal(e)
	}
	rebuilt, e = restored.ValidLearningExportDownload(ctx, rebuilt.ID)
	if e != nil {
		t.Fatal(e)
	}
	zr, e := zip.OpenReader(rebuilt.Path)
	if e != nil {
		t.Fatal(e)
	}
	defer zr.Close()
	found := false
	for _, file := range zr.File {
		if strings.HasPrefix(file.Name, "questions/") {
			r, e := file.Open()
			if e != nil {
				t.Fatal(e)
			}
			b, e := io.ReadAll(r)
			r.Close()
			if e != nil {
				t.Fatal(e)
			}
			found = strings.Contains(string(b), "实际备份后重建成果包")
		}
	}
	if !found {
		t.Fatal("restored readable question missing")
	}
}
