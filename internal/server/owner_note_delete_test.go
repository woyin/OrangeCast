package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/store"
)

// TestOwnerNoteDelete_R23 补齐日志挂账的删除入口：
//   - 删除成功（乐观并发版本匹配）；
//   - 过期版本 → 409；被精读块引用 → 409 且笔记保留；
//   - 不存在的笔记 → 404。
func TestOwnerNoteDelete_R23(t *testing.T) {
	srv := newTestServer(t)
	ctx := t.Context()
	pod, err := srv.store.CreatePodcast(ctx, "https://note-del.example/feed", "ND", "", "")
	check(t, err)
	if _, err := srv.store.MergeEpisodes(ctx, pod.ID, []models.Episode{{GUID: "nd", Title: "ND", AudioURL: "https://nd.mp3"}}); err != nil {
		t.Fatal(err)
	}
	eps, err := srv.store.ListEpisodes(ctx, pod.ID)
	check(t, err)
	epID := eps[0].ID
	tJob, err := srv.store.EnqueueJob(ctx, models.SourceEpisode, epID, models.JobTranscribe)
	check(t, err)
	tv, err := srv.store.CreateArtifactVersion(ctx, models.SourceEpisode, epID, store.KindTranscript, "t", "t", "1", tJob.ID,
		`{"language":"zh","text":"ND","segments":[{"id":"seg-nd","start":0,"end":1,"text":"ND"}]}`)
	check(t, err)
	if _, err := srv.store.MarkJobRunning(ctx, tJob.ID); err != nil {
		t.Fatal(err)
	}
	check(t, srv.store.MarkJobSucceeded(ctx, tJob.ID))
	check(t, srv.store.SetCurrentVersion(ctx, models.SourceEpisode, epID, store.KindTranscript, tv))

	noteA, err := srv.store.CreateOwnerNote(ctx, models.OwnerNote{
		SourceType: string(models.SourceEpisode), SourceID: epID,
		Kind: "owner_reflection", Content: "待删除的理解 A", CitationsJSON: "[]", ReferencesJSON: "[]",
	})
	check(t, err)
	noteB, err := srv.store.CreateOwnerNote(ctx, models.OwnerNote{
		SourceType: string(models.SourceEpisode), SourceID: epID,
		Kind: "owner_reflection", Content: "被精读引用的理解 B", CitationsJSON: "[]", ReferencesJSON: `["seg-nd"]`,
	})
	check(t, err)
	// noteB 被精读块引用。
	_, err = srv.store.CreateEpisodeDigest(ctx, &models.EpisodeDigest{
		SourceType: models.SourceEpisode, SourceID: epID, Title: "引用笔记的精读",
	}, []models.DigestBlock{{Position: 0, Type: "note", Text: "块", NoteID: noteB.ID}})
	check(t, err)

	session := claimOwnerAndLogin(t, srv, "r23del@example.com", "password123")
	getRec := doWithCookie(srv, session, http.MethodGet, "/workbench")
	csrf := ""
	for _, cookie := range getRec.Result().Cookies() {
		if cookie.Name == "cwp_csrf" {
			csrf = cookie.Value
		}
	}

	del := func(noteID string, revision int) *httptest.ResponseRecorder {
		return postForm(t, srv, session, "/api/owner-notes",
			fmt.Sprintf("_csrf=%s&action=delete&note_id=%s&expected_revision=%d&source_type=episode&source_id=%s", csrf, noteID, revision, epID))
	}

	// 过期版本 → 409。
	if rec := del(noteA.ID, noteA.Revision+5); rec.Code != http.StatusConflict {
		t.Fatalf("过期删除应 409: %d", rec.Code)
	}
	// 被精读引用 → 409 且笔记保留。
	if rec := del(noteB.ID, noteB.Revision); rec.Code != http.StatusConflict {
		t.Fatalf("被引用笔记删除应 409: %d", rec.Code)
	}
	if _, err := srv.store.GetOwnerNote(ctx, noteB.ID); err != nil {
		t.Fatalf("被引用笔记必须保留: %v", err)
	}
	// 正常删除。
	if rec := del(noteA.ID, noteA.Revision); rec.Code != http.StatusSeeOther {
		t.Fatalf("正常删除应 303: %d %s", rec.Code, rec.Body.String())
	}
	if _, err := srv.store.GetOwnerNote(ctx, noteA.ID); err != store.ErrNotFound {
		t.Fatalf("删除后应不存在: %v", err)
	}
	// 不存在的笔记 → 404。
	if rec := del(noteA.ID, 1); rec.Code != http.StatusNotFound {
		t.Fatalf("不存在笔记应 404: %d", rec.Code)
	}
}
