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

// CreateIdeationSession starts a durable Owner-directed exploration.
func (s *Store) CreateIdeationSession(ctx context.Context, session models.IdeationSession) (*models.IdeationSession, error) {
	session.ID = uuid.NewString()
	session.Intent = strings.TrimSpace(session.Intent)
	if session.ConstraintsJSON == "" {
		session.ConstraintsJSON = "{}"
	}
	if session.Status == "" {
		session.Status = "active"
	}
	if session.Intent == "" || session.Status != "active" {
		return nil, fmt.Errorf("%w: invalid ideation session", ErrInvalidEditorialState)
	}
	if _, err := s.GetEditorialProfile(ctx, session.EditorialProfileID); err != nil {
		return nil, err
	}
	if session.SelectionsJSON == "" {
		session.SelectionsJSON = "[]"
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO ideation_sessions (id,editorial_profile_id,intent,constraints_json,status,selections_json) VALUES (?,?,?,?,?,?)`, session.ID, session.EditorialProfileID, session.Intent, session.ConstraintsJSON, session.Status, session.SelectionsJSON)
	if err != nil {
		return nil, err
	}
	return s.GetIdeationSession(ctx, session.ID)
}

// GetIdeationSession retrieves a durable Owner-directed exploration.
func (s *Store) GetIdeationSession(ctx context.Context, id string) (*models.IdeationSession, error) {
	v := &models.IdeationSession{}
	err := s.DB.QueryRowContext(ctx, `SELECT id,editorial_profile_id,intent,constraints_json,status,created_at,updated_at,COALESCE(selections_json,'[]') FROM ideation_sessions WHERE id=?`, id).Scan(&v.ID, &v.EditorialProfileID, &v.Intent, &v.ConstraintsJSON, &v.Status, &v.CreatedAt, &v.UpdatedAt, &v.SelectionsJSON)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	return v, err
}

// ListIdeationSessions returns a profile's directed exploration history.
func (s *Store) ListIdeationSessions(ctx context.Context, profileID string) ([]*models.IdeationSession, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,editorial_profile_id,intent,constraints_json,status,created_at,updated_at,COALESCE(selections_json,'[]') FROM ideation_sessions WHERE editorial_profile_id=? ORDER BY updated_at DESC,id DESC`, profileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.IdeationSession
	for rows.Next() {
		v := &models.IdeationSession{}
		if err := rows.Scan(&v.ID, &v.EditorialProfileID, &v.Intent, &v.ConstraintsJSON, &v.Status, &v.CreatedAt, &v.UpdatedAt, &v.SelectionsJSON); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// CreateMaterialDiagnosis stores a reproducible material diagnosis for an ideation session.
func (s *Store) CreateMaterialDiagnosis(ctx context.Context, v models.MaterialDiagnosis) (*models.MaterialDiagnosis, error) {
	v.ID = uuid.NewString()
	if v.DiagnosisJSON == "" {
		return nil, fmt.Errorf("%w: empty material diagnosis", ErrInvalidEditorialState)
	}
	if v.MaterialSnapshotJSON == "" {
		v.MaterialSnapshotJSON = "[]"
	}
	if _, err := s.GetIdeationSession(ctx, v.IdeationSessionID); err != nil {
		return nil, err
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO material_diagnoses (id,ideation_session_id,diagnosis_json,material_snapshot_json) VALUES (?,?,?,?)`, v.ID, v.IdeationSessionID, v.DiagnosisJSON, v.MaterialSnapshotJSON)
	if err != nil {
		return nil, err
	}
	err = s.DB.QueryRowContext(ctx, `SELECT created_at FROM material_diagnoses WHERE id=?`, v.ID).Scan(&v.CreatedAt)
	return &v, err
}

// CreateCreationProposal persists an AI-proposed or Owner-directed claim-led direction.
func (s *Store) CreateCreationProposal(ctx context.Context, v models.CreationProposal) (*models.CreationProposal, error) {
	v.ID = uuid.NewString()
	v.WorkingTitle = strings.TrimSpace(v.WorkingTitle)
	v.ProposedClaim = strings.TrimSpace(v.ProposedClaim)
	if v.Status == "" {
		v.Status = "proposed"
	}
	if v.CreationForm == "" {
		v.CreationForm = "article"
	}
	if v.MaterialIDsJSON == "" {
		v.MaterialIDsJSON = "[]"
	}
	if v.WorkingTitle == "" || v.ProposedClaim == "" || v.Status != "proposed" {
		return nil, fmt.Errorf("%w: invalid creation proposal", ErrInvalidEditorialState)
	}
	if _, err := s.GetEditorialProfile(ctx, v.EditorialProfileID); err != nil {
		return nil, err
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO creation_proposals (id,editorial_profile_id,proposal_batch_id,ideation_session_id,ideation_round_id,status,creation_form,working_title,proposed_claim,owner_claim,audience,rationale,material_ids_json,history_relationship) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, v.ID, v.EditorialProfileID, optionalWorkspaceString(v.ProposalBatchID), optionalWorkspaceString(v.IdeationSessionID), v.IdeationRoundID, v.Status, v.CreationForm, v.WorkingTitle, v.ProposedClaim, optionalWorkspaceString(v.OwnerClaim), v.Audience, v.Rationale, v.MaterialIDsJSON, v.HistoryRelationship)
	if err != nil {
		return nil, err
	}
	return s.GetCreationProposal(ctx, v.ID)
}

// GetCreationProposal retrieves a proposed or Owner-accepted direction.
func (s *Store) GetCreationProposal(ctx context.Context, id string) (*models.CreationProposal, error) {
	v := &models.CreationProposal{}
	var batch, session, owner sql.NullString
	var round sql.NullString
	err := s.DB.QueryRowContext(ctx, `SELECT id,editorial_profile_id,proposal_batch_id,ideation_session_id,ideation_round_id,status,creation_form,working_title,proposed_claim,owner_claim,audience,rationale,material_ids_json,history_relationship,COALESCE(decision_note,''),created_at,updated_at FROM creation_proposals WHERE id=?`, id).Scan(&v.ID, &v.EditorialProfileID, &batch, &session, &round, &v.Status, &v.CreationForm, &v.WorkingTitle, &v.ProposedClaim, &owner, &v.Audience, &v.Rationale, &v.MaterialIDsJSON, &v.HistoryRelationship, &v.DecisionNote, &v.CreatedAt, &v.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	v.ProposalBatchID = batch.String
	v.IdeationSessionID = session.String
	v.IdeationRoundID = round.String
	v.OwnerClaim = owner.String
	return v, nil
}

// ListCreationProposals lists all claim-led directions for one profile, newest first.
func (s *Store) ListCreationProposals(ctx context.Context, profileID string) ([]*models.CreationProposal, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,editorial_profile_id,proposal_batch_id,ideation_session_id,ideation_round_id,status,creation_form,working_title,proposed_claim,owner_claim,audience,rationale,material_ids_json,history_relationship,COALESCE(decision_note,''),created_at,updated_at FROM creation_proposals WHERE editorial_profile_id=? ORDER BY created_at DESC,id DESC`, profileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.CreationProposal
	for rows.Next() {
		v := &models.CreationProposal{}
		var batch, session, round, owner sql.NullString
		if err := rows.Scan(&v.ID, &v.EditorialProfileID, &batch, &session, &round, &v.Status, &v.CreationForm, &v.WorkingTitle, &v.ProposedClaim, &owner, &v.Audience, &v.Rationale, &v.MaterialIDsJSON, &v.HistoryRelationship, &v.DecisionNote, &v.CreatedAt, &v.UpdatedAt); err != nil {
			return nil, err
		}
		v.ProposalBatchID, v.IdeationSessionID, v.IdeationRoundID, v.OwnerClaim = batch.String, session.String, round.String, owner.String
		out = append(out, v)
	}
	return out, rows.Err()
}

// AcceptCreationProposal records the moment an AI proposed claim becomes an OwnerClaim.
func (s *Store) AcceptCreationProposal(ctx context.Context, id, ownerClaim string) error {
	ownerClaim = strings.TrimSpace(ownerClaim)
	if ownerClaim == "" {
		return fmt.Errorf("%w: owner claim required", ErrInvalidEditorialState)
	}
	// R16：复用 DecideProposal 事务核心（最后一条决策自动释放批次）。
	return s.DecideProposal(ctx, id, "accept", ownerClaim, "", "")
}

// CreateResearchNeed records a blocking or enhancement learning gap.
func (s *Store) CreateResearchNeed(ctx context.Context, v models.ResearchNeed) (*models.ResearchNeed, error) {
	v.ID = uuid.NewString()
	v.Question = strings.TrimSpace(v.Question)
	if v.Status == "" {
		v.Status = "open"
	}
	if (v.Severity != "blocking" && v.Severity != "enhancement") || v.Question == "" || v.Status != "open" {
		return nil, fmt.Errorf("%w: invalid research need", ErrInvalidEditorialState)
	}
	if _, err := s.GetCreationProposal(ctx, v.CreationProposalID); err != nil {
		return nil, err
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO research_needs (id,creation_proposal_id,severity,question,status) VALUES (?,?,?,?,?)`, v.ID, v.CreationProposalID, v.Severity, v.Question, v.Status)
	if err != nil {
		return nil, err
	}
	err = s.DB.QueryRowContext(ctx, `SELECT created_at FROM research_needs WHERE id=?`, v.ID).Scan(&v.CreatedAt)
	return &v, err
}

// GetResearchNeed retrieves one research gap by stable identifier.
func (s *Store) GetResearchNeed(ctx context.Context, id string) (*models.ResearchNeed, error) {
	v := &models.ResearchNeed{}
	var srcType, detail string
	var ver int
	var confirmed, invalidated int
	err := s.DB.QueryRowContext(ctx, `SELECT id,creation_proposal_id,severity,question,status,COALESCE(resolution_source_id,''),created_at,COALESCE(resolved_at,''),COALESCE(resolution_source_type,''),COALESCE(resolution_version,0),COALESCE(resolution_detail,''),COALESCE(resolution_owner_confirmed,0),COALESCE(resolution_invalidated,0) FROM research_needs WHERE id=?`, id).Scan(&v.ID, &v.CreationProposalID, &v.Severity, &v.Question, &v.Status, &v.ResolutionSourceID, &v.CreatedAt, &v.ResolvedAt, &srcType, &ver, &detail, &confirmed, &invalidated)
	if err == nil {
		v.ResolutionSourceType, v.ResolutionVersion, v.ResolutionDetail = models.SourceType(srcType), ver, detail
		v.ResolutionOwnerConfirm, v.ResolutionInvalidated = confirmed == 1, invalidated == 1
	}
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	return v, err
}

// ListResearchNeeds returns unresolved and resolved research gaps for a profile.
func (s *Store) ListResearchNeeds(ctx context.Context, profileID string) ([]*models.ResearchNeed, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT r.id,r.creation_proposal_id,r.severity,r.question,r.status,COALESCE(r.resolution_source_id,''),r.created_at,COALESCE(r.resolved_at,''),COALESCE(r.resolution_source_type,''),COALESCE(r.resolution_version,0),COALESCE(r.resolution_detail,''),COALESCE(r.resolution_owner_confirmed,0),COALESCE(r.resolution_invalidated,0) FROM research_needs r JOIN creation_proposals p ON p.id=r.creation_proposal_id WHERE p.editorial_profile_id=? ORDER BY CASE r.status WHEN 'open' THEN 0 ELSE 1 END,r.created_at DESC,r.id DESC`, profileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.ResearchNeed
	for rows.Next() {
		v := &models.ResearchNeed{}
		var srcType, detail string
		var ver, confirmed, invalidated int
		if err := rows.Scan(&v.ID, &v.CreationProposalID, &v.Severity, &v.Question, &v.Status, &v.ResolutionSourceID, &v.CreatedAt, &v.ResolvedAt, &srcType, &ver, &detail, &confirmed, &invalidated); err != nil {
			return nil, err
		}
		v.ResolutionSourceType, v.ResolutionVersion, v.ResolutionDetail = models.SourceType(srcType), ver, detail
		v.ResolutionOwnerConfirm, v.ResolutionInvalidated = confirmed == 1, invalidated == 1
		out = append(out, v)
	}
	return out, rows.Err()
}

// ResolveResearchNeed records the source that supplied the missing learning.
// Research execution itself remains out of scope for V1; an Owner must first
// create a ResearchPlan and later add the resulting Source to this workspace.
// ResolveResearchNeed 用已处理来源解决研究缺口（C04 / ADR-0024 §1）：
// 校验来源存在且已处理（转录/内容就绪），非空 sourceID 不再足以 resolved；
// 来源删除或失效后由 Purge 级联重新阻断相关下游（research_needs 行保留）。
// ResolveResearchNeedWithEvidence 用具体、版本化、Owner 确认的依据解决研究缺口
// （C04/R14）：
//   - 来源类型必须显式且有效；来源必须存在且已处理（仅"存在"不足以 resolved）；
//   - version 必须等于该来源当前版本（旧版本依据拒绝——依据属于确切版本才可用）；
//   - detail 必须是该版本内的具体材料位置（音频=Segment ID；文档=段落 Segment ID），
//     跨来源 Segment 拒绝；空依据拒绝；
//   - resolved 语义是"Owner 认定缺口已解决"，不宣称机器证明事实为真。
func (s *Store) ResolveResearchNeedWithEvidence(ctx context.Context, id string, sourceType models.SourceType, sourceID string, version int, detail string) error {
	sourceID = strings.TrimSpace(sourceID)
	detail = strings.TrimSpace(detail)
	if sourceID == "" {
		return fmt.Errorf("%w: resolution source required", ErrInvalidEditorialState)
	}
	if sourceType != models.SourceEpisode && sourceType != models.SourceUpload && sourceType != models.SourceDocument {
		return fmt.Errorf("%w: 依据来源类型无效（%q）", ErrInvalidEditorialState, sourceType)
	}
	if version <= 0 {
		return fmt.Errorf("%w: 依据缺少来源版本", ErrInvalidEditorialState)
	}
	if detail == "" {
		return fmt.Errorf("%w: 依据缺少具体材料位置", ErrInvalidEditorialState)
	}
	need, err := s.GetResearchNeed(ctx, id)
	if err != nil {
		return err
	}
	if need.Status != "open" {
		return ErrInvalidEditorialState
	}
	if !s.sourceProcessed(ctx, sourceType, sourceID) {
		return fmt.Errorf("%w: 来源 %s 不存在或未处理，不能解决缺口", ErrInvalidEditorialState, sourceID)
	}
	// 版本与具体位置校验：依据必须属于该来源的当前版本。
	switch sourceType {
	case models.SourceDocument:
		doc, err := s.GetDocument(ctx, sourceID)
		if err != nil {
			return fmt.Errorf("%w: 文档 %s 不存在", ErrInvalidEditorialState, sourceID)
		}
		if doc.Version != version {
			return fmt.Errorf("%w: 依据版本 %d 不是文档当前版本 %d", ErrInvalidEditorialState, version, doc.Version)
		}
		var seriesMax int
		if err := s.DB.QueryRowContext(ctx, `SELECT COALESCE(MAX(version),0) FROM documents WHERE series_id=?`, doc.SeriesID).Scan(&seriesMax); err != nil {
			return err
		}
		if doc.Version != seriesMax {
			return fmt.Errorf("%w: 依据版本 %d 不是该文档系列最新版本 %d（旧版本拒绝）", ErrInvalidEditorialState, version, seriesMax)
		}
		if !documentHasSegment(doc, detail) {
			return fmt.Errorf("%w: 段落 %s 不属于文档 %s 的当前版本", ErrInvalidEditorialState, detail, sourceID)
		}
	default:
		av, err := s.GetCurrentVersion(ctx, sourceType, sourceID, KindTranscript)
		if err != nil {
			return fmt.Errorf("%w: 来源 %s 无当前转录版本", ErrInvalidEditorialState, sourceID)
		}
		if av.Version != version {
			return fmt.Errorf("%w: 依据版本 %d 不是转录当前版本 %d", ErrInvalidEditorialState, version, av.Version)
		}
		var payload provider.TranscriptPayload
		if err := json.Unmarshal([]byte(av.Payload), &payload); err != nil {
			return fmt.Errorf("%w: 转录载荷不可解析", ErrInvalidEditorialState)
		}
		found := false
		for _, seg := range payload.Segments {
			if seg.ID == detail {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("%w: Segment %s 不属于来源 %s 的当前版本（跨来源或虚构）", ErrInvalidEditorialState, detail, sourceID)
		}
	}
	result, err := s.DB.ExecContext(ctx,
		`UPDATE research_needs SET status='resolved',resolution_source_id=?,resolved_at=datetime('now'),
		        resolution_source_type=?,resolution_version=?,resolution_detail=?,resolution_owner_confirmed=1,
		        resolution_invalidated=0
		 WHERE id=? AND status='open'`, sourceID, string(sourceType), version, detail, id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrInvalidEditorialState
	}
	return nil
}

// documentHasSegment 判断段落位置 ID 是否属于该文档当前版本（R14 依据校验）。
func documentHasSegment(doc *models.Document, segmentID string) bool {
	for _, seg := range DocumentSegments(doc) {
		if seg.ID == segmentID {
			return true
		}
	}
	return false
}

// reopenResolutionsTx 在同一事务内重开命中的 resolved 缺口，并把受影响提案的
// 已确认 Brief 标为 needs_review——先收集本次精确命中的 proposal 集合（不误伤
// 历史失效行），need 重开与 Brief 传播原子完成，不半传播。
func (s *Store) reopenResolutionsTx(ctx context.Context, tx *sql.Tx, whereSQL string, args ...any) (int64, error) {
	ids := []string{}
	rows, err := tx.QueryContext(ctx,
		`SELECT DISTINCT creation_proposal_id FROM research_needs WHERE `+whereSQL, args...)
	if err != nil {
		return 0, err
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()

	res, err := tx.ExecContext(ctx,
		`UPDATE research_needs SET status='open', resolved_at=NULL, resolution_invalidated=1 WHERE `+whereSQL, args...)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	if n > 0 && len(ids) > 0 {
		ph := strings.TrimRight(strings.Repeat("?,", len(ids)), ",")
		idsAny := make([]any, len(ids))
		for i, id := range ids {
			idsAny[i] = id
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE creation_briefs SET status='needs_review', updated_at=datetime('now')
			 WHERE status='confirmed' AND creation_proposal_id IN (`+ph+`)`, idsAny...); err != nil {
			return 0, err
		}
	}
	return n, nil
}

// invalidateResearchResolutionsWhere 以事务执行一次依据失效传播。
func (s *Store) invalidateResearchResolutionsWhere(ctx context.Context, whereSQL string, args ...any) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := s.reopenResolutionsTx(ctx, tx, whereSQL, args...); err != nil {
		return err
	}
	return tx.Commit()
}

// InvalidateResearchResolutions R14：来源被 Purge/删除时，以该来源为依据的
// resolved 缺口重新置回 open 并标记 invalidated——重新阻断待确认 Brief 与写作；
// 已确认的 Brief 标为 needs_review 待 Owner 复核（审计字段保留）。
func (s *Store) InvalidateResearchResolutions(ctx context.Context, sourceType models.SourceType, sourceID string) error {
	return s.invalidateResearchResolutionsWhere(ctx,
		`resolution_source_id=? AND resolution_source_type=? AND status='resolved'`, sourceID, string(sourceType))
}

// InvalidateSupersededTranscriptResolutions R14（复核修复）：转录重分析切换
// current 版本后，以同一来源**同类型**旧版本为依据的 resolved 缺口重新阻断——
// 重分析不得绕过失效，且不得误伤恰好同 ID 的其他来源类型。
// 版本变更与失效传播是连续操作：失效失败会向调用方返回错误（不伪装未写入成功
// 而静默留下旧依据）。
func (s *Store) InvalidateSupersededTranscriptResolutions(ctx context.Context, sourceType models.SourceType, sourceID string, currentVersion int) error {
	return s.invalidateResearchResolutionsWhere(ctx,
		`resolution_source_id=? AND resolution_source_type=? AND status='resolved' AND resolution_version<>?`,
		sourceID, string(sourceType), currentVersion)
}

// InvalidateSupersededDocumentResolutions R14：同系列出现新文档版本后，
// 以旧版本文档为依据的 resolved 缺口重新阻断。
func (s *Store) InvalidateSupersededDocumentResolutions(ctx context.Context, seriesID string, currentVersion int) error {
	return s.invalidateResearchResolutionsWhere(ctx,
		`resolution_source_type='document' AND status='resolved' AND resolution_version<? AND resolution_source_id IN (SELECT id FROM documents WHERE series_id=?)`,
		currentVersion, seriesID)
}

// sourceProcessed 判断来源存在且已完成处理（转录或文档内容就绪）。
func (s *Store) sourceProcessed(ctx context.Context, _ models.SourceType, sourceID string) bool {
	// 音频来源：有当前转录版本；文档：行存在且内容非空。
	if _, err := s.GetCurrentVersion(ctx, models.SourceEpisode, sourceID, KindTranscript); err == nil {
		return true
	}
	if _, err := s.GetCurrentVersion(ctx, models.SourceUpload, sourceID, KindTranscript); err == nil {
		return true
	}
	var content string
	err := s.DB.QueryRowContext(ctx, `SELECT content FROM documents WHERE id=?`, sourceID).Scan(&content)
	return err == nil && strings.TrimSpace(content) != ""
}

// CreateResearchPlan records a future, Owner-reviewable research contract and
// intentionally does not perform external research.
func (s *Store) CreateResearchPlan(ctx context.Context, plan models.ResearchPlan) (*models.ResearchPlan, error) {
	plan.ID, plan.Question, plan.Scope = uuid.NewString(), strings.TrimSpace(plan.Question), strings.TrimSpace(plan.Scope)
	if plan.Status == "" {
		plan.Status = "draft"
	}
	if plan.Question == "" || plan.Status != "draft" || plan.BudgetCents != nil && *plan.BudgetCents < 0 {
		return nil, fmt.Errorf("%w: invalid research plan", ErrInvalidEditorialState)
	}
	var status string
	if err := s.DB.QueryRowContext(ctx, `SELECT status FROM research_needs WHERE id=?`, plan.ResearchNeedID).Scan(&status); err == sql.ErrNoRows {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, err
	}
	if status != "open" {
		return nil, fmt.Errorf("%w: research need is not open", ErrInvalidEditorialState)
	}
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO research_plans (id,research_need_id,question,scope,budget_cents,status) VALUES (?,?,?,?,?,?)`, plan.ID, plan.ResearchNeedID, plan.Question, plan.Scope, plan.BudgetCents, plan.Status); err != nil {
		return nil, err
	}
	return s.GetResearchPlan(ctx, plan.ID)
}

// GetResearchPlan retrieves one Owner-authorized research contract.
func (s *Store) GetResearchPlan(ctx context.Context, id string) (*models.ResearchPlan, error) {
	plan := &models.ResearchPlan{}
	var budget sql.NullInt64
	var confirmed sql.NullString
	err := s.DB.QueryRowContext(ctx, `SELECT id,research_need_id,question,scope,budget_cents,status,owner_confirmed_at,created_at FROM research_plans WHERE id=?`, id).Scan(&plan.ID, &plan.ResearchNeedID, &plan.Question, &plan.Scope, &budget, &plan.Status, &confirmed, &plan.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if budget.Valid {
		value := budget.Int64
		plan.BudgetCents = &value
	}
	if confirmed.Valid {
		value := confirmed.String
		plan.OwnerConfirmedAt = &value
	}
	return plan, nil
}

// ConfirmResearchPlan records the Owner's explicit authorization to perform future research.
func (s *Store) ConfirmResearchPlan(ctx context.Context, id string) error {
	result, err := s.DB.ExecContext(ctx, `UPDATE research_plans SET status='confirmed',owner_confirmed_at=datetime('now') WHERE id=? AND status='draft'`, id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrInvalidEditorialState
	}
	return nil
}

// HasBlockingResearchNeed reports whether a proposal remains blocked from confirmation.
func (s *Store) HasBlockingResearchNeed(ctx context.Context, proposalID string) (bool, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM research_needs WHERE creation_proposal_id=? AND severity='blocking' AND status!='resolved'`, proposalID).Scan(&n)
	return n > 0, err
}

// CreateCreationBrief creates a reviewable contract only after blocking needs are resolved.
func (s *Store) CreateCreationBrief(ctx context.Context, v models.CreationBrief) (*models.CreationBrief, error) {
	v.ID = uuid.NewString()
	if v.Status == "" {
		v.Status = "draft"
	}
	if v.ClaimPlanJSON == "" {
		v.ClaimPlanJSON = "[]"
	}
	if v.MaterialPlanJSON == "" {
		v.MaterialPlanJSON = "[]"
	}
	if v.ResearchNeedIDsJSON == "" {
		v.ResearchNeedIDsJSON = "[]"
	}
	proposal, err := s.GetCreationProposal(ctx, v.CreationProposalID)
	if err != nil {
		return nil, err
	}
	if proposal.Status != "accepted" || v.OwnerClaim == "" {
		return nil, fmt.Errorf("%w: accepted owner claim required", ErrInvalidEditorialState)
	}
	blocked, err := s.HasBlockingResearchNeed(ctx, v.CreationProposalID)
	if err != nil {
		return nil, err
	}
	if blocked {
		return nil, fmt.Errorf("%w: blocking research need unresolved", ErrInvalidEditorialState)
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO creation_briefs (id,creation_proposal_id,status,owner_claim,claim_plan_json,material_plan_json,research_need_ids_json,outline,style,target_length) VALUES (?,?,?,?,?,?,?,?,?,?)`, v.ID, v.CreationProposalID, v.Status, v.OwnerClaim, v.ClaimPlanJSON, v.MaterialPlanJSON, v.ResearchNeedIDsJSON, v.Outline, v.Style, v.TargetLength)
	if err != nil {
		return nil, err
	}
	return s.GetCreationBrief(ctx, v.ID)
}

// GetCreationBrief retrieves one Owner-reviewable creation contract.
func (s *Store) GetCreationBrief(ctx context.Context, id string) (*models.CreationBrief, error) {
	v := &models.CreationBrief{}
	var confirmed sql.NullString
	err := s.DB.QueryRowContext(ctx, `SELECT id,creation_proposal_id,status,owner_claim,claim_plan_json,material_plan_json,research_need_ids_json,outline,style,target_length,confirmed_at,created_at,updated_at FROM creation_briefs WHERE id=?`, id).Scan(&v.ID, &v.CreationProposalID, &v.Status, &v.OwnerClaim, &v.ClaimPlanJSON, &v.MaterialPlanJSON, &v.ResearchNeedIDsJSON, &v.Outline, &v.Style, &v.TargetLength, &confirmed, &v.CreatedAt, &v.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if confirmed.Valid {
		value := confirmed.String
		v.ConfirmedAt = &value
	}
	return v, err
}

// ListCreationBriefs lists a profile's current and historical creation contracts.
func (s *Store) ListCreationBriefs(ctx context.Context, profileID string) ([]*models.CreationBrief, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT b.id,b.creation_proposal_id,b.status,b.owner_claim,b.claim_plan_json,b.material_plan_json,b.research_need_ids_json,b.outline,b.style,b.target_length,b.confirmed_at,b.created_at,b.updated_at FROM creation_briefs b JOIN creation_proposals p ON p.id=b.creation_proposal_id WHERE p.editorial_profile_id=? ORDER BY b.updated_at DESC,b.id DESC`, profileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.CreationBrief
	for rows.Next() {
		v := &models.CreationBrief{}
		var confirmed sql.NullString
		if err := rows.Scan(&v.ID, &v.CreationProposalID, &v.Status, &v.OwnerClaim, &v.ClaimPlanJSON, &v.MaterialPlanJSON, &v.ResearchNeedIDsJSON, &v.Outline, &v.Style, &v.TargetLength, &confirmed, &v.CreatedAt, &v.UpdatedAt); err != nil {
			return nil, err
		}
		if confirmed.Valid {
			value := confirmed.String
			v.ConfirmedAt = &value
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// CreateCreationBriefDraftFromProposal creates the minimum reviewable contract
// immediately after an Owner accepts a sufficiently evidenced direction.
func (s *Store) CreateCreationBriefDraftFromProposal(ctx context.Context, proposalID string) (*models.CreationBrief, error) {
	proposal, err := s.GetCreationProposal(ctx, proposalID)
	if err != nil {
		return nil, err
	}
	if proposal.Status != "accepted" || proposal.OwnerClaim == "" {
		return nil, fmt.Errorf("%w: accepted owner claim required", ErrInvalidEditorialState)
	}
	var materialIDs []string
	if err := json.Unmarshal([]byte(proposal.MaterialIDsJSON), &materialIDs); err != nil || len(materialIDs) == 0 {
		return nil, fmt.Errorf("%w: accepted proposal needs material", ErrInvalidEditorialState)
	}
	var existing string
	err = s.DB.QueryRowContext(ctx, `SELECT id FROM creation_briefs WHERE creation_proposal_id=? AND status IN ('draft','confirmed') ORDER BY created_at DESC LIMIT 1`, proposalID).Scan(&existing)
	if err == nil {
		return s.GetCreationBrief(ctx, existing)
	}
	if err != sql.ErrNoRows {
		return nil, err
	}
	claimPlan, _ := json.Marshal([]map[string]string{{"kind": string(models.ClaimOwner), "claim": proposal.OwnerClaim}})
	return s.CreateCreationBrief(ctx, models.CreationBrief{CreationProposalID: proposalID, OwnerClaim: proposal.OwnerClaim, ClaimPlanJSON: string(claimPlan), MaterialPlanJSON: proposal.MaterialIDsJSON})
}

// ConfirmCreationBrief records the explicit work-generation authorization.
// ConfirmCreationBrief Owner 确认 Brief（C07 / ADR-0024 §1）：
// 确认前重查——blocking research、材料质量/陈旧/排除、Provider 策略；
// 材料变化后必须通过新校验才可确认（不可沿用旧校验结果）。
func (s *Store) ConfirmCreationBrief(ctx context.Context, id string) error {
	var proposalID string
	if err := s.DB.QueryRowContext(ctx, `SELECT creation_proposal_id FROM creation_briefs WHERE id=?`, id).Scan(&proposalID); err == sql.ErrNoRows {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	blocked, err := s.HasBlockingResearchNeed(ctx, proposalID)
	if err != nil {
		return err
	}
	if blocked {
		return fmt.Errorf("%w: blocking research need unresolved", ErrInvalidEditorialState)
	}
	// C07：确认前逐项校验材料资格（不沿用生成时的状态）。
	if err := s.recheckBriefMaterials(ctx, proposalID); err != nil {
		return err
	}
	r, err := s.DB.ExecContext(ctx, `UPDATE creation_briefs SET status='confirmed',confirmed_at=datetime('now'),updated_at=datetime('now') WHERE id=? AND status IN ('draft','needs_review')`, id)
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrInvalidEditorialState
	}
	return nil
}

// ---- 构思轮次（C02 / ADR-0024 §1）----

// AddIdeationRound 在会话内追加一轮：冻结用户输入、约束与前一轮身份；
// client_nonce 幂等——并发/重复提交命中同 nonce 时返回已有轮（created=false）。
// 材料快照由调用方在入轮前读好并冻结（本方法不再回查当前素材，保证"旧结果
// 不覆盖新范围"）。
func (s *Store) AddIdeationRound(ctx context.Context, sessionID, clientNonce, userInput, constraintsJSON, materialSnapshotJSON string) (*models.IdeationRound, bool, error) {
	if clientNonce == "" {
		return nil, false, fmt.Errorf("%w: client nonce required", ErrInvalidEditorialState)
	}
	session, err := s.GetIdeationSession(ctx, sessionID)
	if err != nil {
		return nil, false, err
	}
	if session.Status != "active" {
		return nil, false, fmt.Errorf("%w: 会话已结束，不能追加轮次", ErrInvalidEditorialState)
	}
	// 同 nonce 幂等：已有则直接返回（并发 CAS 由 UNIQUE 兜底）。
	if existing, err := s.GetIdeationRoundByNonce(ctx, sessionID, clientNonce); err == nil {
		return existing, false, nil
	} else if !errors.Is(err, ErrNotFound) {
		return nil, false, err
	}
	var maxRound int
	var prevID string
	if err := s.DB.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(round_no),0), COALESCE((SELECT id FROM ideation_rounds r2 WHERE r2.session_id=ideation_rounds.session_id ORDER BY round_no DESC LIMIT 1),'')
		 FROM ideation_rounds WHERE session_id=?`, sessionID).Scan(&maxRound, &prevID); err != nil && err != sql.ErrNoRows {
		// 子查询写法在空表时返回 NULL：容错处理如下
		maxRound = 0
		prevID = ""
	}
	round := &models.IdeationRound{
		ID: uuid.NewString(), SessionID: sessionID, RoundNo: maxRound + 1,
		PrevRoundID: prevID, ClientNonce: clientNonce,
		UserInput: userInput, ConstraintsJSON: constraintsJSON,
		MaterialSnapshotJSON: materialSnapshotJSON, Status: models.RoundRecorded,
	}
	_, err = s.DB.ExecContext(ctx,
		`INSERT INTO ideation_rounds (id, session_id, round_no, prev_round_id, client_nonce, user_input, constraints_json, material_snapshot_json, status)
		 VALUES (?,?,?,?,?,?,?,?,?)`,
		round.ID, sessionID, round.RoundNo, round.PrevRoundID, clientNonce,
		userInput, constraintsJSON, materialSnapshotJSON, round.Status)
	if err != nil {
		if isUniqueConstraintErr(err) {
			if existing, gerr := s.GetIdeationRoundByNonce(ctx, sessionID, clientNonce); gerr == nil {
				return existing, false, nil
			}
		}
		return nil, false, fmt.Errorf("写入构思轮次: %w", err)
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE ideation_sessions SET updated_at=datetime('now') WHERE id=?`, sessionID); err != nil {
		return round, true, err
	}
	return round, true, nil
}

// GetIdeationRoundByNonce 按 client_nonce 读取轮次；无则 ErrNotFound。
func (s *Store) GetIdeationRoundByNonce(ctx context.Context, sessionID, clientNonce string) (*models.IdeationRound, error) {
	row := s.DB.QueryRowContext(ctx,
		`SELECT id, session_id, round_no, prev_round_id, client_nonce, user_input, constraints_json, material_snapshot_json, status, output_diagnosis_id, created_at
		 FROM ideation_rounds WHERE session_id=? AND client_nonce=?`, sessionID, clientNonce)
	return scanIdeationRound(row)
}

// ListIdeationRounds 按轮次序返回会话全部轮次（刷新后可继续，历史可回看）。
func (s *Store) ListIdeationRounds(ctx context.Context, sessionID string) ([]*models.IdeationRound, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, session_id, round_no, prev_round_id, client_nonce, user_input, constraints_json, material_snapshot_json, status, output_diagnosis_id, created_at
		 FROM ideation_rounds WHERE session_id=? ORDER BY round_no ASC`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.IdeationRound
	for rows.Next() {
		r, err := scanIdeationRound(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// MarkIdeationRoundDiagnosed 记录轮次的诊断输出引用（C03 接入）。
func (s *Store) MarkIdeationRoundDiagnosed(ctx context.Context, roundID, diagnosisID string, failed bool) error {
	status := models.RoundDiagnosed
	if failed {
		status = models.RoundFailed
	}
	_, err := s.DB.ExecContext(ctx,
		`UPDATE ideation_rounds SET status=?, output_diagnosis_id=? WHERE id=?`,
		status, diagnosisID, roundID)
	return err
}

func scanIdeationRound(row rowScanner) (*models.IdeationRound, error) {
	r := &models.IdeationRound{}
	err := row.Scan(&r.ID, &r.SessionID, &r.RoundNo, &r.PrevRoundID, &r.ClientNonce,
		&r.UserInput, &r.ConstraintsJSON, &r.MaterialSnapshotJSON, &r.Status, &r.OutputDiagnosisID, &r.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return r, nil
}

// GetIdeationRoundByID 按 ID 读取轮次。
func (s *Store) GetIdeationRoundByID(ctx context.Context, roundID string) (*models.IdeationRound, error) {
	return scanIdeationRound(s.DB.QueryRowContext(ctx,
		`SELECT id, session_id, round_no, prev_round_id, client_nonce, user_input, constraints_json, material_snapshot_json, status, output_diagnosis_id, created_at
		 FROM ideation_rounds WHERE id=?`, roundID))
}

// CreateMaterialDiagnosisForRound 为轮次写入诊断（C03）。
func (s *Store) CreateMaterialDiagnosisForRound(ctx context.Context, sessionID, roundID, diagnosisJSON, materialSnapshotJSON string) (*models.MaterialDiagnosis, error) {
	md := &models.MaterialDiagnosis{
		ID: uuid.NewString(), IdeationSessionID: sessionID,
		DiagnosisJSON: diagnosisJSON, MaterialSnapshotJSON: materialSnapshotJSON,
	}
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO material_diagnoses (id, ideation_session_id, ideation_round_id, diagnosis_json, material_snapshot_json)
		 VALUES (?,?,?,?,?)`,
		md.ID, sessionID, roundID, diagnosisJSON, materialSnapshotJSON)
	if err != nil {
		return nil, err
	}
	return md, nil
}

// recheckBriefMaterials 确认前逐项校验 Brief 材料的当前资格（C07）：
// 材料关键观点必须是 ready/owner_confirmed、非 stale、未被 Owner 排除。
// 材料变化/缺口/陈旧都阻止确认。
func (s *Store) recheckBriefMaterials(ctx context.Context, proposalID string) error {
	proposal, err := s.GetCreationProposal(ctx, proposalID)
	if err != nil {
		return err
	}
	var materialIDs []string
	if err := json.Unmarshal([]byte(proposal.MaterialIDsJSON), &materialIDs); err != nil {
		return nil // 无材料 ID 列表的旧数据跳过校验
	}
	for _, id := range materialIDs {
		// C07：旧数据可能引用已不存在的 KeyPoint——跳过不阻断（与 C01 不同，
		// C01 选择时显式排除）。此处只检查仍存在的重点的质量/stale/排除状态。
		if _, err := s.GetKeyPoint(ctx, id); errors.Is(err, ErrNotFound) {
			continue
		}
		el := s.checkMaterialEligibility(ctx, id, "")
		if !el.Eligible {
			return fmt.Errorf("%w: 材料资格确认失败——%s", ErrInvalidEditorialState, el.Reason)
		}
	}
	return nil
}

// ---- ClaimMap（C09）----

// SaveClaimMap 批量写入一个修订的 ClaimMap（先删后插，幂等）。
func (s *Store) SaveClaimMap(ctx context.Context, draftID, revisionID string, entries []models.ClaimMapEntry) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM claim_map_entries WHERE draft_id=? AND revision_id=?`, draftID, revisionID); err != nil {
		return err
	}
	for _, e := range entries {
		materialsJSON, _ := json.Marshal(e.MaterialIDs)
		citationsJSON, _ := json.Marshal(e.CitationRefs)
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO claim_map_entries (id, draft_id, revision_id, excerpt, claim_kind, material_ids_json, source_title, citation_refs_json)
			 VALUES (?,?,?,?,?,?,?,?)`,
			uuid.NewString(), draftID, revisionID, e.Excerpt, e.ClaimKind,
			string(materialsJSON), e.SourceTitle, string(citationsJSON)); err != nil {
			return fmt.Errorf("写入 ClaimMap: %w", err)
		}
	}
	return tx.Commit()
}

// ListClaimMap 读取一个修订的全部 ClaimMap 条目。
func (s *Store) ListClaimMap(ctx context.Context, draftID, revisionID string) ([]models.ClaimMapEntry, error) {
	rows, err := s.DB.QueryContext(ctx,
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
		_ = json.Unmarshal([]byte(materials), &e.MaterialIDs)
		_ = json.Unmarshal([]byte(citations), &e.CitationRefs)
		out = append(out, e)
	}
	return out, rows.Err()
}

// InheritClaimMap 修订时正确继承主张映射（C12 / ADR-0024 §6）：
//   - 旧修订的 ClaimMap 中，逐字出现的片段可继承（身份/证据关系未变化）；
//   - 修改处（新文本或引用变化的片段）重新校验，不自动继承；
//   - 旧审校（claim_reviews）不继承——新修订需要重新审校。
//
// oldRevisionID 为基础修订，newRevisionID 为新修订（已通过 CreateArticleRevision 创建）。
// newMarkdown 是新修订的正文。返回继承的条目数。
func (s *Store) InheritClaimMap(ctx context.Context, oldRevisionID, newRevisionID, draftID, newMarkdown string) (int, error) {
	entries, err := s.ListClaimMap(ctx, draftID, oldRevisionID)
	if err != nil {
		return 0, err
	}
	var inherited []models.ClaimMapEntry
	for _, e := range entries {
		// 只有逐字出现的片段才可继承（身份/证据关系未变化）。
		if strings.Contains(newMarkdown, e.Excerpt) {
			inherited = append(inherited, e)
		}
		// 未逐字出现的片段不继承（需要重新审校，C11 接入）。
	}
	if len(inherited) == 0 {
		return 0, nil
	}
	if err := s.SaveClaimMap(ctx, draftID, newRevisionID, inherited); err != nil {
		return 0, err
	}
	return len(inherited), nil
}

// ---- 双向导航查询（U02）----

// MaterialUsage 单条材料的使用记录。
type MaterialUsage struct {
	Kind   string `json:"kind"` // digest | article
	Title  string `json:"title"`
	Detail string `json:"detail"`
	Link   string `json:"link"`
}

// FindUsageByKeyPoint 反查一个 KeyPoint 被哪些精读文/文章修订采用（U02）。
// 通过该重点的引用 Segment ID 在精读文块和文章 ClaimMap 中搜索。
func (s *Store) FindUsageByKeyPoint(ctx context.Context, keypointID string) ([]MaterialUsage, error) {
	kp, err := s.GetKeyPoint(ctx, keypointID)
	if err != nil {
		return nil, err
	}
	var citations []string
	if err := json.Unmarshal([]byte(kp.CitationsJSON), &citations); err != nil || len(citations) == 0 {
		return nil, nil
	}
	var out []MaterialUsage
	// 1) 精读文：digest_blocks 引用该重点的 Segment ID。
	digestRows, err := s.DB.QueryContext(ctx,
		`SELECT ed.id, ed.title, MAX(ed.version)
		 FROM episode_digests ed
		 JOIN digest_blocks db ON db.digest_id = ed.id
		 WHERE db.citations_json LIKE ?
		 GROUP BY ed.id, ed.title
		 ORDER BY MAX(ed.version) DESC`,
		"%"+citations[0]+"%")
	if err == nil {
		defer digestRows.Close()
		for digestRows.Next() {
			var id, title string
			var version int
			if err := digestRows.Scan(&id, &title, &version); err == nil {
				out = append(out, MaterialUsage{Kind: "digest", Title: title, Detail: fmt.Sprintf("精读文 v%d", version), Link: "/digest/" + id})
			}
		}
	}
	// 2) 文章：claim_map_entries 的 material_ids_json 包含该 KeyPoint ID。
	articleRows, err := s.DB.QueryContext(ctx,
		`SELECT cme.revision_id, ad.title
		 FROM claim_map_entries cme
		 JOIN article_drafts ad ON ad.id = cme.draft_id
		 WHERE cme.material_ids_json LIKE ?`,
		"%"+keypointID+"%")
	if err == nil {
		defer articleRows.Close()
		for articleRows.Next() {
			var revID, title string
			if err := articleRows.Scan(&revID, &title); err == nil {
				out = append(out, MaterialUsage{Kind: "article", Title: title, Detail: "文章修订", Link: "/workbench"})
			}
		}
	}
	return out, nil
}

// FindUsageByNote 反查一条个人笔记被哪些精读文采用。
func (s *Store) FindUsageByNote(ctx context.Context, noteID string) ([]MaterialUsage, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT ed.id, ed.title, ed.version
		 FROM episode_digests ed
		 JOIN digest_blocks db ON db.digest_id = ed.id
		 WHERE db.note_id = ?
		 ORDER BY ed.version DESC`, noteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MaterialUsage
	seen := map[string]bool{}
	for rows.Next() {
		var id, title string
		var version int
		if err := rows.Scan(&id, &title, &version); err == nil && !seen[id] {
			seen[id] = true
			out = append(out, MaterialUsage{Kind: "digest", Title: title, Detail: fmt.Sprintf("精读文 v%d", version), Link: "/digest/" + id})
		}
	}
	return out, rows.Err()
}

// GetMaterialDiagnosis 读取一轮诊断（R13：候选提升与轮次页渲染）。
func (s *Store) GetMaterialDiagnosis(ctx context.Context, id string) (*models.MaterialDiagnosis, error) {
	v := &models.MaterialDiagnosis{}
	err := s.DB.QueryRowContext(ctx,
		`SELECT id, ideation_session_id, diagnosis_json, material_snapshot_json, created_at
		 FROM material_diagnoses WHERE id=?`, id).
		Scan(&v.ID, &v.IdeationSessionID, &v.DiagnosisJSON, &v.MaterialSnapshotJSON, &v.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return v, nil
}

// PromoteDiagnosisClaim R13：Owner 选中诊断中的建议主张 → 幂等创建关联轮次的
// 提案（proposed_claim，尚不承担 OwnerClaim）。重复提升同一轮同一主张返回既有
// 提案（部分唯一索引兜底并发）；主张引用的材料 ID 保留来历（仍参与后续去重）。
func (s *Store) PromoteDiagnosisClaim(ctx context.Context, sessionID, roundID string, claimIndex int) (*models.CreationProposal, error) {
	round, err := s.GetIdeationRoundByID(ctx, roundID)
	if err != nil {
		return nil, err
	}
	if round.SessionID != sessionID {
		return nil, fmt.Errorf("%w: 轮次不属于该会话", ErrInvalidEditorialState)
	}
	if round.OutputDiagnosisID == "" {
		return nil, fmt.Errorf("%w: 轮次尚无诊断结果", ErrInvalidEditorialState)
	}
	md, err := s.GetMaterialDiagnosis(ctx, round.OutputDiagnosisID)
	if err != nil {
		return nil, err
	}
	var diag struct {
		ProposedClaims []struct {
			Claim       string   `json:"claim"`
			MaterialIDs []string `json:"materialIds"`
		} `json:"proposedClaims"`
	}
	if err := json.Unmarshal([]byte(md.DiagnosisJSON), &diag); err != nil {
		return nil, fmt.Errorf("%w: 诊断结果不可解析", ErrInvalidEditorialState)
	}
	if claimIndex < 0 || claimIndex >= len(diag.ProposedClaims) {
		return nil, fmt.Errorf("%w: 候选序号越界", ErrInvalidEditorialState)
	}
	claim := diag.ProposedClaims[claimIndex]
	claim.Claim = strings.TrimSpace(claim.Claim)
	if claim.Claim == "" {
		return nil, fmt.Errorf("%w: 候选主张为空", ErrInvalidEditorialState)
	}
	// 材料成员校验（R13 复核）：主张引用必须全部落在该轮冻结材料快照内——
	// 任一越界引用都拒绝，不静默改变候选依据（混合合法+非法同样拒绝）。
	allowed := map[string]bool{}
	var snapIDs []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(md.MaterialSnapshotJSON), &snapIDs); err == nil {
		for _, m := range snapIDs {
			if m.ID != "" {
				allowed[m.ID] = true
			}
		}
	}
	for _, id := range claim.MaterialIDs {
		if !allowed[id] {
			return nil, fmt.Errorf("%w: 候选主张引用 %s 不在该轮材料集合内", ErrInvalidEditorialState, id)
		}
	}
	if len(claim.MaterialIDs) == 0 {
		return nil, fmt.Errorf("%w: 候选主张缺少材料依据", ErrInvalidEditorialState)
	}
	materials := claim.MaterialIDs
	// 幂等：同轮同主张已提升过 → 返回既有提案。
	existing := func() (*models.CreationProposal, error) {
		var existingID string
		err := s.DB.QueryRowContext(ctx,
			`SELECT id FROM creation_proposals WHERE ideation_round_id=? AND proposed_claim=?`,
			roundID, claim.Claim).Scan(&existingID)
		if err != nil {
			return nil, err
		}
		return s.GetCreationProposal(ctx, existingID)
	}
	if p, err := existing(); err == nil {
		return p, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	session, err := s.GetIdeationSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	materialsJSON, _ := json.Marshal(materials)
	title := claim.Claim
	if len([]rune(title)) > 40 {
		title = string([]rune(title)[:40])
	}
	proposal, err := s.CreateCreationProposal(ctx, models.CreationProposal{
		EditorialProfileID: session.EditorialProfileID,
		IdeationSessionID:  sessionID,
		IdeationRoundID:    roundID,
		WorkingTitle:       title,
		ProposedClaim:      claim.Claim,
		MaterialIDsJSON:    string(materialsJSON),
		Rationale:          fmt.Sprintf("提升自第 %d 轮诊断候选", round.RoundNo),
	})
	if err != nil && isUniqueConstraintErr(err) {
		// 并发重复提升：唯一索引兜底，复用已插入提案（不报错、不产生第二个提案）。
		return existing()
	}
	return proposal, err
}
