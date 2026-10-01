package queue

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

func TestListeningReflectionMaintenanceExpiresWithoutModel(t *testing.T) {
	s, w := newTestWorker(t)
	id := uuid.NewString()
	if _, err := s.DB.Exec(`INSERT INTO listening_reflections(id,source_type,source_id,capture_json,answers_json,start_hash,expires_at)VALUES(?,'episode','expired','{}','{"remember":"private"}','hash','2000-01-01 00:00:00')`, id); err != nil {
		t.Fatal(err)
	}
	w.bundleFor = func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		t.Error("draft maintenance contacted Provider")
		return nil, nil
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); w.Run(ctx) }()
	deadline := time.After(3 * time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-deadline:
			t.Fatal("draft maintenance not completed")
		case <-ticker.C:
			var state, answers string
			if err := s.DB.QueryRow(`SELECT state,answers_json FROM listening_reflections WHERE id=?`, id).Scan(&state, &answers); err != nil {
				t.Fatal(err)
			}
			if state != "expired" {
				continue
			}
			if answers != "{}" {
				t.Fatal(answers)
			}
			cancel()
			select {
			case <-done:
				return
			case <-time.After(time.Second):
				t.Fatal("worker did not stop")
			}
		}
	}
}
