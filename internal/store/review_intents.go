// review_intents.go 独立主张/风格审校的持久任务契约（R20 / C11 / ADR-0024 §6）。
//
// 入队即冻结：精确 revision 的 title/markdown、ClaimMap、CreationBrief 精确版本的
// confirmed claim、完整授权 ArticleMaterial 身份、画像风格约束、provider/model/prompt。
// worker 只消费该强类型快照，不重读可变正文/Brief/Profile。
//
// 幂等与恢复：revision_review_intents 以 (revision, kind) 为身份持久映射到唯一任务；
// queued/running/succeeded 重复点击复用同一 job；failed 点击真实重试同一 job
// （reset queued，保留冻结输入/配置/checkpoint）；新 revision 是新 intent。
// Provider 响应先写 checkpoint（queue 层），业务落库由 SaveClaimReviewOutput /
// SaveStyleReviewOutput 在单事务内原子完成：审校行 + job complete result，
// 以 origin_job_id 幂等，重放不产生第二条审校。
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

// Review kinds（revision_review_intents.kind）。
const (
	ReviewKindClaim = "claim"
	ReviewKindStyle = "style"
)

// ReviewTaskInput 是入队时冻结的强类型完整审校输入（claim 与 style 共用一套身份，
// 按 Kind 消费各自字段）。运行时只读该快照。
type ReviewTaskInput struct {
	Kind       string `json:"kind"`
	RevisionID string `json:"revision_id"`
	DraftID    string `json:"draft_id"`
	ProfileID  string `json:"profile_id"`

	// ClaimReview 冻结输入：精确正文 + ClaimMap + 确认主张 + 授权材料。
	Title           string                 `json:"title"`
	Markdown        string                 `json:"markdown"`
	ClaimMap        []models.ClaimMapEntry `json:"claim_map"`
	ConfirmedClaim  string                 `json:"confirmed_claim"`
	AuthorizedIDs   []string               `json:"authorized_ids"`
	CreationBriefID string                 `json:"creation_brief_id"`
	BriefVersion    int                    `json:"brief_version"`

	// StyleReview 冻结输入：画像风格约束与目标篇幅。
	TargetAudience string `json:"target_audience"`
	Voice          string `json:"voice"`
	StyleGuide     string `json:"style_guide"`
	TargetLength   *int   `json:"target_length"`

	// 授权材料身份（动态来源策略检查用：publication + send policy）。
	Materials []provider.ArticleMaterial `json:"materials"`

	Provider      string `json:"provider"`
	Model         string `json:"model"`
	PromptVersion string `json:"prompt_version"`
}

// reviewIntentID 审校意图身份：同一精确修订的同一种审校永远对应同一个持久任务。
func reviewIntentID(kind, revisionID string) string {
	return fmt.Sprintf("%s_review:%s", kind, revisionID)
}

// IsNewContractRevision 以持久 bridge 判定新契约文章：
// revision → draft.brief_id → creation_article_links.article_brief_id 必须存在。
func (s *Store) IsNewContractRevision(ctx context.Context, revisionID string) (bool, error) {
	var n int
	err := s.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM creation_article_links l
		 JOIN article_revisions r ON r.id=?
		 JOIN article_drafts d ON d.id=r.draft_id AND d.brief_id=l.article_brief_id`, revisionID).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// EnqueueRevisionReview 入队一种独立审校（claim|style）为持久任务（R20）。
// 同一 (revision, kind) 的 queued/running/succeeded 重复点击复用同一 job；
// failed 点击真实重试同一 job（reset queued，保留冻结输入/配置/checkpoint）；
// 新 revision 是新 intent。整个读取-冻结-写入在同一 DB 事务中完成。
func (s *Store) EnqueueRevisionReview(ctx context.Context, revisionID, kind string) (*models.ProcessingJob, error) {
	if kind != ReviewKindClaim && kind != ReviewKindStyle {
		return nil, fmt.Errorf("%w: 未知审校类型 %q", ErrInvalidEditorialState, kind)
	}
	if strings.TrimSpace(revisionID) == "" {
		return nil, fmt.Errorf("%w: revision_id required", ErrInvalidEditorialState)
	}
	job, err := s.enqueueRevisionReviewOnce(ctx, revisionID, kind)
	if err == nil {
		return job, nil
	}
	// 并发竞态：意图唯一约束冲突 → 复用已存在的任务，不产生第二个 job。
	if isUniqueConstraintErr(err) {
		return s.getReviewIntentJob(ctx, reviewIntentID(kind, revisionID))
	}
	return nil, err
}

// getReviewIntentJob 由意图身份读取复用的持久任务。
func (s *Store) getReviewIntentJob(ctx context.Context, intentID string) (*models.ProcessingJob, error) {
	var jobID string
	err := s.DB.QueryRowContext(ctx, `SELECT job_id FROM revision_review_intents WHERE intent_id=?`, intentID).Scan(&jobID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return s.GetJob(ctx, jobID)
}

// ReviewJobForRevision 读取某修订某种审校当前映射的持久任务（无则 ErrNotFound）。
func (s *Store) ReviewJobForRevision(ctx context.Context, revisionID, kind string) (*models.ProcessingJob, error) {
	return s.getReviewIntentJob(ctx, reviewIntentID(kind, revisionID))
}

func (s *Store) enqueueRevisionReviewOnce(ctx context.Context, revisionID, kind string) (*models.ProcessingJob, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	intentID := reviewIntentID(kind, revisionID)
	// 已有意图：succeeded/queued/running 复用同一 job；failed 真实重试同 job。
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
				// 失败重试必须持久生效：提交 reset（同事务冻结/幂等读取的写入），
				// 否则 defer Rollback 会撤销 queued 状态。
				if cerr := tx.Commit(); cerr != nil {
					return nil, cerr
				}
				job.Status = models.StatusQueued
				job.LastError = nil
				return job, nil
			}
			// 并发下已被其他请求重置：回读当前状态后按非 failed 语义返回。
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

	input, err := freezeReviewTaskInputTx(ctx, tx, revisionID, kind)
	if err != nil {
		return nil, err
	}
	inputJSON, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("冻结审校输入快照: %w", err)
	}

	jobType := models.JobClaimReview
	if kind == ReviewKindStyle {
		jobType = models.JobStyleReview
	}
	jobID := uuid.NewString()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO processing_jobs
		   (id, source_type, source_id, job_type, status, is_automated,
		    intent_id, input_snapshot_json, config_version, configured_provider, configured_model)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		jobID, string(models.SourceEpisode), input.DraftID, string(jobType), string(models.StatusQueued), 0,
		intentID, string(inputJSON), input.PromptVersion, input.Provider, input.Model); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO revision_review_intents (intent_id, job_id, revision_id, kind) VALUES (?,?,?,?)`,
		intentID, jobID, revisionID, kind); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &models.ProcessingJob{ID: jobID, SourceType: models.SourceEpisode, SourceID: input.DraftID, JobType: jobType, Status: models.StatusQueued}, nil
}

// freezeReviewTaskInputTx 在事务内读取并冻结一次审校的完整输入。
func freezeReviewTaskInputTx(ctx context.Context, tx *sql.Tx, revisionID, kind string) (*ReviewTaskInput, error) {
	// 1) 精确 revision：冻结 title/markdown；draft/profile 供授权与风格约束使用。
	var draftID, title, markdown string
	err := tx.QueryRowContext(ctx, `SELECT draft_id,title,markdown FROM article_revisions WHERE id=?`, revisionID).Scan(&draftID, &title, &markdown)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: article revision missing", ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(markdown) == "" {
		return nil, fmt.Errorf("%w: 修订正文为空，不能审校", ErrInvalidEditorialState)
	}
	var profileID, briefID string
	if err := tx.QueryRowContext(ctx, `SELECT editorial_profile_id,COALESCE(brief_id,'') FROM article_drafts WHERE id=?`, draftID).Scan(&profileID, &briefID); err != nil {
		return nil, fmt.Errorf("%w: article draft missing", ErrNotFound)
	}
	input := &ReviewTaskInput{
		Kind: kind, RevisionID: revisionID, DraftID: draftID, ProfileID: profileID,
		Title: title, Markdown: markdown,
	}

	// 2) 持久 bridge：新契约判定 + CreationBrief 精确版本/confirmed claim/授权材料。
	var linkID, creationBriefID string
	var briefVersion int
	err = tx.QueryRowContext(ctx,
		`SELECT l.id, l.creation_brief_id, l.creation_brief_version
		 FROM creation_article_links l JOIN article_drafts d ON d.brief_id=l.article_brief_id WHERE d.id=?`,
		draftID).Scan(&linkID, &creationBriefID, &briefVersion)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	isNewContract := linkID != ""
	if kind == ReviewKindClaim && !isNewContract {
		return nil, fmt.Errorf("%w: 修订缺少持久文章桥接（新契约文章才进行独立主张审校）", ErrInvalidEditorialState)
	}

	// 3) 冻结授权材料：来自确认 Brief revision 的不可变 Curator 快照，
	// 只取 ClaimMap 授权引用的材料（不重读可变 keypoint_index）。
	if isNewContract {
		input.CreationBriefID = creationBriefID
		input.BriefVersion = briefVersion
		var ownerClaim, snapshotJSON string
		err = tx.QueryRowContext(ctx,
			`SELECT owner_claim, curator_input_snapshot_json FROM creation_brief_revisions
			 WHERE brief_id=? AND version=?`, creationBriefID, briefVersion).Scan(&ownerClaim, &snapshotJSON)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%w: confirmed Brief revision missing", ErrInvalidEditorialState)
		}
		if err != nil {
			return nil, err
		}
		if kind == ReviewKindClaim {
			input.ConfirmedClaim = ownerClaim
		}
		var snap struct {
			Provider  string                     `json:"provider"`
			Materials []provider.ArticleMaterial `json:"materials"`
		}
		if err := json.Unmarshal([]byte(snapshotJSON), &snap); err != nil {
			return nil, fmt.Errorf("%w: 冻结 Curator 材料快照不可解析: %w", ErrInvalidEditorialState, err)
		}
		input.Materials = snap.Materials
	}

	switch kind {
	case ReviewKindClaim:
		// 冻结 ClaimMap（授权材料 = ClaimMap 条目引用的材料 ID）。
		entries, err := listClaimMapTx(ctx, tx, draftID, revisionID)
		if err != nil {
			return nil, err
		}
		if len(entries) == 0 {
			return nil, fmt.Errorf("%w: 修订缺少 ClaimMap，不能进行独立主张审校", ErrInvalidEditorialState)
		}
		input.ClaimMap = entries
		authorized := map[string]bool{}
		for _, e := range entries {
			for _, id := range e.MaterialIDs {
				authorized[id] = true
			}
		}
		for id := range authorized {
			input.AuthorizedIDs = append(input.AuthorizedIDs, id)
		}
		sort.Strings(input.AuthorizedIDs)
		// ClaimMap 引用的每个材料 ID 都必须在确认快照内：部分缺失说明
		// ClaimMap 与授权快照不一致，不得静默降级为部分授权。
		snapshotIDs := make(map[string]bool, len(input.Materials))
		for _, m := range input.Materials {
			snapshotIDs[m.KeyPointID] = true
		}
		for _, id := range input.AuthorizedIDs {
			if !snapshotIDs[id] {
				return nil, fmt.Errorf("%w: ClaimMap 引用材料 %s 不在确认快照内，不能进行独立主张审校", ErrInvalidEditorialState, id)
			}
		}
		// 冻结材料过滤为授权集合（审校只证明授权范围内表达）。
		filtered := make([]provider.ArticleMaterial, 0, len(input.Materials))
		for _, m := range input.Materials {
			if authorized[m.KeyPointID] {
				filtered = append(filtered, m)
			}
		}
		if len(filtered) == 0 {
			return nil, fmt.Errorf("%w: 授权材料不在确认快照内，不能进行独立主张审校", ErrInvalidEditorialState)
		}
		input.Materials = filtered
		// 冻结 claim_review 配置（R04）：analysis 角色池（判定与生成分离提示词）。
		var prov, model string
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(analysis_provider,''),COALESCE(analysis_model,'') FROM settings WHERE id=1`).Scan(&prov, &model); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		if prov == "" {
			prov = "groq"
		}
		input.Provider = prov
		input.Model = provider.EffectiveModel(prov, model, string(models.JobClaimReview))
		input.PromptVersion = provider.ClaimReviewerPromptVersion

	case ReviewKindStyle:
		// 冻结画像风格约束（运行时不重读可变 Profile）。
		var audience, voice, guide string
		if err := tx.QueryRowContext(ctx,
			`SELECT COALESCE(target_audience,''),COALESCE(voice,''),COALESCE(style_guide,'') FROM editorial_profiles WHERE id=?`,
			profileID).Scan(&audience, &voice, &guide); err != nil {
			return nil, fmt.Errorf("%w: editorial profile missing", ErrNotFound)
		}
		input.TargetAudience, input.Voice, input.StyleGuide = audience, voice, guide
		// 新契约：材料只取该精确 revision ClaimMap 实际引用的 ID（只检查文章实际
		// 使用来源，不因 Curator 未选/未用候选被归档而误阻断）；无 ClaimMap 拒绝。
		if isNewContract {
			entries, err := listClaimMapTx(ctx, tx, draftID, revisionID)
			if err != nil {
				return nil, err
			}
			if len(entries) == 0 {
				return nil, fmt.Errorf("%w: 修订缺少 ClaimMap，不能进行独立风格审校", ErrInvalidEditorialState)
			}
			used := map[string]bool{}
			for _, e := range entries {
				for _, id := range e.MaterialIDs {
					used[id] = true
				}
			}
			// ClaimMap 引用的每个材料 ID 都必须在确认快照内：缺失一个也拒绝，
			// 不得部分过滤（owner/synthesis 无 material ID 合法，不进入 used）。
			snapshotIDs := make(map[string]bool, len(input.Materials))
			for _, m := range input.Materials {
				snapshotIDs[m.KeyPointID] = true
			}
			for id := range used {
				if !snapshotIDs[id] {
					return nil, fmt.Errorf("%w: ClaimMap 引用材料 %s 不在确认快照内，不能进行独立风格审校", ErrInvalidEditorialState, id)
				}
			}
			filtered := make([]provider.ArticleMaterial, 0, len(input.Materials))
			for _, m := range input.Materials {
				if used[m.KeyPointID] {
					filtered = append(filtered, m)
				}
			}
			input.Materials = filtered
		}
		// 目标篇幅：新契约取确认 Brief revision 的精确值；旧文章兼容读 article_briefs。
		if isNewContract {
			var tl sql.NullInt64
			if err := tx.QueryRowContext(ctx, `SELECT target_length FROM creation_brief_revisions WHERE brief_id=? AND version=?`, creationBriefID, briefVersion).Scan(&tl); err != nil && !errors.Is(err, sql.ErrNoRows) {
				return nil, err
			}
			if tl.Valid {
				v := int(tl.Int64)
				input.TargetLength = &v
			}
		} else {
			var tl sql.NullInt64
			if err := tx.QueryRowContext(ctx, `SELECT target_length FROM article_briefs WHERE id=?`, briefID).Scan(&tl); err != nil && !errors.Is(err, sql.ErrNoRows) {
				return nil, err
			}
			if tl.Valid {
				v := int(tl.Int64)
				input.TargetLength = &v
			}
		}
		// 冻结 style_review 配置（R04）：style_editor 角色设置。
		var prov, model string
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(style_editor_provider,''),COALESCE(style_editor_model,'') FROM settings WHERE id=1`).Scan(&prov, &model); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		if prov == "" {
			prov = "groq"
		}
		input.Provider = prov
		input.Model = provider.EffectiveModel(prov, model, string(models.JobStyleReview))
		input.PromptVersion = provider.StyleEditorPromptVersion
	}
	return input, nil
}

// listClaimMapTx 在事务内读取一个修订的全部 ClaimMap 条目。
func listClaimMapTx(ctx context.Context, tx *sql.Tx, draftID, revisionID string) ([]models.ClaimMapEntry, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT excerpt, claim_kind, material_ids_json, source_title, citation_refs_json
		 FROM claim_map_entries WHERE draft_id=? AND revision_id=? ORDER BY created_at`, draftID, revisionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.ClaimMapEntry
	for rows.Next() {
		var e models.ClaimMapEntry
		var materials, citations string
		if err := rows.Scan(&e.Excerpt, &e.ClaimKind, &materials, &e.SourceTitle, &citations); err != nil {
			return nil, err
		}
		// JSON 损坏不静默降级（与 ListClaimMap 同契约）。
		if err := json.Unmarshal([]byte(materials), &e.MaterialIDs); err != nil {
			return nil, fmt.Errorf("解析 ClaimMap 材料 ID（revision %s excerpt %q）: %w", revisionID, e.Excerpt, err)
		}
		if err := json.Unmarshal([]byte(citations), &e.CitationRefs); err != nil {
			return nil, fmt.Errorf("解析 ClaimMap 引用（revision %s excerpt %q）: %w", revisionID, e.Excerpt, err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ---- 审校结果原子落库 ----

// reviewOutputJobCols 校验并读取任务身份与冻结输入；返回 job 的 input 快照。
func (s *Store) reviewOutputInputTx(ctx context.Context, tx *sql.Tx, jobID string, wantKind string) (*ReviewTaskInput, error) {
	var jobType, snapshot string
	err := tx.QueryRowContext(ctx, `SELECT job_type, COALESCE(input_snapshot_json,'') FROM processing_jobs WHERE id=?`, jobID).Scan(&jobType, &snapshot)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: review job missing", ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	wantType := string(models.JobClaimReview)
	if wantKind == ReviewKindStyle {
		wantType = string(models.JobStyleReview)
	}
	if jobType != wantType {
		return nil, fmt.Errorf("%w: 任务类型 %s 与审校类型不符", ErrInvalidEditorialState, jobType)
	}
	input := &ReviewTaskInput{}
	if err := json.Unmarshal([]byte(snapshot), input); err != nil || input.RevisionID == "" {
		return nil, fmt.Errorf("%w: 审校任务缺少冻结输入快照", ErrInvalidEditorialState)
	}
	if input.Kind != wantKind {
		return nil, fmt.Errorf("%w: 审校输入类型不符", ErrInvalidEditorialState)
	}
	return input, nil
}

// confirmReviewJobResult 在事务内确认 job complete result（origin-job 幂等）。
// RowsAffected=0 时必须已 complete，否则拒绝（与 SaveClaimWritingOutput 同契约）。
func confirmReviewJobResultTx(ctx context.Context, tx *sql.Tx, jobID, revisionID, kind, status string) error {
	payload, err := json.Marshal(map[string]any{
		"revision_id": revisionID, "kind": kind, "status": status,
		"origin_job_id": jobID,
		"url":           "/workbench/drafts/revision/" + revisionID,
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

// SaveClaimReviewOutput 原子保存一次独立主张审校：以任务冻结输入校验结果
// （passed findings 空、failed findings 非空、issueKind 合法、excerpt 位于冻结正文、
// 材料 ID 不越权）后，单事务写入 claim_reviews（含 provider/model/prompt + origin_job_id）
// 并确认 job complete result。origin_job 幂等：重放返回已保存审校，不产生第二条。
// provenance 记录实际调用身份：providerName/modelName 为空时回退冻结配置值。
func (s *Store) SaveClaimReviewOutput(ctx context.Context, jobID string, result *provider.ClaimReviewResult, providerName, modelName string) (*models.ClaimReview, error) {
	if err := provider.ValidateClaimReviewResult(result); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidEditorialState, err)
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	input, err := s.reviewOutputInputTx(ctx, tx, jobID, ReviewKindClaim)
	if err != nil {
		return nil, err
	}
	req := &provider.ClaimReviewRequest{
		Markdown: input.Markdown, ClaimMap: convertEntriesToProvider(input.ClaimMap),
		ConfirmedClaim: input.ConfirmedClaim, AuthorizedIDs: input.AuthorizedIDs,
		Materials: input.Materials,
	}
	if err := provider.ValidateClaimReviewAgainstInput(result, req); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidEditorialState, err)
	}
	issues := claimFindingsToIssues(result.Findings)

	// 实际调用身份归一化：先于重放查询，保证所有返回路径 provenance 非空。
	if strings.TrimSpace(providerName) == "" {
		providerName = input.Provider
	}
	if strings.TrimSpace(modelName) == "" {
		modelName = input.Model
	}

	// origin-job 幂等：同任务重放必须命中同一行且状态一致；返回持久行的完整
	// provenance（不用调用参数伪造，也不得被同 revision 更新的其他行取代）。
	var existingID, existingStatus, existingIssues, existingProvider, existingModel, existingPrompt string
	err = tx.QueryRowContext(ctx,
		`SELECT id,status,issues_json,COALESCE(provider,''),COALESCE(model,''),COALESCE(prompt_version,'')
		 FROM claim_reviews WHERE work_revision_id=? AND origin_job_id=?`,
		input.RevisionID, jobID).Scan(&existingID, &existingStatus, &existingIssues, &existingProvider, &existingModel, &existingPrompt)
	switch {
	case err == nil:
		if existingStatus != result.Status || existingIssues != issues {
			return nil, fmt.Errorf("%w: 重放审校结果与持久记录不一致", ErrInvalidEditorialState)
		}
		if err := confirmReviewJobResultTx(ctx, tx, jobID, input.RevisionID, ReviewKindClaim, result.Status); err != nil {
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return &models.ClaimReview{
			ID: existingID, WorkRevisionID: input.RevisionID, Status: existingStatus,
			IssuesJSON: existingIssues, OriginJobID: jobID,
			Provider: strPtr(existingProvider), Model: strPtr(existingModel), PromptVersion: strPtr(existingPrompt),
		}, nil
	case errors.Is(err, sql.ErrNoRows):
		// 首次保存，继续。
	default:
		return nil, err
	}

	review := &models.ClaimReview{
		ID: uuid.NewString(), WorkRevisionID: input.RevisionID, Status: result.Status,
		IssuesJSON: issues, OriginJobID: jobID,
		Provider: strPtr(providerName), Model: strPtr(modelName), PromptVersion: strPtr(input.PromptVersion),
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO claim_reviews (id,work_revision_id,status,issues_json,provider,model,prompt_version,origin_job_id)
		 VALUES (?,?,?,?,?,?,?,?)`,
		review.ID, review.WorkRevisionID, review.Status, review.IssuesJSON,
		review.Provider, review.Model, review.PromptVersion, review.OriginJobID); err != nil {
		// intent 单任务 + (revision, origin_job_id) 唯一索引保证不会并发插入同一行；
		// 唯一冲突只能来自数据异常，直接报错（不在失败事务中继续回读）。
		return nil, err
	}
	if err := confirmReviewJobResultTx(ctx, tx, jobID, input.RevisionID, ReviewKindClaim, result.Status); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return review, nil
}

// SaveStyleReviewOutput 原子保存一次独立风格审校（durable 路径）：failed 必须有
// issues、passed 必须无 issues；单事务写入 article_reviews（kind=style，含完整
// provenance + origin_job_id）并确认 job complete result。origin_job 幂等。
// provenance 记录实际调用身份：providerName/modelName 为空时回退冻结配置值。
func (s *Store) SaveStyleReviewOutput(ctx context.Context, jobID string, result *provider.StyleReviewResult, providerName, modelName string) (*models.ArticleReview, error) {
	if result == nil || (result.Status != "passed" && result.Status != "failed") {
		return nil, fmt.Errorf("%w: 风格审校结论必须为 passed|failed", ErrInvalidEditorialState)
	}
	if result.Status == "passed" && len(result.Issues) > 0 {
		return nil, fmt.Errorf("%w: 通过的风格审校不能包含问题", ErrInvalidEditorialState)
	}
	if result.Status == "failed" && len(result.Issues) == 0 {
		return nil, fmt.Errorf("%w: failed 风格审校必须说明问题", ErrInvalidEditorialState)
	}
	encoded, err := json.Marshal(result.Issues)
	if err != nil {
		return nil, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	input, err := s.reviewOutputInputTx(ctx, tx, jobID, ReviewKindStyle)
	if err != nil {
		return nil, err
	}
	// 实际调用身份归一化：先于重放查询，保证所有返回路径 provenance 非空。
	if strings.TrimSpace(providerName) == "" {
		providerName = input.Provider
	}
	if strings.TrimSpace(modelName) == "" {
		modelName = input.Model
	}

	// origin-job 幂等：同任务重放必须命中同一行且状态一致；provenance 从持久行
	// 读取返回（不用调用参数伪造）。
	var existingID, existingStatus, existingIssues, existingProvider, existingModel, existingPrompt string
	err = tx.QueryRowContext(ctx,
		`SELECT id,status,issues_json,COALESCE(provider,''),COALESCE(model,''),COALESCE(prompt_version,'')
		 FROM article_reviews WHERE revision_id=? AND origin_job_id=?`,
		input.RevisionID, jobID).Scan(&existingID, &existingStatus, &existingIssues, &existingProvider, &existingModel, &existingPrompt)
	switch {
	case err == nil:
		if existingStatus != result.Status || existingIssues != string(encoded) {
			return nil, fmt.Errorf("%w: 重放审校结果与持久记录不一致", ErrInvalidEditorialState)
		}
		if err := confirmReviewJobResultTx(ctx, tx, jobID, input.RevisionID, ReviewKindStyle, result.Status); err != nil {
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return &models.ArticleReview{ID: existingID, RevisionID: input.RevisionID, Kind: ReviewKindStyle, Status: existingStatus, IssuesJSON: existingIssues, Provider: strPtr(existingProvider), Model: strPtr(existingModel), PromptVersion: strPtr(existingPrompt), OriginJobID: jobID}, nil
	case errors.Is(err, sql.ErrNoRows):
		// 首次保存，继续。
	default:
		return nil, err
	}

	review := &models.ArticleReview{
		ID: uuid.NewString(), RevisionID: input.RevisionID, Kind: ReviewKindStyle,
		Status: result.Status, IssuesJSON: string(encoded), OriginJobID: jobID,
		Provider: strPtr(providerName), Model: strPtr(modelName), PromptVersion: strPtr(input.PromptVersion),
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO article_reviews (id,revision_id,kind,status,issues_json,provider,model,prompt_version,origin_job_id)
		 VALUES (?,?,?,?,?,?,?,?,?)`,
		review.ID, review.RevisionID, review.Kind, review.Status, review.IssuesJSON,
		review.Provider, review.Model, review.PromptVersion, review.OriginJobID); err != nil {
		// intent 单任务 + (revision, origin_job_id) 唯一索引保证不会并发插入同一行；
		// 唯一冲突只能来自数据异常，直接报错（不在失败事务中继续回读）。
		return nil, err
	}
	if err := confirmReviewJobResultTx(ctx, tx, jobID, input.RevisionID, ReviewKindStyle, result.Status); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return review, nil
}

// ---- 交付就绪（R20 门禁）----

// ArticlePublicationReadiness 一个精确修订的交付就绪结论（区分新旧契约）。
type ArticlePublicationReadiness struct {
	RevisionID  string
	NewContract bool
	Ready       bool
	Issues      []string
}

// LatestStyleReview 返回某修订最新一条风格审校（无则 ErrNotFound）。
func (s *Store) LatestStyleReview(ctx context.Context, revisionID string) (*models.ArticleReview, error) {
	v := &models.ArticleReview{}
	var p, m, pv sql.NullString
	var cost sql.NullInt64
	err := s.DB.QueryRowContext(ctx,
		`SELECT id,revision_id,kind,status,issues_json,provider,model,prompt_version,cost_cents,COALESCE(origin_job_id,''),created_at
		 FROM article_reviews WHERE revision_id=? AND kind='style' ORDER BY created_at DESC,id DESC LIMIT 1`, revisionID).
		Scan(&v.ID, &v.RevisionID, &v.Kind, &v.Status, &v.IssuesJSON, &p, &m, &pv, &cost, &v.OriginJobID, &v.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if p.Valid {
		v.Provider = &p.String
	}
	if m.Valid {
		v.Model = &m.String
	}
	if pv.Valid {
		v.PromptVersion = &pv.String
	}
	if cost.Valid {
		x := cost.Int64
		v.CostCents = &x
	}
	return v, nil
}

// LatestDurableClaimReview 返回某修订最新一条持久任务产物主张审校（origin_job_id
// 非空；无则 ErrNotFound）。旧同步副本（证据审校拷贝）不参与新契约门禁。
func (s *Store) LatestDurableClaimReview(ctx context.Context, revisionID string) (*models.ClaimReview, error) {
	v := &models.ClaimReview{}
	var p, m, pv sql.NullString
	var cost sql.NullInt64
	err := s.DB.QueryRowContext(ctx, `SELECT id,work_revision_id,status,issues_json,provider,model,prompt_version,cost_cents,origin_job_id,created_at FROM claim_reviews WHERE work_revision_id=? AND origin_job_id!='' ORDER BY created_at DESC,id DESC LIMIT 1`, revisionID).Scan(&v.ID, &v.WorkRevisionID, &v.Status, &v.IssuesJSON, &p, &m, &pv, &cost, &v.OriginJobID, &v.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if p.Valid {
		v.Provider = &p.String
	}
	if m.Valid {
		v.Model = &m.String
	}
	if pv.Valid {
		v.PromptVersion = &pv.String
	}
	if cost.Valid {
		x := cost.Int64
		v.CostCents = &x
	}
	return v, nil
}

// hasAnyClaimReview / hasAnyStyleReview 用于区分“完全缺失”与“仅旧审校记录”。
func (s *Store) hasAnyClaimReview(ctx context.Context, revisionID string) (bool, error) {
	var n int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM claim_reviews WHERE work_revision_id=?`, revisionID).Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}

func (s *Store) hasAnyStyleReview(ctx context.Context, revisionID string) (bool, error) {
	var n int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM article_reviews WHERE revision_id=? AND kind='style'`, revisionID).Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}

// LatestDurableStyleReview 返回某修订最新一条持久任务产物风格审校（无则 ErrNotFound）。
func (s *Store) LatestDurableStyleReview(ctx context.Context, revisionID string) (*models.ArticleReview, error) {
	v := &models.ArticleReview{}
	var p, m, pv sql.NullString
	var cost sql.NullInt64
	err := s.DB.QueryRowContext(ctx,
		`SELECT id,revision_id,kind,status,issues_json,provider,model,prompt_version,cost_cents,origin_job_id,created_at
		 FROM article_reviews WHERE revision_id=? AND kind='style' AND origin_job_id!='' ORDER BY created_at DESC,id DESC LIMIT 1`, revisionID).
		Scan(&v.ID, &v.RevisionID, &v.Kind, &v.Status, &v.IssuesJSON, &p, &m, &pv, &cost, &v.OriginJobID, &v.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if p.Valid {
		v.Provider = &p.String
	}
	if m.Valid {
		v.Model = &m.String
	}
	if pv.Valid {
		v.PromptVersion = &pv.String
	}
	if cost.Valid {
		x := cost.Int64
		v.CostCents = &x
	}
	return v, nil
}
func provenanceComplete(r *models.ClaimReview) bool {
	return r.Provider != nil && strings.TrimSpace(*r.Provider) != "" &&
		r.Model != nil && strings.TrimSpace(*r.Model) != "" &&
		r.PromptVersion != nil && strings.TrimSpace(*r.PromptVersion) != ""
}

// styleProvenanceComplete 同上（style 行）。
func styleProvenanceComplete(r *models.ArticleReview) bool {
	return r.Provider != nil && strings.TrimSpace(*r.Provider) != "" &&
		r.Model != nil && strings.TrimSpace(*r.Model) != "" &&
		r.PromptVersion != nil && strings.TrimSpace(*r.PromptVersion) != ""
}

// EvaluateArticlePublicationReadiness 只读评估一个精确修订是否可交付（R20）：
//   - 新契约文章（持久 bridge 判定）：同一精确 revision 的最新 ClaimReview（持久任务
//     产物，origin_job_id 非空）与最新 StyleReview（持久任务产物）都必须 passed 且
//     携带完整 provider/model/prompt provenance。EvidenceReview 不能替代 ClaimReview；
//     缺失、failed、仅旧审校、其他 revision 的审校均不放行。
//   - 旧文章：沿用既有 evidence+style 兼容规则（IsRevisionReadyForPublication），
//     不破坏读取/导出。
func (s *Store) EvaluateArticlePublicationReadiness(ctx context.Context, revisionID string) (*ArticlePublicationReadiness, error) {
	out := &ArticlePublicationReadiness{RevisionID: revisionID}
	newContract, err := s.IsNewContractRevision(ctx, revisionID)
	if err != nil {
		return nil, err
	}
	out.NewContract = newContract
	if !newContract {
		ready, err := s.IsRevisionReadyForPublication(ctx, revisionID)
		if err != nil {
			return nil, err
		}
		out.Ready = ready
		if !ready {
			out.Issues = append(out.Issues, "当前修订尚未通过证据与风格审校（旧契约规则）")
		}
		return out, nil
	}

	// 新契约：ClaimReview 门禁（只认持久任务产物）。
	claim, err := s.LatestDurableClaimReview(ctx, revisionID)
	switch {
	case errors.Is(err, ErrNotFound):
		any, herr := s.hasAnyClaimReview(ctx, revisionID)
		if herr != nil {
			return nil, herr
		}
		if any {
			out.Issues = append(out.Issues, "仅存在旧版证据审校记录：新契约文章必须通过独立主张审校")
		} else {
			out.Issues = append(out.Issues, "缺少独立主张审校：请对本修订运行主张审校")
		}
	case err != nil:
		return nil, err
	case claim.Status != models.ClaimReviewStatusPassed:
		out.Issues = append(out.Issues, "最新主张审校未通过：请修正问题后重新审校")
	case !provenanceComplete(claim):
		out.Issues = append(out.Issues, "主张审校缺少可信 Provider/Model/Prompt 记录")
	}
	// 新契约：StyleReview 门禁（持久任务产物且 passed）。
	style, err := s.LatestDurableStyleReview(ctx, revisionID)
	switch {
	case errors.Is(err, ErrNotFound):
		any, herr := s.hasAnyStyleReview(ctx, revisionID)
		if herr != nil {
			return nil, herr
		}
		if any {
			out.Issues = append(out.Issues, "仅存在旧版风格审校记录：新契约文章必须通过持久风格审校任务")
		} else {
			out.Issues = append(out.Issues, "缺少独立风格审校：请对本修订运行风格审校")
		}
	case err != nil:
		return nil, err
	case style.Status != models.ClaimReviewStatusPassed:
		out.Issues = append(out.Issues, "最新风格审校未通过：请修正风格问题后重新审校")
	case !styleProvenanceComplete(style):
		out.Issues = append(out.Issues, "风格审校缺少可信 Provider/Model/Prompt 记录")
	}
	out.Ready = len(out.Issues) == 0
	return out, nil
}

// ---- 小工具 ----

func convertEntriesToProvider(entries []models.ClaimMapEntry) []provider.ClaimMapEntry {
	out := make([]provider.ClaimMapEntry, 0, len(entries))
	for _, e := range entries {
		out = append(out, provider.ClaimMapEntry{Excerpt: e.Excerpt, ClaimKind: e.ClaimKind, MaterialIDs: e.MaterialIDs, SourceTitle: e.SourceTitle, CitationRefs: e.CitationRefs})
	}
	return out
}

// claimFindingsToIssues 把强类型 findings 编码为既有 issues_json 字符串列表格式
// （页面/旧读取兼容）。
func claimFindingsToIssues(findings []provider.ClaimReviewFinding) string {
	issues := make([]string, 0, len(findings))
	for _, f := range findings {
		issues = append(issues, f.Excerpt+"： "+f.IssueKind+" — "+f.Detail)
	}
	encoded, _ := json.Marshal(issues)
	return string(encoded)
}

func strPtr(v string) *string { return &v }
