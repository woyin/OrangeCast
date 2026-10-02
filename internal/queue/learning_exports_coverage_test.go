package queue

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/store"
)

func TestLearningExportAssemblyFailureLeavesNoDownload(t *testing.T) {
	for _, scenario := range []string{"cancelled", "directory-file", "rename-directory", "stopped", "broken-snapshot", "invalid-manifest"} {
		t.Run(scenario, func(t *testing.T) {
			s, w := newTestWorker(t)
			q, e := s.CreateLearningQuestion(t.Context(), store.LearningQuestion{Body: "失效后不得发布正文"})
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
			dir := filepath.Join(t.TempDir(), "exports")
			ctx := t.Context()
			switch scenario {
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "directory-file":
				if e = os.WriteFile(dir, []byte("occupied"), 0600); e != nil {
					t.Fatal(e)
				}
			case "rename-directory":
				if e = os.MkdirAll(filepath.Join(dir, v.ID+".zip"), 0700); e != nil {
					t.Fatal(e)
				}
			case "stopped":
				_, e = s.DB.Exec(`UPDATE processing_jobs SET stop_requested=1 WHERE id=?`, v.JobID)
			case "broken-snapshot":
				_, e = s.DB.Exec(`UPDATE learning_export_previews SET snapshot_json='{' WHERE id=?`, p.ID)
			case "invalid-manifest":
				_, e = s.DB.Exec(`UPDATE learning_export_previews SET snapshot_json='{"scope_kind":"question","scope_id":"bad","objects":[{"kind":"../../private","id":"escape","revision":1}]}' WHERE id=?`, p.ID)
			}
			if e != nil {
				t.Fatal(e)
			}
			ConfigureLearningExports(w, s, dir)
			j, e := s.GetJob(t.Context(), v.JobID)
			if e != nil {
				t.Fatal(e)
			}
			if e = w.localExport(ctx, j); e == nil {
				t.Fatal("invalid assembly accepted")
			}
			failed, e := s.GetLearningExport(t.Context(), v.ID)
			if e != nil || failed.Status != "failed" || failed.Error == "" {
				t.Fatal(failed, e)
			}
			if _, e = s.ValidLearningExportDownload(t.Context(), v.ID); !errors.Is(e, store.ErrNotFound) {
				t.Fatal(e)
			}
			entries, _ := os.ReadDir(dir)
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), ".export-") {
					t.Fatal("partial temporary file survived", entry.Name())
				}
			}
			if scenario == "stopped" {
				if _, e = os.Stat(filepath.Join(dir, v.ID+".zip")); !os.IsNotExist(e) {
					t.Fatal("stopped export published", e)
				}
			}
		})
	}
	ConfigureLearningExports(nil, nil, "")
}

func TestLearningExportCleanupOrphansAndCancellation(t *testing.T) {
	s, _ := newTestWorker(t)
	dir := t.TempDir()
	for _, name := range []string{"orphan.zip", ".export-old.tmp", ".export-current.tmp", "keep.txt"} {
		if e := os.WriteFile(filepath.Join(dir, name), []byte("private"), 0600); e != nil {
			t.Fatal(e)
		}
	}
	old := time.Now().Add(-2 * time.Hour)
	if e := os.Chtimes(filepath.Join(dir, ".export-old.tmp"), old, old); e != nil {
		t.Fatal(e)
	}
	if e := os.Mkdir(filepath.Join(dir, "directory.zip"), 0700); e != nil {
		t.Fatal(e)
	}
	if e := CleanupLearningExportFiles(t.Context(), s, dir); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"orphan.zip", ".export-old.tmp"} {
		if _, e := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(e) {
			t.Fatal(name, e)
		}
	}
	for _, name := range []string{".export-current.tmp", "keep.txt", "directory.zip"} {
		if _, e := os.Stat(filepath.Join(dir, name)); e != nil {
			t.Fatal(name, e)
		}
	}
	if e := CleanupLearningExportFiles(t.Context(), s, filepath.Join(dir, "missing")); e != nil {
		t.Fatal(e)
	}
	if e := CleanupLearningExportFiles(t.Context(), s, filepath.Join(dir, "keep.txt")); e == nil {
		t.Fatal("non-directory cleanup succeeded")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if e := RunLearningExportCleanup(ctx, s, dir); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	live, cancelLive := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- RunLearningExportCleanup(live, s, dir) }()
	cancelLive()
	select {
	case e := <-done:
		if !errors.Is(e, context.Canceled) {
			t.Fatal(e)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cleanup ignored cancellation")
	}
}

func TestLearningExportUnwritableDirectoryRejectsTemporaryFile(t *testing.T) {
	s, w := newTestWorker(t)
	q, e := s.CreateLearningQuestion(t.Context(), store.LearningQuestion{Body: "文件系统不可写时不可发布"})
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
	dir := t.TempDir()
	if e = os.Chmod(dir, 0500); e != nil {
		t.Fatal(e)
	}
	defer os.Chmod(dir, 0700)
	probe, e := os.CreateTemp(dir, "permission-probe-")
	if e == nil {
		probe.Close()
		os.Remove(probe.Name())
		t.Skip("process can bypass filesystem permissions")
	}
	ConfigureLearningExports(w, s, dir)
	j, e := s.GetJob(t.Context(), v.JobID)
	if e != nil {
		t.Fatal(e)
	}
	if e = w.localExport(t.Context(), j); e == nil {
		t.Fatal("unwritable destination accepted")
	}
	got, e := s.GetLearningExport(t.Context(), v.ID)
	if e != nil || got.Status != "failed" {
		t.Fatal(got, e)
	}
}

// The wrapper retains a real temporary inode and file descriptor; each injected
// error verifies the assembler's durable publication contract and cleanup.
type failingLearningExportFile struct {
	*os.File
	failure string
	closed  bool
}

func (f *failingLearningExportFile) Chmod(mode os.FileMode) error {
	if f.failure == "chmod" {
		f.File.Close()
		f.closed = true
		return f.File.Chmod(mode)
	}
	return f.File.Chmod(mode)
}
func (f *failingLearningExportFile) Sync() error {
	if f.failure == "sync" {
		f.File.Close()
		f.closed = true
		return f.File.Sync()
	}
	return f.File.Sync()
}
func (f *failingLearningExportFile) Close() error {
	f.closed = true
	err := f.File.Close()
	if f.failure == "close" && err == nil {
		return errors.New("local close failed")
	}
	return err
}

func TestLearningExportDurabilityErrorsNeverPublish(t *testing.T) {
	for _, failure := range []string{"chmod", "sync", "close"} {
		t.Run(failure, func(t *testing.T) {
			s, _ := newTestWorker(t)
			q, e := s.CreateLearningQuestion(t.Context(), store.LearningQuestion{Body: "文件落盘失败不得发布"})
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
			dir := t.TempDir()
			var real *failingLearningExportFile
			assemble := newLearningExportAssembler(s, dir, func(dir, pattern string) (learningExportFile, error) {
				f, e := os.CreateTemp(dir, pattern)
				if e != nil {
					return nil, e
				}
				real = &failingLearningExportFile{File: f, failure: failure}
				return real, nil
			})
			j, e := s.GetJob(t.Context(), v.JobID)
			if e != nil {
				t.Fatal(e)
			}
			if e = assemble(t.Context(), j); e == nil {
				t.Fatal("durability failure accepted")
			}
			if real == nil || !real.closed {
				t.Fatal("file descriptor not closed")
			}
			entries, e := os.ReadDir(dir)
			if e != nil || len(entries) != 0 {
				t.Fatal("partial file leaked", entries, e)
			}
			got, e := s.GetLearningExport(t.Context(), v.ID)
			if e != nil || got.Status != "failed" || got.Path != "" || got.Error == "" {
				t.Fatal(got, e)
			}
			if _, e = s.ValidLearningExportDownload(t.Context(), v.ID); !errors.Is(e, store.ErrNotFound) {
				t.Fatal(e)
			}
		})
	}
}

func TestLearningExportPeriodicCleanupRetriesAndExpiresNewFiles(t *testing.T) {
	s, _ := newTestWorker(t)
	dir := filepath.Join(t.TempDir(), "exports")
	if e := os.WriteFile(dir, []byte("initially unavailable directory"), 0600); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runLearningExportCleanup(ctx, s, dir, 5*time.Millisecond) }()
	// First prove the runner survives an unavailable directory. It must neither
	// terminate nor turn the error into success while waiting for the next pass.
	select {
	case e := <-done:
		t.Fatal("cleanup terminated on recoverable error", e)
	case <-time.After(25 * time.Millisecond):
	}
	if e := os.Remove(dir); e != nil {
		t.Fatal(e)
	}
	if e := os.Mkdir(dir, 0700); e != nil {
		t.Fatal(e)
	}
	q, e := s.CreateLearningQuestion(t.Context(), store.LearningQuestion{Body: "启动后失效的成果包"})
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
	bundle := filepath.Join(dir, v.ID+".zip")
	if e = os.WriteFile(bundle, []byte("expired private bytes"), 0600); e != nil {
		t.Fatal(e)
	}
	if e = s.PublishLearningExport(t.Context(), v.ID, bundle); e != nil {
		t.Fatal(e)
	}
	if _, e = s.DB.Exec(`UPDATE learning_exports SET expires_at='2000-01-01T00:00:00Z' WHERE id=?`, v.ID); e != nil {
		t.Fatal(e)
	}
	orphan := filepath.Join(dir, "created-after-start.zip")
	if e := os.WriteFile(orphan, []byte("sensitive orphan"), 0600); e != nil {
		t.Fatal(e)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		_, e := os.Stat(orphan)
		if os.IsNotExist(e) {
			break
		}
		if e != nil {
			t.Fatal(e)
		}
		if time.Now().After(deadline) {
			t.Fatal("periodic cleanup did not remove orphan")
		}
		time.Sleep(5 * time.Millisecond)
	}
	for {
		got, e := s.GetLearningExport(t.Context(), v.ID)
		_, statErr := os.Stat(bundle)
		if e == nil && got.Status == "expired" && os.IsNotExist(statErr) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("periodic expiry incomplete", got, e, statErr)
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case e := <-done:
		if !errors.Is(e, context.Canceled) {
			t.Fatal(e)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cleanup goroutine leaked")
	}
}

func TestLearningExportCleanupReportsPermissionDenied(t *testing.T) {
	s, _ := newTestWorker(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "orphan.zip")
	if e := os.WriteFile(path, []byte("private"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.Chmod(dir, 0500); e != nil {
		t.Fatal(e)
	}
	defer os.Chmod(dir, 0700)
	probe, e := os.CreateTemp(dir, "permission-probe-")
	if e == nil {
		probe.Close()
		os.Remove(probe.Name())
		t.Skip("process can bypass filesystem permissions")
	}
	if e := CleanupLearningExportFiles(t.Context(), s, dir); e == nil || !strings.Contains(e.Error(), "清理成果包") {
		t.Fatal("permission error swallowed", e)
	}
	if _, e := os.Stat(path); e != nil {
		t.Fatal("private file unexpectedly lost", e)
	}
}

func TestLearningExportWorkerRequiresAssemblerAndRespectsOwnerStop(t *testing.T) {
	s, w := newTestWorker(t)
	q, e := s.CreateLearningQuestion(t.Context(), store.LearningQuestion{Body: "本地导出执行边界"})
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
	j, e := s.GetJob(t.Context(), v.JobID)
	if e != nil {
		t.Fatal(e)
	}
	if e = w.processJob(t.Context(), j); e == nil || !strings.Contains(e.Error(), "执行器未配置") {
		t.Fatal(e)
	}
	ConfigureLearningExports(w, s, t.TempDir())
	if _, e = s.DB.Exec(`UPDATE processing_jobs SET stop_requested=1 WHERE id=?`, j.ID); e != nil {
		t.Fatal(e)
	}
	if e = w.processJob(t.Context(), j); !errors.Is(e, store.ErrRunControlled) {
		t.Fatal(e)
	}
	got, e := s.GetLearningExport(t.Context(), v.ID)
	if e != nil || got.Status != "queued" || got.Path != "" {
		t.Fatal(got, e)
	}
}
