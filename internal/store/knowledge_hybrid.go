package store

import (
	"container/heap"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"runtime"
	"sort"
	"strings"
	"sync"
)

const knowledgeSemanticCandidateLimit = 200

var errEmbeddingCapacity = errors.New("embedding matrix exceeds capacity")

type embeddingEpoch struct{ Index, Delete int64 }
type embeddingMatrixEntry struct {
	Key              string
	Revision, Window int
	Document         int
	Vector           []float32
}
type knowledgeEmbeddingMatrix struct {
	CacheKey  string
	Entries   []embeddingMatrixEntry
	Objects   int
	Documents []semanticDocumentRank
}

// One load per Store bounds duplicate allocations, even for different filters.
// Waiters retain their own cancellation; a cancelled owner does not poison them.
type embeddingMatrixLoad struct {
	Key    string
	Done   chan struct{}
	Matrix *knowledgeEmbeddingMatrix
	Err    error
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
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		s.embeddingMatrixMu.Lock()
		if cached := s.embeddingMatrix; cached != nil && cached.CacheKey == key {
			s.embeddingMatrixMu.Unlock()
			return cached, nil
		}
		if loading := s.embeddingMatrixLoad; loading != nil {
			s.embeddingMatrixMu.Unlock()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-loading.Done:
			}
			if loading.Key == key {
				if loading.Err == nil {
					return loading.Matrix, nil
				}
				if !errors.Is(loading.Err, context.Canceled) && !errors.Is(loading.Err, context.DeadlineExceeded) {
					return nil, loading.Err
				}
			}
			continue
		}
		loading := &embeddingMatrixLoad{Key: key, Done: make(chan struct{})}
		s.embeddingMatrixLoad = loading
		s.embeddingMatrixMu.Unlock()
		matrix, err := s.loadEmbeddingMatrix(ctx, cfg, plan, key, epoch)
		s.embeddingMatrixMu.Lock()
		loading.Matrix, loading.Err = matrix, err
		if err == nil {
			s.embeddingMatrix = matrix
		}
		s.embeddingMatrixLoad = nil
		close(loading.Done)
		s.embeddingMatrixMu.Unlock()
		return matrix, err
	}
}

func (s *Store) loadEmbeddingMatrix(ctx context.Context, cfg *KnowledgeEmbeddingConfig, plan knowledgeSearchPlan, key string, epoch embeddingEpoch) (*knowledgeEmbeddingMatrix, error) {
	var err error
	var globalWindows int
	if err = s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM knowledge_embedding_vectors WHERE config_id=?`, cfg.ID).Scan(&globalWindows); err != nil {
		return nil, err
	}
	if globalWindows > cfg.WindowCapacity {
		return nil, errEmbeddingCapacity
	}
	from := strings.Replace(plan.From, " WHERE ", " JOIN knowledge_embedding_vectors v ON v.doc_key=d.key WHERE ", 1)
	// The CTE comes before the plan's positional placeholders. Name every filter
	// so SQLite ordinal positions cannot silently bind scope values to config IDs.
	args := make([]any, 0, len(plan.Args)+2)
	for i, arg := range plan.Args {
		if named, ok := arg.(sql.NamedArg); ok {
			args = append(args, named)
			continue
		}
		name := fmt.Sprintf("embedding_filter_%d", i)
		from = strings.Replace(from, "?", ":"+name, 1)
		args = append(args, sql.Named(name, arg))
	}
	args = append(args, embeddingSQLArgs(cfg)...)
	from += ` AND v.config_id=:config AND v.revision=d.revision AND ` + embeddingMatrixDocumentQualified()
	rows, err := s.DB.QueryContext(ctx, embeddingQualifiedSourcesCTE()+`SELECT d.key,d.revision,v.window_no,v.dimensions,v.vector`+from+` LIMIT 50001`, args...)
	if err != nil {
		return nil, err
	}
	// Decode each row once, avoiding a retained second full 400MiB blob corpus.
	// No unsafe aliasing: the SQL driver's reusable buffer never enters the cache.
	matrix := &knowledgeEmbeddingMatrix{CacheKey: key, Entries: make([]embeddingMatrixEntry, 0, globalWindows)}
	objects := make(map[string]int, globalWindows)
	var vectorArena []float32
	for rows.Next() {
		if len(matrix.Entries)%256 == 0 && ctx.Err() != nil {
			rows.Close()
			return nil, ctx.Err()
		}
		var item embeddingMatrixEntry
		var dimensions int
		var blob sql.RawBytes
		if err = rows.Scan(&item.Key, &item.Revision, &item.Window, &dimensions, &blob); err != nil {
			rows.Close()
			return nil, err
		}
		if dimensions != cfg.Dimensions {
			rows.Close()
			return nil, ErrInvalidEditorialState
		}
		if len(vectorArena) < dimensions {
			windows := 256
			if remaining := globalWindows - len(matrix.Entries); remaining < windows {
				windows = remaining
			}
			if windows < 1 {
				windows = 1
			}
			vectorArena = make([]float32, windows*dimensions)
		}
		item.Vector = vectorArena[:dimensions:dimensions]
		vectorArena = vectorArena[dimensions:]
		err = decodeEmbeddingVectorInto(blob, item.Vector)
		if err != nil {
			rows.Close()
			return nil, err
		}
		document, exists := objects[item.Key]
		if !exists {
			document = len(matrix.Documents)
			objects[item.Key] = document
			matrix.Documents = append(matrix.Documents, semanticDocumentRank{Key: item.Key, Revision: item.Revision})
		}
		item.Document = document
		matrix.Entries = append(matrix.Entries, item)
		if len(matrix.Entries) > KnowledgeEmbeddingCapacity {
			rows.Close()
			return nil, errEmbeddingCapacity
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	matrix.Objects = len(objects)
	current, err := s.embeddingEpoch(ctx)
	if err != nil {
		return nil, err
	}
	if current != epoch {
		return nil, ErrConflict
	}
	return matrix, nil
}

// semanticRanks computes outside any DB transaction. All vectors are normalized;
// score orders candidates and is never presented as evidence confidence.
func semanticRanks(ctx context.Context, matrix *knowledgeEmbeddingMatrix, query []float32) ([]semanticDocumentRank, error) {
	best := make([]float64, len(matrix.Documents))
	for i := range best {
		best[i] = math.Inf(-1)
	}
	scores, err := semanticWindowScores(ctx, matrix, query)
	if err != nil {
		return nil, err
	}
	for i, entry := range matrix.Entries {
		if scores[i] > best[entry.Document] {
			best[entry.Document] = scores[i]
		}
	}
	// Document slots are assigned once when loading the immutable matrix. Each
	// query needs only one score per document and a bounded exact top-200 heap,
	// rather than allocating a string map and sorting the full corpus again.
	ranks := make(semanticRankHeap, 0, knowledgeSemanticCandidateLimit)
	for i, document := range matrix.Documents {
		if i%256 == 0 && ctx.Err() != nil {
			return nil, ctx.Err()
		}
		document.Score = best[i]
		if len(ranks) < knowledgeSemanticCandidateLimit {
			heap.Push(&ranks, document)
		} else if semanticRankBetter(document, ranks[0]) {
			ranks[0] = document
			heap.Fix(&ranks, 0)
		}
	}
	ordered := make([]semanticDocumentRank, len(ranks))
	for i := len(ordered) - 1; i >= 0; i-- {
		ordered[i] = heap.Pop(&ranks).(semanticDocumentRank)
	}
	return ordered, nil
}

// Large dot products use at most four workers, with one large computation at a
// time across the process. Small queries bypass the gate. No worker outlives its
// request: all partitions finish before scores or an error can be returned.
var semanticComputeSlots = make(chan struct{}, 1)

func semanticWindowScores(ctx context.Context, matrix *knowledgeEmbeddingMatrix, query []float32) ([]float64, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	workers := 1
	if len(matrix.Entries)*len(query) >= 1<<20 {
		workers = min(4, runtime.GOMAXPROCS(0))
	}
	if workers > 1 {
		select {
		case semanticComputeSlots <- struct{}{}:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		defer func() { <-semanticComputeSlots }()
	}
	scores := make([]float64, len(matrix.Entries))
	run := func(start, end int) error {
		for i := start; i < end; i++ {
			if (i-start)%256 == 0 && ctx.Err() != nil {
				return ctx.Err()
			}
			entry := matrix.Entries[i]
			if len(entry.Vector) != len(query) || entry.Document < 0 || entry.Document >= len(matrix.Documents) {
				return ErrInvalidEditorialState
			}
			var dot float64
			for j, v := range entry.Vector {
				dot += float64(v) * float64(query[j])
			}
			scores[i] = dot
		}
		return nil
	}
	if workers == 1 {
		if err := run(0, len(scores)); err != nil {
			return nil, err
		}
		return scores, nil
	}
	errors := make([]error, workers)
	var done sync.WaitGroup
	chunk := (len(scores) + workers - 1) / workers
	for worker := 0; worker < workers-1; worker++ {
		start, end := worker*chunk, min((worker+1)*chunk, len(scores))
		done.Add(1)
		go func() { defer done.Done(); errors[worker] = run(start, end) }()
	}
	errors[workers-1] = run((workers-1)*chunk, len(scores))
	done.Wait()
	for _, err := range errors {
		if err != nil {
			return nil, err
		}
	}
	return scores, nil
}

func semanticRankBetter(a, b semanticDocumentRank) bool {
	if a.Score == b.Score {
		return a.Key < b.Key
	}
	return a.Score > b.Score
}

// The root is the worst retained hit, including the stable key tie-break.
type semanticRankHeap []semanticDocumentRank

func (h semanticRankHeap) Len() int           { return len(h) }
func (h semanticRankHeap) Less(i, j int) bool { return semanticRankBetter(h[j], h[i]) }
func (h semanticRankHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *semanticRankHeap) Push(x any)        { *h = append(*h, x.(semanticDocumentRank)) }
func (h *semanticRankHeap) Pop() any {
	old := *h
	x := old[len(old)-1]
	*h = old[:len(old)-1]
	return x
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

func (s *Store) retrieveHybrid(ctx context.Context, req KnowledgeRetrieveQuery, q KnowledgeSearchQuery, evaluation bool) (KnowledgeRetrieveResult, error) {
	// Validate/normalize pagination before either adapter runs. A successful hybrid
	// needs only its 100-hit lexical pool; loading the requested FTS page first
	// would repeat both the full match count and ranking scan.
	plan, err := s.knowledgePlan(ctx, q)
	if err != nil {
		return KnowledgeRetrieveResult{}, err
	}
	q = plan.Query
	fallback := func(reason string) (KnowledgeRetrieveResult, error) {
		lexical, err := s.SearchKnowledge(ctx, q)
		if err != nil {
			return KnowledgeRetrieveResult{}, err
		}
		return KnowledgeRetrieveResult{KnowledgeSearchResult: lexical, Method: "fts", LexicalCount: len(lexical.Hits), LexicalTotal: lexical.Total, Degradation: reason}, nil
	}
	if req.EmbeddingConfigID == "" {
		return fallback("语义索引尚未配置或开启，本次使用FTS。")
	}
	cfg, err := s.GetKnowledgeEmbeddingConfig(ctx, req.EmbeddingConfigID)
	if err != nil {
		return fallback(semanticDegradation(err))
	}
	if !cfg.Enabled {
		return fallback("语义索引已关闭，本次使用FTS。")
	}
	if !evaluation {
		if !cfg.SemanticEnabled {
			return fallback("语义检索未通过准入并开启，本次使用FTS。")
		}
		_, reason, e := s.KnowledgeEmbeddingQualityGate(ctx, cfg.ID)
		if e != nil {
			return fallback(semanticDegradation(e))
		}
		if reason != "" {
			return fallback(reason)
		}
	}
	epoch, err := s.embeddingEpoch(ctx)
	if err != nil {
		return fallback(semanticDegradation(err))
	}
	vector, err := s.KnowledgeQueryEmbedding(ctx, cfg.ID, q.Text)
	if err != nil {
		return fallback(semanticDegradation(err))
	}
	matrix, err := s.embeddingMatrixFor(ctx, cfg, q, epoch)
	if err != nil {
		return fallback(semanticDegradation(err))
	}
	if len(matrix.Entries) == 0 {
		return fallback("当前筛选范围尚无可用的授权向量，本次使用FTS。")
	}
	semantic, err := semanticRanks(ctx, matrix, vector)
	if err != nil {
		if ctx.Err() != nil {
			return KnowledgeRetrieveResult{}, ctx.Err()
		}
		return fallback(semanticDegradation(err))
	}
	lexicalQuery := q
	lexicalQuery.Page = 1
	lexicalQuery.PerPage = 100
	lexicalQuery.MetadataOnly = true
	lexicalPool, err := s.searchKnowledgeLexicalPool(ctx, lexicalQuery)
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
		return fallback(semanticDegradation(err))
	}
	if current != epoch {
		return fallback(semanticDegradation(ErrConflict))
	}
	if !evaluation {
		refreshed, e := s.GetKnowledgeEmbeddingConfig(ctx, cfg.ID)
		if e != nil {
			return fallback(semanticDegradation(e))
		}
		_, reason, e := s.KnowledgeEmbeddingQualityGate(ctx, cfg.ID)
		if e != nil {
			return fallback(semanticDegradation(e))
		}
		if !refreshed.SemanticEnabled || reason != "" {
			return fallback("检索期间准入或开关已失效，本次使用FTS。")
		}
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
	out := KnowledgeRetrieveResult{KnowledgeSearchResult: KnowledgeSearchResult{Total: len(hits), Page: q.Page, PerPage: q.PerPage}, Method: "rrf", LexicalCount: len(lexicalPool.Hits), LexicalTotal: lexicalPool.Total, SemanticCount: len(semantic), IndexedCount: matrix.Objects, IndexedWindows: len(matrix.Entries), CandidateLimit: knowledgeSemanticCandidateLimit, Coverage: "仅比较Owner选定、当前授权且版本有效的索引；不代表已覆盖整个知识库。"}
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
