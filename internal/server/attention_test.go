package server

import (
	"github.com/woyin/orangecast/internal/models"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAttentionQueueRendersTwoWorkspaceLanes(t *testing.T) {
	srv := newTestServer(t)
	session := claimOwnerAndLogin(t, srv, "attention@example.com", "password123")
	profile, err := srv.store.CreateEditorialProfile(t.Context(), models.EditorialProfile{Name: "队列画像"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.store.CreateProposalBatch(t.Context(), models.ProposalBatch{EditorialProfileID: profile.ID, IdempotencyKey: "attention-snapshot", MaterialSnapshotJSON: "[]"}); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/attention?profile="+profile.ID, nil)
	req.AddCookie(session)
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, "学习泳道") || !strings.Contains(body, "创作泳道") || !strings.Contains(body, "待处理自动发现批次") {
		t.Fatalf("attention queue should surface both lanes and open batch: code=%d body=%q", rec.Code, body)
	}
}
