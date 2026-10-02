package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"strings"
)

// ArticleQualityFeedback anchors an explicit Owner observation to immutable content.
type ArticleQualityFeedback struct {
	ID, ArticleID, ArticleTitle, ContentHash, ParagraphHash, Category, Comment, CreatedAt string
	Revision, ParagraphIndex                                                              int
}

// ArticleQualityCase is a separately accepted, immutable version of the desired result.
type ArticleQualityCase struct {
	ID, FeedbackID, Expected, Classification, ClassificationEvidence, State, InputJSON, BlocksJSON, Fingerprint, CreatedAt string
	Version                                                                                                                int
}

func qualityHash(v string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(v))) }

// RecordArticleQualityFeedback does not accept a case or change writing preferences. -1 means whole article.
func (s *Store) RecordArticleQualityFeedback(ctx context.Context, article string, revision, paragraph int, contentHash, paragraphHash, category, comment string) (string, error) {
	if len([]rune(comment)) > 2000 || (category != "useful" && category != "shallow" && category != "duplicate" && category != "misattribution") {
		return "", ErrInvalidEditorialState
	}
	r, err := s.GetKnowledgeRevision(ctx, article, revision)
	if err != nil {
		return "", err
	}
	if r.ContentHash != contentHash {
		return "", ErrConflict
	}
	var blocks []provider.KnowledgeBlock
	if err = json.Unmarshal([]byte(r.BlocksJSON), &blocks); err != nil {
		return "", err
	}
	if paragraph == -1 {
		if category != "useful" || paragraphHash != "" {
			return "", ErrInvalidEditorialState
		}
	} else if paragraph < 0 || paragraph >= len(blocks) || qualityHash(blocks[paragraph].Text) != paragraphHash {
		return "", ErrConflict
	}
	id := uuid.NewString()
	_, err = s.DB.ExecContext(ctx, `INSERT INTO article_quality_feedback(id,article_id,revision,content_hash,paragraph_index,paragraph_hash,category,comment) VALUES(?,?,?,?,?,?,?,?)`, id, article, revision, contentHash, paragraph, paragraphHash, category, strings.TrimSpace(comment))
	return id, err
}

// AcceptArticleQualityCase creates a new frozen version. The expected version and request UUID make retries safe.
func (s *Store) AcceptArticleQualityCase(ctx context.Context, feedback, key string, expectedVersion int, expected string) (*ArticleQualityCase, error) {
	if _, err := uuid.Parse(key); err != nil {
		return nil, ErrInvalidEditorialState
	}
	if len([]rune(expected)) > 4000 || strings.TrimSpace(expected) == "" {
		return nil, ErrInvalidEditorialState
	}
	hash := qualityHash(fmt.Sprintf("%s\n%d\n%s", feedback, expectedVersion, expected))
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var previous, id string
	err = tx.QueryRowContext(ctx, `SELECT command_hash,case_id FROM article_quality_commands WHERE request_key=?`, key).Scan(&previous, &id)
	if err == nil {
		if previous != hash {
			return nil, ErrConflict
		}
		tx.Rollback()
		return s.GetArticleQualityCase(ctx, id)
	}
	if err != sql.ErrNoRows {
		return nil, err
	}
	var current int
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(version),0) FROM article_quality_cases WHERE feedback_id=?`, feedback).Scan(&current); err != nil {
		return nil, err
	}
	if current != expectedVersion {
		return nil, ErrConflict
	}
	var input, blocks, content, state string
	err = tx.QueryRowContext(ctx, `SELECT r.input_json,r.blocks_json,r.content_hash,r.evidence_status FROM article_quality_feedback f JOIN knowledge_article_revisions r ON r.article_id=f.article_id AND r.revision=f.revision WHERE f.id=? AND f.content_hash=r.content_hash`, feedback).Scan(&input, &blocks, &content, &state)
	if err != nil {
		return nil, err
	}
	if state == "unavailable" {
		return nil, ErrConflict
	}
	id = uuid.NewString()
	fingerprint := qualityHash(input + "\n" + blocks + "\n" + content + "\n" + expected)
	_, err = tx.ExecContext(ctx, `INSERT INTO article_quality_cases(id,feedback_id,version,expected,input_json,blocks_json,fingerprint) VALUES(?,?,?,?,?,?,?)`, id, feedback, current+1, expected, input, blocks, fingerprint)
	if err != nil {
		return nil, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO article_quality_commands VALUES(?,?,?)`, key, hash, id)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetArticleQualityCase(ctx, id)
}

const qualityCaseColumns = `id,feedback_id,version,expected,classification,classification_evidence,state,input_json,blocks_json,fingerprint,created_at`

func scanQualityCase(row interface{ Scan(...any) error }) (*ArticleQualityCase, error) {
	v := &ArticleQualityCase{}
	err := row.Scan(&v.ID, &v.FeedbackID, &v.Version, &v.Expected, &v.Classification, &v.ClassificationEvidence, &v.State, &v.InputJSON, &v.BlocksJSON, &v.Fingerprint, &v.CreatedAt)
	return v, err
}

// GetArticleQualityCase reads an exact accepted case version, including retirement or unavailable evidence; it does not authorize sending.
func (s *Store) GetArticleQualityCase(ctx context.Context, id string) (*ArticleQualityCase, error) {
	return scanQualityCase(s.DB.QueryRowContext(ctx, `SELECT `+qualityCaseColumns+` FROM article_quality_cases WHERE id=?`, id))
}

// ListArticleQualityCases returns at most 200 recent private case versions without replacing historical inputs.
func (s *Store) ListArticleQualityCases(ctx context.Context) ([]*ArticleQualityCase, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+qualityCaseColumns+` FROM article_quality_cases ORDER BY created_at DESC,id LIMIT 200`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*ArticleQualityCase{}
	for rows.Next() {
		v, err := scanQualityCase(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// ClassifyArticleQualityCase is explicit Owner confirmation, never a model diagnosis.
func (s *Store) ClassifyArticleQualityCase(ctx context.Context, id string, version int, class, evidence string) error {
	return s.ClassifyArticleQualityCaseCommand(ctx, id, version, class, evidence, uuid.NewString())
}

// ClassifyArticleQualityCaseCommand records an explicit Owner classification once with UUID replay and version CAS; no model is called.
func (s *Store) ClassifyArticleQualityCaseCommand(ctx context.Context, id string, version int, class, evidence, key string) error {
	if class != "unclassified" && class != "coverage_gap" && class != "recall_miss" && class != "capacity_omission" && class != "unknown" {
		return ErrInvalidEditorialState
	}
	var parsed map[string]json.RawMessage
	if len(evidence) > 8000 || json.Unmarshal([]byte(evidence), &parsed) != nil || parsed == nil {
		return ErrInvalidEditorialState
	}
	if class != "unclassified" && class != "unknown" && len(parsed) == 0 {
		return ErrInvalidEditorialState
	}
	return s.mutateQualityCase(ctx, id, version, "classify", class, evidence, key)
}

// RetireArticleQualityCase retires an accepted version using a fresh command identity; resumable callers use the Command variant.
func (s *Store) RetireArticleQualityCase(ctx context.Context, id string, version int) error {
	return s.RetireArticleQualityCaseCommand(ctx, id, version, uuid.NewString())
}

// RetireArticleQualityCaseCommand retires the exact version with UUID replay, preserving Owner observations and frozen identity.
func (s *Store) RetireArticleQualityCaseCommand(ctx context.Context, id string, version int, key string) error {
	return s.mutateQualityCase(ctx, id, version, "retire", "", "", key)
}
func (s *Store) mutateQualityCase(ctx context.Context, id string, version int, action, class, evidence, key string) error {
	if _, err := uuid.Parse(key); err != nil {
		return ErrInvalidEditorialState
	}
	hash := qualityHash(fmt.Sprintf("%s\n%s\n%d\n%s\n%s", action, id, version, class, evidence))
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var previous string
	err = tx.QueryRowContext(ctx, `SELECT command_hash FROM article_quality_commands WHERE request_key=?`, key).Scan(&previous)
	if err == nil {
		if previous != hash {
			return ErrConflict
		}
		return nil
	}
	if err != sql.ErrNoRows {
		return err
	}
	var result sql.Result
	if action == "classify" {
		result, err = tx.ExecContext(ctx, `UPDATE article_quality_cases SET classification=?,classification_evidence=?,updated_at=datetime('now') WHERE id=? AND version=? AND state='accepted' AND classification='unclassified'`, class, evidence, id, version)
	} else {
		result, err = tx.ExecContext(ctx, `UPDATE article_quality_cases SET state='retired',updated_at=datetime('now') WHERE id=? AND version=? AND state='accepted'`, id, version)
	}
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return ErrConflict
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO article_quality_commands VALUES(?,?,?)`, key, hash, id); err != nil {
		return err
	}
	return tx.Commit()
}

// ArticleQualityManifest freezes accepted cases only and rechecks each material permission at export time.
type ArticleQualityManifest struct {
	Version     int                   `json:"version"`
	Provider    string                `json:"provider"`
	Cases       []*ArticleQualityCase `json:"cases"`
	Fingerprint string                `json:"fingerprint"`
}

// BuildArticleQualityManifest exports only accepted cases after checking every current source and understanding permission; limits are 100 cases and 4 MiB.
func (s *Store) BuildArticleQualityManifest(ctx context.Context, ids []string, name string) (*ArticleQualityManifest, error) {
	if len(ids) == 0 || len(ids) > 100 || strings.TrimSpace(name) == "" {
		return nil, ErrInvalidEditorialState
	}
	m := &ArticleQualityManifest{Version: 1, Provider: name, Cases: []*ArticleQualityCase{}}
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			return nil, ErrInvalidEditorialState
		}
		seen[id] = true
		c, err := s.GetArticleQualityCase(ctx, id)
		if err != nil {
			return nil, err
		}
		if c.State != "accepted" {
			return nil, ErrConflict
		}
		var request provider.KnowledgeArticleRequest
		if err = json.Unmarshal([]byte(c.InputJSON), &request); err != nil {
			return nil, err
		}
		for _, material := range request.Materials {
			if material.Kind == "understanding" {
				if err := checkUnderstandingMaterialReader(ctx, s.DB, name, material); err != nil {
					return nil, err
				}
				continue
			}
			if material.SourceID == "" {
				return nil, ErrInvalidEditorialState
			}
			policy, err := s.GetSourcePolicy(ctx, models.SourceType(material.SourceType), material.SourceID)
			if err != nil {
				return nil, err
			}
			if policy.Archived {
				return nil, ErrInvalidEditorialState
			}
			allowed := policy.ModelDataPolicy == models.ModelDataPolicy("external_allowed")
			for _, approved := range policy.ApprovedProviders {
				if policy.ModelDataPolicy == models.ModelDataPolicy("approved_providers_only") && strings.EqualFold(approved, name) {
					allowed = true
				}
			}
			if !allowed {
				return nil, ErrInvalidEditorialState
			}
		}
		m.Cases = append(m.Cases, c)
	}
	raw, _ := json.Marshal(m)
	if len(raw) > 4*1024*1024 {
		return nil, ErrInvalidEditorialState
	}
	m.Fingerprint = qualityHash(string(raw))
	return m, nil
}

// ListArticleQualityFeedback exposes immutable observations for explicit acceptance.
func (s *Store) ListArticleQualityFeedback(ctx context.Context) ([]ArticleQualityFeedback, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT f.id,f.article_id,f.revision,f.content_hash,f.paragraph_index,f.paragraph_hash,f.category,f.comment,f.created_at,COALESCE(r.title,'原文章已不可用') FROM article_quality_feedback f LEFT JOIN knowledge_article_revisions r ON r.article_id=f.article_id AND r.revision=f.revision ORDER BY f.created_at DESC,f.id LIMIT 200`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ArticleQualityFeedback{}
	for rows.Next() {
		var v ArticleQualityFeedback
		if err = rows.Scan(&v.ID, &v.ArticleID, &v.Revision, &v.ContentHash, &v.ParagraphIndex, &v.ParagraphHash, &v.Category, &v.Comment, &v.CreatedAt, &v.ArticleTitle); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// ArticleQualityFacts are frozen retrieval observations, not model explanations or library-wide diagnoses.
type ArticleQualityFacts struct {
	Candidates        []string
	Coverage          *provider.KnowledgeRecallCoverage
	CapacityOmissions []string
	Gaps              []EvidenceGap
	Boundary          string
}

// ArticleQualityCaseFacts projects frozen recall counts and explicit program/Owner gaps without diagnosing the library or calling a model.
func (s *Store) ArticleQualityCaseFacts(ctx context.Context, id string) (ArticleQualityFacts, error) {
	facts := ArticleQualityFacts{Candidates: []string{"unknown"}, Boundary: "仅本次冻结检索与明确记录；不能据此声称全库缺少材料"}
	c, err := s.GetArticleQualityCase(ctx, id)
	if err != nil {
		return facts, err
	}
	if c.InputJSON == "" {
		return facts, nil
	}
	var request provider.KnowledgeArticleRequest
	if err = json.Unmarshal([]byte(c.InputJSON), &request); err != nil {
		return facts, err
	}
	facts.Coverage = request.Coverage
	for _, candidate := range request.Candidates {
		if candidate.State == "not_read" {
			facts.CapacityOmissions = append(facts.CapacityOmissions, candidate.MaterialID)
		}
	}
	if len(facts.CapacityOmissions) > 0 || (request.Coverage != nil && request.Coverage.LimitReached) {
		facts.Candidates = append(facts.Candidates, "capacity_omission")
	}
	var article string
	if err = s.DB.QueryRowContext(ctx, `SELECT article_id FROM article_quality_feedback WHERE id=?`, c.FeedbackID).Scan(&article); err != nil {
		return facts, err
	}
	gaps, err := s.ListEvidenceGaps(ctx, "article", article)
	if err != nil {
		return facts, err
	}
	for _, gap := range gaps {
		if gap.State != "expired" && (gap.Origin == "program" || gap.Origin == "owner") {
			facts.Gaps = append(facts.Gaps, gap)
		}
	}
	if len(facts.Gaps) > 0 {
		facts.Candidates = append(facts.Candidates, "coverage_gap")
	}
	return facts, nil
}

// FreezeArticleQualityEstimate freezes the existing local model price and exact stage messages for a private run.
// Unknown price stays unknown; this read-only transaction never creates an execution or calls a provider.
func (s *Store) FreezeArticleQualityEstimate(ctx context.Context, name, model string, req *provider.KnowledgeArticleRequest) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = freezeKnowledgeEstimate(ctx, tx, name, model, req)
	return err
}
