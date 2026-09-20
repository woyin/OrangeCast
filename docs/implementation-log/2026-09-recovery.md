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


## R03 — 自动日限额按执行资格计数

- 状态：实现与自动验证完成。
- 交付：
  - 迁移 0046：`processing_jobs.intent_admitted_at`（NULL=未获准；旧行默认 NULL，旧任务兼容；未改已应用迁移）。
  - `store/budget.go`：`HoldBudget` 中旧的"按当日任务行计数"替换为 `AdmitAutomatedDailyIntent`：入队不占额，领取时在同一事务内判定并写入准入时刻；限额统计对象为"当日已获准的自动处理意图"（按来源去重，UTC 日边界与既有语义一致）；同源重试/后续分析解说子任务不重复占名额；手动任务不受限。
  - `store/jobs.go`：`DeferJobForDailyLimit`——额度不足的任务保持 running + 未来租约（10 分钟），原因写入 last_error；现有调度规则下租约到期重新领取再尝试准入，不在队列头部阻塞后续任务，不整批永久失败。
  - `queue/worker.go`：`processClaimed` 对 `ErrAutoDailyLimitReached` 走延迟路径，不 MarkJobFailed、不标记来源失败（此刻尚无预占，无需收尾）。
- 验证：
  - `TestAutoDailyJobLimit` 重写：入队多个自动任务行不占名额；限额 1 时首个意图获准、第二来源被拒；同源子任务/重试放行且不重复计数；手动任务不受限；跨日额度重新可用。
  - `TestWorker_DailyLimitDefersInsteadOfFailing`（queue，真实 ProcessOne 入口）：第一集完成占名额；第二集延迟（running+未来租约+原因可见），未到期不被重复领取；跨日后继续完成。
  - `go test -race -run 'TestAutoDailyJobLimit|TestHoldBudget|TestBudgetPending' ./internal/store/`、`go test -race -run 'TestWorker' ./internal/queue/` 通过；`go test ./internal/queue/ ./internal/store/ ./internal/server/` 通过；`go vet ./internal/...`、`gofmt`、`git diff --check` 通过；迁移计数断言 45→46。
- 提交：`fix(ingestion): enforce daily limits on admitted processing intents`。
- 限制：一天边界沿用既有 UTC 日语义（未改局部时区行为）；延迟租约固定 10 分钟，未做指数退避；进度页对"延迟重试"的专门展示随 R25 浏览器验收核对。

## R04 — 执行使用冻结的配置和来源

- 状态：实现与自动验证完成。
- 交付：
  - `store/jobs.go`：`FreezeJobTaskConfig`——入队时把当前 settings 的任务级 Provider 与生效模型（`provider.EffectiveModel`，空配置不产生不可追溯模型）写入任务行；只对新任务（configured_provider 为空）生效，不覆盖显式传入的 spec 配置；`config_version` 冻结已知提示/契约版本（精读 digest-writer-v1），未知留空不杜撰；本地任务（解说 TTS 等）不冻结。
  - 三条入队路径统一接入冻结：`enqueueJob`（订阅自动摄取）、`enqueueAnalyze`（转录后衔接）、`EnqueueJobIdempotent`（幂等意图入队）。子任务语义：输入快照经 `InheritJobInputSnapshot` 继承上游（B08 既有），任务配置在子任务自身入队时按其任务角色冻结。
  - `queue/worker.go`：`taskConfigFor` 优先读取持久冻结配置；为空（旧任务）走当前设置兼容路径，不伪造历史配置。
  - `queue/worker.go`：`enforceSourceSendPolicy`——外发类任务（转录/分析/精读/高光/重点质量/渠道改写/构思诊断/独立审校）在执行时动态检查来源模型数据策略（`CanSendSourceToProvider`）；LocalOnly、未批准 Provider 或 Owner 撤销授权阻止外发，以可见失败终结；无策略记录的来源类型放行。
- 验证：
  - `TestWorker_ExecutesFrozenConfig`：入队冻结 A；排队后改设置任务输入不漂移；新重分析任务冻结新配置 B，旧任务仍为 A。
  - `TestWorker_LegacyJobFallsBackToSettings`：无冻结配置的旧任务回退当前设置，不杜撰。
  - `TestWorker_PolicyRevocationStillBlocks`：入队后设 LocalOnly，执行时 Provider 不被调用，任务以可见原因失败。
  - `go test -timeout 30m ./internal/queue/ ./internal/store/ ./internal/server/` 全部通过；`go vet ./internal/...`、`gofmt`、`git diff --check` 通过；新测试 `-race` 通过。
- 提交：`fix(jobs): execute persisted provider and source snapshots`。
- 限制：处理深度与来源版本冻结沿用 B05/B06/B08 快照机制（本任务验证其被 worker 消费）；策略检查粒度为任务级 Provider，未细化到每次独立模型调用（R08+ 按阶段补）。

## R05 — 自动质量判断形成可发现素材

- 状态：实现与自动验证完成。
- 交付：
  - `queue/worker.go`（doAnalyze）：KeyPoint 索引完成后幂等入队质量判定（`EnqueueKeypointQualityJob`，意图 `kpq:<source>:cv<N>` 冻结卡片版本与转录版本）。knowledge 与 knowledge_dj 深度均获得质量步骤；DJ 链不依赖质量全部通过（判定是独立任务，失败只显式可见，不回抹卡片、不阻塞解说衔接）。
  - `store/creation_workspace.go`：K03 消费路径 `ApplyKeypointQualityResults`——同一事务内只消费与当前内容指纹（`FingerprintKeypoint`）且卡片版本都匹配的判定，更新 `keypoint_index.quality_status`；进入发现资格的通过写一次 `quality_approved` MaterialChange（ON CONFLICT 幂等，重跑不增加变化）。约束：origin=edited/manual、`owner_confirmed`、production 已挑选/排除/搁置的重点不被自动判定覆盖；指纹或版本不匹配的旧结果不批准新内容（SkippedNoMatch 可观测）。
  - `queue/keypoint_quality.go`（doKeypointQualityJob）：判定落库后应用结果，结果 JSON 携带 applied/unchanged/skipped 统计。
  - 测试基建：`fakeAnalyzer` 支持判定接口（默认保守 needs_review，不伪装通过）；`qualityAnalyzer` 可注入分析卡片。
- 验证：
  - `TestAnalysisChainsQualityToReady`：真实分析入口→索引→自动衔接判定→`ready` + 恰好一条变化；重跑判定不新增变化（可参与发现：`discovery_schedule` 既有 `quality_status IN ('ready','owner_confirmed')` 过滤开始有真实供给）。
  - `TestApplyKeypointQuality_StaleAndOwnerGuard`：owner_confirmed/排除/人工修改不被覆盖；内容变化后旧指纹判定不批准（保持 needs_review，可读可重试）。
  - 既有端到端判定测试保持通过（模型不可用显式失败、程序拦截不调模型）。
  - `go test -timeout 40m ./internal/queue/ ./internal/store/ ./internal/server/` 全部通过；`go test -race -timeout 40m ./internal/queue/` 通过；`go vet ./internal/...`、`gofmt`、`git diff --check`、临时构建通过。
- 提交：`fix(keypoints): connect quality jobs to usable material state`。
- 限制：质量判定按卡片版本入队一次，重点级增量判定（部分重点变化）沿用整卡重判；判定任务与 DJ 解说任务的执行顺序由 created_at 决定，未做优先级调度。

## 里程碑 M1 检查（R01–R05 交付后）

- 状态：实现与自动验证完成（cover-gate 为已知既有债务，属 R24 范围）。
- 结果：
  - `go test ./...` 通过（修复 `internal/backup` 迁移计数断言 44→46 跟随新增迁移）。
  - `go vet ./...` 通过；`make lint` 通过；临时目录 `go build ./cmd/cloudwisepod` 通过。
  - 队列/存储包 `-race` 通过（R02–R05 各提交内已记录）。
  - `make cover-gate` 仍失败：queue 77.3%、store 74.0%（基线即失败：evalset 94.0/provider 86.7/queue 77.3/store 75.2 均低于门槛）。本轮新增代码引入少量未覆盖行，store 由 75.2 微降至 74.0，未修改任何门槛或豁免；按计划由 R24 以行为测试补足，不以降低门槛方式通过。
- 用户可观察结果（M1 验收对象）：自动处理受全局预算与日限额约束（本次预估计入、恢复幂等、未知结果不盲目重试、额度不足可延迟重试），自动重点经独立质量判定形成 ready 素材并可进入发现；旧任务与旧数据兼容路径有回归。

## R06 — 精读冻结完整笔记输入

- 状态：实现与自动验证完成。
- 交付：
  - `store/digests_enqueue.go`：`DigestInputSnapshot` 新增 `notes_recorded` 与 `notes[]`（`DigestFrozenNote`：ID、类型、确切文本、乐观版本 Revision、引用锚点 Anchors）。入队时冻结每条选中笔记的确切内容；`notes_recorded=true` 区分"本次确切选择（含空选择）"与"未记录选择的旧数据"；旧字段 `note_ids` 保留读取兼容。
  - `queue/digest.go`：新任务（notes_recorded=true）运行时只消费冻结文本/类型，不用库内内容覆盖；对已删除笔记做存在性检查并显式记日志（冻结文本仍参与生成，失效引用不静默）；旧任务（notes_recorded=false）按 NoteIDs 从当前库解析（nil=取全部的旧语义保持），不伪造冻结文本。
  - Episode/Upload/Document 的不可变来源读取路径为 G01 既有实现（快照版本/文档证据），本任务验证其与笔记冻结共同构成完整输入。
- 验证：
  - `TestDoDigest_FrozenNoteContents`：入队快照含文本/类型/锚点；排队后编辑、删除（SQL 模拟，存储层无删除 API）、新增笔记，生成仍按冻结文本。
  - `TestDoDigest_EmptyNoteSelectionStaysEmpty`：空选择显式记录且保持为空，入队后新增不进入。
  - `TestDoDigest_LegacySnapshotResolvesNoteIDs`：旧快照按 NoteIDs 兼容解析。
  - `go test -timeout 40m ./internal/queue/ ./internal/store/ ./internal/server/` 全部通过；`go vet ./internal/...`、`gofmt`、`git diff --check`、`go test -race -run TestDoDigest` 通过。
- 提交：`fix(digests): freeze selected note contents at enqueue time`。
- 限制：Owner 笔记目前无删除入口（仅有创建/编辑），删除场景以存储层 SQL 验证；Owner 可见的失效引用提示属页面层（R22/R25）；备份可恢复性由 DB 内快照保证，端到端备份恢复用例在 R23。

## R07 — 精读修订继承快照与确认关系

- 状态：实现与自动验证完成。
- 交付：
  - `store/digests.go`（CreateDigestRevision）：派生修订在同一发布事务内继承父修订证据血缘——`SourceSnapshotID`、检索落源（含 confirmed/pending/rejected 状态）与事实缺口。新增 `ExcludeDocumentID`：来源剔除时该 Document 的落源不继承到新修订，其已消解缺口转回未消解（document_id 置空）；其余落源确认与缺口原样继承。不再因修订而丢失快照（旧实现派生后即触发 legacy_snapshot 就绪问题——整篇被无条件降级）。
  - `server/digest.go`：`deriveRevisionWithoutSource` 传入剔除 Document；`handleDigestEdit` 保存后跳转新修订 ID（旧实现跳旧 ID，GET 展示旧版本）；旧版本仍可按 ID 回溯；当前指针 = MAX(version) 在发布事务提交后生效。
- 验证：
  - `TestDigestRevision_InheritsProvenance`（store）：标题编辑继承快照/落源状态/缺口且就绪状态与父一致、无 legacy_snapshot；剔除派生后 doc-a 落源不继承、其缺口转未消解、doc-b 不受影响；旧版本可读。
  - `TestDigestEdit_RedirectsToNewRevision`（server，真实路由）：303 跳转 `/digest/<新ID>`，当前指针展示刚保存版本，旧版本回溯不变。
  - `go test ./...` 全部通过；`go vet ./internal/...`、`gofmt`、`git diff --check` 通过；digest 相关 store/server 测试 `-race` 通过。
- 提交：`fix(digests): preserve provenance across derived revisions`。
- 限制：剔除派生的重复提交守卫沿用（同父+同原因不重复派生）；缺口“转回未消解”未自动重新触发补织（属 R08 恢复路径）。

## R08 — 精读阶段恢复与渠道改写

- 状态：实现与自动验证完成。
- 交付：
  - `queue/digest.go`（digestCheckpoint 扩展）：逐阶段断点——`landed_gaps`（缺口 → 首个落源结果与 Document，逐项落源后立即持久化）、`woven`（补织结果，发布前持久化）、`published_digest_id`（发布后中断复用，不重复发布）、`inline_rewrite_done`（内联渠道改写完成后不重做）。恢复时：已落源缺口直接复用断点（不重复 Search/抓取建档），补织完成则复用结果，已发布修订直接读取。
  - 发布后立即 `SaveJobResult(complete)`：结果先于终态持久化，崩溃恢复走快路径，不产生重复修订（发布为单事务，版本 = MAX+1）。
  - `queue/digest_rewrite.go`：既有契约验证保留——只消费快照指定修订与渠道，输入指纹复用，失败保留主文；本任务补齐回归。
  - 已落源 Document 幂等（同 origin_url 复用）为既有实现，回归覆盖。
- 验证：
  - `TestDigestResolveGaps_ResumeFromCheckpoint`：同一持久化断点下重启，Search/补织调用计数不增加，落源 Document 复用。
  - `TestDoDigest_PublishCrashResume`：清终态模拟发布后中断，恢复后修订唯一（v1）、渠道产物保持、任务成功终结。
  - `TestDigestRewriteJob_RetryKeepsMainText`：同修订同渠道重试不重复调用 Provider；其他渠道不受波及；主文修订不被渠道改写改变。
  - `go test -timeout 40m ./internal/queue/ ./internal/store/ ./internal/server/` 全部通过；`go test -race -run TestDigest ./internal/queue/` 通过；`go vet ./internal/...`、`gofmt`、`git diff --check`、构建通过。
- 提交：`fix(digests): resume search and composition from checkpoints`。
- 限制：渠道改写若在 Provider 响应后、落库前崩溃，重试会再调用一次 Provider（结果未知保守重试一次，落库由 Upsert 幂等兜底）；响应级缓存（先缓存后提交）属 R19 Writer 契约，如需可回移。

## R09 — DJPlan 接入真实生产流程

- 状态：实现与自动验证完成（页面交互细节待 R25 浏览器验收）。
- 交付：
  - 生产编排：`queue/highlight.go`——knowledge_dj 深度（手动或自动）高光持久化成功后，幂等衔接 DJ 清单任务（冻结刚产出的高光版本）；`knowledge` 深度不衔接（不生成 DJ）。清单任务在队列中位于解说之后，解说可用时清单使用真实时长；无解说也能编排（Gist 脚本估计时长，页面标注）。
  - 入队统一：`store/dj_plans.go` 新增 `EnqueueDJPlanJob`（幂等意图 `dj_plan:<source>:hv<N>:<target>`）与 `GetLatestDJPlanForSource`；worker 链接与页面生成动作共用。
  - 页面消费：`server/source_handlers.go` `handleDJ` 重写——只渲染持久化清单及其条目（含目标时长、顺序、选择理由、逐项 Start/End/SegmentIDs），不再用"当前高光"冒充清单；页面也不再解析当前转录/高光载荷（原始数据损坏不再 500）。旧来源无清单 → 显式兼容状态 + POST `/sources/<t>/<id>/dj/plan` 生成动作（幂等入队）；GET 不触发模型调用。
  - `templates/dj.html`：按清单条目渲染（evidence 行保留播放器 JS 契约 data-start/end/segments；narration 行显示脚本与试听入口）；无清单渲染生成表单。
- 验证：
  - `TestPipeline_KnowledgeDJDepthChainsFullDJ` 扩展：knowledge_dj 全链路 6 步（转录→分析→质量→高光→解说→DJ 清单）后存在含 evidence 项的持久化清单；`TestPipeline_KnowledgeDepthStopsAfterKeyPoints` 验证 knowledge 深度不生成 DJ。
  - `TestDJRenders_WithNarrationURLs`、`TestDJBriefCaptureButtons` 等以持久化清单驱动重写；新增 `TestDJ_NoPlan_ShowsGenerateAction`（有高光无清单 → 可用生成动作，POST 幂等入队；无清单不冒充高光）；`TestDJ_NoHighlight_404`、`TestDJ_TranscriptMissing_404`、`TestDJ_CorruptHighlight_500`、`TestDJ_CorruptTranscript_500` 按新契约更新为兼容状态断言（旧契约依赖"当前高光即清单"，属本轮替代的错误契约）。
  - `go test -timeout 40m ./internal/queue/ ./internal/server/ ./internal/store/` 全部通过；race（Pipeline/DJ/Digest）通过；`go vet`、`gofmt`、`git diff --check`、构建通过。
- 提交：`fix(dj): enqueue and render persisted listening plans`。
- 限制：旧清单的转录冻结段由 `frozenSegments` 兼容读取（无快照记录回退当前版本——旧任务语义，已在卡内标注）；清单项的解说音频真正进入播放队列属 R10；进度恢复契约（D07/D08）属 R11。

## R10 — 开场、过渡、收尾真正进入音频队列

- 状态：实现与自动验证完成（本地 TTS 真机真实 wav 听感验收待 R25/R26，需本地 Kokoro 环境）。
- 交付：
  - `queue/narration.go`：`synthesizePlanScripts`——解说任务在 gist 合成后，为最新 DJ 清单的脚本项目（开场/过渡/收尾/无真实解说的 Gist）按计划身份数成音频：伪 ID `plan:<planID>:p<position>` 绑定确切计划版本与位置；缓存身份含脚本文本指纹+计划版本+伪 ID（脚本指纹），重试不重合成；单项失败不阻塞其余项（checkpoint 可见）。音频仍写入 narrationDir、走独立 narration 播放元素——不进入 EvidenceAudio、不作引用证据。
  - `server/source_handlers.go`（handleDJ）：脚本项目解说 URL 解析优先 `plan:<id>:p<pos>` 身份，回退高光真实解说；旧计划音频不被新计划覆盖（新计划新伪 ID 独立版本化，旧行不动）。
  - `templates/dj.html`：脚本项目携带 `data-kind="narration"` 与 `data-narration`；`buildItems` 按 kind 分派——脚本项有音频则按计划顺序入播放队列，无音频跳过（部分失败仍连播原音），AI 解说与原音持续区分。
  - 复用现有 TTS 配置与缓存（CreateNarrationCached / GetNarrationByCacheKey），无新增迁移（计划绑定经伪 ID + 缓存身份持久化于 narrations 表）。
- 验证：
  - `TestNarrationTask_SynthesizesPlanScripts`：2 gist + 2 脚本产出；指纹命中不重复合成；新计划（同高光版本）脚本独立合成且旧计划音频保留。
  - `TestNarrationTask_ScriptPartialFailure`：任务级成功、脚本项目合成成功、原音不受影响。
  - `TestDJ_ScriptItemsCarryNarrationURL`（server，真实路由）：脚本项目渲染计划绑定 URL 与 AI 解说身份标注。
  - `go test ./...` 全部通过；race（Narration/DJ）通过；`go vet`、`gofmt`、`git diff --check`、构建通过。
- 提交：`feat(dj): play versioned intro transition and outro narration`。
- 限制：过渡脚本中的 AnchorHighlightID 尚未用于 D08 锚定增强；合成请求与播放项目一致性、部分失败连播的浏览器交互验证属 R25；本地 TTS 真实 wav 听感属外部验收。

## R11 — 修复续听 JSON、项目定位和并发保存

- 状态：实现与自动验证完成（浏览器交互细节待 R25）。
- 交付：
  - JSON 契约（`models/dj.go`）：`ListeningProgress` 显式 snake_case JSON 标签——GET 与 POST 同名字段同语义；无记录 GET 返回 `{}`。
  - 定位（`templates/dj.html`）：`item_position` 语义固定为持久化清单项位置（`dj_plan_items.position`，含解说与原音），页面对象携带 `data-position`；恢复优先按 `item_position` 精确定位、回退 `highlight_id`（原音），不再把"含解说的索引"当"仅原音索引"用。
  - 恢复：一次 `load + playItems` 连播目标及其后队列，移除旧实现 `playItems([target])` + `setTimeout` 二次调用的重复 play 竞态；恢复保存的倍速；清单变更（plan_id 不一致）提示不静默跳段；目标位置越界/不存在时明确提示且不恢复。
  - 进度身份：`plan_id` 绑定确切持久化清单 ID（不再用 `highlights:vN` 别名）。
  - 存储（`store/listening_progress.go`）：SELECT+UPDATE 改为单语句原子 UPSERT（`ON CONFLICT(source_type,source_id) DO UPDATE ... WHERE excluded.seq > seq`）——同时首次保存、多标签并发、倒序请求都原子安全。
- 验证：
  - `TestListeningProgress_CAS`（store，-race）：8 并发混合 seq 首存/更新 → 恰好保留最大 seq 状态。
  - `TestListeningProgress_JSONContract`（server）：POST snake_case → GET 同字段同值；无记录空对象。
  - `TestListeningProgress_RoundTrip` 既有倒序覆盖保持通过；`TestListeningProgress_PageWiring` 更新为稳定位置契约（`item_position`）。
  - `go test -timeout 40m ./internal/queue/ ./internal/server/ ./internal/store/` 全部通过；`go vet`、`gofmt`、`git diff --check`、构建通过。
- 提交：`fix(player): restore progress using stable plan item identities`。
- 限制：暂停/跳段/重复点击与"解说与原音交错时恢复第 2 段"的浏览器交互验证属 R25（DOM/JS 行为已按契约实现并以模板断言固定关键标识）。

## R12 — 素材选择成为构思输入

- 状态：实现与自动验证完成。
- 交付：
  - 迁移 0047：`ideation_sessions.selections_json`（默认 '[]'，旧行兼容）。
  - `store/creation_selection.go`：`MaterialSnapshotFromSelections`——从持久化素材选择（两集重点 + 个人笔记）逐项冻结当前内容与版本（重点：内容+引用+卡片版本；笔记：内容+引用+乐观版本）；资格不再满足（stale/Owner 排除/质量下降/已删除/选择时被排除）的材料保留在快照中附显式错误——不静默丢弃或替换用户选材。
  - `store/creation_flow.go`：会话读写含 selections_json。
  - `server/creation_workspace.go`：会话创建绑定 `selection_ids`；轮次创建优先从会话绑定的选择冻结材料快照（`material_ids` 表单直传保持兼容）。首次 GET 只读不付费、nonce 幂等、轮次追加式（旧结果不覆盖新一轮）均为既有契约，回归保持。
- 验证：
  - `TestIdeationSelectionsFlowIntoRound`（server，真实路由）：选择（Owner 确认重点 + 个人笔记）→ 绑定会话 → 轮次快照含两者内容与身份；重复提交同 nonce 不新增轮次。
  - `go test ./...` 全部通过（迁移计数断言 46→47 跟随）；`go vet`、`gofmt`、`git diff --check` 通过。
- 提交：`fix(ideation): carry selected materials into persistent sessions`。
- 限制：资格失效材料的页面级反馈样式属 R22/R25（快照错误字段已结构化可渲染）。

## R13 — 多轮诊断和候选提升入口

- 状态：实现与自动验证完成。
- 交付：
  - `queue/ideation.go`（doIdeationDiagnosisJob）：诊断实际读取已冻结前轮——遍历该会话当前轮之前的轮次（输入 + 已落库诊断的支持/反驳/缺口/候选主张摘要）作为 PriorRounds；最新约束 = 本轮 ConstraintsJSON；过期/后续轮次不进入本轮上下文。输出引用限制在材料集合内由 ValidateIdeationDiagnosis 既有校验保持（虚构引用显式失败）；空材料只出缺口（诚实，不伪造支持）。
  - 迁移 0048：`creation_proposals.ideation_round_id` + 部分唯一索引 `(ideation_round_id, proposed_claim)`——提案绑定来源轮次，同轮同主张唯一。
  - `store/creation_flow.go`：`PromoteDiagnosisClaim`——Owner 选中建议主张 → 创建 proposed 提案（不承担 OwnerClaim，material IDs 来历保留）；提升时对主张材料做该轮快照成员校验（脏数据/无效历史显式拒绝）；幂等（先查既有 + 唯一索引兜底并发，冲突后复用已插入提案）。`GetMaterialDiagnosis` 新增读取。
  - `server/creation_workspace.go` + 路由：`POST /workbench/ideation/promote` 生产入口；轮次详情页渲染每轮诊断（pre 展示 JSON）与候选主张的提升按钮（旧轮次输出可读，只归属原轮次）。
- 验证：
  - `TestIdeationDiagnosis_MultiRoundPriorContext`（queue）：第二轮请求 PriorRounds 实际包含第一轮输入与诊断摘要（候选主张可见）。
  - `TestIdeationClaimPromote_EndToEnd`（server，真实路由）：提升 → 1 条关联轮次的提案、无 OwnerClaim、材料来历保留；串行重复 + 4 并发重复 → 仍恰好 1 条；脏数据主张（引用不在快照）400 且不落库；轮次页渲染诊断与提升按钮。
  - `go test ./...` 全部通过（backup 迁移计数 47→48 跟随）；race（Ideation）通过；`go vet`、`gofmt`、`git diff --check` 通过。
- 提交：`feat(ideation): promote grounded multi-turn diagnoses to proposals`。
- 限制：诊断摘要以 JSON pre 形式渲染（结构化卡片样式属 R22/R25）；诊断中 supports/contradicts 的逐条渲染未拆分字段（原始 JSON 保留完整可读）。

## R13 follow-up — 候选依据严格校验与诊断结构化渲染（复核修复）

- 状态：实现与自动验证完成。
- 交付：
  - `store/creation_flow.go`（PromoteDiagnosisClaim）：主张引用改为**全部**必须落在该轮冻结材料快照内——任一越界引用即拒绝（此前过滤非法 ID 后放行剩余的作法会静默改变候选依据，不符契约）；零材料依据同样拒绝。另修复 `IdeationRoundID` 空值插入 NOT NULL 约束失败（空串直插，不走 NULL）。
  - `server/creation_workspace.go` + `templates/ideation_rounds.html`：诊断由原样 JSON pre 改为结构化视图渲染——明确的"支持/反驳/补充/缺口"区块与"建议主张（候选，尚非 Owner 主张）"提升按钮。
- 验证：
  - `TestIdeationClaimPromote_EndToEnd` 扩展：混合合法+非法材料引用的主张 400 拒绝且不落库；轮次页断言实际标签（支持/反驳/补充/缺口）与诊断文本、建议主张、提升按钮。
  - `go test ./...` 全部通过（迁移计数 47→48 跟随修复 IdeationRoundID 约束）；race（Ideation）通过；`go vet`、`gofmt`、`git diff --check` 通过。
- 提交：`fix(ideation): reject invalid claim materials and render structured diagnosis`（R13 follow-up 原子修复）。

## R14 — 研究缺口用具体依据解决

- 状态：实现与自动验证完成。
- 交付：
  - 迁移 0049：`research_needs` 新增 `resolution_source_type/version/detail/owner_confirmed/invalidated`（旧行默认值兼容）。
  - `store/creation_flow.go`：`ResolveResearchNeedWithEvidence`——来源类型必须显式有效；来源存在且已处理；version 必须等于来源当前版本（音频=当前转录版本，文档=文档版本且必须是该 series 最新版本）；detail 必须是该版本内的具体材料位置（Segment/文档段落 ID，跨来源/虚构拒绝）；空依据拒绝；resolved 语义为"Owner 认定缺口已解决"，不宣称机器证明事实为真（Owner 确认位单独记录）。
  - 失效传播（三条路径，同一事务内 need 重开 + 精确 proposal 集合的 confirmed Brief 标 `needs_review`，不半传播、不误伤无关 Brief；审计字段保留）：Purge/删除（`InvalidateResearchResolutions`，挂入 worker.ResumePurges）；转录重分析切换 current（`SetCurrentVersion(KindTranscript)` 后 `InvalidateSupersededTranscriptResolutions`，重分析不绕过）；同系列新文档版本（`CreateDocumentVersion` 后 `InvalidateSupersededDocumentResolutions`；旧文档 ID/版本依据此后被"系列最新版本"校验拒绝）。
  - `ConfirmCreationBrief` 接受 `needs_review` 复核再确认；HasBlockingResearchNeed 对重开的 open 缺口自然恢复阻断。
  - `server/creation_workspace.go`：resolve 表单扩展 `source_type/resolution_version/resolution_detail`。
- 验证：
  - `TestResearchNeed_ResolveRequiresEvidence`（server）：虚构来源、空依据、旧版本、跨来源 Segment 均拒绝；有效依据解决并记录类型/版本/位置/Owner 确认。
  - `TestResearchNeed_TranscriptReanalysisReblocks`（store）：重分析切版本 → need 重开+invalidated、阻断恢复、Brief needs_review；无关提案 Brief 不受误伤；新版本有效依据再解决后 needs_review 可复核确认。
  - `TestResearchNeed_DocumentNewVersionReblocks`（store）：新文档版本 → 旧版本依据失效传播；旧 ID/版本拒绝；新版本可再解决。
  - 既有 brief/研究流程测试迁移到新签名；`go test ./...` 全部通过（backup 迁移计数 48→49）；race（Research/Purge）通过；`go vet`、`gofmt`、`git diff --check` 通过。
- 提交：`fix(research): resolve gaps with versioned owner-confirmed evidence`。
- 限制：已成文文章的"待复核"状态属 R20/R21（当前传播到 Brief 层）；研究计划执行仍为 V1 外的范围。

## R14 follow-up — 转录失效按精确来源类型匹配（复核修复）

- 状态：实现与自动验证完成。
- 交付：`InvalidateSupersededTranscriptResolutions` 的 WHERE 由 `resolution_source_type IN ('episode','upload')` 改为 `resolution_source_type=?` 精确匹配——同 sourceID 的其他来源类型不被误伤；注释明确版本变更与失效传播是连续操作，失效失败向调用方返回错误（不伪装未写入）。
- 验证：`TestResearchNeed_SameIDDifferentSourceTypeNotInvalidated`——同 ID Document 依据不被 Episode 维度失效误伤；同类型版本切换才正确失效。
- 提交：`fix(research): match transcript invalidation by exact source type`。

## 门禁事故记录与修复（0e64e71）

- 事故：0e64e71（workbench resolve 表单补全）在 `go test ./...` 明确失败（backup/migrate 迁移计数断言未更新到 49）的情况下被提交——原因是验证命令经 `grep|head; echo DONE` 管道掩盖了 go test 的非零退出码。
- 修复：补齐 migrate_test/backup_test 的 version 49 断言（列表末尾、AppliedVersion、len(applied)、失败迁移保持值、备份库版本）。重跑保留退出码的 `go test ./...` → EXIT=0；`go vet ./...`、`gofmt`、`git diff --check` 通过。
- 规则更正（后续所有提交生效）：验证命令不得经 grep/head 管道丢失退出码；使用 `set -o pipefail` 或单独命令，非零即停止，不提交。

## R15 — 发现使用历史材料且验证新增价值

- 状态：实现与自动验证完成。
- 交付：
  - `server/automatic_discovery.go`（automaticDiscoveryRequest）：当前窗口材料完整发送（跨集要求保持）；以窗口材料为种子调用 `SearchKeyPointsHybrid` 做**有界历史召回**（≤6 条），逐条校验 ready/owner_confirmed、非 stale、画像资格（`IsKeyPointEligibleForProfile`，错误/false 均排除）、Provider 策略（`CanSendSourceToProvider`，LocalOnly/撤销排除）——不发送全库；新增第二 Theme `historical-context` 标注"仅供对照与延续"。`ScoutRequest` 新增 `HistoricalWorks`（≤10 条精确作品历史投影）。
  - 当前窗口按 `theme.ID=="automatic-discovery"` 显式识别；`executeAutomaticProposalBatch` 分别传递 currentMaterials 与 sentMaterials（全集），`automaticCreationProposals` 两集合显式区分——新增价值判定只看当前窗口。
  - 输出校验（先校验后落库）：伪造 ID（不在发送快照）丢弃；**纯历史重组**（只引用历史素材、满足跨集但无当前窗口）丢弃；跨集 ≥2 来源保持；空标题/主张显式拒绝；同义/重复复用既有可解释边界 `editorialTitleNearDuplicate`（包含且 ≥6 字符，或 bigram Jaccard ≥0.65，与手工 Scout 一致）且**成对比较**（同一条既有候选/作品的标题与主张双命中才 HardDuplicate；标题命中 A + 主张命中 B 不误判）；单边命中标记 `possible_duplicate:<ids>`；`follow_up` 引用当前窗口新证据标记 `follow_up_with_new_evidence`，两者可同时成立（组合格式 `follow_up_with_new_evidence;possible_duplicate:<ids>`）。空结果保留实际产出并记录 shortage（不凑数）。
- 反例（测试覆盖）：
  - 纯历史重组（两个历史 Source，满足跨集）被挡；伪造 ID 丢弃；单来源丢弃；精确重复丢弃；
  - 成对 near-dup 挡、不成对（title≈A + claim≈B）保留；
  - follow_up + possible_duplicate 组合标记；
  - LocalOnly / 画像不相关历史材料不进入发送快照（集成测试 `TestAutomaticDiscoveryRequest_HistoricalRecall`：changes 按 SourceID 显式过滤为当前窗口两集，历史 Theme 召回非空且 ≤6）；
  - HistoricalWorks 恰好 10 条且按最新截取（固定递增 created_at，含 work-11、不含 work-00）。
- 门禁证据：
  - `go test -run 'TestAutomaticCreationProposalsValidation|TestAutomaticDiscoveryRequest_HistoricalRecall' ./internal/server/` 通过；
  - `go test ./internal/server/` 通过（20.9s）；
  - `go test ./...` 全部 ok；
  - `go vet ./internal/server/`、`go vet ./...` 通过；`gofmt` 通过；
  - `go test -race ./internal/server/` 通过（568.141s）。
- 提交：`fix(discovery): ground new proposals in bounded historical context`。
- 限制：召回质量依赖本地嵌入/FTS（无外部服务）；同义判定为字符 bigram 边界（≥0.65/成对），语义级同义不承诺。

## R16 — 所有候选完成决策后释放批次

- 状态：实现与自动验证完成，等待复核后提交（当前工作区保留 R16 原子候选改动，未混入 R17）。
- 交付：
  - 迁移 0050：`creation_proposals.decision_note` 稳定 JSON 决策备注；同事务回填旧库中 `ready` 且不存在 `proposed` 子提案的批次为 `completed`，保留已有 `completed_at`；仍有 proposed 候选的 ready 批次不改变。
  - `store/proposal_batches.go`：`DecideProposal` 统一 Accept/Save/Reject 事务核心；同决策幂等，Accept 只有相同 OwnerClaim 幂等、不同主张返回冲突；Reject 保存 feedbackKind/reason JSON；Save 不丢 material/history 来历；最后一项在 batch status=ready 且 remaining=0 时同事务置 completed。三个旧 Store API 改为 wrapper，真实 HTTP handler 复用该核心。
  - `FinalizeAutomaticProposalBatch` 零候选直接 completed，不形成 ready 背压；同快照 idempotency key 不补货，新的窗口 key 可创建新批次。
- 验证：
  - `TestRunAutomaticDiscoveryCreatesOneDurableBatchAndCreationProposal` 扩展：首批完成后写新变化，生产 `RunAutomaticDiscovery` 第二次 Scout 成功并生成第二批/提案；第二批决策完成后写第三轮两个 Source、六条新变化，Provider 失败仍到达 Scout 并保留 failed 批次。
  - `proposal_batch_r16_test.go`：真实 HTTP 最后一项释放、并发两项决策 batch completed + remaining=0、零候选自动 completed、同快照不重建；同 accept+相同 OwnerClaim 303、不同 OwnerClaim 400、同 reject 不覆盖首个 decision_note；save 后材料/历史来历保持且参与 automaticCreationProposals 去重。
  - `TestMigration0050_BackfillsReadyBatches`：zero/processed ready 批次回填 completed，open 保持 ready，已有 completed_at 保持。
  - backup fixture 写入非空 decision_note，Restore 后按 proposed_claim 精确读取并解析一致。
  - 直接运行通过：`go test ./internal/server/`、`go test ./internal/store/`、`go test ./internal/backup/`、`go test ./...`、`go vet ./...`、`gofmt`、`git diff --check`。
- 限制：R16 当前尚未提交，待复核后按 `fix(proposals): complete batches atomically after owner decisions` 提交；零候选 completed 的用户提示样式属 R22/R25。

## R16 follow-up — 复核收尾

- `proposal_batch_r16_test.go`：重复 reject 的 `GetCreationProposal` 现在检查并传播读取错误；零候选测试新增 `HasOpenProposalBatch=false` 断言。
- 直接验证：`go test -run 'TestProposalBatch_' ./internal/server/` 通过；`go test -race -run TestProposalBatch_ConcurrentLastTwoDecisions_R16 ./internal/server/` 通过（4.028s）。

## R17 — Curator 方案、不可变 Brief revision、CAS 编辑与精确确认（未提交）

- 状态：实现与自动验证完成，当前工作区保留改动，未提交，未进入 R18。
- 交付：
  - 接受主张真实 HTTP 路径重新读取 accepted Proposal.OwnerClaim；创建 draft 后幂等入队 `JobCuratorBrief`，冻结 base_version、OwnerClaim、provider/model/prompt、SourceType/CardVersion、材料内容/引用。
  - 迁移 0051：creation_brief_revisions 不可变 revision；旧 confirmed 回填 confirmed_version=1，draft=0；v1 revision 回填；current revision 成为读取真源。
  - Curator 结果 checkpoint 保存完整 Result+Usage；恢复不二次调用；origin_job_id 幂等；usage receipt 唯一；ClaimPlan thesis 固定 OwnerClaim，模型 thesis 保存 curator_thesis；selected/rejected 合法性由公共 ValidateCuratorResult 校验。
  - CAS 编辑事务校验 OwnerClaim/outline、selected 非空、JSON 合法、selected/rejected 无重复/交叉且属于 Proposal candidates；继承 Curator metadata；编辑使 status=draft、confirmed_version=0。
  - 精确确认事务校验 current revision 存在、Proposal accepted/OwnerClaim、blocking gap、selected 材料 ready/owner_confirmed、非 stale/排除、source 身份/版本/归档、冻结 provider policy（external/approved allow/deny/LocalOnly/未知）。
  - Workbench 使用真实 textarea/input 编辑 OwnerClaim、outline、selected/rejected、style、target length、claim type、questions、notes；GET 从 current revision 投影；stale edit/confirm 返回 409。
- 专项测试证据：
  - provider：fabricated、selected/rejected 重复与交叉拒绝。
  - queue：正常 provider→真实 v2 revision/result version；overlap/missing/archived source 在 Provider 前失败、calls=0、revision v1；checkpoint replay calls=0、revision 唯一、receipt=1、result version=2、snapshot sourceID 真实。
  - store：CAS 成功/递增/stale/非法 JSON/空 selected/重复交叉/越界/metadata 继承；确认 valid、stale、OwnerExcluded、archived、approved allow/deny、LocalOnly、old version、blocking gap；current revision 删除读取失败。
  - server：真实 accept→snapshot/job；重复 accept Brief/job 各一；GET 表单真实字段→原样 POST→revision 投影；stale edit/confirm 409；确认门禁保留。
  - migration/backup：0051 confirmed/draft 回填及 v1；v1/v2 current/confirmed/revision 字段、decision_note 恢复精确。
  - v1 draft ClaimPlan 是可编辑对象：Curator 完成前真实 HTTP POST edit 可成功生成下一 revision；draft 创建路径复用 proposal 的任意既有 Brief 状态，8 并发调用稳定复用一个 draft ID/一行。
  - 真实 Workbench GET→原样 POST 测试覆盖 OwnerClaim、outline、selected/rejected、style、target length、claim type、questions、notes，刷新后逐字段保留；stale edit/confirm 409。
  - Create/confirm current revision 缺失、Curator policy/source/stale/excluded/archive/阻断校验均有直接测试；accepted OwnerClaim、ClaimPlan thesis、Curator metadata 与 checkpoint Usage/receipt/replay 受测。
- 门禁：`go test ./internal/server/`、`./internal/queue/`、`./internal/store/`、`./internal/backup/`、`go test ./...`、`go vet ./...`、`gofmt`、`git diff --check` 通过；Curator checkpoint race 通过。未调用真实付费模型。
- 限制：尚未提交；R18 未开始。
- 复核修复：CAS 编辑与 Curator revision 应用现在同时清空 `confirmed_at`（不残留旧授权时间）；`TestCreationBrief_EditInvalidatesPriorConfirmation` 断言 confirmed→编辑后 draft、confirmed_version=0、ConfirmedAt=nil。
