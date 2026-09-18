// budget.go 单 Owner 全局预算治理（B04 / ADR-0024 §5）。
// 在既有画像/单篇限制之上提供全局月度检查：覆盖学习（转录/分析/高光）、
// 本地 TTS（确定零成本）、digest（无画像也入账）与文章的已知费用，加上在途预估。
// 调用前预占（事务内），结算以实际费用替换；失败释放区分是否发生远端调用。
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
		`SELECT COALESCE(SUM(estimated_cost_cents),0) FROM budget_reservations WHERE status = 'held' AND created_at >= datetime('now','start of month')`).Scan(&held); err != nil {
		return 0, err
	}
	return editorial.Int64 + learning.Int64 + held.Int64, nil
}

// HoldBudget 任务领取后的调用前预占（B04）：
//  1. 自动任务受单日数量上限约束；
//  2. 已配置预算时，模型必须有价格（否则配置缺口，unknown 不伪装为零成本）；
//  3. 同一事务内判断已知用量 + 有效在途预占 + 本次预估之和：恰好等于上限可执行，
//     超过则拒绝；并发预占由事务串行化保证。
//
// 预算未配置时不预占（记录 nil 语义），行为与旧路径一致。
func (s *Store) HoldBudget(ctx context.Context, jobID, operation string, automated bool, providerName, model string, estimateUnitsIn, estimateUnitsOut int) (*models.BudgetReservation, error) {
	// 日限额只约束订阅自动产生的任务。
	if automated {
		limit, err := s.GetAutoDailyJobLimit(ctx)
		if err != nil {
			return nil, err
		}
		if limit != nil {
			var today int64
			if err := s.DB.QueryRowContext(ctx,
				`SELECT COUNT(*) FROM processing_jobs WHERE is_automated = 1 AND created_at >= datetime('now','start of day')`).Scan(&today); err != nil {
				return nil, err
			}
			if today >= *limit {
				return nil, fmt.Errorf("%w: 今日自动任务已达 %d 个上限", ErrAutoDailyLimitReached, *limit)
			}
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
		`SELECT COALESCE(SUM(estimated_cost_cents),0) FROM budget_reservations WHERE status = 'held' AND created_at >= datetime('now','start of month')`).Scan(&held); err != nil {
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
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return res, nil
}

// SettleBudget 任务完成后以实际费用结算（替换 held 预估）；实际费用不能为负。
func (s *Store) SettleBudget(ctx context.Context, jobID string, actualCents int64) error {
	if actualCents < 0 {
		return fmt.Errorf("%w: 实际费用不能为负", ErrInvalidEditorialState)
	}
	res, err := s.getReservationByJob(ctx, jobID)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx,
		`UPDATE budget_reservations SET status = ?, actual_cost_cents = ?, settled_at = datetime('now') WHERE id = ? AND status = ?`,
		models.BudgetSettled, actualCents, res.ID, models.BudgetHeld)
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
