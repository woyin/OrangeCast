package server

import (
	"context"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/woyin/orangecast/internal/models"
)

func TestSnipWindowBehavior(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal("Node required for snip window behavior")
	}
	if out, err := exec.Command(node, filepath.Join("testdata", "snip-window.cjs")).CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
}

func TestSnipsPage(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "snips@example.com", "password123")
	if body := doWithCookie(srv, cookie, http.MethodGet, "/snips").Body.String(); !strings.Contains(body, "还没有保存的片段") {
		t.Errorf("空状态: %s", body)
	}
	id := seedExportableEpisode(t, srv)
	ctx := context.Background()
	if _, err := srv.store.DB.ExecContext(ctx, `UPDATE episodes SET title='好莱坞沟通课' WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.UpsertEvidenceAudio(ctx, models.SourceEpisode, id, "a.mp3", "mp3", 100, "sha-snip"); err != nil {
		t.Fatal(err)
	}
	snap, err := srv.store.FreezeSourceSnapshot(ctx, models.SourceEpisode, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.store.CreateLearningExcerpt(ctx, snap.ID, []string{"seg-0001"}); err != nil {
		t.Fatal(err)
	}
	rec := doWithCookie(srv, cookie, http.MethodGet, "/snips")
	body := rec.Body.String()
	for _, want := range []string{"通胀是物价上升", "好莱坞沟通课", "0:07 – 0:12", "/sources/episode/" + id + "?t=7.0"} {
		if !strings.Contains(body, want) {
			t.Errorf("片段页应含 %q", want)
		}
	}
	// 来源被删除：仍列出，标题回落。
	srv.store.DB.ExecContext(ctx, `PRAGMA foreign_keys=OFF`)
	srv.store.DB.ExecContext(ctx, `DELETE FROM episodes WHERE id=?`, id)
	if body := doWithCookie(srv, cookie, http.MethodGet, "/snips").Body.String(); !strings.Contains(body, "来源已删除") {
		t.Errorf("来源缺失时应回落标题")
	}
	if rec := doWithCookie(srv, cookie, http.MethodPost, "/snips"); rec.Code != http.StatusMethodNotAllowed && rec.Code != http.StatusForbidden {
		t.Errorf("POST 应被拒绝，实际 %d", rec.Code)
	}
}

func TestClock(t *testing.T) {
	for in, want := range map[float64]string{0: "0:00", 65.9: "1:05", 3725: "1:02:05"} {
		if got := clock(in); got != want {
			t.Errorf("clock(%v)=%q want %q", in, got, want)
		}
	}
}
