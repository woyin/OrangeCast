package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"hash/fnv"
	"math"
	"sort"
	"strings"
	"unicode"

	"github.com/woyin/orangecast/internal/models"
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

func cosineLocal(a, b []float64) float64 {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	score := 0.0
	for i := 0; i < n; i++ {
		score += a[i] * b[i]
	}
	return score
}

// SearchKeyPointsHybrid combines exact FTS rank with a versioned local vector
// projection. Search ranking is derived data; returned KeyPoints retain their
// original Citation chain.
func (s *Store) SearchKeyPointsHybrid(ctx context.Context, query string, limit int) ([]*KeyPointRow, error) {
	if limit < 1 || limit > 100 {
		limit = 50
	}
	lexical, _, lexicalErr := s.SearchKeyPoints(ctx, query, 1, limit)
	lexicalRank := map[string]int{}
	for i, keyPoint := range lexical {
		lexicalRank[keyPoint.ID] = i
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT ki.id,ki.source_type,ki.source_id,ki.source_title,ki.content,ki.description,ki.citations_json,ki.relation_kind,ki.time_start,ki.time_end,ki.card_version,ki.origin,ki.production_status,ki.parent_keypoint_id,ki.evidence_status,ki.created_at,ke.vector_json
		FROM keypoint_embeddings ke JOIN keypoint_index ki ON ki.id=ke.keypoint_id WHERE ke.provider='local' AND ke.model='char-ngram-v1' LIMIT 5000`)
	if err != nil {
		if lexicalErr == nil {
			return lexical, nil
		}
		return nil, err
	}
	defer rows.Close()
	type scored struct {
		keyPoint *KeyPointRow
		score    float64
	}
	queryVector := localTextEmbedding(query)
	var candidates []scored
	for rows.Next() {
		keyPoint := &KeyPointRow{}
		var relation, origin, status, payload string
		if err := rows.Scan(&keyPoint.ID, &keyPoint.SourceType, &keyPoint.SourceID, &keyPoint.SourceTitle, &keyPoint.Content, &keyPoint.Description, &keyPoint.CitationsJSON, &relation, &keyPoint.TimeStart, &keyPoint.TimeEnd, &keyPoint.CardVersion, &origin, &status, &keyPoint.ParentKeyPointID, &keyPoint.EvidenceStatus, &keyPoint.CreatedAt, &payload); err != nil {
			return nil, err
		}
		keyPoint.RelationKind = models.RelationKind(relation)
		keyPoint.Origin = models.KeyPointOrigin(origin)
		keyPoint.ProductionStatus = models.KeyPointProductionStatus(status)
		var vector []float64
		if json.Unmarshal([]byte(payload), &vector) != nil {
			continue
		}
		score := cosineLocal(queryVector, vector)
		if rank, ok := lexicalRank[keyPoint.ID]; ok {
			score += 2 - float64(rank)/float64(limit+1)
		}
		candidates = append(candidates, scored{keyPoint, score})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].score > candidates[j].score })
	result := make([]*KeyPointRow, 0, limit)
	for _, candidate := range candidates {
		if len(result) == limit {
			break
		}
		if candidate.score > 0 {
			result = append(result, candidate.keyPoint)
		}
	}
	if len(result) == 0 && lexicalErr != nil {
		return nil, lexicalErr
	}
	return result, nil
}
