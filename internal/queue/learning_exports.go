package queue

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/woyin/orangecast/internal/learningbundle"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/store"
)

// ConfigureLearningExports installs the actual local exporter, independent of AI keys/budgets.
func ConfigureLearningExports(w *Worker, s *store.Store, dir string) {
	if w == nil {
		return
	}
	w.WithLearningExporter(newLearningExportAssembler(s, dir, func(dir, pattern string) (learningExportFile, error) {
		return os.CreateTemp(dir, pattern)
	}))
}

// learningExportFile isolates durable local file IO while the assembler owns all
// cleanup and publication decisions. The production implementation is os.File.
type learningExportFile interface {
	Write([]byte) (int, error)
	Name() string
	Chmod(os.FileMode) error
	Sync() error
	Close() error
}

func newLearningExportAssembler(s *store.Store, dir string, createTemp func(string, string) (learningExportFile, error)) func(context.Context, *models.ProcessingJob) error {
	return func(ctx context.Context, job *models.ProcessingJob) (err error) {
		defer func() {
			if err != nil {
				_ = s.FailLearningExport(context.WithoutCancel(ctx), job.SourceID, err)
			}
		}()
		if err = s.CleanupLearningExports(ctx); err != nil {
			return err
		}
		snap, err := s.LearningExportSnapshot(ctx, job.SourceID)
		if err != nil {
			return err
		}
		if err = os.MkdirAll(dir, 0700); err != nil {
			return err
		}
		f, err := createTemp(dir, ".export-*.tmp")
		if err != nil {
			return err
		}
		tmp := f.Name()
		defer os.Remove(tmp)
		if err = f.Chmod(0600); err != nil {
			f.Close()
			return err
		}
		_, err = learningbundle.WriteZIP(f, snap)
		if err == nil {
			err = f.Sync()
		}
		closeErr := f.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		final := filepath.Join(dir, job.SourceID+".zip")
		if err = os.Rename(tmp, final); err != nil {
			return err
		}
		if err = s.PublishLearningExport(ctx, job.SourceID, final); err != nil {
			os.Remove(final)
			return err
		}
		return nil
	}
}

// CleanupLearningExportFiles handles restart/orphan cleanup as well as expiry.
func CleanupLearningExportFiles(ctx context.Context, s *store.Store, dir string) error {
	if err := s.CleanupLearningExports(ctx); err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		info, e := entry.Info()
		if e != nil {
			return e
		}
		remove := strings.HasPrefix(name, ".export-") && time.Since(info.ModTime()) > time.Hour
		if strings.HasSuffix(name, ".zip") {
			id := strings.TrimSuffix(name, ".zip")
			v, e := s.GetLearningExport(ctx, id)
			remove = e != nil || v.Status != "ready" || v.ExpiresAt <= time.Now().UTC().Format(time.RFC3339)
		}
		if remove {
			if e = os.Remove(filepath.Join(dir, name)); e != nil && !os.IsNotExist(e) {
				return fmt.Errorf("清理成果包: %w", e)
			}
		}
	}
	return nil
}

// RunLearningExportCleanup performs restart cleanup and bounded periodic expiry maintenance.
func RunLearningExportCleanup(ctx context.Context, s *store.Store, dir string) error {
	return runLearningExportCleanup(ctx, s, dir, time.Hour)
}

func runLearningExportCleanup(ctx context.Context, s *store.Store, dir string, interval time.Duration) error {
	if err := CleanupLearningExportFiles(ctx, s, dir); err != nil {
		log.Printf("学习成果临时文件清理失败，将在下轮重试: %v", err)
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := CleanupLearningExportFiles(ctx, s, dir); err != nil {
				log.Printf("学习成果临时文件清理失败，将在下轮重试: %v", err)
			}
		}
	}
}
