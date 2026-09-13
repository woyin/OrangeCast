package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
)

// digestCols / digestBlockCols 保持 SELECT 列序与 scanDigest/scanDigestBlock 一致。
const (
	digestCols     = `id,source_type,source_id,version,title,degraded,provider,model,prompt_version,COALESCE(parent_digest_id,''),COALESCE(reason,''),COALESCE(source_snapshot_id,''),created_at`
	digestBlockCnf = `id,digest_id,position,block_type,text,citations_json,target_source_id,note_id,created_at`
)

func scanDigest(row interface{ Scan(...any) error }) (*models.EpisodeDigest, error) {
	d := &models.EpisodeDigest{}
	var degraded int
	if err := row.Scan(&d.ID, &d.SourceType, &d.SourceID, &d.Version, &d.Title, &degraded, &d.Provider, &d.Model, &d.PromptVersion, &d.ParentDigestID, &d.Reason, &d.SourceSnapshotID, &d.CreatedAt); err != nil {
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

// CreateEpisodeDigest 兼容入口：无落源与缺口的原子发布。
func (s *Store) CreateEpisodeDigest(ctx context.Context, d *models.EpisodeDigest, blocks []models.DigestBlock) (*models.EpisodeDigest, error) {
	return s.PublishEpisodeDigest(ctx, d, blocks, nil, nil)
}

// PublishEpisodeDigest 原子发布一份完整修订（G02）：正文、块、检索落源、缺口
// 在单个事务内写入——不存在"半份修订成为 current"的中间状态；失败整体回滚可重试。
func (s *Store) PublishEpisodeDigest(ctx context.Context, d *models.EpisodeDigest, blocks []models.DigestBlock, searchRows []models.DigestSearchSource, factGaps []models.DigestFactGap) (*models.EpisodeDigest, error) {
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
		`INSERT INTO episode_digests (id,source_type,source_id,version,title,degraded,provider,model,prompt_version,parent_digest_id,reason,source_snapshot_id)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		d.ID, string(d.SourceType), d.SourceID, d.Version, d.Title, degraded, d.Provider, d.Model, d.PromptVersion, d.ParentDigestID, d.Reason, d.SourceSnapshotID); err != nil {
		return nil, fmt.Errorf("写入精读文: %w", err)
	}
	for i := range blocks {
		b := &blocks[i]
		b.ID = uuid.NewString() // 块身份属于修订（G03）：新修订一律新块 ID
		b.DigestID = d.ID
		b.Position = i + 1
		cites, _ := json.Marshal(b.Citations)
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO digest_blocks (id,digest_id,position,block_type,text,citations_json,target_source_id,note_id)
			 VALUES (?,?,?,?,?,?,?,?)`,
			b.ID, b.DigestID, b.Position, string(b.Type), b.Text, string(cites), b.TargetSourceID, b.NoteID); err != nil {
			return nil, fmt.Errorf("写入内容块: %w", err)
		}
	}
	for _, row := range searchRows {
		status := row.Status
		if status == "" {
			status = "pending" // 与 AddDigestSearchSources 语义一致（G02 修复点）
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO digest_search_sources (id,digest_id,query,url,title,document_id,status)
			 VALUES (?,?,?,?,?,?,?)`,
			uuid.NewString(), d.ID, row.Query, row.URL, row.Title, row.DocumentID, status); err != nil {
			return nil, fmt.Errorf("写入检索落源: %w", err)
		}
	}
	for _, gap := range factGaps {
		if _, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO digest_fact_gaps (id,digest_id,text,document_id) VALUES (?,?,?,?)`,
			uuid.NewString(), d.ID, gap.Text, gap.DocumentID); err != nil {
			return nil, fmt.Errorf("写入事实缺口: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetEpisodeDigest(ctx, d.ID)
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
		`INSERT INTO digest_rewrites (id,digest_id,channel,text,provider,model,input_hash,digest_version) VALUES (?,?,?,?,?,?,?,?)
		 ON CONFLICT(digest_id,channel) DO UPDATE SET text=excluded.text,provider=excluded.provider,model=excluded.model,input_hash=excluded.input_hash,digest_version=excluded.digest_version,created_at=datetime('now')`,
		r.ID, r.DigestID, r.Channel, r.Text, r.Provider, r.Model, r.InputHash, r.DigestVersion)
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

// ErrDigestRevisionConflict 编辑携带的基础修订已过期（G03）。
var ErrDigestRevisionConflict = errors.New("digest revision conflict")

// DigestRevisionInput 派生新修订的输入。
// BaseVersionClaimed 是编辑表单携带的基准版本：不等于该 Source 当前最大版本时
// 返回 ErrDigestRevisionConflict（过期编辑）。
type DigestRevisionInput struct {
	Base               *models.EpisodeDigest
	BaseVersionClaimed int
	Reason             string
	NewTitle           string               // 空则继承基础标题
	Blocks             []models.DigestBlock // 调整后的完整块序列
}

// CreateDigestRevision 从基础修订派生一份新修订（G03）：版本 = MAX+1，
// 记录父修订与原因；旧版本内容及引用不变。base.Version 不等于当前最大版本时
// 返回 ErrDigestRevisionConflict（过期编辑）。
// 约束（块身份不可越权）：转述/引用事实必须保留引用；笔记块必须携带 NoteID
// 且文本与该笔记当前内容一致（改笔记内容应回 OwnerNote 编辑再显式选用）。
func (s *Store) CreateDigestRevision(ctx context.Context, in DigestRevisionInput) (*models.EpisodeDigest, error) {
	if in.Base == nil || in.Base.ID == "" {
		return nil, fmt.Errorf("%w: 缺少基础修订", ErrInvalidEditorialState)
	}
	base, err := s.GetEpisodeDigest(ctx, in.Base.ID)
	if err != nil {
		return nil, err
	}
	if in.BaseVersionClaimed > 0 {
		var maxVersion int
		if err := s.DB.QueryRowContext(ctx,
			`SELECT COALESCE(MAX(version),0) FROM episode_digests WHERE source_type=? AND source_id=?`,
			string(base.SourceType), base.SourceID).Scan(&maxVersion); err != nil {
			return nil, err
		}
		if in.BaseVersionClaimed != maxVersion {
			return nil, ErrDigestRevisionConflict
		}
	}
	if len(in.Blocks) == 0 {
		return nil, fmt.Errorf("%w: 修订不能清空全部内容", ErrInvalidEditorialState)
	}
	for _, b := range in.Blocks {
		if b.Text == "" {
			return nil, fmt.Errorf("%w: 修订块文本为空", ErrInvalidEditorialState)
		}
		switch b.Type {
		case models.DigestBlockParaphrase, models.DigestBlockCitedFact:
			if len(b.Citations) == 0 {
				return nil, fmt.Errorf("%w: 转述/引用事实块必须保留依据引用", ErrInvalidEditorialState)
			}
		case models.DigestBlockNote:
			if b.NoteID == "" {
				return nil, fmt.Errorf("%w: 笔记块必须关联既有笔记", ErrInvalidEditorialState)
			}
			note, err := s.GetOwnerNote(ctx, b.NoteID)
			if err != nil {
				return nil, fmt.Errorf("%w: 笔记块引用的笔记不存在", ErrInvalidEditorialState)
			}
			if b.Text != note.Content {
				return nil, fmt.Errorf("%w: 笔记块文本必须与所选笔记一致（请回笔记编辑）", ErrInvalidEditorialState)
			}
		}
	}
	title := base.Title
	if in.NewTitle != "" {
		title = in.NewTitle
	}
	return s.PublishEpisodeDigest(ctx, &models.EpisodeDigest{
		SourceType: base.SourceType, SourceID: base.SourceID, Title: title, Degraded: base.Degraded,
		Provider: base.Provider, Model: base.Model, PromptVersion: base.PromptVersion,
		ParentDigestID: base.ID, Reason: strings.TrimSpace(in.Reason),
	}, in.Blocks, nil, nil)
}

// GetDigestRewriteInputHash 读取某修订渠道版本的输入指纹；无记录返回空串。
func (s *Store) GetDigestRewriteInputHash(ctx context.Context, digestID, channel string) (string, error) {
	var h string
	err := s.DB.QueryRowContext(ctx, `SELECT COALESCE(input_hash,'') FROM digest_rewrites WHERE digest_id=? AND channel=?`, digestID, channel).Scan(&h)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return h, err
}

// ListEpisodeDigestsForSource 列出一个 Source 的精读修订（新→旧，G07 单集页衔接）。
func (s *Store) ListEpisodeDigestsForSource(ctx context.Context, sourceType models.SourceType, sourceID string) ([]*models.EpisodeDigest, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT `+digestCols+` FROM episode_digests WHERE source_type=? AND source_id=? ORDER BY version DESC`,
		string(sourceType), sourceID)
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
