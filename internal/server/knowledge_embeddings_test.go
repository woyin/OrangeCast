package server

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/provider"
)

func TestKnowledgeEmbeddingJobAutomationProjectionIsReadonly(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "embedding-projection@example.com", "password123")
	client, err := provider.NewEmbeddingClient("secret-sentinel", "https://private-endpoint.example/v1", "vector", 0)
	if err != nil {
		t.Fatal(err)
	}
	job, _, err := srv.store.ReserveKnowledgeEmbeddingPreflight(t.Context(), uuid.NewString(), client.Config())
	if err != nil {
		t.Fatal(err)
	}
	var before, after int
	if err = srv.store.DB.QueryRow(`SELECT total_changes()`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	page := doWithCookie(srv, cookie, "GET", "/automation/"+job.ID)
	if page.Code != 200 || !strings.Contains(page.Body.String(), "embedding-utf8-byte-bound-v1") || !strings.Contains(page.Body.String(), "冻结价格未知") || strings.Contains(page.Body.String(), "secret-sentinel") || strings.Contains(page.Body.String(), "private-endpoint") {
		t.Fatal(page.Code, page.Body.String())
	}
	if err = srv.store.DB.QueryRow(`SELECT total_changes()`).Scan(&after); err != nil || before != after {
		t.Fatal("GET changed index", before, after, err)
	}
}
