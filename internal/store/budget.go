// budget.go 单 Owner 全局预算治理（B04 / ADR-0024 §5）。
// 在既有画像/单篇限制之上提供全局月度检查：覆盖学习（转录/分析/高光）、
// 本地 TTS（确定零成本）、digest（无画像也入账）与文章的已知费用，加上在途预估。
// 一 job 一行预占（UNIQUE(job_id)）：调用前在事务内预占，恢复领取按持久状态复用；
// 结算以实际费用替换；失败收尾区分：已获得结果 → 结算；远端结果未知 →
// pending_remote 保留待处理；明确未调用 → 释放。
// 未配置价格的付费自动任务返回配置缺口（ErrBudgetUnpriced），不冒充免费。
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
)

// ErrBudgetExhausted 全局月度预算不足（含在途预估）。
var ErrBudgetExhausted = errors.New("monthly budget exhausted")

// ErrBudgetUnpriced 已配置预算但模型未登记价格：付费自动任务被阻止的配置缺口。
var ErrBudgetUnpriced = errors.New("model price missing while budget is configured")

// ErrAutoDailyLimitReached 自动处理任务达到当日数量上限。
var ErrAutoDailyLimitReached = errors.New("automated daily job limit reached")

// GetOwnerMonthlyBudget 读取全局月度预算（分）；未配置返回 nil。
func (s *Store) GetOwnerMonthlyBudget(ctx context.Context) (*int64, error) {
	var budget sql.NullInt64
	if err := s.DB.QueryRowContext(ctx, `SELECT monthly_budget_cents FROM settings WHERE id = 1`).Scan(&budget); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	if !budget.Valid {
		return nil, nil
	}
	v := budget.Int64
	return &v, nil
}

// SetOwnerMonthlyBudget 设置全局月度预算；nil 取消配置。
func (s *Store) SetOwnerMonthlyBudget(ctx context.Context, cents *int64) error {
	if cents != nil && *cents < 0 {
		return fmt.Errorf("%w: 预算不能为负", ErrInvalidEditorialState)
	}
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO settings (id) VALUES (1) ON CONFLICT(id) DO NOTHING`); err != nil {
		return err
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE settings SET monthly_budget_cents = ? WHERE id = 1`, cents)
	return err
}

// GetAutoDailyJobLimit 读取自动处理单日任务上限；未配置返回 nil。
func (s *Store) GetAutoDailyJobLimit(ctx context.Context) (*int64, error) {
	var limit sql.NullInt64
	if err := s.DB.QueryRowContext(ctx, `SELECT auto_daily_job_limit FROM settings WHERE id = 1`).Scan(&limit); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	if !limit.Valid {
		return nil, nil
	}
	v := limit.Int64
	return &v, nil
}

// SetAutoDailyJobLimit 设置自动处理单日任务上限；nil 取消限制。
func (s *Store) SetAutoDailyJobLimit(ctx context.Context, limit *int64) error {
	if limit != nil && *limit < 0 {
		return fmt.Errorf("%w: 日限额不能为负", ErrInvalidEditorialState)
	}
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO settings (id) VALUES (1) ON CONFLICT(id) DO NOTHING`); err != nil {
		return err
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE settings SET auto_daily_job_limit = ? WHERE id = 1`, limit)
	return err
}

// OwnerMonthlyUsageCents 单 Owner 当月已计费用（分）：
// 文章/digest 的 editorial_usage_records（含无画像的 digest_*）+ 学习侧 usage_records
// 的已知费用（NULL=未知不计入数值）+ 当前 held 在途预估。
// 注意：这是本地账面口径；估计值不宣称远端实际费用不会超过预估。
func (s *Store) OwnerMonthlyUsageCents(ctx context.Context) (int64, error) {
	var editorial, learning, held sql.NullInt64
	if err := s.DB.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(cost_cents),0) FROM editorial_usage_records WHERE created_at >= datetime('now','start of month')`).Scan(&editorial); err != nil {
		return 0, err
	}
	if err := s.DB.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(estimated_cost),0) FROM usage_records WHERE estimated_cost IS NOT NULL AND created_at >= datetime('now','start of month')`).Scan(&learning); err != nil {
		return 0, err
	}
	if err := s.DB.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(estimated_cost_cents),0) FROM budget_reservations WHERE status IN ('held','pending_remote') AND created_at >= datetime('now','start of month')`).Scan(&held); err != nil {
		return 0, err
	}
	return editorial.Int64 + learning.Int64 + held.Int64, nil
}

// AdmitAutomatedDailyIntent 原子获取日限额执行资格（R03）。
//   - 限额统计对象：当日已获准的自动处理意图，按来源（source_type, source_id）去重——
//     同一集的后续分析/解说子任务与同一意图的重试不重复占名额；
//   - 额度获取与执行资格原子关联：入队不占额，领取时在同一事务内判定并写入
//     intent_admitted_at；未获准的排队任务不提前耗尽额度；
//   - 一天边界与既有语义一致（UTC 日，datetime('now','start of day')）。
func (s *Store) AdmitAutomatedDailyIntent(ctx context.Context, jobID string) error {
	limit, err := s.GetAutoDailyJobLimit(ctx)
	if err != nil {
		return err
	}
	if limit == nil {
		return nil
	}
	var automated bool
	var sourceType, sourceID string
	err = s.DB.QueryRowContext(ctx,
		`SELECT is_automated, source_type, source_id FROM processing_jobs WHERE id = ?`, jobID).
		Scan(&automated, &sourceType, &sourceID)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: 任务 %s 不存在", ErrInvalidEditorialState, jobID)
	}
	if err != nil {
		return err
	}
	if !automated {
		return nil // 手动任务不受日限额约束
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// 当日已获准名额（排除本任务自身来源：同源重试/子任务不重复计数）。
	var admitted int64
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(DISTINCT source_type || ':' || source_id) FROM processing_jobs
		 WHERE is_automated = 1 AND intent_admitted_at IS NOT NULL
		   AND intent_admitted_at >= datetime('now','start of day')
		   AND NOT (source_type = ? AND source_id = ?)`, sourceType, sourceID).Scan(&admitted); err != nil {
		return err
	}
	if admitted >= *limit {
		return fmt.Errorf("%w: 今日自动处理额度已满（%d 个来源），任务保持排队待重试", ErrAutoDailyLimitReached, *limit)
	}
	// 同任务重试不重复计数：已有准入时刻的行不再改写；额度获取与执行资格同事务。
	if _, err := tx.ExecContext(ctx,
		`UPDATE processing_jobs SET intent_admitted_at = datetime('now') WHERE id = ? AND intent_admitted_at IS NULL`, jobID); err != nil {
		return err
	}
	return tx.Commit()
}

// reuseOrRejectReservation 按持久状态决定已有预占的转移（B02）：
// 活跃状态（held/pending_remote）复用；已终结（settled/released_*）明确拒绝，
// 不覆盖已结算金额；复用不构成重新调用模型的授权。
func reuseOrRejectReservation(res *models.BudgetReservation) (*models.BudgetReservation, error) {
	switch res.Status {
	case models.BudgetHeld, models.BudgetPendingRemote:
		return res, nil
	default:
		return nil, fmt.Errorf("%w: 任务 %s 已有 %s 预占，不能重复预占", ErrInvalidEditorialState, res.JobID, res.Status)
	}
}

// HoldBudget 任务领取后的调用前预占（B04/B02/R03）：
//  1. 自动任务先原子获取日限额执行资格（R03）：按来源去重、入队不占额、
//     同任务重试不重复计数；额度不足返回 ErrAutoDailyLimitReached，任务保持可重试；
//  2. 已配置预算时，模型必须有价格（否则配置缺口，unknown 不伪装为零成本）；
//  3. 同一事务内：先读同 job 预占行（活跃复用/终结拒绝），再判断已知用量 +
//     有效在途预占 + 本次预估之和（恰好等于上限可执行，超过则拒绝）；并发预占由
//     事务串行化保证，同 job 并发首次预占命中 UNIQUE(job_id) 时复用已插入行。
//
// 预算未配置时不预占（记录 nil 语义），行为与旧路径一致。
func (s *Store) HoldBudget(ctx context.Context, jobID, operation string, automated bool, providerName, model string, estimateUnitsIn, estimateUnitsOut int) (*models.BudgetReservation, error) {
	// 日限额按执行资格计数（R03）：仅约束订阅自动产生的任务。
	if automated {
		if err := s.AdmitAutomatedDailyIntent(ctx, jobID); err != nil {
			return nil, err
		}
	}

	budget, err := s.GetOwnerMonthlyBudget(ctx)
	if err != nil {
		return nil, err
	}
	if budget == nil {
		return nil, nil // 未配置全局预算：不预占，不阻塞（画像/单篇限制仍然生效）
	}

	// 已配置预算：付费模型必须登记价格（自动任务的配置缺口必须显式暴露）。
	if _, err := s.GetModelPrice(ctx, providerName, model); err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, fmt.Errorf("%w: %s/%s 未登记价格，无法核算自动任务预算", ErrBudgetUnpriced, providerName, model)
		}
		return nil, err
	}
	if estimateUnitsIn < 0 || estimateUnitsOut < 0 {
		return nil, fmt.Errorf("%w: 预估单位不能为负 (%d, %d)", ErrInvalidEditorialState, estimateUnitsIn, estimateUnitsOut)
	}
	estimate, known, err := s.ResolveUsageCost(ctx, providerName, model, estimateUnitsIn, estimateUnitsOut)
	if err != nil {
		return nil, err
	}
	if !known {
		return nil, fmt.Errorf("%w: %s/%s 按预估单位无法核算费用", ErrBudgetUnpriced, providerName, model)
	}

	// 事务内串行化并发预占：读占用 → 校验 → 插入 held。
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	// 事务内先读同 job 预占行：已有则按持久状态复用或拒绝——必须在费用求和之前，
	// 否则同 job 并发时第二方会把第一方的占用计入限额而错误拒绝。
	r := &models.BudgetReservation{}
	switch err := tx.QueryRowContext(ctx,
		`SELECT id, job_id, operation, estimated_cost_cents, status, reason, actual_cost_cents, created_at, settled_at
		 FROM budget_reservations WHERE job_id = ?`, jobID).
		Scan(&r.ID, &r.JobID, &r.Operation, &r.EstimatedCostCents, &r.Status, &r.Reason, &r.ActualCostCents, &r.CreatedAt, &r.SettledAt); {
	case err == nil:
		return reuseOrRejectReservation(r)
	case !errors.Is(err, sql.ErrNoRows):
		return nil, err
	}
	usage := &sql.NullInt64{}
	var editorial, held sql.NullInt64
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(cost_cents),0) FROM editorial_usage_records WHERE created_at >= datetime('now','start of month')`).Scan(&editorial); err != nil {
		return nil, err
	}
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(estimated_cost),0) FROM usage_records WHERE estimated_cost IS NOT NULL AND created_at >= datetime('now','start of month')`).Scan(usage); err != nil {
		return nil, err
	}
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(estimated_cost_cents),0) FROM budget_reservations WHERE status IN ('held','pending_remote') AND created_at >= datetime('now','start of month')`).Scan(&held); err != nil {
		return nil, err
	}
	total := editorial.Int64 + usage.Int64 + held.Int64
	if total+estimate > *budget {
		return nil, fmt.Errorf("%w: 当月已计 %d 分 + 在途预估 + 本次预估 %d 分，超过预算 %d 分", ErrBudgetExhausted, total, estimate, *budget)
	}
	res := &models.BudgetReservation{
		ID: uuid.NewString(), JobID: jobID, Operation: operation,
		EstimatedCostCents: estimate, Status: models.BudgetHeld,
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO budget_reservations (id, job_id, operation, estimated_cost_cents, status) VALUES (?, ?, ?, ?, ?)`,
		res.ID, res.JobID, res.Operation, res.EstimatedCostCents, res.Status); err != nil {
		if !isUniqueConstraintErr(err) {
			return nil, err
		}
		// 同 job 并发首次预占：对方已插入，按持久状态复用或拒绝（不报唯一约束错误）。
		r := &models.BudgetReservation{}
		if err := tx.QueryRowContext(ctx,
			`SELECT id, job_id, operation, estimated_cost_cents, status, reason, actual_cost_cents, created_at, settled_at
			 FROM budget_reservations WHERE job_id = ?`, jobID).
			Scan(&r.ID, &r.JobID, &r.Operation, &r.EstimatedCostCents, &r.Status, &r.Reason, &r.ActualCostCents, &r.CreatedAt, &r.SettledAt); err != nil {
			return nil, err
		}
		return reuseOrRejectReservation(r)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return res, nil
}

// SettleBudget 任务完成后以实际费用结算（替换 held 预估）；实际费用不能为负。
// 重复结算幂等：仅 held/pending_remote 转入 settled，不覆盖已结算金额。
func (s *Store) SettleBudget(ctx context.Context, jobID string, actualCents int64) error {
	if actualCents < 0 {
		return fmt.Errorf("%w: 实际费用不能为负", ErrInvalidEditorialState)
	}
	res, err := s.getReservationByJob(ctx, jobID)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx,
		`UPDATE budget_reservations SET status = ?, actual_cost_cents = ?, settled_at = datetime('now') WHERE id = ? AND status IN (?, ?)`,
		models.BudgetSettled, actualCents, res.ID, models.BudgetHeld, models.BudgetPendingRemote)
	return err
}

// MarkBudgetPendingRemote 失败收尾（B02）：远端调用已发出但结果未知时，
// 预占从 held 转为 pending_remote 保留待处理，预估继续计入月度占用，
// 不假定未知调用免费。重复调用与对已终结预占的调用均幂等（no-op）。
func (s *Store) MarkBudgetPendingRemote(ctx context.Context, jobID string) error {
	res, err := s.getReservationByJob(ctx, jobID)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx,
		`UPDATE budget_reservations SET status = ? WHERE id = ? AND status = ?`,
		models.BudgetPendingRemote, res.ID, models.BudgetHeld)
	return err
}

// ReleaseBudget 失败释放：remoteCallHappened=false 直接解除预估；
// true 表示远端可能已收费（结果未知）——解除预占，但已落账的 receipt 仍计入月度。
func (s *Store) ReleaseBudget(ctx context.Context, jobID string, remoteCallHappened bool) error {
	res, err := s.getReservationByJob(ctx, jobID)
	if err != nil {
		return err
	}
	status := models.BudgetReleasedNoCall
	if remoteCallHappened {
		status = models.BudgetReleasedUnknown
	}
	_, err = s.DB.ExecContext(ctx,
		`UPDATE budget_reservations SET status = ?, settled_at = datetime('now') WHERE id = ? AND status = ?`,
		status, res.ID, models.BudgetHeld)
	return err
}

// ListBudgetReservations 列出最近的预算预占（预算页可见，含配置缺口与释放原因）。
func (s *Store) ListBudgetReservations(ctx context.Context, limit int) ([]*models.BudgetReservation, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, job_id, operation, estimated_cost_cents, status, reason, actual_cost_cents, created_at, settled_at
		 FROM budget_reservations ORDER BY created_at DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.BudgetReservation
	for rows.Next() {
		r := &models.BudgetReservation{}
		if err := rows.Scan(&r.ID, &r.JobID, &r.Operation, &r.EstimatedCostCents, &r.Status, &r.Reason, &r.ActualCostCents, &r.CreatedAt, &r.SettledAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) getReservationByJob(ctx context.Context, jobID string) (*models.BudgetReservation, error) {
	r := &models.BudgetReservation{}
	err := s.DB.QueryRowContext(ctx,
		`SELECT id, job_id, operation, estimated_cost_cents, status, reason, actual_cost_cents, created_at, settled_at
		 FROM budget_reservations WHERE job_id = ?`, jobID).
		Scan(&r.ID, &r.JobID, &r.Operation, &r.EstimatedCostCents, &r.Status, &r.Reason, &r.ActualCostCents, &r.CreatedAt, &r.SettledAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return r, nil
}
