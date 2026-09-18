package store

import (
	"sync"
	"testing"

	"github.com/woyin/orangecast/internal/models"
)

// TestListeningProgress_CAS R11：原子 UPSERT——同时首次保存恰好一行；
// 并发混合 seq 只保留最大 seq 的状态；倒序请求不覆盖新状态。
func TestListeningProgress_CAS(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	var wg sync.WaitGroup
	seqs := []int64{5, 9, 1, 7, 3, 8, 2, 6}
	for _, sq := range seqs {
		sq := sq
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.SaveListeningProgress(ctx, &models.ListeningProgress{
				SourceType: models.SourceEpisode, SourceID: "ep-cas", PlanID: "p1",
				ItemPosition: int(sq), ItemOffsetSeconds: float64(sq), Speed: 1, Seq: sq,
			}); err != nil {
				t.Errorf("并发保存不应失败: %v", err)
			}
		}()
	}
	wg.Wait()
	p, err := s.GetListeningProgress(ctx, models.SourceEpisode, "ep-cas")
	if err != nil {
		t.Fatal(err)
	}
	if p.Seq != 9 || p.ItemPosition != 9 {
		t.Fatalf("应保留最大 seq 的状态: %+v", p)
	}
}
