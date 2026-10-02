package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
)

func TestLearningExportMetadataAndExpiredSensitiveSnapshot(t *testing.T) {
	s, _, _, _ := knowledgeStoreFixture(t)
	q := createQuestion(t, s)
	for _, scope := range []LearningExportScope{{Kind: "unknown", ID: q.ID}, {Kind: "question"}} {
		if _, e := s.PreviewLearningExport(t.Context(), scope); !errors.Is(e, ErrInvalidEditorialState) {
			t.Fatal(e)
		}
	}
	p, e := s.PreviewLearningExport(t.Context(), LearningExportScope{Kind: "question", ID: q.ID})
	if e != nil {
		t.Fatal(e)
	}
	for _, args := range [][2]string{{"not-uuid", "owner"}, {uuid.NewString(), ""}} {
		if _, e = s.CreateLearningExport(t.Context(), p.ID, p.Hash, args[0], args[1]); !errors.Is(e, ErrInvalidEditorialState) {
			t.Fatal(e)
		}
	}
	if _, e = s.CreateLearningExport(t.Context(), "absent", p.Hash, uuid.NewString(), "owner"); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
	if _, e = s.CreateLearningExport(t.Context(), p.ID, "different", uuid.NewString(), "owner"); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	v, e := s.CreateLearningExport(t.Context(), p.ID, p.Hash, uuid.NewString(), "owner")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.ValidLearningExportDownload(t.Context(), v.ID); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	if _, e = s.GetLearningExport(t.Context(), "absent"); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
	if _, e = s.LearningExportSnapshot(t.Context(), "absent"); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
	if _, e = s.ValidLearningExportDownload(t.Context(), "absent"); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
	path := filepath.Join(t.TempDir(), "bundle.zip")
	if e = os.WriteFile(path, []byte("bundle"), 0600); e != nil {
		t.Fatal(e)
	}
	if e = s.PublishLearningExport(t.Context(), v.ID, path); e != nil {
		t.Fatal(e)
	}
	list, e := s.ListLearningExports(t.Context())
	if e != nil || len(list) != 1 || list[0].ID != v.ID || list[0].Status != "ready" {
		t.Fatal(list, e)
	}
	for _, mode := range []os.FileMode{0644, 0600} {
		if e = os.Chmod(path, mode); e != nil {
			t.Fatal(e)
		}
		got, e := s.GetLearningExport(t.Context(), v.ID)
		if e != nil {
			t.Fatal(e)
		}
		if mode == 0644 && (got.Status != "failed" || got.Error == "") {
			t.Fatal(got)
		}
		if mode == 0600 && got.Status != "ready" {
			t.Fatal(got)
		}
	}
	if _, e = s.DB.Exec(`UPDATE learning_exports SET expires_at='2000-01-01T00:00:00Z';UPDATE learning_export_previews SET expires_at='2000-01-01T00:00:00Z'`); e != nil {
		t.Fatal(e)
	}
	got, e := s.GetLearningExport(t.Context(), v.ID)
	if e != nil || got.Status != "expired" {
		t.Fatal(got, e)
	}
	if e = s.CleanupLearningExports(t.Context()); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(path); !os.IsNotExist(e) {
		t.Fatal(e)
	}
	var raw, storedPath, status string
	if e = s.DB.QueryRow(`SELECT snapshot_json,path,status FROM learning_export_previews JOIN learning_exports ON preview_id=learning_export_previews.id WHERE learning_exports.id=?`, v.ID).Scan(&raw, &storedPath, &status); e != nil {
		t.Fatal(e)
	}
	if raw != "{}" || storedPath != "" || status != "expired" {
		t.Fatal(raw, storedPath, status)
	}
	if e = s.CleanupLearningExports(t.Context()); e != nil {
		t.Fatal("cleanup must be idempotent", e)
	}
}

func TestLearningExportCancellationCorruptPreviewAndPrivateFile(t *testing.T) {
	for _, scenario := range []string{"cancelled", "corrupt-scope", "corrupt-body", "missing-file", "symlink", "cleanup-file-error"} {
		t.Run(scenario, func(t *testing.T) {
			s, _, _, _ := knowledgeStoreFixture(t)
			q := createQuestion(t, s)
			p, e := s.PreviewLearningExport(t.Context(), LearningExportScope{Kind: "question", ID: q.ID})
			if e != nil {
				t.Fatal(e)
			}
			v, e := s.CreateLearningExport(t.Context(), p.ID, p.Hash, uuid.NewString(), "owner")
			if e != nil {
				t.Fatal(e)
			}
			path := filepath.Join(t.TempDir(), "bundle.zip")
			switch scenario {
			case "cancelled":
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				if _, e = s.PreviewLearningExport(ctx, LearningExportScope{Kind: "question", ID: q.ID}); !errors.Is(e, context.Canceled) {
					t.Fatal(e)
				}
				if _, e = s.CreateLearningExport(ctx, p.ID, p.Hash, uuid.NewString(), "owner"); !errors.Is(e, context.Canceled) {
					t.Fatal(e)
				}
				if e = s.PublishLearningExport(ctx, v.ID, path); !errors.Is(e, context.Canceled) {
					t.Fatal(e)
				}
				if _, e = s.ListLearningExports(ctx); !errors.Is(e, context.Canceled) {
					t.Fatal(e)
				}
				if e = s.CleanupLearningExports(ctx); !errors.Is(e, context.Canceled) {
					t.Fatal(e)
				}
			case "corrupt-scope":
				if _, e = s.DB.Exec(`UPDATE learning_export_previews SET scope_json='{' WHERE id=?`, p.ID); e != nil {
					t.Fatal(e)
				}
				if _, e = s.GetLearningExport(t.Context(), v.ID); e == nil {
					t.Fatal("invalid scope accepted")
				}
				if _, e = s.ListLearningExports(t.Context()); e == nil {
					t.Fatal("invalid preview listed")
				}
			case "corrupt-body":
				if _, e = s.DB.Exec(`UPDATE learning_export_previews SET snapshot_json='{' WHERE id=?`, p.ID); e != nil {
					t.Fatal(e)
				}
				if _, e = s.LearningExportSnapshot(t.Context(), v.ID); e == nil {
					t.Fatal("invalid snapshot accepted")
				}
			case "missing-file", "symlink":
				if scenario == "symlink" {
					target := filepath.Join(t.TempDir(), "target")
					if e = os.WriteFile(target, []byte("private"), 0600); e != nil {
						t.Fatal(e)
					}
					if e = os.Symlink(target, path); e != nil {
						t.Fatal(e)
					}
				}
				if e = s.PublishLearningExport(t.Context(), v.ID, path); e != nil {
					t.Fatal(e)
				}
				if _, e = s.ValidLearningExportDownload(t.Context(), v.ID); !errors.Is(e, ErrNotFound) {
					t.Fatal(e)
				}
			case "cleanup-file-error":
				if e = os.Mkdir(path, 0700); e != nil {
					t.Fatal(e)
				}
				if e = os.WriteFile(filepath.Join(path, "private"), []byte("data"), 0600); e != nil {
					t.Fatal(e)
				}
				if e = s.PublishLearningExport(t.Context(), v.ID, path); e != nil {
					t.Fatal(e)
				}
				if _, e = s.DB.Exec(`UPDATE learning_exports SET expires_at='2000-01-01T00:00:00Z' WHERE id=?`, v.ID); e != nil {
					t.Fatal(e)
				}
				if e = s.CleanupLearningExports(t.Context()); e == nil {
					t.Fatal("file removal failure swallowed")
				}
				if e = os.RemoveAll(path); e != nil {
					t.Fatal(e)
				}
				if e = s.CleanupLearningExports(t.Context()); e != nil {
					t.Fatal(e)
				}
			}
		})
	}
}

func TestLearningExportExistingConfirmationRejectsStaleAndFailure(t *testing.T) {
	s, _, _, _ := knowledgeStoreFixture(t)
	q := createQuestion(t, s)
	p, e := s.PreviewLearningExport(t.Context(), LearningExportScope{Kind: "question", ID: q.ID})
	if e != nil {
		t.Fatal(e)
	}
	got, e := s.GetLearningExportPreview(t.Context(), p.ID)
	if e != nil || got.Hash != p.Hash || got.Count != p.Count {
		t.Fatal(got, e)
	}
	if _, e = s.GetLearningExportPreview(t.Context(), "absent"); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
	v, e := s.CreateLearningExport(t.Context(), p.ID, p.Hash, uuid.NewString(), "owner")
	if e != nil {
		t.Fatal(e)
	}
	if e = s.FailLearningExport(t.Context(), v.ID, errors.New("local disk full")); e != nil {
		t.Fatal(e)
	}
	failed, e := s.GetLearningExport(t.Context(), v.ID)
	if e != nil || failed.Error != "local disk full" || failed.Status != "failed" {
		t.Fatal(failed, e)
	}
	if _, e = s.ValidLearningExportDownload(t.Context(), v.ID); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
	if _, e = s.DB.Exec(`UPDATE learning_export_previews SET expires_at='2000-01-01T00:00:00Z' WHERE id=?`, p.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = s.GetLearningExportPreview(t.Context(), p.ID); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
}
