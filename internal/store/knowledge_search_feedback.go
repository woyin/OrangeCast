package store

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/google/uuid"
)

// KnowledgeSearchFeedback is an Owner observation, not a verified quality gate.
type KnowledgeSearchFeedback struct {
	Query, Key, Label, Method, CreatedAt string
	Revision                             int
}

// RecordKnowledgeSearchFeedback saves an explicit Owner relevance observation.
func (s *Store) RecordKnowledgeSearchFeedback(ctx context.Context, f KnowledgeSearchFeedback) error {
	if strings.TrimSpace(f.Query) == "" || len([]rune(f.Query)) > 200 || len(f.Key) > 200 || f.Revision < 1 || (f.Label != "relevant" && f.Label != "irrelevant") || (f.Method != "fts" && f.Method != "rrf" && f.Method != "rerank_fts" && f.Method != "rerank_rrf") {
		return ErrInvalidEditorialState
	}
	// The INSERT uses the same local visibility/source plan as retrieval, so a
	// concurrent purge or archive cannot be reported as a successfully saved label.
	plan, err := s.knowledgePlan(ctx, KnowledgeSearchQuery{})
	if err != nil {
		return err
	}
	args := []any{uuid.NewString(), f.Query, f.Key, f.Revision, f.Label, f.Method}
	args = append(args, plan.Args...)
	args = append(args, f.Key, f.Revision)
	res, err := s.DB.ExecContext(ctx, `INSERT INTO knowledge_search_feedback(id,query,doc_key,revision,label,method) SELECT ?,?,?,?,?,?`+plan.From+` AND d.key=? AND d.revision=?`, args...)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return ErrConflict
	}
	return nil
}

// ExportKnowledgeSearchFeedback returns bounded current-object observations.
func (s *Store) ExportKnowledgeSearchFeedback(ctx context.Context) ([]KnowledgeSearchFeedback, error) {
	plan, err := s.knowledgePlan(ctx, KnowledgeSearchQuery{})
	if err != nil {
		return nil, err
	}
	from := strings.Replace(plan.From, " WHERE ", " JOIN knowledge_search_feedback f ON f.doc_key=d.key AND f.revision=d.revision WHERE ", 1)
	rows, err := s.DB.QueryContext(ctx, `SELECT f.query,f.doc_key,f.revision,f.label,f.method,f.created_at`+from+` ORDER BY f.created_at DESC,f.rowid DESC LIMIT 200`, plan.Args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []KnowledgeSearchFeedback{}
	for rows.Next() {
		var f KnowledgeSearchFeedback
		if err = rows.Scan(&f.Query, &f.Key, &f.Revision, &f.Label, &f.Method, &f.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// MarshalSearchFeedbackExport makes the limit and evidence status explicit.
func MarshalSearchFeedbackExport(items []KnowledgeSearchFeedback) ([]byte, error) {
	return json.Marshal(struct {
		HumanAttested     bool                      `json:"human_attested"`
		QualityGatePassed bool                      `json:"quality_gate_passed"`
		Limit             int                       `json:"limit"`
		Observations      []KnowledgeSearchFeedback `json:"observations"`
	}{false, false, 200, items})
}
