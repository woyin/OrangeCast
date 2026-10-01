package store

import (
	"context"
	"strings"
)

// RetrievalPurpose separates Owner-local browsing from metadata admitted for a
// particular external call. Ranking is not evidence that a claim is supported.
type RetrievalPurpose string

const (
	RetrieveLocal    RetrievalPurpose = "local"
	RetrieveExternal RetrievalPurpose = "external"
)

type KnowledgeRetrieveQuery struct {
	Search            KnowledgeSearchQuery
	Purpose           RetrievalPurpose
	Semantic          bool
	EmbeddingConfigID string
}

type KnowledgeRetrieveResult struct {
	KnowledgeSearchResult
	Method        string
	Degradation   string
	LexicalCount  int
	SemanticCount int
	IndexedCount  int
}

// Retrieve is the single bounded retrieval seam. The existing FTS projection is
// its default adapter; no read generates vectors or starts a remote operation.
func (s *Store) Retrieve(ctx context.Context, req KnowledgeRetrieveQuery) (KnowledgeRetrieveResult, error) {
	q := req.Search
	if req.Purpose == "" {
		req.Purpose = RetrieveLocal
	}
	switch req.Purpose {
	case RetrieveLocal:
		if q.SendProvider != "" || q.RecallProfileID != "" {
			return KnowledgeRetrieveResult{}, ErrInvalidEditorialState
		}
	case RetrieveExternal:
		q.SendProvider = strings.TrimSpace(q.SendProvider)
		if q.SendProvider == "" || len(q.SendProvider) > 200 {
			return KnowledgeRetrieveResult{}, ErrInvalidEditorialState
		}
		// Remote-purpose reads admit metadata first. Full evidence is subsequently
		// loaded and rechecked at the existing paid-call boundary.
		q.Recall, q.MetadataOnly = true, true
		q.IncludeDrafts, q.IncludeHistory = false, false
	default:
		return KnowledgeRetrieveResult{}, ErrInvalidEditorialState
	}
	lexical, err := s.SearchKnowledge(ctx, q)
	if err != nil {
		return KnowledgeRetrieveResult{}, err
	}
	out := KnowledgeRetrieveResult{KnowledgeSearchResult: lexical, Method: "fts", LexicalCount: len(lexical.Hits)}
	if req.Semantic {
		out.Degradation = "语义检索尚未就绪，本次使用本地词项检索；相关排序不代表来源支持结论。"
	}
	return out, nil
}
