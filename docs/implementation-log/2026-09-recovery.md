# 恢复计划执行记录（2026-09-18 计划）

对应计划：[2026-09-18 审计后开发计划](../superpowers/plans/2026-09-18-goal-recovery-plan.md)。
开发分支：`fix/learning-creation-recovery`。基线：`dc0cefb7c07637eff649e6884682ada0d1b6c086`。

约定：每个任务一条记录，含交付行为、验证命令与结果、实际限制；任务与其记录同一原子提交。提交 SHA 在下一次文档更新时补齐。
状态标记：`实现与自动验证完成` / `浏览器验证完成` / `真实内容质量通过` / `待验收（外部条件）`。

## R00 — 纠正交付状态，建立可追踪执行记录

- 状态：实现与自动验证完成。
- 交付：
  - 新计划文件入库（`docs/superpowers/plans/2026-09-18-goal-recovery-plan.md`）。
  - 旧计划（2026-09-12）头部状态替换为指向恢复计划的替代说明，撤回“50/53 完成”表述；历史提交与验证记录保留。
  - 建立本执行记录文件。
- 验证：文档相对链接存在且可解析；分支已建立；工作区无未知改动（仅本任务文件）。
- 提交：`docs(plan): replace completion claims with recovery tasks`（6309b13）。

## R01 — 预算检查计入本次预占

- 状态：实现与自动验证完成。
- 交付（`internal/store/budget.go`、`internal/store/budget_test.go`）：
  - `HoldBudget` 在同一事务内判断已知用量 + 有效在途预占 + **本次预估**之和：恰好等于上限可执行，超过才拒绝。修复旧实现 `total >= budget` 既漏算本次预估、又错误拒绝恰好等于上限的缺陷（旧测试甚至允许 5 分预算预占 1000 分预估）。
  - 预估单位为负、结算实际费用为负：显式拒绝（`ErrInvalidEditorialState`），不能借负费用扩大预算空间。
  - 无预算配置不预占、unknown 价格返回 `ErrBudgetUnpriced` 不伪装免费：语义保持并有测试。
- 验证：
  - `go test -race -run 'TestHoldBudget|TestOwnerMonthlyUsageCents|TestAutoDailyJobLimit' ./internal/store/` 通过（含新契约：5 分预算拒绝 1000 分预估且不落库；恰好等于上限放行；并发预占账面总额不超预算）。
  - `go test ./internal/store/ ./internal/queue/ ./internal/server/` 全部通过。
  - `go vet ./internal/store/` 通过（主线程复核）；`gofmt`、`git diff --check` 通过；临时目录 `go build ./cmd/cloudwisepod` 通过（主线程复核）。
  - `TestOwnerMonthlyUsageCents_Aggregates` 同步修正：hold 须被预算覆盖才获准，预算从 100 分调至 5000 分以覆盖 2000 分预估。
- 提交：`fix(budget): include requested reservation in limit checks`（73f3159，含本记录 amend）。
- 限制：预估仍是本地保守估计，不宣称远端实际费用不超过预估（既有口径不变）。
