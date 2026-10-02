package store

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"runtime"
	"sort"
	"testing"
	"time"

	"github.com/woyin/orangecast/internal/provider"
)

// This measures actual SQL loading, authorization, ranking and projection at the
// maximum supported dimension. Synthetic vectors do not establish recall quality.
func BenchmarkKnowledgeHybridMaximumDimensions(b *testing.B) {
	for _, size := range []int{10000, 50000} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			s, err := Open(filepath.Join(b.TempDir(), "hybrid.db"))
			if err != nil {
				b.Fatal(err)
			}
			b.Cleanup(func() { s.Close() })
			client, err := provider.NewEmbeddingClient("benchmark-only", "https://benchmark.invalid/v1", "synthetic", 2048)
			if err != nil {
				b.Fatal(err)
			}
			cfg := client.Config()
			if err = s.RegisterKnowledgeEmbeddingConfig(b.Context(), cfg); err != nil {
				b.Fatal(err)
			}
			doc, err := s.CreatePastedDocument(b.Context(), "固定来源", "记忆练习")
			if err != nil {
				b.Fatal(err)
			}
			if _, err = s.ChangeKnowledgeEmbeddingScope(b.Context(), cfg.ID, 1, true, []EmbeddingSource{{"document", doc.ID}}); err != nil {
				b.Fatal(err)
			}
			blob := make([]byte, 2048*4)
			binary.LittleEndian.PutUint32(blob, math.Float32bits(1))
			tx, err := s.DB.BeginTx(b.Context(), nil)
			if err != nil {
				b.Fatal(err)
			}
			defer tx.Rollback()
			docs, err := tx.Prepare(`INSERT INTO knowledge_search_docs(key,kind,object_id,source_type,source_id,revision,title,body,created_at,updated_at)VALUES(?,'owner_reflection',?,'document',?,1,'固定来源','记忆练习的条件','2026-10-01','2026-10-01')`)
			if err != nil {
				b.Fatal(err)
			}
			vectors, err := tx.Prepare(`INSERT INTO knowledge_embedding_vectors(config_id,doc_key,window_no,revision,content_hash,dimensions,vector)VALUES(?,?,0,1,'synthetic',2048,?)`)
			if err != nil {
				b.Fatal(err)
			}
			for i := 0; i < size; i++ {
				key := fmt.Sprintf("benchmark:%06d", i)
				if _, err = docs.Exec(key, key, doc.ID); err != nil {
					b.Fatal(err)
				}
				if _, err = vectors.Exec(cfg.ID, key, blob); err != nil {
					b.Fatal(err)
				}
			}
			docs.Close()
			vectors.Close()
			if err = tx.Commit(); err != nil {
				b.Fatal(err)
			}
			job, _, err := s.ReserveKnowledgeQueryEmbedding(b.Context(), cfg.ID, "记忆练习")
			if err != nil {
				b.Fatal(err)
			}
			claimed, err := s.ClaimNextJob(b.Context(), "60 seconds")
			if err != nil || claimed == nil || claimed.ID != job.ID {
				b.Fatal(claimed, err)
			}
			ex, err := s.GetJobExecution(b.Context(), job.ID)
			if err != nil {
				b.Fatal(err)
			}
			var in KnowledgeEmbeddingJobInput
			if err = json.Unmarshal([]byte(ex.InputSnapshotJSON), &in); err != nil {
				b.Fatal(err)
			}
			vec := make([]float32, 2048)
			vec[0] = 1
			if err = s.CommitKnowledgeEmbeddingResponse(b.Context(), job.ID, in, &provider.EmbeddingResult{Model: cfg.Model, Dimensions: 2048, Vectors: [][]float32{vec}, UsageKnown: true}); err != nil {
				b.Fatal(err)
			}
			if err = s.MarkJobSucceeded(b.Context(), job.ID); err != nil {
				b.Fatal(err)
			}
			request := KnowledgeRetrieveQuery{Search: KnowledgeSearchQuery{Text: "记忆练习"}, Semantic: true, EmbeddingConfigID: cfg.ID}
			for _, cold := range []bool{true, false} {
				name := "warm"
				if cold {
					name = "cold"
				}
				b.Run(name, func(b *testing.B) {
					var times []time.Duration
					var peak uint64
					b.ReportAllocs()
					b.ResetTimer()
					for i := 0; i < b.N; i++ {
						if cold {
							s.embeddingMatrixMu.Lock()
							s.embeddingMatrix = nil
							s.embeddingMatrixMu.Unlock()
						}
						start := time.Now()
						result, err := s.EvaluateKnowledgeRetrieval(b.Context(), request)
						times = append(times, time.Since(start))
						if err != nil || result.Method != "rrf" || result.IndexedCount != size {
							b.Fatal(result, err)
						}
						var mem runtime.MemStats
						runtime.ReadMemStats(&mem)
						if mem.HeapInuse > peak {
							peak = mem.HeapInuse
						}
					}
					b.StopTimer()
					sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
					index := (len(times)*95+99)/100 - 1
					b.ReportMetric(float64(times[index].Microseconds())/1000, "p95-ms")
					b.ReportMetric(float64(peak)/(1024*1024), "sampled-heap-MiB")
				})
			}
		})
	}
}
