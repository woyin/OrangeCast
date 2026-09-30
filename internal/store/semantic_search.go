package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"hash/fnv"
	"math"
	"strings"
	"unicode"
)

const localEmbeddingDimensions = 192

type sqlExecer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func indexLocalKeyPointEmbedding(ctx context.Context, exec sqlExecer, keyPointID, text string) error {
	payload, _ := json.Marshal(localTextEmbedding(text))
	_, err := exec.ExecContext(ctx, `INSERT INTO keypoint_embeddings(keypoint_id,provider,model,dimensions,vector_json) VALUES(?,?,?,?,?)
		ON CONFLICT(keypoint_id) DO UPDATE SET provider=excluded.provider,model=excluded.model,dimensions=excluded.dimensions,vector_json=excluded.vector_json,indexed_at=datetime('now')`,
		keyPointID, "local", "char-ngram-v1", localEmbeddingDimensions, string(payload))
	return err
}

func localTextEmbedding(value string) []float64 {
	clean := []rune(strings.ToLower(strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			return r
		}
		return ' '
	}, value)))
	vector := make([]float64, localEmbeddingDimensions)
	for n := 1; n <= 3; n++ {
		for i := 0; i+n <= len(clean); i++ {
			gram := strings.TrimSpace(string(clean[i : i+n]))
			if gram == "" {
				continue
			}
			h := fnv.New64a()
			_, _ = h.Write([]byte(gram))
			vector[int(h.Sum64()%localEmbeddingDimensions)]++
		}
	}
	norm := 0.0
	for _, value := range vector {
		norm += value * value
	}
	if norm = math.Sqrt(norm); norm > 0 {
		for i := range vector {
			vector[i] /= norm
		}
	}
	return vector
}

// SearchKeyPointsHybrid retains the public interface while using the shared local index.
// The legacy char-ngram projection remains an offline comparison artifact; reads
// no longer load and sort 5000 unrelated vectors.
func (s *Store) SearchKeyPointsHybrid(ctx context.Context, query string, limit int) ([]*KeyPointRow, error) {
	if limit < 1 || limit > 100 {
		limit = 50
	}
	result, err := s.SearchKnowledge(ctx, KnowledgeSearchQuery{Text: query, Kind: "keypoint", PerPage: limit, Recall: true})
	if err != nil {
		return nil, err
	}
	out := make([]*KeyPointRow, 0, len(result.Hits))
	for _, hit := range result.Hits {
		kp, err := s.GetKeyPoint(ctx, hit.ObjectID)
		if err != nil {
			return nil, err
		}
		out = append(out, kp)
	}
	return out, nil
}
