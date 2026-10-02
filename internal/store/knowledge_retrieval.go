package store

import (
	"context"
	"strings"
)

// RetrievalPurpose separates Owner-local browsing from metadata admitted for a
// particular external call. Ranking is not evidence that a claim is supported.
type RetrievalPurpose string

const (
	// RetrieveLocal 选择本地检索用途，不授予模型外发权限。
	RetrieveLocal RetrievalPurpose = "local"
	// RetrieveExternal 仅返回所选供应商当前允许外发的材料。
	RetrieveExternal RetrievalPurpose = "external"
)

// KnowledgeRetrieveQuery 区分检索用途、查询范围及显式向量身份。
type KnowledgeRetrieveQuery struct {
	Search            KnowledgeSearchQuery
	Purpose           RetrievalPurpose
	Semantic          bool
	EmbeddingConfigID string
}

// KnowledgeRetrieveResult 返回有界结果与融合、降级的解释。
type KnowledgeRetrieveResult struct {
	LexicalTotal   int
	IndexedWindows int
	CandidateLimit int
	Coverage       string
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
	return s.retrieveKnowledge(ctx, req, false)
}

// EvaluateKnowledgeRetrieval compares the authorized index before search activation.
// This explicit read-only measurement entry never enables search or sends content.
// It retains all source permissions, versions, capacity and invalidation checks.
func (s *Store) EvaluateKnowledgeRetrieval(ctx context.Context, req KnowledgeRetrieveQuery) (KnowledgeRetrieveResult, error) {
	return s.retrieveKnowledge(ctx, req, true)
}

func (s *Store) retrieveKnowledge(ctx context.Context, req KnowledgeRetrieveQuery, evaluation bool) (KnowledgeRetrieveResult, error) {
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
	if req.Semantic {
		return s.retrieveHybrid(ctx, req, q, evaluation)
	}
	lexical, err := s.SearchKnowledge(ctx, q)
	if err != nil {
		return KnowledgeRetrieveResult{}, err
	}
	out := KnowledgeRetrieveResult{KnowledgeSearchResult: lexical, Method: "fts", LexicalCount: len(lexical.Hits), LexicalTotal: lexical.Total}
	return out, nil
}
