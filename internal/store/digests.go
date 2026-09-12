package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
)

// digestCols / digestBlockCols 保持 SELECT 列序与 scanDigest/scanDigestBlock 一致。
const (
	digestCols     = `id,source_type,source_id,version,title,degraded,provider,model,prompt_version,created_at`
	digestBlockCnf = `id,digest_id,position,block_type,text,citations_json,target_source_id,note_id,created_at`
)

func scanDigest(row interface{ Scan(...any) error }) (*models.EpisodeDigest, error) {
	d := &models.EpisodeDigest{}
	var degraded int
	if err := row.Scan(&d.ID, &d.SourceType, &d.SourceID, &d.Version, &d.Title, &degraded, &d.Provider, &d.Model, &d.PromptVersion, &d.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	d.Degraded = degraded != 0
	return d, nil
}

func scanDigestBlock(row interface{ Scan(...any) error }) (*models.DigestBlock, error) {
	b := &models.DigestBlock{}
	var citations string
	if err := row.Scan(&b.ID, &b.DigestID, &b.Position, &b.Type, &b.Text, &citations, &b.TargetSourceID, &b.NoteID, &b.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	_ = json.Unmarshal([]byte(citations), &b.Citations)
	return b, nil
}

// CreateEpisodeDigest 写入一次不可变精读文修订（含全部内容块），版本 = 该 Source 已有最大版本 + 1。
// 版本空间与 narrations 同构（ADR-0023 §1）：并发下由 UNIQUE(source_type,source_id,version) 兜底。
func (s *Store) CreateEpisodeDigest(ctx context.Context, d *models.EpisodeDigest, blocks []models.DigestBlock) (*models.EpisodeDigest, error) {
	if d == nil || d.SourceID == "" || d.Title == "" {
		return nil, fmt.Errorf("%w: digest requires source and title", ErrInvalidEditorialState)
	}
	for i := range blocks {
		if blocks[i].Text == "" {
			return nil, fmt.Errorf("%w: digest block %d has empty text", ErrInvalidEditorialState, i)
		}
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var maxVersion int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(version),0) FROM episode_digests WHERE source_type=? AND source_id=?`, string(d.SourceType), d.SourceID).Scan(&maxVersion); err != nil {
		return nil, fmt.Errorf("读取精读文最大版本: %w", err)
	}
	d.ID = uuid.NewString()
	d.Version = maxVersion + 1
	degraded := 0
	if d.Degraded {
		degraded = 1
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO episode_digests (id,source_type,source_id,version,title,degraded,provider,model,prompt_version)
		 VALUES (?,?,?,?,?,?,?,?,?)`,
		d.ID, string(d.SourceType), d.SourceID, d.Version, d.Title, degraded, d.Provider, d.Model, d.PromptVersion); err != nil {
		return nil, fmt.Errorf("写入精读文: %w", err)
	}
	for i := range blocks {
		b := blocks[i]
		citations, _ := json.Marshal(b.Citations)
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO digest_blocks (id,digest_id,position,block_type,text,citations_json,target_source_id,note_id)
			 VALUES (?,?,?,?,?,?,?,?)`,
			uuid.NewString(), d.ID, i, string(b.Type), b.Text, string(citations), b.TargetSourceID, b.NoteID); err != nil {
			return nil, fmt.Errorf("写入精读文内容块: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return d, nil
}

// GetCurrentEpisodeDigest 返回该 Source 当前采用的精读文修订（MAX(version)）；无版本返回 ErrNotFound。
func (s *Store) GetCurrentEpisodeDigest(ctx context.Context, sourceType models.SourceType, sourceID string) (*models.EpisodeDigest, error) {
	d, err := scanDigest(s.DB.QueryRowContext(ctx,
		`SELECT `+digestCols+` FROM episode_digests WHERE source_type=? AND source_id=? ORDER BY version DESC LIMIT 1`,
		string(sourceType), sourceID))
	if err != nil {
		return nil, err
	}
	return d, nil
}

// GetEpisodeDigest 按 ID 加载精读文修订。
func (s *Store) GetEpisodeDigest(ctx context.Context, id string) (*models.EpisodeDigest, error) {
	return scanDigest(s.DB.QueryRowContext(ctx, `SELECT `+digestCols+` FROM episode_digests WHERE id=?`, id))
}

// ListEpisodeDigests 返回全部 Source 的当前精读文修订（每 Source 取 MAX(version)），新→旧。
func (s *Store) ListEpisodeDigests(ctx context.Context) ([]*models.EpisodeDigest, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT `+digestCols+` FROM episode_digests d WHERE version=(SELECT MAX(version) FROM episode_digests m WHERE m.source_type=d.source_type AND m.source_id=d.source_id) ORDER BY d.created_at DESC, d.id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.EpisodeDigest
	for rows.Next() {
		d, err := scanDigest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// ListDigestBlocks 返回一份精读文修订的全部内容块，按 position 升序。
func (s *Store) ListDigestBlocks(ctx context.Context, digestID string) ([]*models.DigestBlock, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT `+digestBlockCnf+` FROM digest_blocks WHERE digest_id=? ORDER BY position ASC`, digestID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.DigestBlock
	for rows.Next() {
		b, err := scanDigestBlock(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// AddDigestSearchSources 记录本次生成经 SourceSearch 沉淀的 Document Source（⑥b 侧栏，pending）。
func (s *Store) AddDigestSearchSources(ctx context.Context, digestID string, sources []models.DigestSearchSource) error {
	for i := range sources {
		src := sources[i]
		if src.DigestID == "" {
			src.DigestID = digestID
		}
		if src.URL == "" || src.DocumentID == "" {
			return fmt.Errorf("%w: search source requires url and document", ErrInvalidEditorialState)
		}
		if _, err := s.DB.ExecContext(ctx,
			`INSERT INTO digest_search_sources (id,digest_id,query,url,title,document_id) VALUES (?,?,?,?,?,?)
			 ON CONFLICT(digest_id,url) DO NOTHING`,
			uuid.NewString(), src.DigestID, src.Query, src.URL, src.Title, src.DocumentID); err != nil {
			return fmt.Errorf("写入检索落源: %w", err)
		}
	}
	return nil
}

// ListDigestSearchSources 返回精读文的全部检索落源。
func (s *Store) ListDigestSearchSources(ctx context.Context, digestID string) ([]*models.DigestSearchSource, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id,digest_id,query,url,title,document_id,status,created_at FROM digest_search_sources WHERE digest_id=? ORDER BY created_at ASC, id ASC`, digestID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.DigestSearchSource
	for rows.Next() {
		src := &models.DigestSearchSource{}
		if err := rows.Scan(&src.ID, &src.DigestID, &src.Query, &src.URL, &src.Title, &src.DocumentID, &src.Status, &src.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, src)
	}
	return out, rows.Err()
}

// SetDigestSearchSourceStatus 更新一条检索落源的确认状态（confirmed | rejected）。
// rejected 语义由调用方落实：删除引用该 Document 的 cited_fact 块（DeleteDigestBlocksForTarget）。
func (s *Store) SetDigestSearchSourceStatus(ctx context.Context, id, status string) error {
	if status != "confirmed" && status != "rejected" && status != "pending" {
		return fmt.Errorf("%w: unknown search source status %q", ErrInvalidEditorialState, status)
	}
	res, err := s.DB.ExecContext(ctx, `UPDATE digest_search_sources SET status=? WHERE id=?`, status, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// AddDigestFactGaps 记录未消解或已消解的 FactGap（document_id 空表示未消解）。
func (s *Store) AddDigestFactGaps(ctx context.Context, digestID string, gaps []models.DigestFactGap) error {
	for i := range gaps {
		g := gaps[i]
		if g.Text == "" {
			return fmt.Errorf("%w: fact gap requires text", ErrInvalidEditorialState)
		}
		if _, err := s.DB.ExecContext(ctx,
			`INSERT INTO digest_fact_gaps (id,digest_id,text,document_id) VALUES (?,?,?,?) ON CONFLICT(digest_id,text) DO NOTHING`,
			uuid.NewString(), digestID, g.Text, g.DocumentID); err != nil {
			return fmt.Errorf("写入事实缺口: %w", err)
		}
	}
	return nil
}

// ListDigestFactGaps 返回精读文的全部事实缺口。
func (s *Store) ListDigestFactGaps(ctx context.Context, digestID string) ([]*models.DigestFactGap, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id,digest_id,text,document_id,created_at FROM digest_fact_gaps WHERE digest_id=? ORDER BY created_at ASC, id ASC`, digestID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.DigestFactGap
	for rows.Next() {
		g := &models.DigestFactGap{}
		if err := rows.Scan(&g.ID, &g.DigestID, &g.Text, &g.DocumentID, &g.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// UpsertDigestRewrite 写入或覆盖一个渠道语气版本（F3：唯一事实源是长文版，渠道版本是投影）。
func (s *Store) UpsertDigestRewrite(ctx context.Context, r *models.DigestRewrite) (*models.DigestRewrite, error) {
	if r.DigestID == "" || r.Channel == "" || r.Text == "" {
		return nil, fmt.Errorf("%w: rewrite requires digest, channel and text", ErrInvalidEditorialState)
	}
	r.ID = uuid.NewString()
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO digest_rewrites (id,digest_id,channel,text,provider,model) VALUES (?,?,?,?,?,?)
		 ON CONFLICT(digest_id,channel) DO UPDATE SET text=excluded.text,provider=excluded.provider,model=excluded.model,created_at=datetime('now')`,
		r.ID, r.DigestID, r.Channel, r.Text, r.Provider, r.Model)
	if err != nil {
		return nil, fmt.Errorf("写入渠道改写: %w", err)
	}
	return r, nil
}

// GetDigestRewrite 读取一个渠道语气版本；无则 ErrNotFound。
func (s *Store) GetDigestRewrite(ctx context.Context, digestID, channel string) (*models.DigestRewrite, error) {
	r := &models.DigestRewrite{}
	err := s.DB.QueryRowContext(ctx,
		`SELECT id,digest_id,channel,text,provider,model,created_at FROM digest_rewrites WHERE digest_id=? AND channel=?`, digestID, channel).
		Scan(&r.ID, &r.DigestID, &r.Channel, &r.Text, &r.Provider, &r.Model, &r.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return r, err
}

// DeleteDigestBlocksForTarget 删除引用指定目标 Source 的内容块（⑥b 剔除落源的落实）。
func (s *Store) DeleteDigestBlocksForTarget(ctx context.Context, digestID, targetSourceID string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM digest_blocks WHERE digest_id=? AND target_source_id=?`, digestID, targetSourceID)
	return err
}

// DeleteEpisodeDigestsForSource Purge 时删除该 Source 的全部精读文修订及关联行。
func (s *Store) DeleteEpisodeDigestsForSource(ctx context.Context, sourceType models.SourceType, sourceID string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM episode_digests WHERE source_type=? AND source_id=?`, string(sourceType), sourceID)
	return err
}

// GetDocumentByOriginURL 按 origin_url 查找已落源的网页 Document（SourceSearch 幂等复用）。
func (s *Store) GetDocumentByOriginURL(ctx context.Context, originURL string) (*models.Document, error) {
	d := &models.Document{}
	err := s.DB.QueryRowContext(ctx,
		`SELECT id,title,origin_kind,origin_url,content,content_sha256,series_id,version,production_use,model_data_policy,archived_at,created_at,updated_at FROM documents WHERE origin_url=? ORDER BY version DESC LIMIT 1`, originURL).
		Scan(&d.ID, &d.Title, &d.OriginKind, &d.OriginURL, &d.Content, &d.ContentSHA256, &d.SeriesID, &d.Version, &d.ProductionUse, &d.ModelDataPolicy, &d.ArchivedAt, &d.CreatedAt, &d.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return d, err
}
