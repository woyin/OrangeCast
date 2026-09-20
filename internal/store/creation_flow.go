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

// CreateCreationBrief creates a reviewable draft; blocking needs are enforced at confirmation.
func (s *Store) CreateCreationBrief(ctx context.Context, v models.CreationBrief) (*models.CreationBrief, error) {
	if v.ID == "" {
		v.ID = uuid.NewString()
	}
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
	// Blocking research needs stop confirmation, not creation of a reviewable draft.
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `INSERT INTO creation_briefs (id,creation_proposal_id,status,owner_claim,claim_plan_json,material_plan_json,research_need_ids_json,outline,style,target_length,current_version,confirmed_version) VALUES (?,?,?,?,?,?,?,?,?,?,1,?)`, v.ID, v.CreationProposalID, v.Status, v.OwnerClaim, v.ClaimPlanJSON, v.MaterialPlanJSON, v.ResearchNeedIDsJSON, v.Outline, v.Style, v.TargetLength, boolToInt(v.Status == "confirmed")); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO creation_brief_revisions (id,brief_id,version,origin_job_id,owner_claim,claim_plan_json,material_plan_json,research_need_ids_json,outline,style,target_length) VALUES (?,?,?,?,?,?,?,?,?,?,?)`, uuid.NewString(), v.ID, 1, "", v.OwnerClaim, v.ClaimPlanJSON, v.MaterialPlanJSON, v.ResearchNeedIDsJSON, v.Outline, v.Style, v.TargetLength); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetCreationBrief(ctx, v.ID)
}

// CreateCreationBriefRevisionCAS appends an immutable Brief revision and moves the
// current pointer only when expectedVersion matches. Editing invalidates prior confirmation.
func (s *Store) CreateCreationBriefRevisionCAS(ctx context.Context, briefID string, expectedVersion int, revision models.CreationBriefRevision) (*models.CreationBriefRevision, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var current int
	if err := tx.QueryRowContext(ctx, `SELECT current_version FROM creation_briefs WHERE id=?`, briefID).Scan(&current); err != nil {
		return nil, err
	}
	if expectedVersion != current {
		return nil, ErrCreationBriefVersionConflict
	}
	if strings.TrimSpace(revision.OwnerClaim) == "" || strings.TrimSpace(revision.Outline) == "" {
		return nil, fmt.Errorf("%w: OwnerClaim 与提纲不能为空", ErrInvalidEditorialState)
	}
	var proposalID, currentPrompt, currentSnapshot, currentMaterialPlan, proposalMaterials string
	if err := tx.QueryRowContext(ctx, `SELECT creation_proposal_id FROM creation_briefs WHERE id=?`, briefID).Scan(&proposalID); err != nil {
		return nil, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT curator_prompt_version,curator_input_snapshot_json,material_plan_json FROM creation_brief_revisions WHERE brief_id=? AND version=?`, briefID, current).Scan(&currentPrompt, &currentSnapshot, &currentMaterialPlan); err != nil {
		return nil, err
	}
	var allowed []string
	if err := tx.QueryRowContext(ctx, `SELECT material_ids_json FROM creation_proposals WHERE id=?`, proposalID).Scan(&proposalMaterials); err != nil {
		return nil, err
	}
	allowedSet := map[string]bool{}
	_ = json.Unmarshal([]byte(proposalMaterials), &allowed)
	for _, id := range allowed {
		allowedSet[id] = true
	}
	selected, rejected, ok := parseMaterialPlan(revision.MaterialPlanJSON)
	if !ok || len(selected) == 0 {
		return nil, fmt.Errorf("%w: selected materials must be a non-empty JSON array/object", ErrInvalidEditorialState)
	}
	seen := map[string]bool{}
	for _, id := range append(append([]string{}, selected...), rejected...) {
		if id == "" || seen[id] || !allowedSet[id] {
			return nil, fmt.Errorf("%w: invalid or intersecting Brief material IDs", ErrInvalidEditorialState)
		}
		seen[id] = true
	}
	if revision.CuratorPromptVersion == "" {
		revision.CuratorPromptVersion = currentPrompt
	}
	if revision.CuratorInputSnapshotJSON == "" {
		revision.CuratorInputSnapshotJSON = currentSnapshot
	}
	next := current + 1
	revision.ID = uuid.NewString()
	revision.BriefID = briefID
	revision.Version = next
	_, err = tx.ExecContext(ctx, `INSERT INTO creation_brief_revisions (id,brief_id,version,origin_job_id,owner_claim,claim_plan_json,material_plan_json,research_need_ids_json,outline,style,target_length,claim_type,unresolved_questions_json,notes,curator_prompt_version,curator_input_snapshot_json) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, revision.ID, briefID, next, revision.OriginJobID, revision.OwnerClaim, revision.ClaimPlanJSON, revision.MaterialPlanJSON, revision.ResearchNeedIDsJSON, revision.Outline, revision.Style, revision.TargetLength, revision.ClaimType, revision.UnresolvedQuestionsJSON, revision.Notes, revision.CuratorPromptVersion, revision.CuratorInputSnapshotJSON)
	if err != nil {
		return nil, err
	}
	update, err := tx.ExecContext(ctx, `UPDATE creation_briefs SET current_version=?,confirmed_version=0,confirmed_at=NULL,status='draft',owner_claim=?,claim_plan_json=?,material_plan_json=?,research_need_ids_json=?,outline=?,style=?,target_length=?,updated_at=datetime('now') WHERE id=? AND current_version=?`, next, revision.OwnerClaim, revision.ClaimPlanJSON, revision.MaterialPlanJSON, revision.ResearchNeedIDsJSON, revision.Outline, revision.Style, revision.TargetLength, briefID, current)
	if err != nil {
		return nil, err
	}
	if n, err := update.RowsAffected(); err != nil {
		return nil, err
	} else if n != 1 {
		return nil, ErrCreationBriefVersionConflict
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &revision, nil
}

// ConfirmCreationBriefVersion confirms exactly the current immutable revision.
func (s *Store) confirmCreationBriefVersionTx(ctx context.Context, tx *sql.Tx, briefID string, version int) (string, error) {
	var current, confirmed int
	var proposalID string
	if err := tx.QueryRowContext(ctx, `SELECT current_version,confirmed_version,creation_proposal_id FROM creation_briefs WHERE id=?`, briefID).Scan(&current, &confirmed, &proposalID); errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	} else if err != nil {
		return "", err
	}
	if current != version {
		return "", ErrCreationBriefVersionConflict
	}
	var materialPlan, ownerClaimRevision, outlineRevision, snapshotJSON string
	if err := tx.QueryRowContext(ctx, `SELECT material_plan_json,owner_claim,outline,curator_input_snapshot_json FROM creation_brief_revisions WHERE brief_id=? AND version=?`, briefID, version).Scan(&materialPlan, &ownerClaimRevision, &outlineRevision, &snapshotJSON); errors.Is(err, sql.ErrNoRows) {
		return "", ErrCreationBriefVersionConflict
	} else if err != nil {
		return "", err
	}
	var proposalStatus, ownerClaim string
	if err := tx.QueryRowContext(ctx, `SELECT status,COALESCE(owner_claim,'') FROM creation_proposals WHERE id=?`, proposalID).Scan(&proposalStatus, &ownerClaim); err != nil {
		return "", err
	}
	if proposalStatus != "accepted" || strings.TrimSpace(ownerClaim) == "" || strings.TrimSpace(ownerClaimRevision) == "" || strings.TrimSpace(outlineRevision) == "" {
		return "", fmt.Errorf("%w: exact accepted revision prerequisites failed", ErrInvalidEditorialState)
	}
	var blocked int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM research_needs WHERE creation_proposal_id=? AND severity='blocking' AND status!='resolved'`, proposalID).Scan(&blocked); err != nil {
		return "", err
	}
	if blocked > 0 {
		return "", fmt.Errorf("%w: blocking research need unresolved", ErrInvalidEditorialState)
	}
	if err := s.recheckBriefRevisionTx(ctx, tx, proposalID, ownerClaimRevision, materialPlan, snapshotJSON); err != nil {
		return "", err
	}
	res, err := tx.ExecContext(ctx, `UPDATE creation_briefs SET status='confirmed',confirmed_version=?,confirmed_at=datetime('now'),updated_at=datetime('now') WHERE id=? AND current_version=?`, version, briefID, version)
	if err != nil {
		return "", err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return "", ErrCreationBriefVersionConflict
	}
	return proposalID, nil
}

func (s *Store) ConfirmCreationBriefVersion(ctx context.Context, briefID string, version int) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := s.confirmCreationBriefVersionTx(ctx, tx, briefID, version); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) GetCreationBrief(ctx context.Context, id string) (*models.CreationBrief, error) {
	v := &models.CreationBrief{}
	var confirmed sql.NullString
	err := s.DB.QueryRowContext(ctx, `SELECT id,creation_proposal_id,status,owner_claim,claim_plan_json,material_plan_json,research_need_ids_json,outline,style,target_length,confirmed_at,created_at,updated_at,current_version,confirmed_version FROM creation_briefs WHERE id=?`, id).Scan(&v.ID, &v.CreationProposalID, &v.Status, &v.OwnerClaim, &v.ClaimPlanJSON, &v.MaterialPlanJSON, &v.ResearchNeedIDsJSON, &v.Outline, &v.Style, &v.TargetLength, &confirmed, &v.CreatedAt, &v.UpdatedAt, &v.CurrentVersion, &v.ConfirmedVersion)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if confirmed.Valid {
		value := confirmed.String
		v.ConfirmedAt = &value
	}
	// R17 真源：旧 brief 内容列只兼容保留；current_version 对应的不可变 revision
	// 投影到读取对象，避免刷新仍显示 Curator 前的占位内容。
	rev, revErr := s.GetCreationBriefRevision(ctx, id, v.CurrentVersion)
	if revErr != nil {
		return nil, revErr
	}
	v.OwnerClaim, v.ClaimPlanJSON, v.MaterialPlanJSON = rev.OwnerClaim, rev.ClaimPlanJSON, rev.MaterialPlanJSON
	v.ResearchNeedIDsJSON, v.Outline, v.Style = rev.ResearchNeedIDsJSON, rev.Outline, rev.Style
	v.TargetLength = rev.TargetLength
	v.ClaimType, v.UnresolvedQuestionsJSON, v.Notes = rev.ClaimType, rev.UnresolvedQuestionsJSON, rev.Notes
	v.SelectedMaterialIDsJSON, v.RejectedMaterialIDsJSON = materialPlanParts(rev.MaterialPlanJSON)
	return v, nil
}

// ListCreationBriefs lists a profile's current and historical creation contracts.
func (s *Store) ListCreationBriefs(ctx context.Context, profileID string) ([]*models.CreationBrief, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT b.id FROM creation_briefs b JOIN creation_proposals p ON p.id=b.creation_proposal_id WHERE p.editorial_profile_id=? ORDER BY b.updated_at DESC,b.id DESC`, profileID)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	out := make([]*models.CreationBrief, 0, len(ids))
	for _, id := range ids {
		v, err := s.GetCreationBrief(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// EnqueueCuratorBriefJob creates a durable, idempotent Curator task for an accepted
// CreationProposal. The caller supplies a frozen input snapshot and selected brief.
func (s *Store) EnqueueCuratorBriefJob(ctx context.Context, proposalID, briefID, inputSnapshotJSON, providerName, modelName string) (*models.ProcessingJob, error) {
	return s.enqueueCuratorBriefJob(ctx, proposalID, briefID, inputSnapshotJSON, providerName, modelName)
}

func (s *Store) enqueueCuratorBriefJob(ctx context.Context, proposalID, briefID, inputSnapshotJSON, providerName, modelName string) (*models.ProcessingJob, error) {
	intent := "curator_brief:" + proposalID
	var existingID, status string
	if err := s.DB.QueryRowContext(ctx, `SELECT id,status FROM processing_jobs WHERE intent_id=? AND job_type=? AND source_id=? ORDER BY created_at DESC LIMIT 1`, intent, string(models.JobCuratorBrief), proposalID).Scan(&existingID, &status); err == nil && status == string(models.StatusSucceeded) {
		return nil, nil // 成功后的接受重放不生成第二个 Curator job/revision
	} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	job, _, err := s.EnqueueJobIdempotent(ctx, JobIntentSpec{SourceType: models.SourceEpisode, SourceID: proposalID, JobType: models.JobCuratorBrief, IntentID: intent, InputSnapshotJSON: inputSnapshotJSON, ConfiguredProvider: providerName, ConfiguredModel: modelName, ConfigVersion: provider.CuratorPromptVersion})
	return job, err
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
	err = s.DB.QueryRowContext(ctx, `SELECT id FROM creation_briefs WHERE creation_proposal_id=? ORDER BY created_at DESC LIMIT 1`, proposalID).Scan(&existing)
	if err == nil {
		return s.GetCreationBrief(ctx, existing)
	}
	if err != sql.ErrNoRows {
		return nil, err
	}
	claimPlan, _ := json.Marshal(map[string]any{"thesis": proposal.OwnerClaim, "claim_type": "", "unresolved_questions": []string{}})
	materialPlan, _ := json.Marshal(map[string]any{"selected": materialIDs, "rejected": []string{}})
	brief, err := s.CreateCreationBrief(ctx, models.CreationBrief{ID: "curator-brief:" + proposalID, CreationProposalID: proposalID, OwnerClaim: proposal.OwnerClaim, ClaimPlanJSON: string(claimPlan), MaterialPlanJSON: string(materialPlan)})
	if err != nil && isUniqueConstraintErr(err) {
		return s.GetCreationBrief(ctx, "curator-brief:"+proposalID)
	}
	return brief, err
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
	r, err := s.DB.ExecContext(ctx, `UPDATE creation_briefs SET status='confirmed',confirmed_version=current_version,confirmed_at=datetime('now'),updated_at=datetime('now') WHERE id=? AND status IN ('draft','needs_review')`, id)
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
func (s *Store) recheckBriefMaterialsForPlan(ctx context.Context, proposalID, materialPlanJSON string) error {
	proposal, err := s.GetCreationProposal(ctx, proposalID)
	if err != nil {
		return err
	}
	ids := mustProposalMaterialIDs(materialPlanJSON)
	if len(ids) == 0 {
		ids = mustProposalMaterialIDs(proposal.MaterialIDsJSON)
	}
	return s.recheckKeyPointMaterialIDs(ctx, ids)
}

func mustProposalMaterialIDs(raw string) []string {
	var ids []string
	if json.Unmarshal([]byte(raw), &ids) == nil {
		return ids
	}
	var object struct {
		Selected []string `json:"selected"`
	}
	_ = json.Unmarshal([]byte(raw), &object)
	return object.Selected
}

func (s *Store) recheckKeyPointMaterialIDs(ctx context.Context, materialIDs []string) error {
	for _, id := range materialIDs {
		kp, err := s.GetKeyPoint(ctx, id)
		if err != nil {
			return fmt.Errorf("%w: Brief 材料不存在", ErrInvalidEditorialState)
		}
		if kp.QualityStatus != models.KeyPointReady && kp.QualityStatus != models.KeyPointOwnerConfirmed {
			return fmt.Errorf("%w: Brief 材料质量未达标", ErrInvalidEditorialState)
		}
		if kp.StaleAt != "" || kp.EvidenceStatus == "stale" {
			return fmt.Errorf("%w: Brief 材料已陈旧", ErrInvalidEditorialState)
		}
		var excluded int
		if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM editorial_relevance WHERE keypoint_id=? AND owner_override='excluded'`, id).Scan(&excluded); err != nil {
			return err
		}
		if excluded > 0 {
			return fmt.Errorf("%w: Brief 材料已被 Owner 排除", ErrInvalidEditorialState)
		}
	}
	return nil
}

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
// JSON 损坏不静默降级：material_ids/citation_refs 解析失败返回带上下文错误
// （入队/读取/导出路径都不得基于部分数据继续）。
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

func (s *Store) recheckBriefMaterialsForPlanTx(ctx context.Context, tx *sql.Tx, proposalID, materialPlanJSON string) error {
	ids := mustProposalMaterialIDs(materialPlanJSON)
	if len(ids) == 0 {
		var raw string
		if err := tx.QueryRowContext(ctx, `SELECT material_ids_json FROM creation_proposals WHERE id=?`, proposalID).Scan(&raw); err != nil {
			return err
		}
		ids = mustProposalMaterialIDs(raw)
	}
	for _, id := range ids {
		var quality, evidence, stale string
		if err := tx.QueryRowContext(ctx, `SELECT quality_status,evidence_status,COALESCE(stale_at,'') FROM keypoint_index WHERE id=?`, id).Scan(&quality, &evidence, &stale); err != nil {
			return fmt.Errorf("%w: Brief 材料不存在", ErrInvalidEditorialState)
		}
		if quality != string(models.KeyPointReady) && quality != string(models.KeyPointOwnerConfirmed) {
			return fmt.Errorf("%w: Brief 材料质量未达标", ErrInvalidEditorialState)
		}
		if stale != "" || evidence == "stale" {
			return fmt.Errorf("%w: Brief 材料已陈旧", ErrInvalidEditorialState)
		}
		var excluded int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM editorial_relevance WHERE keypoint_id=? AND owner_override='excluded'`, id).Scan(&excluded); err != nil {
			return err
		}
		if excluded > 0 {
			return fmt.Errorf("%w: Brief 材料已被 Owner 排除", ErrInvalidEditorialState)
		}
	}
	return nil
}

// GetCreationBriefRevision reads one immutable Brief revision.
func (s *Store) GetCreationBriefRevision(ctx context.Context, briefID string, version int) (*models.CreationBriefRevision, error) {
	r := &models.CreationBriefRevision{}
	err := s.DB.QueryRowContext(ctx, `SELECT id,brief_id,version,origin_job_id,owner_claim,claim_plan_json,material_plan_json,research_need_ids_json,outline,style,target_length,claim_type,unresolved_questions_json,notes,curator_prompt_version,curator_input_snapshot_json,created_at FROM creation_brief_revisions WHERE brief_id=? AND version=?`, briefID, version).
		Scan(&r.ID, &r.BriefID, &r.Version, &r.OriginJobID, &r.OwnerClaim, &r.ClaimPlanJSON, &r.MaterialPlanJSON, &r.ResearchNeedIDsJSON, &r.Outline, &r.Style, &r.TargetLength, &r.ClaimType, &r.UnresolvedQuestionsJSON, &r.Notes, &r.CuratorPromptVersion, &r.CuratorInputSnapshotJSON, &r.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return r, nil
}

// ApplyCuratorResultRevision persists one Curator result exactly once per durable job.
// Existing origin_job_id is replay-safe; concurrent edits produce a Brief CAS conflict.
func (s *Store) ApplyCuratorResultRevision(ctx context.Context, briefID, jobID string, expectedVersion int, revision models.CreationBriefRevision) (*models.CreationBriefRevision, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var existing models.CreationBriefRevision
	err = tx.QueryRowContext(ctx, `SELECT id,brief_id,version,origin_job_id,owner_claim,claim_plan_json,material_plan_json,research_need_ids_json,outline,style,target_length,claim_type,unresolved_questions_json,notes,curator_prompt_version,curator_input_snapshot_json,created_at FROM creation_brief_revisions WHERE brief_id=? AND origin_job_id=?`, briefID, jobID).Scan(&existing.ID, &existing.BriefID, &existing.Version, &existing.OriginJobID, &existing.OwnerClaim, &existing.ClaimPlanJSON, &existing.MaterialPlanJSON, &existing.ResearchNeedIDsJSON, &existing.Outline, &existing.Style, &existing.TargetLength, &existing.ClaimType, &existing.UnresolvedQuestionsJSON, &existing.Notes, &existing.CuratorPromptVersion, &existing.CuratorInputSnapshotJSON, &existing.CreatedAt)
	if err == nil {
		return &existing, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	var current int
	if err := tx.QueryRowContext(ctx, `SELECT current_version FROM creation_briefs WHERE id=?`, briefID).Scan(&current); err != nil {
		return nil, err
	}
	if expectedVersion != current {
		return nil, ErrCreationBriefVersionConflict
	}
	revision.ID, revision.BriefID, revision.Version, revision.OriginJobID = uuid.NewString(), briefID, current+1, jobID
	_, err = tx.ExecContext(ctx, `INSERT INTO creation_brief_revisions (id,brief_id,version,origin_job_id,owner_claim,claim_plan_json,material_plan_json,research_need_ids_json,outline,style,target_length,claim_type,unresolved_questions_json,notes,curator_prompt_version,curator_input_snapshot_json) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, revision.ID, briefID, revision.Version, jobID, revision.OwnerClaim, revision.ClaimPlanJSON, revision.MaterialPlanJSON, revision.ResearchNeedIDsJSON, revision.Outline, revision.Style, revision.TargetLength, revision.ClaimType, revision.UnresolvedQuestionsJSON, revision.Notes, revision.CuratorPromptVersion, revision.CuratorInputSnapshotJSON)
	if err != nil {
		if isUniqueConstraintErr(err) {
			return nil, ErrCreationBriefVersionConflict
		}
		return nil, err
	}
	res, err := tx.ExecContext(ctx, `UPDATE creation_briefs SET current_version=?,confirmed_version=0,confirmed_at=NULL,status='draft',updated_at=datetime('now') WHERE id=? AND current_version=?`, revision.Version, briefID, current)
	if err != nil {
		return nil, err
	}
	if n, err := res.RowsAffected(); err != nil {
		return nil, err
	} else if n != 1 {
		return nil, ErrCreationBriefVersionConflict
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &revision, nil
}

func materialPlanParts(raw string) (string, string) {
	var ids []string
	if json.Unmarshal([]byte(raw), &ids) == nil {
		b, _ := json.Marshal(ids)
		return string(b), "[]"
	}
	var object struct {
		Selected []string `json:"selected"`
		Rejected []string `json:"rejected"`
	}
	if json.Unmarshal([]byte(raw), &object) != nil {
		return "[]", "[]"
	}
	sel, _ := json.Marshal(object.Selected)
	rej, _ := json.Marshal(object.Rejected)
	return string(sel), string(rej)
}

func parseMaterialPlan(raw string) (selected, rejected []string, ok bool) {
	var ids []string
	if json.Unmarshal([]byte(raw), &ids) == nil {
		return ids, nil, true
	}
	var object struct {
		Selected []string `json:"selected"`
		Rejected []string `json:"rejected"`
	}
	if json.Unmarshal([]byte(raw), &object) != nil {
		return nil, nil, false
	}
	return object.Selected, object.Rejected, true
}

func (s *Store) recheckBriefRevisionTx(ctx context.Context, tx *sql.Tx, proposalID, ownerClaim, materialPlan, snapshotJSON string) error {
	selected, _, ok := parseMaterialPlan(materialPlan)
	if !ok || len(selected) == 0 {
		return fmt.Errorf("%w: revision selected materials required", ErrInvalidEditorialState)
	}
	var allowedRaw string
	if err := tx.QueryRowContext(ctx, `SELECT material_ids_json FROM creation_proposals WHERE id=?`, proposalID).Scan(&allowedRaw); err != nil {
		return err
	}
	var allowed []string
	_ = json.Unmarshal([]byte(allowedRaw), &allowed)
	set := map[string]bool{}
	for _, id := range allowed {
		set[id] = true
	}
	for _, id := range selected {
		if !set[id] {
			return fmt.Errorf("%w: revision material outside proposal candidates", ErrInvalidEditorialState)
		}
	}
	var snap struct {
		Provider  string `json:"provider"`
		Materials []struct {
			ID          string            `json:"keyPointId"`
			SourceType  models.SourceType `json:"sourceType"`
			SourceID    string            `json:"sourceId"`
			CardVersion int               `json:"cardVersion"`
		} `json:"materials"`
	}
	if err := json.Unmarshal([]byte(snapshotJSON), &snap); err != nil {
		return fmt.Errorf("%w: revision Curator snapshot invalid", ErrInvalidEditorialState)
	}
	for _, id := range selected {
		var sourceType, sourceID string
		var frozenCardVersion int
		for _, m := range snap.Materials {
			if m.ID == id {
				sourceType, sourceID, frozenCardVersion = string(m.SourceType), m.SourceID, m.CardVersion
			}
		}
		if sourceType == "" || sourceID == "" {
			return fmt.Errorf("%w: selected material missing frozen source", ErrInvalidEditorialState)
		}
		if sourceType != string(models.SourceEpisode) && sourceType != string(models.SourceUpload) && sourceType != string(models.SourceDocument) {
			return fmt.Errorf("%w: frozen source type invalid", ErrInvalidEditorialState)
		}
		var actualType, actualSource string
		var actualCardVersion int
		if err := tx.QueryRowContext(ctx, `SELECT source_type,source_id,card_version FROM keypoint_index WHERE id=?`, id).Scan(&actualType, &actualSource, &actualCardVersion); err != nil {
			return fmt.Errorf("%w: selected KeyPoint missing", ErrInvalidEditorialState)
		}
		if actualType != sourceType || actualSource != sourceID || (frozenCardVersion > 0 && actualCardVersion != frozenCardVersion) {
			return fmt.Errorf("%w: selected material source/version differs from frozen snapshot", ErrInvalidEditorialState)
		}
		var quality, evidence, stale string
		if err := tx.QueryRowContext(ctx, `SELECT quality_status,evidence_status,COALESCE(stale_at,'') FROM keypoint_index WHERE id=?`, id).Scan(&quality, &evidence, &stale); err != nil {
			return err
		}
		if quality != string(models.KeyPointReady) && quality != string(models.KeyPointOwnerConfirmed) || evidence == "stale" || stale != "" {
			return fmt.Errorf("%w: selected material stale or not ready", ErrInvalidEditorialState)
		}
		var excluded int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM editorial_relevance WHERE keypoint_id=? AND owner_override='excluded'`, id).Scan(&excluded); err != nil {
			return err
		}
		if excluded > 0 {
			return fmt.Errorf("%w: selected material excluded", ErrInvalidEditorialState)
		}
		var archived *string
		var policy, approvedJSON string
		if err := tx.QueryRowContext(ctx, fmt.Sprintf(`SELECT archived_at,model_data_policy,approved_providers_json FROM %s WHERE id=?`, sourceTable(models.SourceType(sourceType))), sourceID).Scan(&archived, &policy, &approvedJSON); err != nil {
			return fmt.Errorf("%w: frozen source unavailable", ErrInvalidEditorialState)
		}
		if archived != nil {
			return fmt.Errorf("%w: frozen source archived", ErrInvalidEditorialState)
		}
		switch models.ModelDataPolicy(policy) {
		case models.ModelDataExternalAllowed:
		case models.ModelDataLocalOnly:
			return fmt.Errorf("%w: frozen Curator provider policy denied", ErrInvalidEditorialState)
		case models.ModelDataApprovedProvidersOnly:
			var approved []string
			if err := json.Unmarshal([]byte(approvedJSON), &approved); err != nil {
				return err
			}
			matched := false
			for _, name := range approved {
				if strings.EqualFold(strings.TrimSpace(name), strings.TrimSpace(snap.Provider)) && strings.TrimSpace(snap.Provider) != "" {
					matched = true
					break
				}
			}
			if !matched {
				return fmt.Errorf("%w: frozen Curator provider not approved", ErrInvalidEditorialState)
			}
		default:
			return fmt.Errorf("%w: unknown source model data policy", ErrInvalidEditorialState)
		}
	}
	return nil
}

// ---- R19：持久 Writer 入队 ----

// ClaimWritingTaskInput 是入队时冻结的强类型完整写作输入：确认 Brief 精确版本、
// 全部文章桥接身份（link/proposal/article proposal/article brief）、草稿/画像、受众、
// Owner 主张、提纲/风格/篇幅、不可变 Curator 快照中的完整 ArticleMaterial（来源类型/
// ID/卡片版本/内容/引用）与 Provider/模型/提示词版本。运行时只消费该快照，
// 不重读可变的 Brief/Proposal/KeyPoint。
type ClaimWritingTaskInput struct {
	CreationBriefID       string                     `json:"creation_brief_id"`
	BriefVersion          int                        `json:"brief_version"`
	CreationProposalID    string                     `json:"creation_proposal_id"`
	CreationArticleLinkID string                     `json:"creation_article_link_id"`
	ArticleProposalID     string                     `json:"article_proposal_id"`
	ArticleBriefID        string                     `json:"article_brief_id"`
	DraftID               string                     `json:"draft_id"`
	ProfileID             string                     `json:"profile_id"`
	Audience              string                     `json:"audience"`
	OwnerClaim            string                     `json:"owner_claim"`
	Outline               string                     `json:"outline"`
	Style                 string                     `json:"style"`
	TargetLength          *int                       `json:"target_length"`
	Materials             []provider.ArticleMaterial `json:"materials"`
	Provider              string                     `json:"provider"`
	Model                 string                     `json:"model"`
	PromptVersion         string                     `json:"prompt_version"`
}

// EnqueueClaimWritingForCreationBrief 兼容 wrapper（内部用）：以当前确认版本为目标
// 调用版本化入队。生产 HTTP 路径必须使用带 expected_version 的版本化入口。
func (s *Store) EnqueueClaimWritingForCreationBrief(ctx context.Context, briefID string) (*models.ProcessingJob, error) {
	version, err := s.currentConfirmedBriefVersion(ctx, briefID)
	if err != nil {
		return nil, err
	}
	return s.EnqueueClaimWritingForCreationBriefVersion(ctx, briefID, version)
}

// EnqueueClaimWritingForCreationBriefVersion 在同一个 DB 事务中完成：expected==
// current==confirmed 的精确版本验证、link version 匹配、读取不可变 Brief revision
// 与其冻结 Curator 材料快照（不重读可变 keypoint_index 正文）、创建/复用 Writer 专属
// ArticleDraft（claim_writing_drafts 映射）、冻结强类型输入、持久化 job 与
// claim_writing_intents 意图。并发/完成态重复点击由 claim_writing_intents 主键幂等
// 复用同一 draft+job，不使用进程锁。版本过期返回 ErrCreationBriefVersionConflict。
func (s *Store) EnqueueClaimWritingForCreationBriefVersion(ctx context.Context, briefID string, expectedVersion int) (*models.ProcessingJob, error) {
	if strings.TrimSpace(briefID) == "" {
		return nil, fmt.Errorf("%w: creation_brief_id required", ErrInvalidEditorialState)
	}
	if expectedVersion <= 0 {
		return nil, fmt.Errorf("%w: expected_version required", ErrCreationBriefVersionConflict)
	}
	job, err := s.enqueueClaimWritingOnce(ctx, briefID, expectedVersion)
	if err == nil {
		return job, nil
	}
	// 并发竞态：意图或映射唯一约束冲突 → 复用已存在的任务，不产生第二个 job。
	if isUniqueConstraintErr(err) {
		return s.getClaimWritingIntentJob(ctx, claimWritingIntentID(briefID, expectedVersion))
	}
	return nil, err
}

// claimWritingIntentID 写作意图身份：同一 Brief 的同一确认版本永远对应同一个
// 持久任务；确认新版本后（编辑→重新确认）允许再次生成。任务完成后重复点击
// 仍复用同一 job（claim_writing_intents 兜底，不依赖 processing_jobs 活跃约束）。
func claimWritingIntentID(briefID string, version int) string {
	return fmt.Sprintf("claim_writing:%s:v%d", briefID, version)
}

// currentConfirmedBriefVersion 读取当前精确确认版本；非精确确认状态返回错误。
func (s *Store) currentConfirmedBriefVersion(ctx context.Context, briefID string) (int, error) {
	var current, confirmed int
	err := s.DB.QueryRowContext(ctx, `SELECT current_version,confirmed_version FROM creation_briefs WHERE id=? AND status='confirmed'`, briefID).Scan(&current, &confirmed)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, err
	}
	if confirmed == 0 || current != confirmed {
		return 0, fmt.Errorf("%w: exact confirmed Brief required", ErrInvalidEditorialState)
	}
	return confirmed, nil
}

// getClaimWritingIntentJob 由意图身份读取复用的持久任务。
func (s *Store) getClaimWritingIntentJob(ctx context.Context, intentID string) (*models.ProcessingJob, error) {
	var jobID string
	err := s.DB.QueryRowContext(ctx, `SELECT job_id FROM claim_writing_intents WHERE intent_id=?`, intentID).Scan(&jobID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return s.GetJob(ctx, jobID)
}

// scanProcessingJobRow 在给定 queryer（含打开的事务）上读取一条完整任务行，
// 避免事务打开期间跨连接回读造成死锁（测试库常为单连接）。
func scanProcessingJobRow(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, jobID string) (*models.ProcessingJob, error) {
	j := &models.ProcessingJob{}
	err := q.QueryRowContext(ctx,
		`SELECT id, source_type, source_id, job_type, status, attempt_count, last_error, lease_until, heartbeat_at, is_automated, created_at, updated_at
		 FROM processing_jobs WHERE id = ?`, jobID).
		Scan(&j.ID, &j.SourceType, &j.SourceID, &j.JobType, &j.Status, &j.AttemptCount, &j.LastError, &j.LeaseUntil, &j.HeartbeatAt, &j.Automated, &j.CreatedAt, &j.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return j, nil
}

func (s *Store) enqueueClaimWritingOnce(ctx context.Context, briefID string, expectedVersion int) (*models.ProcessingJob, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	// 1) 精确 confirmed + expected 验证：expected=current=confirmed 才可生成。
	var proposalID string
	var currentVersion, confirmedVersion int
	var status string
	err = tx.QueryRowContext(ctx, `SELECT creation_proposal_id,current_version,confirmed_version,status FROM creation_briefs WHERE id=?`, briefID).Scan(&proposalID, &currentVersion, &confirmedVersion, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if status != "confirmed" || confirmedVersion == 0 {
		return nil, fmt.Errorf("%w: exact confirmed Brief required", ErrInvalidEditorialState)
	}
	if expectedVersion != currentVersion || expectedVersion != confirmedVersion {
		return nil, fmt.Errorf("%w: expected v%d, current v%d, confirmed v%d", ErrCreationBriefVersionConflict, expectedVersion, currentVersion, confirmedVersion)
	}
	intentID := claimWritingIntentID(briefID, confirmedVersion)
	// 完成态重复点击：意图已持久 → 直接复用同一 job（processing_jobs 的部分唯一
	// 索引只约束活跃任务，这里由 claim_writing_intents 兜底）。
	var existingJobID string
	switch err := tx.QueryRowContext(ctx, `SELECT job_id FROM claim_writing_intents WHERE intent_id=?`, intentID).Scan(&existingJobID); {
	case err == nil:
		// 同事务内回读任务行（不跨连接，避免单连接库死锁）。
		job, gerr := scanProcessingJobRow(ctx, tx, existingJobID)
		if gerr != nil {
			return nil, gerr
		}
		return job, nil
	case errors.Is(err, sql.ErrNoRows):
		// 首次入队，继续。
	default:
		return nil, err
	}

	// 2) 持久文章链接：冻结全部精确桥接 ID；link 版本必须与确认版本一致。
	var linkID, articleProposalID, articleBriefID string
	var linkVersion int
	err = tx.QueryRowContext(ctx, `SELECT id,article_proposal_id,article_brief_id,creation_brief_version FROM creation_article_links WHERE creation_brief_id=?`, briefID).Scan(&linkID, &articleProposalID, &articleBriefID, &linkVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: confirmed Brief 缺少持久文章链接", ErrInvalidEditorialState)
	}
	if err != nil {
		return nil, err
	}
	if linkVersion != confirmedVersion {
		return nil, fmt.Errorf("%w: 文章链接版本 %d 与确认版本 %d 不一致", ErrInvalidEditorialState, linkVersion, confirmedVersion)
	}

	// 3) Proposal 保持 accepted，读取画像与受众（同一事务内一致读）。
	var profileID, audience string
	err = tx.QueryRowContext(ctx, `SELECT editorial_profile_id,COALESCE(audience,'') FROM creation_proposals WHERE id=? AND status='accepted'`, proposalID).Scan(&profileID, &audience)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: creation proposal 不再是 accepted", ErrInvalidEditorialState)
	}
	if err != nil {
		return nil, err
	}

	// 4) 创建或复用 Writer 专属草稿（claim_writing_drafts 映射主键保证单草稿；
	// 历史重复草稿不受影响，也不依赖 article_drafts 全局唯一索引）。
	draftID, err := findOrCreateWriterDraftTx(ctx, tx, articleBriefID)
	if err != nil {
		return nil, err
	}

	// 5) 读取不可变 Brief revision；授权材料只来自 revision 冻结的 Curator 输入
	// 快照（完整 ArticleMaterial），按 material plan selected IDs 保序筛选。
	// 不读取可变 keypoint_index 正文：确认后卡片/内容变化不会静默进入授权输入。
	rev, err := readCreationBriefRevisionTx(ctx, tx, briefID, confirmedVersion)
	if err != nil {
		return nil, err
	}
	selected, _, ok := parseMaterialPlan(rev.MaterialPlanJSON)
	if !ok || len(selected) == 0 {
		return nil, fmt.Errorf("%w: selected materials required", ErrInvalidEditorialState)
	}
	var snap struct {
		Provider  string                     `json:"provider"`
		Materials []provider.ArticleMaterial `json:"materials"`
	}
	if err := json.Unmarshal([]byte(rev.CuratorInputSnapshotJSON), &snap); err != nil {
		return nil, fmt.Errorf("%w: 冻结 Curator 材料快照不可解析: %w", ErrInvalidEditorialState, err)
	}
	byID := make(map[string]provider.ArticleMaterial, len(snap.Materials))
	for _, m := range snap.Materials {
		byID[m.KeyPointID] = m
	}
	materials := make([]provider.ArticleMaterial, 0, len(selected))
	for _, id := range selected {
		m, found := byID[id]
		if !found {
			return nil, fmt.Errorf("%w: 选中材料 %s 不在确认快照内", ErrInvalidEditorialState, id)
		}
		if m.SourceType == "" || m.SourceID == "" || m.CardVersion <= 0 ||
			strings.TrimSpace(m.SourceTitle) == "" || strings.TrimSpace(m.Content) == "" || len(m.Citations) == 0 {
			return nil, fmt.Errorf("%w: 冻结材料 %s 身份/内容/引用不完整", ErrInvalidEditorialState, id)
		}
		materials = append(materials, m)
	}

	// 6) 冻结 Writer 配置（R04）：读取当前 settings 的 Writer 角色并写入快照。
	var writerProvider, writerModel string
	err = tx.QueryRowContext(ctx, `SELECT COALESCE(writer_provider,''),COALESCE(writer_model,'') FROM settings WHERE id=1`).Scan(&writerProvider, &writerModel)
	if errors.Is(err, sql.ErrNoRows) {
		writerProvider, writerModel = "", ""
	} else if err != nil {
		return nil, err
	}
	if writerProvider == "" {
		writerProvider = "groq"
	}
	model := provider.EffectiveModel(writerProvider, writerModel, string(models.JobClaimWriting))
	input := ClaimWritingTaskInput{
		CreationBriefID: briefID, BriefVersion: confirmedVersion,
		CreationProposalID: proposalID, CreationArticleLinkID: linkID,
		ArticleProposalID: articleProposalID, ArticleBriefID: articleBriefID,
		DraftID: draftID, ProfileID: profileID, Audience: audience,
		OwnerClaim: rev.OwnerClaim, Outline: rev.Outline, Style: rev.Style, TargetLength: rev.TargetLength,
		Materials: materials, Provider: writerProvider, Model: model, PromptVersion: provider.ClaimWriterPromptVersion,
	}
	inputJSON, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("冻结写作输入快照: %w", err)
	}

	// 7) 持久化 job 与意图（同事务）：configured_* 直接写入，R04 配置冻结不回读设置。
	jobID := uuid.NewString()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO processing_jobs
		   (id, source_type, source_id, job_type, status, is_automated,
		    intent_id, input_snapshot_json, config_version, configured_provider, configured_model)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		jobID, string(models.SourceEpisode), draftID, string(models.JobClaimWriting), string(models.StatusQueued), 0,
		intentID, string(inputJSON), provider.ClaimWriterPromptVersion, writerProvider, model); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO claim_writing_intents (intent_id, job_id, draft_id) VALUES (?,?,?)`,
		intentID, jobID, draftID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &models.ProcessingJob{ID: jobID, SourceType: models.SourceEpisode, SourceID: draftID, JobType: models.JobClaimWriting, Status: models.StatusQueued}, nil
}

// findOrCreateWriterDraftTx 复用或建立 Writer 专属草稿映射（claim_writing_drafts，
// article_brief_id 主键）。历史遗留的同 Brief 多草稿保持原样：映射只决定新写入路径
// 使用哪个草稿；并发建立由映射主键兜底，冲突时回读。
func findOrCreateWriterDraftTx(ctx context.Context, tx *sql.Tx, articleBriefID string) (string, error) {
	var mapped string
	err := tx.QueryRowContext(ctx, `SELECT draft_id FROM claim_writing_drafts WHERE article_brief_id=?`, articleBriefID).Scan(&mapped)
	if err == nil {
		// 映射存在：确认草稿行仍可读后复用（同事务内，不跨连接）。
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM article_drafts WHERE id=?`, mapped).Scan(&n); err != nil {
			return "", err
		}
		if n == 1 {
			return mapped, nil
		}
		return "", fmt.Errorf("%w: Writer 草稿映射指向缺失的草稿", ErrInvalidEditorialState)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	var profileID, title string
	if err := tx.QueryRowContext(ctx, `SELECT p.editorial_profile_id,COALESCE(b.thesis,'') FROM article_briefs b JOIN article_proposals p ON p.id=b.proposal_id WHERE b.id=?`, articleBriefID).Scan(&profileID, &title); err != nil {
		return "", err
	}
	draftID := uuid.NewString()
	if _, err := tx.ExecContext(ctx, `INSERT INTO article_drafts (id, editorial_profile_id, brief_id, title, status) VALUES (?,?,?,?,?)`, draftID, profileID, articleBriefID, title, "drafting"); err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO claim_writing_drafts (article_brief_id, draft_id) VALUES (?,?)`, articleBriefID, draftID); err != nil {
		if isUniqueConstraintErr(err) {
			// 并发建立：回读胜出映射（同事务内）。
			var winner string
			if qerr := tx.QueryRowContext(ctx, `SELECT draft_id FROM claim_writing_drafts WHERE article_brief_id=?`, articleBriefID).Scan(&winner); qerr != nil {
				return "", qerr
			}
			return winner, nil
		}
		return "", err
	}
	return draftID, nil
}

// readCreationBriefRevisionTx 在事务内读取一个不可变 Brief revision（含冻结的
// Curator 输入快照，作为唯一授权材料来源）。
func readCreationBriefRevisionTx(ctx context.Context, tx *sql.Tx, briefID string, version int) (*models.CreationBriefRevision, error) {
	r := &models.CreationBriefRevision{}
	err := tx.QueryRowContext(ctx, `SELECT id,brief_id,version,owner_claim,claim_plan_json,material_plan_json,outline,style,target_length,curator_input_snapshot_json FROM creation_brief_revisions WHERE brief_id=? AND version=?`, briefID, version).
		Scan(&r.ID, &r.BriefID, &r.Version, &r.OwnerClaim, &r.ClaimPlanJSON, &r.MaterialPlanJSON, &r.Outline, &r.Style, &r.TargetLength, &r.CuratorInputSnapshotJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: confirmed Brief revision missing", ErrInvalidEditorialState)
	}
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(r.OwnerClaim) == "" || strings.TrimSpace(r.Outline) == "" {
		return nil, fmt.Errorf("%w: confirmed Brief revision incomplete", ErrInvalidEditorialState)
	}
	if strings.TrimSpace(r.CuratorInputSnapshotJSON) == "" {
		return nil, fmt.Errorf("%w: confirmed Brief revision 缺少冻结材料快照", ErrInvalidEditorialState)
	}
	return r, nil
}

// MapCreationBriefsToDrafts 返回画像内 creation_brief_id → Writer/最新草稿 ID 的
// 映射（工作台"打开文章"下一步）。优先 Writer 专属映射草稿，否则回退该兼容 Brief
// 下最早的草稿（历史重复草稿兼容）。
func (s *Store) MapCreationBriefsToDrafts(ctx context.Context, profileID string) (map[string]string, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT l.creation_brief_id, d.id
		 FROM creation_article_links l
		 JOIN creation_proposals p ON p.id = l.creation_proposal_id
		 JOIN article_drafts d ON d.brief_id = l.article_brief_id
		 LEFT JOIN claim_writing_drafts cwd ON cwd.article_brief_id = l.article_brief_id
		 WHERE p.editorial_profile_id = ?
		 ORDER BY l.creation_brief_id,
		   CASE WHEN cwd.draft_id = d.id THEN 0 ELSE 1 END, d.created_at, d.id`, profileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var briefID, draftID string
		if err := rows.Scan(&briefID, &draftID); err != nil {
			return nil, err
		}
		if _, exists := out[briefID]; !exists {
			out[briefID] = draftID
		}
	}
	return out, rows.Err()
}
