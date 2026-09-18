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

## R02 — 预占在任务恢复时幂等（R02-a + R02-b）

拆分边界与验收：

- **R02-a**：同一 job 一行预占（0029 表级 UNIQUE(job_id)）下的复用与合法转移：事务内先读同 job 预占行（活跃复用、终结明确拒绝，不覆盖已结算金额），并发首次预占命中 UNIQUE 时复用已插入行；新增 `pending_remote` 状态（远端结果未知保留待处理，预估继续计入占用，不假定免费）；`JobResultComplete` 恢复快路径同步结算；失败收尾按结果已知性分类。
- **R02-b**：用任务的持久阶段（`remote_call_started`，不用 Worker 共享内存、不用 receipt 反推）记录"已到达远端调用边界"，使调用前失败能明确 `released_no_call`；unknown 结果阻断并可见，不自动重执行；预占后/响应缓存后/业务落库后的重启恢复用例补齐。

### R02-a — 预占复用、状态转移与快路径结算

- 状态：实现与自动验证完成。
- 交付：
  - `internal/store/budget.go`：事务内先读同 job 预占行（`reuseOrRejectReservation`：held/pending_remote 复用、settled/released_* 明确拒绝，复用不构成重调授权）；并发首次预占 UNIQUE 兜底；占用统计含 pending_remote；`SettleBudget` 接受 held/pending_remote 且幂等；新增 `MarkBudgetPendingRemote`；负预估单位与负实际费用拒绝。
  - `internal/models/models.go`：`BudgetPendingRemote` 状态与注释（一 job 一行）。
  - `internal/queue/worker.go`：`JobResultComplete` 快路径同步结算；`releaseJobBudget` 替换为 `finalizeJobBudgetOnFailure`（receipt 已知 → 结算；否则保守 pending_remote）。
- 验证：
  - `go test -race -count=3 -run 'TestHoldBudget|TestBudgetPendingRemote' ./internal/store/` 通过（含：5 分预算同 job 并发全复用同一行 `TestHoldBudget_ConcurrentSameJob_TightBudget`；恢复领取复用；终结拒绝；pending 转移）。
  - `go test -race -timeout 15m ./internal/store/` 通过（约 645s，基线同量级）；`go test -race -timeout 20m ./internal/queue/` 通过。
  - `go test ./internal/queue/ ./internal/store/ ./internal/server/` 通过；`go vet ./internal/...`、`gofmt`、`git diff --check` 通过。
- 提交：`fix(queue): resume jobs with idempotent budget reservations`（cac7986）。

### R02-b — 持久调用边界与未知结果阻断

- 状态：实现与自动验证完成。
- 交付：
  - 迁移 0045：`processing_jobs.remote_call_started`（旧行默认 0，旧任务兼容；未改已应用迁移）。
  - `store/job_intents.go`：`MarkJobRemoteCallStarted`（幂等，仅 queued/running）；`GetJobExecution` 返回该标记。
  - `queue/worker.go`：`markRemoteCallStarted` 在发起远端调用前持久化调用边界（标记失败只记日志，宁可保守归入"结果未知"）；`finalizeJobBudgetOnFailure` 按持久状态分类：有已知费用 receipt → 结算；已达边界无 receipt → `pending_remote`；未达边界（调用前失败）→ `released_no_call`。
  - 各任务调用边界标记：转录、分析、高光、精读 compose/补织/渠道语气（digest.go）、渠道改写（digest_rewrite.go）、重点质量判定、构思诊断、独立审校。
  - `processClaimed`：`result_state=unknown` 恢复时阻断自动重执行并以可见失败终结（"请人工确认后重新入队"），不重调模型、不冒充完成；预占保留 pending_remote。
  - 纠正 `TestWorker_UnknownResultReexecutes` → `TestWorker_UnknownResultBlocksReexecution`；迁移计数断言 44→45（migrate_test/backup_test，新增迁移的正常跟随）。
- 验证：
  - 新增回归：调用前失败明确释放且占用归零（`TestWorker_PreCallFailureReleasesBudget`）；预占后模拟重启只预占/计费/结算一次（`TestWorker_ResumeAfterHoldReusesReservation`）；未知结果阻断且预占保留。
  - `go test -timeout 30m ./internal/queue/ ./internal/store/ ./internal/server/` 全部通过；`go test -race -timeout 25m ./internal/queue/` 通过；`go vet ./internal/...`、`gofmt`、`git diff --check` 通过。
- 提交：`fix(queue): gate unknown results behind persisted call boundaries`。
- 限制：pending_remote 目前无自动解除路径，Owner 处置入口（预算页）待后续任务；逐阶段 checkpoint 恢复属 R08；全量 store -race 约 10.5 分钟，接近默认 10 分钟超时，后续里程碑用更大 `-timeout` 执行。

