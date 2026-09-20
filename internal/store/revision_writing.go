// revision_writing.go claim-aware AI 修订持久任务（R21 / C12 / ADR-0024 §6）。
//
// 新契约文章的 AI 修订走 durable worker：HTTP 只入队（重复点击复用同 job，
// failed 真实重试同 job），关闭页面不取消。入队冻结精确 base revision、该修订
// ClaimMap、durable Claim/Style 审核反馈、CreationBrief 精确确认版本/OwnerClaim、
// 原确认 Curator 授权材料与 Writer provider/model/prompt——运行时只消费快照，
// 不重读可变正文/Brief/Profile。旧文章保持同步 runRevisionWriter 兼容路径。
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
	"github.com/woyin/orangecast/internal/provider"
)

// RevisionWritingTaskInput 是入队时冻结的强类型完整 AI 修订输入。
type RevisionWritingTaskInput struct {
	Kind             string `json:"kind"` // 恒为 "ai_revision"
	BaseRevisionID   string `json:"base_revision_id"`
	DraftID          string `json:"draft_id"`
	ProfileID        string `json:"profile_id"`
	Title            string `json:"title"`
	ExistingMarkdown string `json:"existing_markdown"`

	// 原 ClaimMap（base revision 的映射；输出须保持仍存在片段的身份）。
	ExistingClaimMap []models.ClaimMapEntry `json:"existing_claim_map"`

	// durable Claim/Style 审核反馈（issues 列表；至少一条）。
	ReviewFeedback []string `json:"review_feedback"`

	// CreationBrief 精确确认版本与授权范围。
	CreationBriefID string                     `json:"creation_brief_id"`
	BriefVersion    int                        `json:"brief_version"`
	OwnerClaim      string                     `json:"owner_claim"`
	Outline         string                     `json:"outline"`
	Style           string                     `json:"style"`
	TargetLength    *int                       `json:"target_length"`
	Audience        string                     `json:"audience"`
	Materials       []provider.ArticleMaterial `json:"materials"`
	OwnerNotes      []provider.ClaimOwnerNote  `json:"ownerNotes"`

	Provider      string `json:"provider"`
	Model         string `json:"model"`
	PromptVersion string `json:"prompt_version"`
}

// claimRevisionIntentID AI 修订意图身份：同一 base revision 永远对应同一个持久任务。
func claimRevisionIntentID(baseRevisionID string) string {
	return fmt.Sprintf("claim_revision:%s", baseRevisionID)
}

// EnqueueClaimRevisionForRevision 为新契约文章的 base revision 入队 durable AI
// 修订（R21）。重复点击（queued/running/succeeded）复用同一 job；failed 点击真实
// 重试同一 job（reset queued，保留冻结输入/config/checkpoint）。旧文章修订
// （无持久 bridge）返回 ErrInvalidEditorialState，由 HTTP 兼容走同步路径。
func (s *Store) EnqueueClaimRevisionForRevision(ctx context.Context, baseRevisionID string) (*models.ProcessingJob, error) {
	if strings.TrimSpace(baseRevisionID) == "" {
		return nil, fmt.Errorf("%w: base revision required", ErrInvalidEditorialState)
	}
	job, err := s.enqueueClaimRevisionOnce(ctx, baseRevisionID)
	if err == nil {
		return job, nil
	}
	if isUniqueConstraintErr(err) {
		return s.getReviewIntentJob(ctx, claimRevisionIntentID(baseRevisionID))
	}
	return nil, err
}

func (s *Store) enqueueClaimRevisionOnce(ctx context.Context, baseRevisionID string) (*models.ProcessingJob, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	intentID := claimRevisionIntentID(baseRevisionID)
	// 已有意图：复用/重试语义与 R20 审校完全一致。
	var existingJobID string
	switch err := tx.QueryRowContext(ctx, `SELECT job_id FROM revision_review_intents WHERE intent_id=?`, intentID).Scan(&existingJobID); {
	case err == nil:
		job, gerr := scanProcessingJobRow(ctx, tx, existingJobID)
		if gerr != nil {
			return nil, gerr
		}
		if job.Status == models.StatusFailed {
			res, uerr := tx.ExecContext(ctx,
				`UPDATE processing_jobs SET status='queued', last_error=NULL, updated_at=datetime('now')
				 WHERE id=? AND status='failed'`, existingJobID)
			if uerr != nil {
				return nil, uerr
			}
			if n, rerr := res.RowsAffected(); rerr != nil {
				return nil, rerr
			} else if n == 1 {
				if cerr := tx.Commit(); cerr != nil {
					return nil, cerr
				}
				job.Status = models.StatusQueued
				job.LastError = nil
				return job, nil
			}
			job, gerr = scanProcessingJobRow(ctx, tx, existingJobID)
			if gerr != nil {
				return nil, gerr
			}
		}
		if cerr := tx.Commit(); cerr != nil {
			return nil, cerr
		}
		return job, nil
	case errors.Is(err, sql.ErrNoRows):
		// 首次入队，继续。
	default:
		return nil, err
	}

	input, err := freezeRevisionWritingInputTx(ctx, tx, baseRevisionID)
	if err != nil {
		return nil, err
	}
	inputJSON, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("冻结 AI 修订输入快照: %w", err)
	}

	jobID := uuid.NewString()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO processing_jobs
		   (id, source_type, source_id, job_type, status, is_automated,
		    intent_id, input_snapshot_json, config_version, configured_provider, configured_model)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		jobID, string(models.SourceEpisode), input.DraftID, string(models.JobClaimRevision), string(models.StatusQueued), 0,
		intentID, string(inputJSON), input.PromptVersion, input.Provider, input.Model); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO revision_review_intents (intent_id, job_id, revision_id, kind) VALUES (?,?,?,?)`,
		intentID, jobID, baseRevisionID, "ai_revision"); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &models.ProcessingJob{ID: jobID, SourceType: models.SourceEpisode, SourceID: input.DraftID, JobType: models.JobClaimRevision, Status: models.StatusQueued}, nil
}

// freezeRevisionWritingInputTx 在事务内读取并冻结一次 AI 修订的完整输入。
func freezeRevisionWritingInputTx(ctx context.Context, tx *sql.Tx, baseRevisionID string) (*RevisionWritingTaskInput, error) {
	// 1) 精确 base revision + 持久 bridge（新契约专属；旧文章走同步兼容路径）。
	var draftID, title, markdown string
	err := tx.QueryRowContext(ctx, `SELECT draft_id,title,markdown FROM article_revisions WHERE id=?`, baseRevisionID).Scan(&draftID, &title, &markdown)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var profileID string
	if err := tx.QueryRowContext(ctx, `SELECT editorial_profile_id FROM article_drafts WHERE id=?`, draftID).Scan(&profileID); err != nil {
		return nil, fmt.Errorf("%w: article draft missing", ErrNotFound)
	}
	input := &RevisionWritingTaskInput{
		Kind: "ai_revision", BaseRevisionID: baseRevisionID, DraftID: draftID,
		ProfileID: profileID, Title: title, ExistingMarkdown: markdown,
	}
	var linkID, creationBriefID string
	var briefVersion int
	err = tx.QueryRowContext(ctx,
		`SELECT l.id, l.creation_brief_id, l.creation_brief_version
		 FROM creation_article_links l JOIN article_drafts d ON d.brief_id=l.article_brief_id WHERE d.id=?`,
		draftID).Scan(&linkID, &creationBriefID, &briefVersion)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%w: 修订缺少持久文章桥接（新契约文章才进行 durable AI 修订）", ErrInvalidEditorialState)
		}
		return nil, err
	}
	input.CreationBriefID = creationBriefID
	input.BriefVersion = briefVersion

	// 2) base ClaimMap（原映射身份）。
	entries, err := listClaimMapTx(ctx, tx, draftID, baseRevisionID)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("%w: base revision 缺少 ClaimMap，不能进行 claim-aware AI 修订", ErrInvalidEditorialState)
	}
	input.ExistingClaimMap = entries

	// 3) durable Claim/Style 审核反馈（至少一条，否则同步路径同样会拒绝）。
	for _, q := range []struct {
		query string
	}{
		{`SELECT issues_json FROM claim_reviews WHERE work_revision_id=? AND origin_job_id!='' ORDER BY created_at DESC,id DESC LIMIT 1`},
		{`SELECT issues_json FROM article_reviews WHERE revision_id=? AND kind='style' AND origin_job_id!='' ORDER BY created_at DESC,id DESC LIMIT 1`},
	} {
		var issuesJSON string
		err := tx.QueryRowContext(ctx, q.query, baseRevisionID).Scan(&issuesJSON)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		if issuesJSON != "" {
			var issues []string
			if err := json.Unmarshal([]byte(issuesJSON), &issues); err != nil {
				return nil, fmt.Errorf("解析审校反馈: %w", err)
			}
			input.ReviewFeedback = append(input.ReviewFeedback, issues...)
		}
	}
	if len(input.ReviewFeedback) == 0 {
		return nil, fmt.Errorf("%w: 该修订没有可供处理的 durable 审校反馈", ErrInvalidEditorialState)
	}

	// 4) 确认 Brief 精确版本：OwnerClaim/提纲/风格/篇幅 + 原授权 Curator 材料
	//（按 material plan selected IDs 保序筛选，与 R19 写作同一授权范围）。
	var outline, style, ownerClaim, snapshotJSON, materialPlan string
	var targetLength sql.NullInt64
	err = tx.QueryRowContext(ctx,
		`SELECT outline,COALESCE(style,''),owner_claim,curator_input_snapshot_json,target_length,material_plan_json
		 FROM creation_brief_revisions WHERE brief_id=? AND version=?`,
		creationBriefID, briefVersion).Scan(&outline, &style, &ownerClaim, &snapshotJSON, &targetLength, &materialPlan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: confirmed Brief revision missing", ErrInvalidEditorialState)
	}
	if err != nil {
		return nil, err
	}
	// 受众以 proposal 快照为准（与 R19 写作输入一致）。
	var audience string
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(audience,'') FROM creation_proposals WHERE id=(SELECT creation_proposal_id FROM creation_briefs WHERE id=?)`, creationBriefID).Scan(&audience); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	input.OwnerClaim, input.Outline, input.Style, input.Audience = ownerClaim, outline, style, audience
	if targetLength.Valid {
		v := int(targetLength.Int64)
		input.TargetLength = &v
	}
	selected, _, ok := parseMaterialPlan(materialPlan)
	if !ok || len(selected) == 0 {
		return nil, fmt.Errorf("%w: selected materials required", ErrInvalidEditorialState)
	}
	var snap struct {
		Provider  string                     `json:"provider"`
		Materials []provider.ArticleMaterial `json:"materials"`
	}
	if err := json.Unmarshal([]byte(snapshotJSON), &snap); err != nil {
		return nil, fmt.Errorf("%w: 冻结 Curator 材料快照不可解析: %w", ErrInvalidEditorialState, err)
	}
	byID := make(map[string]provider.ArticleMaterial, len(snap.Materials))
	for _, m := range snap.Materials {
		byID[m.KeyPointID] = m
	}
	for _, id := range selected {
		m, found := byID[id]
		if !found {
			return nil, fmt.Errorf("%w: 选中材料 %s 不在确认快照内", ErrInvalidEditorialState, id)
		}
		input.Materials = append(input.Materials, m)
	}
	// R23：冻结 OwnerNotes（与写作同链：proposal → ideation session → selections）。
	var proposalID string
	err = tx.QueryRowContext(ctx,
		`SELECT creation_proposal_id FROM creation_briefs WHERE id=?`, creationBriefID).Scan(&proposalID)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && strings.TrimSpace(proposalID) == "") {
		return nil, fmt.Errorf("%w: confirmed Brief 缺少所属 proposal", ErrInvalidEditorialState)
	}
	if err != nil {
		return nil, err
	}
	ownerNotes, err := freezeOwnerNotesForProposalTx(ctx, tx, proposalID)
	if err != nil {
		return nil, err
	}
	input.OwnerNotes = ownerNotes

	// 5) 冻结 Writer 配置（R04）：与 R19 写作同池。
	var prov, model string
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(writer_provider,''),COALESCE(writer_model,'') FROM settings WHERE id=1`).Scan(&prov, &model); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if prov == "" {
		prov = "groq"
	}
	input.Provider = prov
	input.Model = provider.EffectiveModel(prov, model, string(models.JobClaimWriting))
	input.PromptVersion = provider.ClaimRevisionWriterPromptVersion
	return input, nil
}

// SaveClaimRevisionOutput 原子保存一次 durable AI 修订（R21）：
//   - 以冻结输入校验输出（完整正文 + 完整 ClaimMap + 材料成员身份，与 R19 同契约）；
//   - CAS：draft.current_revision_id 仍等于 base revision，Owner 已产生新修订时
//     返回 ErrConflict，不覆盖；
//   - 单事务写新 immutable revision（origin_job_id=任务）+ canonical claim_maps +
//     claim_map_entries + current 指针 + job complete result；
//   - origin_job 幂等：重放校验输出与持久 revision 一致后补齐 result，不产生第二条。
//
// provenance 记录实际调用身份：providerName/modelName 为空时回退冻结配置值。
func (s *Store) SaveClaimRevisionOutput(ctx context.Context, jobID string, result *provider.ClaimAwareWritingResult, providerName, modelName string) (*models.ArticleRevision, error) {
	if result == nil {
		return nil, fmt.Errorf("%w: AI 修订输出为空", ErrInvalidEditorialState)
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	// 任务身份与冻结输入。
	var jobType, snapshot string
	err = tx.QueryRowContext(ctx, `SELECT job_type, COALESCE(input_snapshot_json,'') FROM processing_jobs WHERE id=?`, jobID).Scan(&jobType, &snapshot)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: revision job missing", ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	if jobType != string(models.JobClaimRevision) {
		return nil, fmt.Errorf("%w: 任务类型 %s 与 AI 修订不符", ErrInvalidEditorialState, jobType)
	}
	input := &RevisionWritingTaskInput{}
	if err := json.Unmarshal([]byte(snapshot), input); err != nil || input.BaseRevisionID == "" || input.Kind != "ai_revision" {
		return nil, fmt.Errorf("%w: AI 修订任务缺少冻结输入快照", ErrInvalidEditorialState)
	}
	if strings.TrimSpace(providerName) == "" {
		providerName = input.Provider
	}
	if strings.TrimSpace(modelName) == "" {
		modelName = input.Model
	}
	if strings.TrimSpace(result.Title) == "" || strings.TrimSpace(result.Markdown) == "" || len(result.ClaimMap) == 0 {
		return nil, fmt.Errorf("%w: AI 修订必须返回非空标题、正文与完整 ClaimMap", ErrInvalidEditorialState)
	}
	// store 侧再次校验（不信任重放）：与 provider/queue 同一份专用修订校验
	//（ValidateClaimMap + 新正文唯一 excerpt + 映射无重复 + 保留片段身份完整）。
	req := provider.ClaimAwareWritingRequest{
		Title: input.Title, Audience: input.Audience, Outline: input.Outline,
		Style: input.Style, SourceAttribution: "轻量", ConfirmedClaim: input.OwnerClaim,
		TargetLength: input.TargetLength, Materials: input.Materials,
		OwnerNotes:       input.OwnerNotes,
		ExistingMarkdown: input.ExistingMarkdown, RevisionFeedback: input.ReviewFeedback,
		ExistingClaimMap: convertEntriesToProvider(input.ExistingClaimMap),
	}
	if err := provider.ValidateClaimRevisionMap(result, req); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidEditorialState, err)
	}

	// origin-job 幂等重放：同任务已落 revision 时校验 title/markdown/ClaimMap 完整
	// 身份集合一致（excerpt/kind/materials/source/citations），任何漂移都拒绝。
	var existingID, existingTitle, existingMarkdown string
	err = tx.QueryRowContext(ctx,
		`SELECT id,title,markdown FROM article_revisions WHERE draft_id=? AND origin_job_id=?`,
		input.DraftID, jobID).Scan(&existingID, &existingTitle, &existingMarkdown)
	if err == nil {
		if existingTitle != result.Title || existingMarkdown != result.Markdown {
			return nil, fmt.Errorf("%w: 重放输出与持久修订不一致", ErrInvalidEditorialState)
		}
		rows, err := tx.QueryContext(ctx,
			`SELECT excerpt, claim_kind, material_ids_json, source_title, citation_refs_json
			 FROM claim_map_entries WHERE draft_id=? AND revision_id=? ORDER BY excerpt`, input.DraftID, existingID)
		if err != nil {
			return nil, err
		}
		persisted := map[string]models.ClaimMapEntry{}
		persistedRows := 0
		for rows.Next() {
			var e models.ClaimMapEntry
			var materials, citations string
			if err := rows.Scan(&e.Excerpt, &e.ClaimKind, &materials, &e.SourceTitle, &citations); err != nil {
				rows.Close()
				return nil, err
			}
			if err := json.Unmarshal([]byte(materials), &e.MaterialIDs); err != nil {
				rows.Close()
				return nil, fmt.Errorf("解析持久 ClaimMap 材料 ID: %w", err)
			}
			if err := json.Unmarshal([]byte(citations), &e.CitationRefs); err != nil {
				rows.Close()
				return nil, fmt.Errorf("解析持久 ClaimMap 引用: %w", err)
			}
			if _, dup := persisted[e.Excerpt]; dup {
				rows.Close()
				return nil, fmt.Errorf("%w: 持久 ClaimMap 存在重复 excerpt %q（身份歧义）", ErrInvalidEditorialState, e.Excerpt)
			}
			persisted[e.Excerpt] = e
			persistedRows++
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
		if persistedRows != len(result.ClaimMap) {
			return nil, fmt.Errorf("%w: 重放 ClaimMap 条数与持久不一致", ErrInvalidEditorialState)
		}
		// canonical claim_maps 投影必须与 result 同数量且身份一致（owner_claim 不属于 result，不比较）。
		canonicalRows, err := listCanonicalClaimMapsTx(ctx, tx, existingID)
		if err != nil {
			return nil, err
		}
		if len(canonicalRows) != len(result.ClaimMap) {
			return nil, fmt.Errorf("%w: 重放 canonical ClaimMap 条数与持久不一致", ErrInvalidEditorialState)
		}
		canonicalByExcerpt := make(map[string]canonicalClaimMap, len(canonicalRows))
		for _, row := range canonicalRows {
			if _, dup := canonicalByExcerpt[row.excerpt]; dup {
				return nil, fmt.Errorf("%w: 持久 canonical ClaimMap 存在重复 excerpt %q", ErrInvalidEditorialState, row.excerpt)
			}
			canonicalByExcerpt[row.excerpt] = row
		}
		for _, e := range result.ClaimMap {
			row, found := canonicalByExcerpt[e.Excerpt]
			if !found {
				return nil, fmt.Errorf("%w: canonical ClaimMap excerpt %q 不在持久记录", ErrInvalidEditorialState, e.Excerpt)
			}
			// canonical 投影无 source_title 列，比较 kind/materials/citations。
			if row.claimKind != e.ClaimKind ||
				!sameStringIdentity(row.materials, e.MaterialIDs) ||
				!sameStringIdentity(row.citations, e.CitationRefs) {
				return nil, fmt.Errorf("%w: canonical ClaimMap excerpt %q 身份与 result 不一致", ErrInvalidEditorialState, e.Excerpt)
			}
		}
		for _, e := range result.ClaimMap {
			p, found := persisted[e.Excerpt]
			if !found {
				return nil, fmt.Errorf("%w: 重放 ClaimMap excerpt %q 不在持久记录", ErrInvalidEditorialState, e.Excerpt)
			}
			if p.ClaimKind != e.ClaimKind || p.SourceTitle != e.SourceTitle ||
				!sameStringIdentity(p.MaterialIDs, e.MaterialIDs) ||
				!sameStringIdentity(p.CitationRefs, e.CitationRefs) {
				return nil, fmt.Errorf("%w: 重放 ClaimMap excerpt %q 身份与持久不一致", ErrInvalidEditorialState, e.Excerpt)
			}
		}
		if err := confirmRevisionJobResultTx(ctx, tx, jobID, input, result, providerName, modelName); err != nil {
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return s.GetArticleRevision(ctx, existingID)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	// CAS：base 仍为 current 才可保存；Owner 已产生新修订时不覆盖。
	var currentID string
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(current_revision_id,'') FROM article_drafts WHERE id=?`, input.DraftID).Scan(&currentID); err != nil {
		return nil, err
	}
	if currentID != input.BaseRevisionID {
		return nil, fmt.Errorf("%w: base 修订已过期（current=%s base=%s），AI 修订不覆盖", ErrConflict, currentID, input.BaseRevisionID)
	}

	var version int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(version),0)+1 FROM article_revisions WHERE draft_id=?`, input.DraftID).Scan(&version); err != nil {
		return nil, err
	}
	revision := &models.ArticleRevision{
		ID: uuid.NewString(), DraftID: input.DraftID, Version: version, OriginJobID: jobID,
		Title: result.Title, Markdown: result.Markdown, Origin: "ai_edit",
		Provider: strPtr(providerName), Model: strPtr(modelName), PromptVersion: strPtr(input.PromptVersion),
	}
	res, err := tx.ExecContext(ctx,
		`INSERT INTO article_revisions (id,draft_id,version,origin_job_id,title,markdown,origin,provider,model,prompt_version)
		 VALUES (?,?,?,?,?,?,?,?,?,?)`,
		revision.ID, revision.DraftID, revision.Version, revision.OriginJobID, revision.Title,
		revision.Markdown, revision.Origin, revision.Provider, revision.Model, revision.PromptVersion)
	if err != nil {
		return nil, err
	}
	if n, err := res.RowsAffected(); err != nil {
		return nil, err
	} else if n != 1 {
		return nil, fmt.Errorf("%w: AI 修订插入未生效", ErrInvalidEditorialState)
	}
	// SQL 级守卫 CAS：并发下 Owner 抢先推进 current 时 UPDATE 失效 → 回滚无孤儿。
	casRes, err := tx.ExecContext(ctx,
		`UPDATE article_drafts SET current_revision_id=?,status='reviewing',updated_at=datetime('now') WHERE id=? AND current_revision_id=?`,
		revision.ID, input.DraftID, input.BaseRevisionID)
	if err != nil {
		return nil, err
	}
	if n, err := casRes.RowsAffected(); err != nil {
		return nil, err
	} else if n != 1 {
		return nil, fmt.Errorf("%w: base 修订已过期，AI 修订不覆盖", ErrConflict)
	}
	for _, e := range result.ClaimMap {
		materials, err := json.Marshal(e.MaterialIDs)
		if err != nil {
			return nil, fmt.Errorf("序列化 ClaimMap 材料: %w", err)
		}
		citations, err := json.Marshal(e.CitationRefs)
		if err != nil {
			return nil, fmt.Errorf("序列化 ClaimMap 引用: %w", err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO claim_maps (id,work_revision_id,claim_kind,excerpt,keypoint_ids_json,owner_claim,verified_fact_source_ids_json) VALUES (?,?,?,?,?,?,?)`,
			uuid.NewString(), revision.ID, e.ClaimKind, e.Excerpt, string(materials), input.OwnerClaim, string(citations)); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO claim_map_entries (id,draft_id,revision_id,excerpt,claim_kind,material_ids_json,source_title,citation_refs_json) VALUES (?,?,?,?,?,?,?,?)`,
			uuid.NewString(), input.DraftID, revision.ID, e.Excerpt, e.ClaimKind, string(materials), e.SourceTitle, string(citations)); err != nil {
			return nil, err
		}
	}
	if err := confirmRevisionJobResultTx(ctx, tx, jobID, input, result, providerName, modelName); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return revision, nil
}

// sameStringIdentity 私有字符串集合比较（顺序无关）。
func sameStringIdentity(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	counts := make(map[string]int, len(a))
	for _, v := range a {
		counts[v]++
	}
	for _, v := range b {
		if counts[v] == 0 {
			return false
		}
		counts[v]--
	}
	return true
}

// confirmRevisionJobResultTx 确认 AI 修订任务的 complete result（origin-job 幂等）。
// payload 记录实际调用身份（providerName/modelName），不冒充冻结配置值。
func confirmRevisionJobResultTx(ctx context.Context, tx *sql.Tx, jobID string, input *RevisionWritingTaskInput, result *provider.ClaimAwareWritingResult, providerName, modelName string) error {
	revisionID := ""
	if err := tx.QueryRowContext(ctx, `SELECT id FROM article_revisions WHERE draft_id=? AND origin_job_id=?`, input.DraftID, jobID).Scan(&revisionID); err != nil {
		return err
	}
	payload, err := json.Marshal(map[string]any{
		"base_revision_id": input.BaseRevisionID, "draft_id": input.DraftID,
		"revision_id": revisionID, "title": result.Title, "claim_count": len(result.ClaimMap),
		"provider": providerName, "model": modelName, "prompt_version": input.PromptVersion,
		"intent_id": claimRevisionIntentID(input.BaseRevisionID), "origin_job_id": jobID,
		"url": "/workbench/drafts/" + input.DraftID,
	})
	if err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx,
		`UPDATE processing_jobs SET result_json=?,result_state='complete',updated_at=datetime('now')
		 WHERE id=? AND status IN ('queued','running') AND result_state != ?`,
		string(payload), jobID, models.JobResultComplete)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		var state string
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(result_state,'') FROM processing_jobs WHERE id=?`, jobID).Scan(&state); err != nil {
			return err
		}
		if state != models.JobResultComplete {
			return fmt.Errorf("%w: 任务 %s 不在接受结果的状态", ErrInvalidEditorialState, jobID)
		}
	}
	return nil
}

// ClaimRevisionJobForRevision 读取某 base revision 映射的 durable AI 修订任务。
func (s *Store) ClaimRevisionJobForRevision(ctx context.Context, baseRevisionID string) (*models.ProcessingJob, error) {
	return s.getReviewIntentJob(ctx, claimRevisionIntentID(baseRevisionID))
}
