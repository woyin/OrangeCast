package store

import (
	"fmt"
	"path/filepath"
	"runtime"
	"sort"
	"testing"
	"time"
)

// BenchmarkKnowledgeWideRecall measures the local projection query at 100%
// lexical hit rate. These are synthetic index rows, not graded real notes.
func BenchmarkKnowledgeWideRecall(b *testing.B) {
	for _, size := range []int{10000, 50000} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			s, err := Open(filepath.Join(b.TempDir(), "projection.db"))
			if err != nil {
				b.Fatal(err)
			}
			b.Cleanup(func() { s.Close() })
			doc, err := s.CreatePastedDocument(b.Context(), "固定来源", "记忆练习")
			if err != nil {
				b.Fatal(err)
			}
			tx, err := s.DB.BeginTx(b.Context(), nil)
			if err != nil {
				b.Fatal(err)
			}
			defer tx.Rollback()
			stmt, err := tx.PrepareContext(b.Context(), `INSERT INTO knowledge_search_docs(key,kind,object_id,source_type,source_id,title,body,created_at,updated_at)VALUES(?,'owner_reflection',?,'document',?,'固定检索来源',?,'2026-10-01','2026-10-01')`)
			if err != nil {
				b.Fatal(err)
			}
			for i := 0; i < size; i++ {
				id := fmt.Sprintf("benchmark:%d", i)
				if _, err := stmt.ExecContext(b.Context(), id, id, doc.ID, fmt.Sprintf("记忆练习的条件，索引记录%d。", i)); err != nil {
					b.Fatal(err)
				}
			}
			if err := stmt.Close(); err != nil {
				b.Fatal(err)
			}
			if err := tx.Commit(); err != nil {
				b.Fatal(err)
			}
			q := KnowledgeSearchQuery{Text: "记忆练习", Kind: "materials", Recall: true, MetadataOnly: true, PerPage: 200, SendProvider: "pod"}
			var times []time.Duration
			var peakHeap uint64
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				start := time.Now()
				result, err := s.SearchKnowledge(b.Context(), q)
				times = append(times, time.Since(start))
				var memory runtime.MemStats
				runtime.ReadMemStats(&memory)
				if memory.HeapInuse > peakHeap {
					peakHeap = memory.HeapInuse
				}
				if err != nil || result.Total != size || len(result.Hits) != 200 {
					b.Fatal(result.Total, len(result.Hits), err)
				}
			}
			b.StopTimer()
			sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
			idx := (len(times)*95+99)/100 - 1
			b.ReportMetric(float64(times[idx].Microseconds())/1000, "p95-ms")
			b.ReportMetric(float64(peakHeap)/(1024*1024), "sampled-heap-MiB")
		})
	}
}
