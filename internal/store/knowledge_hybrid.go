package store

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

const knowledgeSemanticCandidateLimit = 200

var errEmbeddingCapacity = errors.New("embedding matrix exceeds capacity")

type embeddingEpoch struct{ Index, Delete int64 }
type embeddingMatrixEntry struct {
	Key              string
	Revision, Window int
	Vector           []float32
}
type knowledgeEmbeddingMatrix struct {
	CacheKey string
	Entries  []embeddingMatrixEntry
	Objects  int
}
type semanticDocumentRank struct {
	Key      string
	Revision int
	Score    float64
}
type fusedKnowledgeRank struct {
	Key, MatchKind string
	Revision       int
	Score          float64
}

func (s *Store) embeddingEpoch(ctx context.Context) (embeddingEpoch, error) {
	var epoch embeddingEpoch
	err := s.DB.QueryRowContext(ctx, `SELECT index_epoch,delete_epoch FROM knowledge_embedding_state WHERE id=1`).Scan(&epoch.Index, &epoch.Delete)
	return epoch, err
}

// embeddingMatrixFor holds at most one bounded matrix per Store. Only stable
// identities and vectors are cached; source bodies never enter this cache.
func (s *Store) embeddingMatrixFor(ctx context.Context, cfg *KnowledgeEmbeddingConfig, q KnowledgeSearchQuery, epoch embeddingEpoch) (*knowledgeEmbeddingMatrix, error) {
	q.Text = ""
	q.MetadataOnly = true
	plan, err := s.knowledgePlan(ctx, q)
	if err != nil {
		return nil, err
	}
	if plan.Empty {
		return &knowledgeEmbeddingMatrix{}, nil
	}
	normalized := plan.Query
	normalized.Page = 0
	normalized.PerPage = 0
	filter, _ := json.Marshal(normalized)
	key := fmt.Sprintf("%s:%d:%d:%d:%x", cfg.ID, cfg.Revision, epoch.Index, epoch.Delete, sha256.Sum256(filter))
	s.embeddingMatrixMu.Lock()
	cached := s.embeddingMatrix
	s.embeddingMatrixMu.Unlock()
	if cached != nil && cached.CacheKey == key {
		return cached, nil
	}
	var globalWindows int
	if err = s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM knowledge_embedding_vectors WHERE config_id=?`, cfg.ID).Scan(&globalWindows); err != nil {
		return nil, err
	}
	if globalWindows > KnowledgeEmbeddingCapacity {
		return nil, errEmbeddingCapacity
	}
	from := strings.Replace(plan.From, " WHERE ", " JOIN knowledge_embedding_vectors v ON v.doc_key=d.key WHERE ", 1)
	args := append(append([]any(nil), plan.Args...), embeddingSQLArgs(cfg)...)
	from += ` AND v.config_id=:config AND v.revision=d.revision AND ` + embeddingDocumentQualified()
	rows, err := s.DB.QueryContext(ctx, `SELECT d.key,d.revision,v.window_no,v.dimensions,v.vector`+from+` ORDER BY d.key,v.window_no LIMIT 50001`, args...)
	if err != nil {
		return nil, err
	}
	// Release the database cursor before normalization/CPU comparison. Peak
	// memory is bounded by 50k * 2048 dimensions; cold loading is measured separately.
	type rawEntry struct {
		Key                          string
		Revision, Window, Dimensions int
		Blob                         []byte
	}
	var raw []rawEntry
	for rows.Next() {
		var item rawEntry
		if err = rows.Scan(&item.Key, &item.Revision, &item.Window, &item.Dimensions, &item.Blob); err != nil {
			rows.Close()
			return nil, err
		}
		if item.Dimensions != cfg.Dimensions {
			rows.Close()
			return nil, ErrInvalidEditorialState
		}
		raw = append(raw, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if len(raw) > KnowledgeEmbeddingCapacity {
		return nil, errEmbeddingCapacity
	}
	matrix := &knowledgeEmbeddingMatrix{CacheKey: key, Entries: make([]embeddingMatrixEntry, len(raw))}
	objects := map[string]bool{}
	for i, item := range raw {
		if i%256 == 0 && ctx.Err() != nil {
			return nil, ctx.Err()
		}
		vector, err := decodeEmbeddingVector(item.Blob, item.Dimensions)
		if err != nil {
			return nil, err
		}
		matrix.Entries[i] = embeddingMatrixEntry{Key: item.Key, Revision: item.Revision, Window: item.Window, Vector: vector}
		objects[item.Key] = true
		raw[i].Blob = nil
	}
	matrix.Objects = len(objects)
	current, err := s.embeddingEpoch(ctx)
	if err != nil {
		return nil, err
	}
	if current != epoch {
		return nil, ErrConflict
	}
	s.embeddingMatrixMu.Lock()
	s.embeddingMatrix = matrix
	s.embeddingMatrixMu.Unlock()
	return matrix, nil
}

// semanticRanks computes outside any DB transaction. All vectors are normalized;
// score orders candidates and is never presented as evidence confidence.
func semanticRanks(ctx context.Context, matrix *knowledgeEmbeddingMatrix, query []float32) ([]semanticDocumentRank, error) {
	best := make(map[string]semanticDocumentRank, matrix.Objects)
	for i, entry := range matrix.Entries {
		if i%256 == 0 && ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if len(entry.Vector) != len(query) {
			return nil, ErrInvalidEditorialState
		}
		var dot float64
		for j, v := range entry.Vector {
			dot += float64(v) * float64(query[j])
		}
		old, exists := best[entry.Key]
		if !exists || dot > old.Score {
			best[entry.Key] = semanticDocumentRank{Key: entry.Key, Revision: entry.Revision, Score: dot}
		}
	}
	ranks := make([]semanticDocumentRank, 0, len(best))
	for _, item := range best {
		ranks = append(ranks, item)
	}
	sort.Slice(ranks, func(i, j int) bool {
		if ranks[i].Score == ranks[j].Score {
			return ranks[i].Key < ranks[j].Key
		}
		return ranks[i].Score > ranks[j].Score
	})
	if len(ranks) > knowledgeSemanticCandidateLimit {
		ranks = ranks[:knowledgeSemanticCandidateLimit]
	}
	return ranks, nil
}

func fuseKnowledgeRanks(lexical []KnowledgeSearchHit, semantic []semanticDocumentRank) []fusedKnowledgeRank {
	const rrfK = 60.0
	scores := map[string]fusedKnowledgeRank{}
	for i, hit := range lexical {
		if _, duplicate := scores[hit.Key]; duplicate {
			continue
		}
		scores[hit.Key] = fusedKnowledgeRank{Key: hit.Key, Revision: hit.Revision, MatchKind: "lexical", Score: 1 / (rrfK + float64(i+1))}
	}
	for i, hit := range semantic {
		current, exists := scores[hit.Key]
		if exists && current.Revision != hit.Revision {
			continue
		}
		if !exists {
			current = fusedKnowledgeRank{Key: hit.Key, Revision: hit.Revision, MatchKind: "semantic"}
		} else {
			current.MatchKind = "both"
		}
		current.Score += 1 / (rrfK + float64(i+1))
		scores[hit.Key] = current
	}
	ranks := make([]fusedKnowledgeRank, 0, len(scores))
	for _, item := range scores {
		ranks = append(ranks, item)
	}
	sort.Slice(ranks, func(i, j int) bool {
		if ranks[i].Score == ranks[j].Score {
			return ranks[i].Key < ranks[j].Key
		}
		return ranks[i].Score > ranks[j].Score
	})
	return ranks
}

func semanticDegradation(err error) string {
	switch {
	case errors.Is(err, errEmbeddingCapacity):
		return "向量索引超过50,000个完整窗口容量，本次使用FTS。"
	case errors.Is(err, ErrNotFound):
		return "当前查询尚无有效向量缓存；明确准备后才能进行语义搜索，本次使用FTS。"
	case errors.Is(err, ErrConflict):
		return "索引版本、来源范围或权限已变化，本次使用FTS；查询缓存需明确重新准备。"
	case errors.Is(err, ErrInvalidEditorialState):
		return "向量维度、数据或准入状态不合格，本次使用FTS。"
	default:
		return "向量索引当前不可读取，本次使用FTS。"
	}
}

func (s *Store) retrieveHybrid(ctx context.Context, req KnowledgeRetrieveQuery, q KnowledgeSearchQuery, lexical KnowledgeSearchResult) (KnowledgeRetrieveResult, error) {
	fallback := func(reason string, fresh bool) (KnowledgeRetrieveResult, error) {
		if fresh {
			var err error
			lexical, err = s.SearchKnowledge(ctx, q)
			if err != nil {
				return KnowledgeRetrieveResult{}, err
			}
		}
		return KnowledgeRetrieveResult{KnowledgeSearchResult: lexical, Method: "fts", LexicalCount: len(lexical.Hits), LexicalTotal: lexical.Total, Degradation: reason}, nil
	}
	if req.EmbeddingConfigID == "" {
		return fallback("语义索引尚未配置或开启，本次使用FTS。", false)
	}
	cfg, err := s.GetKnowledgeEmbeddingConfig(ctx, req.EmbeddingConfigID)
	if err != nil {
		return fallback(semanticDegradation(err), false)
	}
	if !cfg.Enabled {
		return fallback("语义索引已关闭，本次使用FTS。", false)
	}
	epoch, err := s.embeddingEpoch(ctx)
	if err != nil {
		return fallback(semanticDegradation(err), false)
	}
	vector, err := s.KnowledgeQueryEmbedding(ctx, cfg.ID, q.Text)
	if err != nil {
		return fallback(semanticDegradation(err), false)
	}
	matrix, err := s.embeddingMatrixFor(ctx, cfg, q, epoch)
	if err != nil {
		return fallback(semanticDegradation(err), errors.Is(err, ErrConflict))
	}
	if len(matrix.Entries) == 0 {
		return fallback("当前筛选范围尚无可用的授权向量，本次使用FTS。", false)
	}
	semantic, err := semanticRanks(ctx, matrix, vector)
	if err != nil {
		if ctx.Err() != nil {
			return KnowledgeRetrieveResult{}, ctx.Err()
		}
		return fallback(semanticDegradation(err), false)
	}
	lexicalQuery := q
	lexicalQuery.Page = 1
	lexicalQuery.PerPage = 100
	lexicalQuery.MetadataOnly = true
	lexicalPool, err := s.SearchKnowledge(ctx, lexicalQuery)
	if err != nil {
		return KnowledgeRetrieveResult{}, err
	}
	ranked := fuseKnowledgeRanks(lexicalPool.Hits, semantic)
	keys := make([]string, len(ranked))
	for i, item := range ranked {
		keys[i] = item.Key
	}
	// Reuse the exact scope/version/provider predicates at output. Only a bounded
	// prefix of each shortlisted body is read, never the entire library's text.
	finalQuery := q
	finalQuery.Text = ""
	finalQuery.MetadataOnly = true
	finalQuery.Page = 1
	finalQuery.PerPage = 100
	currentHits := make(map[string]KnowledgeSearchHit, len(keys))
	for page := 1; page <= 4; page++ {
		finalQuery.Page = page
		eligible, err := s.searchKnowledgeKeys(ctx, finalQuery, keys)
		if err != nil {
			return KnowledgeRetrieveResult{}, err
		}
		for _, hit := range eligible.Hits {
			currentHits[hit.Key] = hit
		}
		if page*eligible.PerPage >= eligible.Total {
			break
		}
	}
	current, err := s.embeddingEpoch(ctx)
	if err != nil {
		return fallback(semanticDegradation(err), false)
	}
	if current != epoch {
		return fallback(semanticDegradation(ErrConflict), true)
	}
	hits := make([]KnowledgeSearchHit, 0, len(ranked))
	for _, item := range ranked {
		hit, exists := currentHits[item.Key]
		if !exists || hit.Revision != item.Revision {
			continue
		}
		hit.MatchKind = item.MatchKind
		hit.Rank = item.Score
		switch item.MatchKind {
		case "both":
			hit.Reason = "词项与模型向量共同召回；RRF排序不代表事实支持。"
		case "semantic":
			hit.Reason = "模型向量召回；相似度不代表事实支持。"
		default:
			hit.Reason = "词项召回；此对象本次没有语义匹配。"
		}
		hits = append(hits, hit)
	}
	out := KnowledgeRetrieveResult{KnowledgeSearchResult: KnowledgeSearchResult{Total: len(hits), Page: lexical.Page, PerPage: lexical.PerPage}, Method: "rrf", LexicalCount: len(lexicalPool.Hits), LexicalTotal: lexical.Total, SemanticCount: len(semantic), IndexedCount: matrix.Objects, IndexedWindows: len(matrix.Entries), CandidateLimit: knowledgeSemanticCandidateLimit, Coverage: "仅比较Owner选定、当前授权且版本有效的索引；不代表已覆盖整个知识库。"}
	start := (out.Page - 1) * out.PerPage
	if start < len(hits) {
		end := start + out.PerPage
		if end > len(hits) {
			end = len(hits)
		}
		out.Hits = hits[start:end]
	}
	return out, nil
}
