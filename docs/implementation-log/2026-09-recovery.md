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
