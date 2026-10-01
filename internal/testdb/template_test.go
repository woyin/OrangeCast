package testdb

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

type testHandle struct{ closeErr error }

func (h *testHandle) Close() error { return h.closeErr }

func migrationDirectory(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "0001_schema.sql"), []byte("CREATE TABLE example(id);"), 0600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestTemplateCopiesIndependentFilesAndRebuildsOnMigrationChange(t *testing.T) {
	migrations := migrationDirectory(t)
	var initialized atomic.Int32
	opener := func(path string) (*testHandle, error) {
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			initialized.Add(1)
			if err = os.WriteFile(path, []byte("SQLite format 3\x00fixture"), 0600); err != nil {
				return nil, err
			}
		}
		return &testHandle{}, nil
	}
	dir := t.TempDir()
	paths := make([]string, 8)
	var wg sync.WaitGroup
	for i := range paths {
		paths[i] = filepath.Join(dir, string(rune('a'+i))+".db")
		wg.Add(1)
		go func(path string) {
			defer wg.Done()
			h, err := Open(path, migrations, opener)
			if err != nil {
				t.Error(err)
				return
			}
			h.Close()
		}(paths[i])
	}
	wg.Wait()
	if initialized.Load() != 1 {
		t.Fatal("template initialized repeatedly", initialized.Load())
	}
	if err := os.WriteFile(paths[0], []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	unchanged, err := os.ReadFile(paths[1])
	if err != nil || string(unchanged) != "SQLite format 3\x00fixture" {
		t.Fatal("shared mutable file", err)
	}
	if err = os.WriteFile(filepath.Join(migrations, "0002_added.sql"), []byte("ALTER TABLE example ADD COLUMN name;"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = Open(filepath.Join(dir, "updated.db"), migrations, opener); err != nil || initialized.Load() != 2 {
		t.Fatal("migration fingerprint ignored", err)
	}
	if _, err = Open(paths[1], migrations, opener); err == nil {
		t.Fatal("existing target overwritten")
	}
}

func TestTemplateFailuresDoNotCachePartialDatabases(t *testing.T) {
	t.Run("missing migrations", func(t *testing.T) {
		if _, err := fingerprint(filepath.Join(t.TempDir(), "missing")); err == nil {
			t.Fatal("missing migrations")
		}
	})
	t.Run("empty migrations", func(t *testing.T) {
		if _, err := fingerprint(t.TempDir()); err == nil {
			t.Fatal("empty migrations")
		}
	})
	for _, mode := range []string{"open-error", "close-error", "missing-file", "invalid-header", "live-wal"} {
		t.Run(mode, func(t *testing.T) {
			migrations := migrationDirectory(t)
			sentinel := errors.New("initialization failed")
			opener := func(path string) (*testHandle, error) {
				if mode == "open-error" {
					return nil, sentinel
				}
				if mode == "missing-file" {
					return &testHandle{}, nil
				}
				bytes := []byte("SQLite format 3\x00fixture")
				if mode == "invalid-header" {
					bytes = []byte("partial")
				}
				if err := os.WriteFile(path, bytes, 0600); err != nil {
					return nil, err
				}
				if mode == "live-wal" {
					if err := os.WriteFile(path+"-wal", []byte("uncommitted"), 0600); err != nil {
						return nil, err
					}
				}
				if mode == "close-error" {
					return &testHandle{closeErr: sentinel}, nil
				}
				return &testHandle{}, nil
			}
			target := filepath.Join(t.TempDir(), "target.db")
			if _, err := Open(target, migrations, opener); err == nil {
				t.Fatal("partial template accepted")
			}
			good := func(path string) (*testHandle, error) {
				if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
					if err = os.WriteFile(path, []byte("SQLite format 3\x00recovered"), 0600); err != nil {
						return nil, err
					}
				}
				return &testHandle{}, nil
			}
			if _, err := Open(target, migrations, good); err != nil {
				t.Fatal("failed template cached", err)
			}
		})
	}
	t.Run("opener runs for clone", func(t *testing.T) {
		migrations := migrationDirectory(t)
		opener := func(path string) (*testHandle, error) {
			if strings.Contains(path, "template.db") {
				if err := os.WriteFile(path, []byte("SQLite format 3\x00fixture"), 0600); err != nil {
					return nil, err
				}
				return &testHandle{}, nil
			}
			return nil, errors.New("connection setup failed")
		}
		if _, err := Open(filepath.Join(t.TempDir(), "target.db"), migrations, opener); err == nil {
			t.Fatal("normal opener not called")
		}
	})
}

type failingCloneWriter struct{ writeErr, closeErr error }

func (w failingCloneWriter) Write(p []byte) (int, error) {
	if w.writeErr != nil {
		return 0, w.writeErr
	}
	return len(p), nil
}
func (w failingCloneWriter) Close() error { return w.closeErr }

func TestTemplateDiskFailuresAndInvalidMigrationFile(t *testing.T) {
	t.Run("migration read failure", func(t *testing.T) {
		dir := migrationDirectory(t)
		if err := os.Symlink(filepath.Join(dir, "missing"), filepath.Join(dir, "0002_broken.sql")); err != nil {
			t.Fatal(err)
		}
		if _, err := Open(filepath.Join(t.TempDir(), "target.db"), dir, func(string) (*testHandle, error) { t.Fatal("invalid migrations initialized"); return nil, nil }); err == nil {
			t.Fatal("unreadable migration accepted")
		}
	})
	t.Run("non SQL files ignored", func(t *testing.T) {
		dir := migrationDirectory(t)
		if err := os.Mkdir(filepath.Join(dir, "notes.sql"), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("unrelated"), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := fingerprint(dir); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("missing directory", func(t *testing.T) {
		if _, err := Open(filepath.Join(t.TempDir(), "target.db"), "/not-a-migration-directory", func(string) (*testHandle, error) { return nil, nil }); err == nil {
			t.Fatal("missing directory accepted")
		}
	})
	t.Run("template temp directory failure", func(t *testing.T) {
		missing := filepath.Join(t.TempDir(), "missing")
		t.Setenv("TMPDIR", missing)
		if _, err := buildTemplate(func(string) (*testHandle, error) { return nil, nil }); err == nil {
			t.Fatal("temp directory failure ignored")
		}
	})
	for _, mode := range []string{"write", "close"} {
		t.Run(mode, func(t *testing.T) {
			dir := migrationDirectory(t)
			path := filepath.Join(t.TempDir(), "target.db")
			old := createClone
			defer func() { createClone = old }()
			createClone = func(string) (io.WriteCloser, error) {
				w := failingCloneWriter{}
				if mode == "write" {
					w.writeErr = errors.New("disk full")
				} else {
					w.closeErr = errors.New("close failed")
				}
				return w, nil
			}
			opener := func(path string) (*testHandle, error) {
				if err := os.WriteFile(path, []byte("SQLite format 3\x00fixture"), 0600); err != nil {
					return nil, err
				}
				return &testHandle{}, nil
			}
			if _, err := Open(path, dir, opener); err == nil {
				t.Fatal("disk failure ignored")
			}
		})
	}
}
