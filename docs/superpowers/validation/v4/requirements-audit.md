# 剩余47卡／141子要求独立审计

审计日期：2026-10-02；基线 HEAD：`d2d5b15e8e5e2aa170aaa25523eb42fa541b795c`。本审计仅修改此文档；源码与门禁由模块所有者持续更新。它是只读需求核对，不是工程完成声明。

依据：[详细47卡计划](../../plans/2026-10-02-personal-learning-v4-remaining-47-work-plan.md)。逐条核对原始141项，未使用旧 checkbox 或代理完成口述判定通过。下面链接是实际文件；测试存在与测试通过分别说明。没有重新运行整包，以避免干扰正在进行的最终门禁。

## 结论与证据适用性

最终结论：47卡／141子要求的工程实现、局部技术证据与最终全库门禁已成立，功能一致提交410e774。下面外部真实质量/实体设备状态仍保留，文档交付收尾与最终工作树clean由根代理随后实查。此前G理解/缓存语义、F SSR及CLI预算、U SSR保稿、O staging/原子发布/schema草稿复制均已出现实际修复；正式F/U/G/S/V01/V02/E报告和现行源码已核对。O正式README、Node24完整终态、真实Go摘录/DB/CDP与7截图已核；nativeIDB26工具终态观察已归档（session53638 exit0，26条），明确缺原stdout并未伪造；真实Go session24234 PASS154.44观察另有归档。最后冻结776文件候选最终race/coverage/lint/vet/build/42JS/diff均已终态通过，正式日志和SHA可核；功能一致提交410e774已完成；文档交付收尾由根代理进行。S冷性能失败依规则保持语义关闭；真实文本write冻结模型合同失败，review未发，人工/手机待验。

「局部证据成立」表示实际源码与具体定向/继承证据支持对应行为，并非全库完成；「待当前实证」表示实现/测试入口存在但要求的当前运行证据不足；「缺口」是可定位的实现不满足。外部个人资料、真实模型收益、Owner五维人评和实体手机均不由自建资料/协议桩/viewport替代。

本次独立运行：G/U store/server/backup定向通过3.011/1.669/1.270s；其组合race store36.815/server20.218s通过，backup当时在对比用途两个不同来源校验失败；该失败后续已修复，最新65.736s结果由根代理报告，未由本审计重新运行。E/O store/server/queue/backup/learningbundle定向通过2.841/3.994/1.870/1.713/2.299s；实际offline Node19/19通过。它们仅适用于当时输入树。C08四包定向race及C09/W浏览器记录为此前局部证据，见对应报告，不能替代最终V03。

权限与费用专项：备份实际Restore而非复制模板；purge清理派生正文/引用/向量但保数字账本和Owner原文；导出发布/下载和离线同步都复核当前权限；质量CLI读缓存也复核权限。unknown token/model/cost不补0且阻止不明确预算准入；已知checkpoint恢复不重复远端调用。文章默认automatic，预览计划是显式选择；查询、反馈/分类、理解保存、补听、导出及离线同步均有零模型边界。语义无有效报告failclosed，不能以性能夹具宣称真实Recall收益。

## S07 — 验证检索收益并交付开关

证据入口：[internal/store/knowledge_embedding_settings.go](../../../../internal/store/knowledge_embedding_settings.go)；[internal/store/knowledge_embedding_settings_test.go](../../../../internal/store/knowledge_embedding_settings_test.go)；[internal/store/knowledge_hybrid_benchmark_test.go](../../../../internal/store/knowledge_hybrid_benchmark_test.go)。

已核对测试函数（存在不等于当前通过）：`TestEmbeddingSettingsCASAtomicScopePriceCapacity`、`TestEmbeddingQualityGateFailClosedAndInvalidation`、`TestEmbeddingQualityReportRejectsIncompleteOrSynthetic`、`TestEmbeddingQualityIdentityIncludesCoverageEventsAndUnderstandingChoices`、`TestEmbeddingSettingsWriteFailureRollsBackChosenScopePriceAndVectors`。

| 子项与原始要求 | 独立结论 | 当前证据及限制 |
| --- | --- | --- |
| **S07.1** 补齐索引范围、费用和容量编辑：配置修订 CAS、权限预览、过期配置冲突及重建操作，复用已交付预检入口。 | 局部证据成立；最终门禁通过；功能提交410e774 | 配置修订 CAS、权限预览/重建及旧配置冲突均有实际接口与测试；费用未知不按0准入。 |
| **S07.2** 持久化质量门槛报告身份：绑定语料、来源范围、索引/模型/维数、融合规则、价格、机器和源码版本；任一相关身份变化使旧报告失效。 | 局部证据成立；最终门禁通过；功能提交410e774 | 报告绑定语料/索引 epoch/模型/维数/价格/融合规则及实际运行机/可执行文件身份；任一变化重新匹配。 |
| **S07.3** 诊断冷查询分配和加载成本，优化后按同一机器、维数和语料复测冷热延迟/内存；实现通过才允许开启、未达标/缺报告则保持关闭的完整 UI。 | 失败准入已验证；语义保持关闭 | 0089 doc_key索引及真实失效回归；新冻结二进制四进程各20样本，2048维/10k/50k，实际RSS独立计量。cold p95 175.1/837.7ms超过150/500，性能失败按契约保持关闭，FTS继续；真实embedding配置未提供，Recall待验。 |

## C08 — 迁入旧单来源StudyChat持久执行

证据入口：[internal/store/legacy_study_tasks.go](../../../../internal/store/legacy_study_tasks.go)；[internal/store/legacy_study_recovery_test.go](../../../../internal/store/legacy_study_recovery_test.go)；[internal/queue/legacy_study_boundaries_test.go](../../../../internal/queue/legacy_study_boundaries_test.go)；[internal/server/static/player.js](../../../../internal/server/static/player.js)。

已核对测试函数（存在不等于当前通过）：`TestLegacyStudyStoreStageAdoptionAndReceiptSemantics`、`TestLegacyStudyStoreKnownResponseRetryIdentityAndUnknownConsent`、`TestLegacyStudyStoreAdmissionFailuresAreAtomic`、`TestLegacyStudyStoreRevokedStoppedAndInvalidInputs`、`TestLegacyStudyStoreReviewFailureRollsBackVisibleAdoption`、`TestLegacyStudyWorkerRejectsCorruptBoundaryWithoutSupplierCall`、`TestLegacyStudyWorkerPreservesUsageForUnusablePaidResponses`、`TestLegacyStudyWorkerCheckpointAndReceiptFailureCannotResendUnknown`、`TestLegacyStudyWorkerMissingConnectionExecutionAndUnknownResultWrite`。

| 子项与原始要求 | 独立结论 | 当前证据及限制 |
| --- | --- | --- |
| **C08.1** 冻结旧单来源范围、真实转录快照与有界历史；建立旧会话的新轮命令 UUID/hash/CAS 及任务适配器，不给旧消息补造来源或付费事实。 | 局部证据成立；最终门禁通过；功能提交410e774 | 迁移0080；单来源真实快照与有界历史冻结；同UUID不同载荷409，历史消息不补造收费事实。 |
| **C08.2** 将生成和 ReferenceCheck 接入两个持久阶段：可取消调用、准确用量、独立 checkpoint/receipt、阶段预算与停止复查；只在检查通过事务内追加可见回答。 | 局部证据成立；最终门禁通过；功能提交410e774 | 生成/独立ReferenceCheck双阶段，真实QA模型冻结；预算、receipt、已知断点零重发、unknown显式新attempt、停用/清理晚到回执及原子采用均有实际故障测试。 |
| **C08.3** 保留旧 POST/历史地址，新增任务身份及只读轮询响应；播放器持久保存请求身份，处理 202/未知/冲突/恢复；旧无身份客户端得到明确兼容反馈且不发生隐藏付费。 | 局部证据成立；最终门禁通过；功能提交410e774 | 旧POST 428无身份零调用；202持久身份与只读GET恢复；当前下一问草稿独立按来源保存，退出清除。真实浏览器修复后严格2调用，无切页重发。 |

## C09 — 验收跨集与旧对话的恢复竞争

证据入口：[internal/queue/question_recovery_v4_test.go](../../../../internal/queue/question_recovery_v4_test.go)；[internal/server/legacy_study_test.go](../../../../internal/server/legacy_study_test.go)；[docs/superpowers/validation/c09/2026-10-02-question-recovery.md](../../../../docs/superpowers/validation/c09/2026-10-02-question-recovery.md)。

已核对测试函数（存在不等于当前通过）：`TestQuestionRecoveryV4ThreeSourceConflictPreservesConditions`、`TestQuestionRecoveryV4TwoWindowsReplayOneFrozenTurn`、`TestQuestionRecoveryV4AdmissionDoesNotSendUnconfirmedOrPrivateBodies`、`TestQuestionRecoveryV4UnderstandingReferenceWithdrawalBlocksDispatch`、`TestLegacyStudyHTTPRejectsUnidentifiedClientsWithoutPaying`、`TestLegacyStudyHTTPFrozenIdempotencyStatusAndCompatibilityHistory`、`TestLegacyStudyHTTPForeignSessionNeverDispatchesOrAppends`、`TestLegacyStudyHTTPRetryRejectsUnknownWithoutExplicitConsent`、`TestLegacyStudyControllerPersistentIdentityAndRecovery`。

| 子项与原始要求 | 独立结论 | 当前证据及限制 |
| --- | --- | --- |
| **C09.1** 建立三来源条件冲突、空材料、建议未确认、本地权限和历史来源失效的固定用例，对照新问答与旧单来源协议。 | 局部证据成立；最终门禁通过；功能提交410e774 | 三来源条件冲突、空/未确认/local-only与失效来源夹具均进入真实queue/provider边界。 |
| **C09.2** 覆盖双窗口、生成后崩溃、审校后崩溃、账本故障、未知响应、停用/删除竞争；核对已知恢复零新调用及费用唯一性。 | 局部证据成立；最终门禁通过；功能提交410e774 | 对应恢复/账本故障/unknown/停止清理race及唯一receipt断言；通过记录为局部候选，非最终全量。 |
| **C09.3** 运行真实路由/Worker/数据库的独立浏览器旅程：录入下一问时轮询、切页播放和恢复；分别登记桩、真实模型、人工质量。 | 局部证据成立；最终门禁通过；功能提交410e774 | 仓库正式浏览器报告包含新问答70.390s、旧QA349.286s及草稿修复87.170s；切页audio持续、下一问不被轮询清空；真实模型/人评待验。 |

## G01 — 建立可处置的材料缺口投影

证据入口：[internal/store/evidence_gaps.go](../../../../internal/store/evidence_gaps.go)；[internal/store/evidence_gaps_test.go](../../../../internal/store/evidence_gaps_test.go)。

已核对测试函数（存在不等于当前通过）：`TestEvidenceGapOwnerCASHistoryAndExpiry`、`TestEvidenceGapLocalCandidatesNoPermissionOrPaidWork`、`TestEvidenceGapModelMissingReadOnlyDedupAndScopeExpiry`、`TestEvidenceGapHomeNextActionOnlyOpenOwnerJudgments`、`TestEvidenceGapRealAudioCandidatesAndUntreatedTitle`。

| 子项与原始要求 | 独立结论 | 当前证据及限制 |
| --- | --- | --- |
| **G01.1** 为缺口定义父对象/修订、类型、来历、覆盖范围与解释，复用文章/更新已有 Missing 字段，建立稳定去重身份。 | 局部证据成立；最终门禁通过；功能提交410e774 | TestEvidenceGapModelMissingReadOnlyDedupAndScopeExpiry；Missing仅投影、父身份/hash及来源分类。 |
| **G01.2** 实现待处理/有帮助/仍不足/忽略/过期等处置及 CAS 记录；父正文或范围改变时使判断过期，不暗改旧结论。 | 局部证据成立；最终门禁通过；功能提交410e774 | TestEvidenceGapOwnerCASHistoryAndExpiry；父与自身CAS、历史、范围过期。 |
| **G01.3** 接入来源/父稿/问题清理及恢复规则；给出仅基于已查范围的只读投影，未知原因保留为未知。 | 局部证据成立；最终门禁通过；功能提交410e774 | 0082触发器清理派生内容；未知程序原因保持unknown；Source purge浏览器记录。 |

## G02 — 检索缺口候选并确认范围

证据入口：[internal/store/evidence_gaps.go](../../../../internal/store/evidence_gaps.go)；[internal/server/evidence_gaps.go](../../../../internal/server/evidence_gaps.go)；[internal/store/evidence_gaps_test.go](../../../../internal/store/evidence_gaps_test.go)。

已核对测试函数（存在不等于当前通过）：`TestEvidenceGapOwnerCASHistoryAndExpiry`、`TestEvidenceGapLocalCandidatesNoPermissionOrPaidWork`、`TestEvidenceGapModelMissingReadOnlyDedupAndScopeExpiry`、`TestEvidenceGapHomeNextActionOnlyOpenOwnerJudgments`、`TestEvidenceGapRealAudioCandidatesAndUntreatedTitle`。

| 子项与原始要求 | 独立结论 | 当前证据及限制 |
| --- | --- | --- |
| **G02.1** 接入统一 Retrieve 的本地用途，明确触发最多 20 项候选，记录覆盖方式、遗漏和可验证的匹配原因。 | 局部修复证据成立；最终门禁通过；功能提交410e774 | 实际FTS/已授权语义缓存配置选择已接UI；无有效报告/查询缓存则明确FTS回退，不生成query embedding、不新增paidjob。缓存RRf/method一致确认测试，不冒称真实语义收益。 |
| **G02.2** 解析候选真实快照/片段或段落位置，显示已关联状态；确认调用既有问题关系 CAS，候选本身不改变权限。 | 局部修复证据成立；最终门禁通过；功能提交410e774 | understanding snapshot真实UUID/Version/空Source关联已实现；resolver及0088 CHECK同步升级，87→88旧关系字段/回滚/foreign_key_check测试，local_only不自动授权。 |
| **G02.3** 提供只读候选与明确确认接口；测试 local_only 本地可读、未处理来源不自动处理、重复确认和旧修订冲突。 | 局部修复证据成立；最终门禁通过；功能提交410e774 | 确认/补听携带同semantic/configID及retrieval_method，重查方法改变或篡改409；HTTP理解实际确认、权限复核且零模型。 |

## G03 — 扩展区间队列身份与迁移

证据入口：[internal/store/learning_excerpts.go](../../../../internal/store/learning_excerpts.go)；[internal/store/learning_excerpts_test.go](../../../../internal/store/learning_excerpts_test.go)。

已核对测试函数（存在不等于当前通过）：`TestLearningExcerptsFrozenWindowsQueueAndProgress`、`TestLearningExcerptsUpgrade69PreservesQueueIDsAndSelection`、`TestLearningExcerptsQueueCASLimitAndPurge`。

| 子项与原始要求 | 独立结论 | 当前证据及限制 |
| --- | --- | --- |
| **G03.1** 为补听条目定义真实 snapshot、segment window、audioSHA 与区间身份，设计新增迁移和分类型唯一约束。 | 局部证据成立；最终门禁通过；功能提交410e774 | TestLearningExcerptsFrozenWindowsQueueAndProgress：snapshot/hash/绝对区间真实冻结。 |
| **G03.2** 将区间加入/移除/重排接入既有队列 CAS、去重和 500 项上限；同一集不同区间可共存。 | 局部证据成立；最终门禁通过；功能提交410e774 | TestLearningExcerptsQueueCASLimitAndPurge：500上限/重复幂等/不同区间/CAS。 |
| **G03.3** 以真实 0069 旧库升级，保持原条目 ID、顺序和当前选择；新增来源删除、音频替换和快照失效检查。 | 局部证据成立；最终门禁通过；功能提交410e774 | 实际≤0069 SQLite升级保留队列ID/position/revision/selection/autoplay，音频替换拒绝。 |

## G04 — 播放补听区间并隔离进度

证据入口：[internal/server/static/listening-session.js](../../../../internal/server/static/listening-session.js)；[internal/server/testdata/listening-excerpts.cjs](../../../../internal/server/testdata/listening-excerpts.cjs)；[internal/store/learning_excerpts_test.go](../../../../internal/store/learning_excerpts_test.go)。

已核对测试函数（存在不等于当前通过）：`TestLearningExcerptsFrozenWindowsQueueAndProgress`、`TestLearningExcerptsUpgrade69PreservesQueueIDsAndSelection`、`TestLearningExcerptsQueueCASLimitAndPurge`。

| 子项与原始要求 | 独立结论 | 当前证据及限制 |
| --- | --- | --- |
| **G04.1** 扩展根播放器与 transport 支持真实区间起止和局部游标；刷新恢复保持暂停，绝对音频时间仍作为笔记锚点。 | 局部证据成立；最终门禁通过；功能提交410e774 | 实际listening-excerpts.cjs：旧快照暂停、seek边界、笔记绝对时间与segment身份。 |
| **G04.2** 分离整集、DJ、区间进度身份；明确区间结束、连播、循环及睡眠的优先级，不自动标记整集听完。 | 局部证据成立；最终门禁通过；功能提交410e774 | 独立区间进度，不污染整集/另区间；Node睡眠/循环/末尾优先级。 |
| **G04.3** 补齐原音丢失/替换时的拒播及解释；用实际脚本和浏览器验证多个区间与整集/DJ 切换。 | 实际浏览器组合已证明；手机待验 | 正式补验09–11实际DJ/两个摘录/原音保持同Audio、四份独立进度，UI循环、2.4秒自然加速睡眠到期优先停止、刷新暂停恢复；不等于手机后台15分钟。 |

## G05 — 交付缺口补听页面与下一步

证据入口：[internal/server/templates/evidence_gaps.html](../../../../internal/server/templates/evidence_gaps.html)；[internal/server/evidence_gaps_test.go](../../../../internal/server/evidence_gaps_test.go)；[docs/acceptance/2026-10-02-gaps-browser.md](../../../../docs/acceptance/2026-10-02-gaps-browser.md)。

已核对测试函数（存在不等于当前通过）：`TestEvidenceGapHTTPReadOnlyExplicitFindAndCAS`、`TestEvidenceGapDraftBehavior`、`TestEvidenceGapActualRouterPrivacyAndCSRFFallback`、`TestEvidenceGapHTTPUnderstandingAndSemanticFallback`。

| 子项与原始要求 | 独立结论 | 当前证据及限制 |
| --- | --- | --- |
| **G05.1** 提供问题缺口页：范围与来历、候选、确认、加入补听、文档阅读和 Owner 处置，动作使用共用表单契约。 | 局部证据成立；最终门禁通过；功能提交410e774 | SSR处置/查找/确认/入队齐全，HTTP、Node、正式浏览器支持。 |
| **G05.2** 接入首页下一步投影，显示建议原因；未经确认不进生成范围，文档进入阅读入口而非音频队列。 | 局部证据成立；最终门禁通过；功能提交410e774 | 下一步显示原因；仅Playable给音频入口，文档阅读，候选不自动授权。 |
| **G05.3** 完成 SSR、冲突保留、重复动作、播放/草稿连续导航和焦点测试；听完不自动关闭缺口。 | 局部证据成立；最终门禁通过；功能提交410e774 | 双标签409保留文字与CAS，跨页audio/整理稿和焦点；听完不自动关闭。 |

## G06 — 验收补听的失效与恢复

证据入口：[internal/store/learning_excerpts_test.go](../../../../internal/store/learning_excerpts_test.go)；[internal/backup/learning_excerpts_test.go](../../../../internal/backup/learning_excerpts_test.go)；[docs/acceptance/2026-10-02-gaps-browser.md](../../../../docs/acceptance/2026-10-02-gaps-browser.md)。

已核对测试函数（存在不等于当前通过）：`TestLearningExcerptsFrozenWindowsQueueAndProgress`、`TestLearningExcerptsUpgrade69PreservesQueueIDsAndSelection`、`TestLearningExcerptsQueueCASLimitAndPurge`、`TestLearningExcerptsV2RestorePreservesFrozenLearningAndPaidFacts`。

| 子项与原始要求 | 独立结论 | 当前证据及限制 |
| --- | --- | --- |
| **G06.1** 组合多区间与父版本变化、音频 hash 变化、来源清理、重复加入及旧队列升级用例。 | 局部证据成立；最终门禁通过；功能提交410e774 | 区间/去重/purge/音频身份/历史队列/父范围测试实际通过。 |
| **G06.2** 执行 v2 备份恢复与真实播放身份核对；区间、整集、DJ 进度互不污染。 | 当前目标恢复已证明；最终门禁通过；功能提交410e774 | 实际区间v2 restore与综合恢复均通过；最新真实两来源、逐stage生产校验、0088实际理解关系race count3 PASS65.736s，命令/session56476已落主集成报告。 |
| **G06.3** 独立浏览器复验跨页播放和失效提示，核对读/播放零转录、零 DJ 生成、零模型调用及账本保留。 | 实际浏览器已证明；产品质量外部待验 | 正式缺口旅程及09–11补验实际DJ/循环/睡眠/刷新，零新增usage；自建静音WAV不能代表可听性/手机后台。 |

## U01 — 建立无伪来源的理解快照模型

证据入口：[internal/store/understanding_snapshots.go](../../../../internal/store/understanding_snapshots.go)；[internal/store/understanding_snapshots_test.go](../../../../internal/store/understanding_snapshots_test.go)。

已核对测试函数（存在不等于当前通过）：`TestUnderstandingSnapshotModelNoSourceImmutableAndDelete`、`TestUnderstandingSnapshotSaveCASReplayRollback`、`TestUnderstandingSnapshotPermissionsPurgeKeepsOwner`、`TestUnderstandingSnapshotNoSourceQuestionStudyAdmissionAndRevocation`、`TestUnderstandingSnapshotBackupRestorePreservesHistoryCurrentAndReferences`。

| 子项与原始要求 | 独立结论 | 当前证据及限制 |
| --- | --- | --- |
| **U01.1** 建立理解头、不可变快照及多来源 Reference，区分正文版本和当前选择；允许没有来源的个人回答。 | 局部证据成立；最终门禁通过；功能提交410e774 | 独立snapshot无伪Source，正文不可变；保存与当前选择分离。 |
| **U01.2** 为快照建立自身默认 local_only 的模型数据策略和 Provider 清单，验证真实引用版本，不借用伪 Source 或 Source Citation。 | 局部证据成立；最终门禁通过；功能提交410e774 | 确切Reference版本、自身local-only与全部引用权限交集；理解不得当Citation。 |
| **U01.3** 来源清理移除引用正文并明确失效，保留 Owner 答案；问题删除级联清理，新增迁移和备份读取夹具。 | 局部证据成立；最终门禁通过；功能提交410e774 | 0083清理引用正文/派生向量保留Owner正文；定向及当前组合v2恢复count3证据成立，最终门禁通过；功能提交410e774。 |

## U02 — 实现保存与选择当前的CAS命令

证据入口：[internal/store/understanding_snapshots.go](../../../../internal/store/understanding_snapshots.go)；[internal/server/understanding_snapshots_test.go](../../../../internal/server/understanding_snapshots_test.go)。

已核对测试函数（存在不等于当前通过）：`TestUnderstandingSnapshotSaveHTTPAuthCASAndOwnerConfirmation`、`TestUnderstandingSnapshotHistoryShowsChangedReferenceWithoutOverwritingAnswer`。

| 子项与原始要求 | 独立结论 | 当前证据及限制 |
| --- | --- | --- |
| **U02.1** 实现保存新版本的命令：问题修订、理解头修订、父版本、UUID/hash 与完整引用在同一事务校验。 | 局部证据成立；最终门禁通过；功能提交410e774 | UUID/hash、question/head双CAS及Reference事务解析；并发/故障回滚实际测试。 |
| **U02.2** 实现明确选择当前版本与有界历史读取；保存与选择独立，旧版本不重写，状态不会自动变为问题已解决。 | 局部证据成立；最终门禁通过；功能提交410e774 | 有界历史/明确choose；保存不自动current，不修改solved。 |
| **U02.3** 接入鉴权/CSRF 接口，测试并发同输入唯一、旧修订 409、事务故障回滚及来源失效。 | 局部修复证据成立；最终门禁通过；功能提交410e774 | 认证/CSRF/事务故障正常；新增SSR恢复表单及TestUnderstandingSSRConflictRetainsExactSubmittedDraftIdentity保存原answer/head/parent/UUID，JSON合同不变，未落盘或偷换修订。 |

## U03 — 交付理解编辑器与历史差异

证据入口：[internal/server/static/understandings.js](../../../../internal/server/static/understandings.js)；[internal/server/understanding_snapshots_test.go](../../../../internal/server/understanding_snapshots_test.go)；[docs/superpowers/plans/2026-10-02-understanding-snapshots-validation.md](../../../../docs/superpowers/plans/2026-10-02-understanding-snapshots-validation.md)。

已核对测试函数（存在不等于当前通过）：`TestUnderstandingSnapshotSaveHTTPAuthCASAndOwnerConfirmation`、`TestUnderstandingSnapshotHistoryShowsChangedReferenceWithoutOverwritingAnswer`。

| 子项与原始要求 | 独立结论 | 当前证据及限制 |
| --- | --- | --- |
| **U03.1** 提供当前答案、不确定之处、下一步和引用编辑，接入 C07 对话草稿及明确的笔记引用导入。 | 局部证据成立；最终门禁通过；功能提交410e774 | 模型草稿与笔记明确导入，Owner二次确认，不自动新增OwnerNote。 |
| **U03.2** 按问题/理解头身份保存草稿，显示旧版本差异、引用变化与当前选择；父版本变化不得覆盖未提交文字。 | 局部修复证据成立；最终门禁通过；功能提交410e774 | JS双标签稿及历史选择；SSR409 exact身份恢复表单保稿、不自动改最新版本/checkbox清空，原问题与head命名空间保留。 |
| **U03.3** 接入共用表单及 SSR，验证双窗口冲突、历史选择、清理提示和焦点；模型导入必须 Owner 确认，不新增 OwnerNote。 | purge浏览器及SSR修复已证明；最终门禁通过；功能提交410e774 | 正式purge浏览器原文消失/Owner答案保留/警告显示及两张repo截图；SSR恢复新增exactidentity测试，nil Draft模板安全保护，V02 fresh fixture已使用修复。 |

## U04 — 接入理解的检索与文章选材

证据入口：[internal/store/understanding_eligibility.go](../../../../internal/store/understanding_eligibility.go)；[internal/store/knowledge_updates.go](../../../../internal/store/knowledge_updates.go)；[internal/store/understanding_snapshots_test.go](../../../../internal/store/understanding_snapshots_test.go)。

已核对测试函数（存在不等于当前通过）：`TestUnderstandingSnapshotModelNoSourceImmutableAndDelete`、`TestUnderstandingSnapshotSaveCASReplayRollback`、`TestUnderstandingSnapshotPermissionsPurgeKeepsOwner`、`TestUnderstandingSnapshotNoSourceQuestionStudyAdmissionAndRevocation`、`TestUnderstandingSnapshotBackupRestorePreservesHistoryCurrentAndReferences`。

| 子项与原始要求 | 独立结论 | 当前证据及限制 |
| --- | --- | --- |
| **U04.1** 新增当前理解的独立检索 material kind、不可变版本和变更事件；历史只在明确历史查询中读取。 | 局部证据成立；最终门禁通过；功能提交410e774 | current/history独立检索类；选择事件使索引失效。 |
| **U04.2** 实现自身与全部引用来源的聚合外发资格；没有 source_id 不得绕过授权，不能误标为原话。 | 局部证据成立；最终门禁通过；功能提交410e774 | 空Source不绕自身/全部引用/嵌套文章理解权限及冻结身份。 |
| **U04.3** 接入文章选材、问题冻结、向量资格和已有文章更新提案；验证撤权/清理即时失效及旧材料兼容。 | 局部证据成立；最终门禁通过；功能提交410e774 | 问答/选材/embedding冻结理解；current变更传播learning_question文章更新信号，撤回/purge即时失效。 |

## U05 — 验收理解演变与清理恢复

证据入口：[internal/store/understanding_snapshots_test.go](../../../../internal/store/understanding_snapshots_test.go)；[internal/backup/personal_learning_v4_restore_test.go](../../../../internal/backup/personal_learning_v4_restore_test.go)；[docs/superpowers/plans/2026-10-02-understanding-snapshots-validation.md](../../../../docs/superpowers/plans/2026-10-02-understanding-snapshots-validation.md)。

已核对测试函数（存在不等于当前通过）：`TestUnderstandingSnapshotModelNoSourceImmutableAndDelete`、`TestUnderstandingSnapshotSaveCASReplayRollback`、`TestUnderstandingSnapshotPermissionsPurgeKeepsOwner`、`TestUnderstandingSnapshotNoSourceQuestionStudyAdmissionAndRevocation`、`TestUnderstandingSnapshotBackupRestorePreservesHistoryCurrentAndReferences`、`TestPersonalLearningV4RealRestoreRelationships`。

| 子项与原始要求 | 独立结论 | 当前证据及限制 |
| --- | --- | --- |
| **U05.1** 固定跨集理解→新材料→新版本→当前选择的全过程，对照旧正文和引用身份保持不变。 | 局部证据成立；最终门禁通过；功能提交410e774 | 引用旧版本不随笔记修改，保存v2后current仍v1；实际浏览器历史完整。 |
| **U05.2** 覆盖双窗口、撤权/清理、当前指针、检索/向量、选材以及升级和 v2 恢复组合。 | 当前目标恢复已证明；最终门禁通过；功能提交410e774 | CAS/purge/search/向量定向通过；0088真实理解关系综合v2恢复当前race count3 PASS65.736s，主集成报告命令/session可核。 |
| **U05.3** 独立浏览器验收编辑及历史差异，将真实有用性与模型/人工评分另列，给 E/O 组稳定读取契约。 | 局部证据成立；最终门禁通过；功能提交410e774 | 工程浏览器证据成立；真人理解质量、真实笔记/模型/人评外部待验。 |

## W01 — 定义用途目录与新提示版本

证据入口：[internal/provider/writing_modes.go](../../../../internal/provider/writing_modes.go)；[internal/provider/writing_modes_test.go](../../../../internal/provider/writing_modes_test.go)。

已核对测试函数（存在不等于当前通过）：`TestWritingModeCatalogFrozen`、`TestWritingModeRequestAndActualMessages`、`TestWritingModeStructures`、`TestKnowledgePromptCompatibilityWritingModes`、`TestWritingModeUnderstandingRemainsOwnerReflection`。

| 子项与原始要求 | 独立结论 | 当前证据及限制 |
| --- | --- | --- |
| **W01.1** 定义默认综合、知识解释、观点对比、实践指南四用途目录，冻结规则版本和 fingerprint。 | 局部证据成立；最终门禁通过；功能提交410e774 | 四模式目录、writing-purpose-v1指纹与固定JSON金样。 |
| **W01.2** 新增用途请求版本，保留旧 v1–v4 解码、消息、输出上限和历史阅读契约。 | 局部证据成立；最终门禁通过；功能提交410e774 | 新v5实际请求；旧v1–v4允许解码/不加用途字段，旧输出界限保持。 |
| **W01.3** 拒绝未知用途/规则版本；以固定序列化金样和默认综合回归验证，不增加权限或模型调用。 | 局部证据成立；最终门禁通过；功能提交410e774 | 未知模式/版本拒绝、固定synthesis序列化金样；无额外检索/模型调用。 |

## W02 — 冻结用途到所有文章阶段

证据入口：[internal/store/knowledge_writing_purposes.go](../../../../internal/store/knowledge_writing_purposes.go)；[internal/queue/writing_purpose_test.go](../../../../internal/queue/writing_purpose_test.go)；[internal/store/knowledge_writing_purposes_test.go](../../../../internal/store/knowledge_writing_purposes_test.go)。

已核对测试函数（存在不等于当前通过）：`TestWritingPurposeQueueFourModesFreezeActualMessagesAndEstimates`、`TestWritingPurposeBadStructureAndOutputOverrunKeepPaidResponse`、`TestWritingPurposeLegacyV4KnownCheckpointRecoveryNeverRecalls`、`TestWritingPurposeDefaultsAndDistinctDiscoveryIdentity`、`TestWritingPurposeExplicitRevisionPreservesPassedVersionAndFrozenStages`、`TestWritingPurposeKnownLegacyStagePreserved`。

| 子项与原始要求 | 独立结论 | 当前证据及限制 |
| --- | --- | --- |
| **W02.1** 将用途、规则版本和指纹写入发现、候选、写作、修订、审校的冻结输入及幂等身份。 | 局部证据成立；最终门禁通过；功能提交410e774 | 实际冻结用途到request及revision身份；所有阶段实际messages测试。 |
| **W02.2** 实际接入 U04 理解材料及完整消息估价；同范围不同用途形成可区分请求，用途变化建立新修订。 | 局部证据成立；最终门禁通过；功能提交410e774 | 理解作为Owner reflection入消息/估价，不能当来源引文；用途变化新revision。 |
| **W02.3** 保持旧通过版、审校哈希与断点原样；验证旧已知响应恢复零付费重发，不把用途改变当成知识增量。 | 局部证据成立；最终门禁通过；功能提交410e774 | LegacyV4KnownCheckpointRecoveryNeverRecalls验证旧checkpoint只有原receipt，旧通过版保留。 |

## W03 — 实现用途结构及审校判据

证据入口：[internal/provider/writing_modes.go](../../../../internal/provider/writing_modes.go)；[internal/provider/writing_modes_test.go](../../../../internal/provider/writing_modes_test.go)；[internal/queue/writing_purpose_test.go](../../../../internal/queue/writing_purpose_test.go)。

已核对测试函数（存在不等于当前通过）：`TestWritingModeCatalogFrozen`、`TestWritingModeRequestAndActualMessages`、`TestWritingModeStructures`、`TestKnowledgePromptCompatibilityWritingModes`、`TestWritingModeUnderstandingRemainsOwnerReflection`、`TestWritingPurposeQueueFourModesFreezeActualMessagesAndEstimates`、`TestWritingPurposeBadStructureAndOutputOverrunKeepPaidResponse`、`TestWritingPurposeLegacyV4KnownCheckpointRecoveryNeverRecalls`。

| 子项与原始要求 | 独立结论 | 当前证据及限制 |
| --- | --- | --- |
| **W03.1** 把四用途结构要求写入选题、写作和审校实际消息，不以页面标签代替行为。 | 局部证据成立；最终门禁通过；功能提交410e774 | KnowledgeArticleMessages实际调用四用途结构规则，非仅页面标签。 |
| **W03.2** 增加程序结构检查和用途审校判据，区分例子不足、比较条件缺失、实践证据不足和输出超限。 | 局部证据成立；最终门禁通过；功能提交410e774 | 结构校验/独立review及输出超限失败，不推进。 |
| **W03.3** 建立同素材四模式夹具；拒绝编造经历/效果/例子，错误响应保留 receipt 且不推进，默认综合回归。 | 局部证据成立；最终门禁通过；功能提交410e774 | 同素材四模式messages/结构夹具；坏响应保留numericusage及原响应。 |

## W04 — 持久化可选写作计划与确认

证据入口：[internal/store/knowledge_writing_plans.go](../../../../internal/store/knowledge_writing_plans.go)；[internal/store/knowledge_writing_plans_test.go](../../../../internal/store/knowledge_writing_plans_test.go)。

已核对测试函数（存在不等于当前通过）：`TestKnowledgeWritingPlanExactRevisionAndIdempotency`、`TestKnowledgeWritingPlanBlockedPreservesPlan`、`TestKnowledgeWritingPlanTamperAndSourceRevocation`、`TestKnowledgeWritingPlanConcurrentConfirmationAndRollback`、`TestKnowledgeWritingPlanPricePurposeAndParentRevision`。

| 子项与原始要求 | 独立结论 | 当前证据及限制 |
| --- | --- | --- |
| **W04.1** 持久化选材后的可选计划：材料、用途、提纲、引用范围、指纹和版本；默认自动模式不插入等待。 | 局部证据成立；最终门禁通过；功能提交410e774 | 不可变计划确切writeRequest/hash/parentrevision，自动默认直接ready不等待。 |
| **W04.2** Owner 编辑产生新计划修订，确认确切版本才准入写作预算/控制；确认身份必须唯一对应写作任务。 | 局部证据成立；最终门禁通过；功能提交410e774 | edit新版本；Confirm冻结版本幂等唯一job，事务复核预算/控制/材料身份。 |
| **W04.3** 验证旧计划 409、重复确认、预算/停止阻断、来源变化和事务恢复；不能重新选材冒充已确认计划。 | 局部证据成立；最终门禁通过；功能提交410e774 | 旧版本/重复/并发确认、预算unknown/stop、sourcepurge、价格变化及事务故障测试。 |

## W05 — 交付用途与计划交互

证据入口：[internal/server/knowledge_writing_coverage_test.go](../../../../internal/server/knowledge_writing_coverage_test.go)；[internal/server/writing_browser_acceptance_test.go](../../../../internal/server/writing_browser_acceptance_test.go)；[docs/acceptance/2026-10-02-writing-purpose.md](../../../../docs/acceptance/2026-10-02-writing-purpose.md)。

已核对测试函数（存在不等于当前通过）：`TestKnowledgeWritingCoverageInvalidGenerationPreservesScope`、`TestKnowledgeWritingCoveragePlanFailuresDoNotAdmitOrMutate`、`TestKnowledgeWritingCoverageExplicitRevisionGuardsAndMissingPlan`、`TestKnowledgeWritingCoverageManualSaveCannotInjectPurposeSections`、`TestKnowledgeWritingCoverageExistingQuestionPurposeRequiresNewRevision`、`TestWritingPurposeBrowserHarness`。

| 子项与原始要求 | 独立结论 | 当前证据及限制 |
| --- | --- | --- |
| **W05.1** 扩展偏好和文章表单的用途选择、是否预览计划、计划编辑与确认，旧偏好保留兼容默认值。 | 局部证据成立；最终门禁通过；功能提交410e774 | 实际用途、预览计划及编辑/确认表单；默认synthesis/automatic兼容。 |
| **W05.2** 页面解释当前阶段、用途、估计费用与阻断；已生成文章修改用途走明确新修订，原通过版继续可读。 | 局部证据成立；最终门禁通过；功能提交410e774 | 新用途新修订、旧v1继续阅读下载；实际历史页用冻结用途规则。 |
| **W05.3** 验收默认全自动与计划确认两条 SSR/JS 路径，轮询不清稿、播放不中断、查看计划零付费。 | 实际双路径及连续播放已证明；最终门禁通过；功能提交410e774 | 独立writing browser自动/preview/dirty/confirm，SSR HTTP路径；V02 fresh实际root toggle启动后十导航同Audio且paused=false/time20.269619→21.684919，补齐持续播放证据。 |

## W06 — 验收四用途及旧通过版兼容

证据入口：[internal/queue/writing_purpose_test.go](../../../../internal/queue/writing_purpose_test.go)；[internal/store/knowledge_writing_plans_test.go](../../../../internal/store/knowledge_writing_plans_test.go)；[docs/acceptance/2026-10-02-writing-purpose.md](../../../../docs/acceptance/2026-10-02-writing-purpose.md)。

已核对测试函数（存在不等于当前通过）：`TestWritingPurposeQueueFourModesFreezeActualMessagesAndEstimates`、`TestWritingPurposeBadStructureAndOutputOverrunKeepPaidResponse`、`TestWritingPurposeLegacyV4KnownCheckpointRecoveryNeverRecalls`、`TestKnowledgeWritingPlanExactRevisionAndIdempotency`、`TestKnowledgeWritingPlanBlockedPreservesPlan`、`TestKnowledgeWritingPlanTamperAndSourceRevocation`、`TestKnowledgeWritingPlanConcurrentConfirmationAndRollback`、`TestKnowledgeWritingPlanPricePurposeAndParentRevision`。

| 子项与原始要求 | 独立结论 | 当前证据及限制 |
| --- | --- | --- |
| **W06.1** 固定四用途的结构、引用、归因及材料不足基准，连同补听、理解材料和计划状态验证。 | 局部证据成立；最终门禁通过；功能提交410e774 | 四模式结构/不足、理解权限、计划状态工程夹具；不能推断真实文章质量。 |
| **W06.2** 组合旧文章升级、改用途、旧断点、价格/预算、停止竞争及独立审校身份，保持旧通过版可读。 | 局部证据成立；最终门禁通过；功能提交410e774 | 旧v4断点、改用途、预算价格停止并发、独立阶段模型/审校哈希测试。 |
| **W06.3** 提供同材料有界真实 POD 对照入口及完整报告；缺配置、缺人工评分分别待验，不降低结构或事实要求。 | 待当前实证/最终门禁 | quality-eval有界冻结案例真实POD入口；真实四模式同材料输出与五维人评尚待外部验证。 |

## F01 — 将段落反馈冻结为版本案例

证据入口：[internal/store/article_feedback_cases.go](../../../../internal/store/article_feedback_cases.go)；[internal/store/article_feedback_cases_test.go](../../../../internal/store/article_feedback_cases_test.go)。

已核对测试函数（存在不等于当前通过）：`TestArticleFeedbackCaseLifecycle`、`TestArticleFeedbackCaseUsefulWholeArticle`、`TestArticleFeedbackCaseSourcePurgeRetainsOwnerRecord`、`TestArticleFeedbackCaseFrozenFacts`。

| 子项与原始要求 | 独立结论 | 当前证据及限制 |
| --- | --- | --- |
| **F01.1** 反馈绑定文章 ID、确切正文修订/哈希和稳定段落身份；旧文章使用可验证位置/内容哈希。 | 局部证据成立；最终门禁通过；功能提交410e774 | article/revision/contenthash/paragraphhash确切绑定；整篇useful不伪错误段落。 |
| **F01.2** 分离反馈与接纳案例，接纳 UUID/hash 唯一；期望编辑产生新案例版本，不改原文章或偏好。 | 局部证据成立；最终门禁通过；功能提交410e774 | feedback与accepted case分离，UUID/CAS新caseversion，不改文章/偏好。 |
| **F01.3** 接入来源/文章清理与恢复，移除不应保留的冻结正文；有用反馈不要求伪造错误段落。 | 局部证据成立；最终门禁通过；功能提交410e774 | sourcepurge移除冻结正文，Owner评论保留；真实v2专项恢复。 |

## F02 — 给出材料不足与反馈的事实分类

证据入口：[internal/store/article_feedback_cases.go](../../../../internal/store/article_feedback_cases.go)；[internal/store/article_feedback_cases_test.go](../../../../internal/store/article_feedback_cases_test.go)。

已核对测试函数（存在不等于当前通过）：`TestArticleFeedbackCaseLifecycle`、`TestArticleFeedbackCaseUsefulWholeArticle`、`TestArticleFeedbackCaseSourcePurgeRetainsOwnerRecord`、`TestArticleFeedbackCaseFrozenFacts`。

| 子项与原始要求 | 独立结论 | 当前证据及限制 |
| --- | --- | --- |
| **F02.1** 读取已记录的检索覆盖、容量遗漏、材料及阶段事实，给出可解释分类候选及依据。 | 局部证据成立；最终门禁通过；功能提交410e774 | 只读取已记录coverage/capacity/material/stage事实，不运行诊断模型。 |
| **F02.2** Owner 明确确认分类；模型原因不直接写成事实，没有证据保持未分类，新证据不暗改旧结论。 | 局部证据成立；最终门禁通过；功能提交410e774 | 明确classify UUID/CAS；unknown保留，新事实不暗改旧分类。 |
| **F02.3** 建立覆盖/漏召回/容量/未知分类表及旧字段缺失测试；页面读取和分类动作零诊断模型调用。 | 局部证据成立；最终门禁通过；功能提交410e774 | 旧缺字段、unknown/coverage/容量表测试，GET及classify零AI。 |

## F03 — 交付段落反馈与案例管理

证据入口：[internal/server/article_feedback_cases.go](../../../../internal/server/article_feedback_cases.go)；[internal/server/templates/quality_cases.html](../../../../internal/server/templates/quality_cases.html)；[internal/server/article_feedback_cases_test.go](../../../../internal/server/article_feedback_cases_test.go)。

已核对测试函数（存在不等于当前通过）：`TestArticleQualityHTTPPrivacyAnchorsAndCAS`。

| 子项与原始要求 | 独立结论 | 当前证据及限制 |
| --- | --- | --- |
| **F03.1** 提供段落选择、问题/期望编辑和案例管理，保留原四种反馈及明确写作偏好入口。 | 局部证据成立；最终门禁通过；功能提交410e774 | 段落反馈、原四分类、反馈选择/案例管理实际表单入口。 |
| **F03.2** 接纳/修订/退役使用 UUID 和 CAS；定位失效提示原版本，不偷换新段落，不自动调用模型。 | 局部证据成立；最终门禁通过；功能提交410e774 | accept/classify/retire UUID/CAS，确切旧正文，不自动模型。 |
| **F03.3** 完成历史文章、409 保留、SSR/实际脚本和浏览器测试；公开报告不包含私有正文或凭据。 | 修复及实际浏览器已证明；最终门禁通过；功能提交410e774 | 正式质量报告：SSR预填UUID、无JS native303成功/409保原request/version/feedback/expected；实际Chromium历史入口→接纳→旧CAS保稿→退役，真实CWPForms VM及四包race通过。 |

## F04 — 把接纳案例接入冻结评测清单

证据入口：[internal/store/article_feedback_cases.go](../../../../internal/store/article_feedback_cases.go)；[internal/evalset/article_quality.go](../../../../internal/evalset/article_quality.go)；[cmd/quality-eval/main.go](../../../../cmd/quality-eval/main.go)。

| 子项与原始要求 | 独立结论 | 当前证据及限制 |
| --- | --- | --- |
| **F04.1** 仅从明确接纳且权限有效的案例生成有界评测 manifest，冻结来源、用途、问题、期望及全部指纹。 | 局部证据成立；最终门禁通过；功能提交410e774 | accepted+当前权限限定manifest，数量100/4MiB，冻结purpose/input/source/expected/hash。 |
| **F04.2** 建立隔离副本运行和恢复入口，同样本比较从同一快照开始；未接纳案例不外发。 | 局部与预算修复已证明；真实合同失败保留 | CLI隔离output和preview零AI；生产预算账本HTTP前准入/pending，价未知且配置预算则拒绝。已知receipt补账零重发；真实一次write模型返回不匹配失败，不发送review。 |
| **F04.3** 记录原输出、失败、checkpoint、receipt 和缺失 human；用自建素材测试 schema、权限与副本互不污染。 | 工程及真实失败回执已证明；人工待验 | 真实write仅一次6853ms、冻结google/gemini-3.8-flash-high返回gemini-3.8-flash合同不一致；保留1428/983数字用量，1receipt、unknown cost/预算pending_remote，再run不新增HTTP，review未发。 |

## F05 — 生成质量与估价回归比较报告

证据入口：[internal/evalset/article_quality.go](../../../../internal/evalset/article_quality.go)；[internal/evalset/article_quality_test.go](../../../../internal/evalset/article_quality_test.go)；[docs/quality-cases.md](../../../../docs/quality-cases.md)。

已核对测试函数（存在不等于当前通过）：`TestArticleQualityRunCheckpointAndUnknown`、`TestArticleQualitySevereHumanError`、`TestArticleQualityCostRegressionGolden`。

| 子项与原始要求 | 独立结论 | 当前证据及限制 |
| --- | --- | --- |
| **F05.1** 按确切阶段模型、提示、用途、语料和价格版本比较归因、引用、缺口、结构、耗时及费用。 | 局部证据成立；最终门禁通过；功能提交410e774 | 冻结exact model/prompt/purpose/corpus/price及原文/validation/review事实；报告不将规则通过冒称事实质量。 |
| **F05.2** 分别记录已知/未知用量与成本、估价误差和人工字段，严重事实/归因错误独立判失败。 | 已知/未知与真实失败证据成立；人工待验 | 真实token已知1428/983，价格/费用unknown不补0；实际model!=freeze独立失败，review未发，人工没有分数；估价/严重错误规则与固定report测试。 |
| **F05.3** 提供明确有界真实运行入口及固定报告金样；报告不自动切换生产提示或模型，人工缺失不自动赋分。 | 明确有界入口已证明；真实两阶段外部待验 | 生产CLI preview/--run、预算前置及报告金样已验；真实接口调用write失败在冻结模型合同，非预算漏检，未自动修配置或继续review，human pending。 |

## F06 — 验收私有质量案例生命周期

证据入口：[internal/store/article_feedback_cases_test.go](../../../../internal/store/article_feedback_cases_test.go)；[internal/backup/article_quality_restore_test.go](../../../../internal/backup/article_quality_restore_test.go)；[docs/quality-cases.md](../../../../docs/quality-cases.md)。

已核对测试函数（存在不等于当前通过）：`TestArticleFeedbackCaseLifecycle`、`TestArticleFeedbackCaseUsefulWholeArticle`、`TestArticleFeedbackCaseSourcePurgeRetainsOwnerRecord`、`TestArticleFeedbackCaseFrozenFacts`、`TestArticleQualityV2RestorePreservesOwnerVersionsAndPurgeBoundary`。

| 子项与原始要求 | 独立结论 | 当前证据及限制 |
| --- | --- | --- |
| **F06.1** 验证接纳→重跑→修订→退役，旧案例冻结输入不被新材料暗改。 | 局部证据成立；最终门禁通过；功能提交410e774 | lifecycle与case历史输入不可变/重放测试，独立CLIstage路由/断点。 |
| **F06.2** 组合来源撤权/清理、文章删除、临时报告、已付费账本及 v2 备份恢复，防止缓存绕权。 | 局部证据成立；最终门禁通过；功能提交410e774 | sourcewithdraw/purge缓存复核，articledelete清冻结、paidnumeric保留及v2实际恢复。 |
| **F06.3** 核对私有字段清理与公开聚合输出；分别登记技术、模型和人工质量结果。 | 工程及真实失败报告已证明；质量外部待验 | 0700/0600私有artifact/公开脱敏；实际write模型不一致、numeric receipt及重跑零HTTP已记录。真实两阶段文章质量及人评不能宣称通过。 |

## E01 — 建立有界一致性导出预览

证据入口：[internal/store/learning_exports.go](../../../../internal/store/learning_exports.go)；[internal/store/learning_exports_test.go](../../../../internal/store/learning_exports_test.go)。

已核对测试函数（存在不等于当前通过）：`TestLearningExportBoundedIdentityAndWithdrawal`、`TestLearningExportRequestLimitExpiryAndObjectLimit`、`TestLearningExportThemePassedHistoryDraftAndUnderstanding`、`TestLearningExportObjectLimitAndOwnerStop`。

| 子项与原始要求 | 独立结论 | 当前证据及限制 |
| --- | --- | --- |
| **E01.1** 实现问题/Theme 范围与当前/历史/草稿选择，默认仅通过版；有界快照读取不扩大模型外发权。 | 局部证据成立；最终门禁通过；功能提交410e774 | 问题/Theme、理解current/history、文章passed/draft筛选真实测试。 |
| **E01.2** 短事务复制确切对象/修订，生成预览 hash 与来源状态序列；限制 500 对象、100MiB、100 请求身份。 | 局部证据成立；最终门禁通过；功能提交410e774 | 两短事务冻结/渲染后复核，100预览/请求及500对象/100MiB限制。 |
| **E01.3** 范围/源状态改变使旧预览过期；测试大库、缺失材料、并发撤回、默认范围及迁移恢复。 | 当前升级/权限局部证据成立；最终门禁通过；功能提交410e774 | 0086状态/来源撤回触发器和原字段升级；V01实际0069 Open及独立恢复已补旧文章/会话/队列/控制/known/unknown逐字段，targetrace9.410s由根报告，当前源可核。 |

## E02 — 实现确定性Markdown与ZIP清单

证据入口：[internal/learningbundle/bundle.go](../../../../internal/learningbundle/bundle.go)；[internal/learningbundle/bundle_test.go](../../../../internal/learningbundle/bundle_test.go)。

已核对测试函数（存在不等于当前通过）：`TestZIPDeterministicReadableAndVerified`、`TestRenderRejectsUnsafeOrMissingTargets`、`TestRenderBounds`、`TestWriteZIPPropagatesWriterFailure`、`TestSafeUnicodeIdentityAndEscapedTitle`。

| 子项与原始要求 | 独立结论 | 当前证据及限制 |
| --- | --- | --- |
| **E02.1** 建立 stable-id 文件路径和 Markdown 模板，保留中文、类别、确切版本、真实来源定位及相对链接。 | 局部证据成立；最终门禁通过；功能提交410e774 | 稳定身份路径/确切版本及Unicode文件名安全。 |
| **E02.2** 流式构建顺序固定的 ZIP 和每文件 hash/size manifest，拒绝路径穿越、重复名、超限和坏内部链接。 | 局部证据成立；最终门禁通过；功能提交410e774 | 实际ZIP解包，Markdown links内部路径及目标验证；不依赖在线URL阅读。 |
| **E02.3** 实际解包核对可读正文、所有链接及哈希；包内排除 key、Cookie、私人录音和任务原输入。 | 局部证据成立；最终门禁通过；功能提交410e774 | hash/size/路径核对；明确对象投影排除job原输入/凭据。 |

## E03 — 通过无模型任务生成和鉴权下载

证据入口：[internal/queue/learning_exports.go](../../../../internal/queue/learning_exports.go)；[internal/queue/learning_exports_test.go](../../../../internal/queue/learning_exports_test.go)；[internal/server/learning_exports_test.go](../../../../internal/server/learning_exports_test.go)。

已核对测试函数（存在不等于当前通过）：`TestLearningExportRealZIPWithoutModelAndRestartCleanup`、`TestLearningExportsSSRAndCommands`、`TestLearningExportStatusPolling`、`TestLearningExportAuthenticatedPreviewConfirmationAndPrivacy`、`TestLearningExportJSONNavigationUsesExactReadOnlyPreview`。

| 子项与原始要求 | 独立结论 | 当前证据及限制 |
| --- | --- | --- |
| **E03.1** 实现 A06 无模型执行分支的实际打包器，创建命令幂等；文件私有 0600，默认 24 小时过期。 | 局部证据成立；最终门禁通过；功能提交410e774 | 0600临时写/Sync/rename真实打包，nil模型重建，幂等restart。 |
| **E03.2** 下载时重新鉴权与核对源序列/范围，生成、撤回与清理竞争不得发布失效文件。 | 局部证据成立；最终门禁通过；功能提交410e774 | publish事务及download再次复核，auth/OpenRoot路径及sourcewithdraw拒绝。 |
| **E03.3** 增加临时文件失败、重启清理、文件丢失和过期恢复；无模型配置仍可生成，零预算/模型调用。 | 局部证据成立；最终门禁通过；功能提交410e774 | 过期/周期restart清理及Sync/Close/permission故障，零AI。 |

## E04 — 交付范围选择和导出状态页面

证据入口：[internal/server/templates/learning_exports.html](../../../../internal/server/templates/learning_exports.html)；[internal/server/learning_exports_test.go](../../../../internal/server/learning_exports_test.go)。

已核对测试函数（存在不等于当前通过）：`TestLearningExportsSSRAndCommands`、`TestLearningExportStatusPolling`、`TestLearningExportAuthenticatedPreviewConfirmationAndPrivacy`、`TestLearningExportJSONNavigationUsesExactReadOnlyPreview`。

| 子项与原始要求 | 独立结论 | 当前证据及限制 |
| --- | --- | --- |
| **E04.1** 提供范围、版本/草稿开关、预览数量、明确确认、后台状态、下载与重建动作。 | 局部证据成立；最终门禁通过；功能提交410e774 | scope/options/preview/confirm/status/download/rebuild SSR齐全。 |
| **E04.2** 旧预览禁止确认新内容；轮询仅更新状态，选项和文字保留，SSR 可生成/下载。 | 局部证据成立；最终门禁通过；功能提交410e774 | 实际JS保留选择/401/409/unmount，确切previewID/hash。 |
| **E04.3** 独立浏览器验证问题/Theme 导出和 ZIP 阅读、撤回失效及文件缺失；保持根播放。 | 实际问题/Theme与失败下载已证明；最终门禁通过；功能提交410e774 | 正式export-theme-browser记录fresh独立18135：真实Theme pipeline、browser blobdownload、404缺ZIP及409撤回就地保URL/选择/草稿，同Audio10.11→85.93秒持续播放；131.05s PASS，目标race11.876。问题ZIP阅读另有正式报告，question/article调用均0。 |

## E05 — 验收成果包范围与恢复兼容

证据入口：[internal/backup/learning_exports_test.go](../../../../internal/backup/learning_exports_test.go)；[internal/store/learning_exports_test.go](../../../../internal/store/learning_exports_test.go)。

已核对测试函数（存在不等于当前通过）：`TestLearningExportsV2BackupExcludesZIPAndRestoresRebuild`、`TestLearningExportBoundedIdentityAndWithdrawal`、`TestLearningExportRequestLimitExpiryAndObjectLimit`、`TestLearningExportThemePassedHistoryDraftAndUnderstanding`、`TestLearningExportObjectLimitAndOwnerStop`。

| 子项与原始要求 | 独立结论 | 当前证据及限制 |
| --- | --- | --- |
| **E05.1** 组合多源理解、质量案例、旧通过版、历史与草稿、大范围、撤回/清理竞争的范围对照。 | 局部证据成立；最终门禁通过；功能提交410e774 | 多源/历史/理解/案例及stop/withdraw竞争定向tests实际通过。 |
| **E05.2** 实际升级和 v2 备份恢复，确认临时 ZIP 不入实例备份，恢复后可零模型重建。 | 当前恢复已证明；最终门禁通过；功能提交410e774 | 真实v2 tar排除ZIP/restore后nil模型重建；0069综合升级及实际两来源/0088关系恢复count3 PASS65.736s，当前正式日志已落。 |
| **E05.3** 解包核对对象/版本/链接及原 Markdown 下载兼容，浏览器实际阅读；不把成果包当成实例恢复包。 | 实际ZIP阅读及Theme下载已证明；最终门禁通过；功能提交410e774 | 问题ZIP9文件/36links与中文index/article/source reader已验；Theme真实ZIP5文件SHA/size/UTF8及立即revoke后可读。缺ZIP404和撤回409不丢草稿/播放器。旧Markdown兼容由相关HTTP测试支持。 |

## O01 — 定义离线设备授权和撤回清单

证据入口：[internal/store/offline_learning.go](../../../../internal/store/offline_learning.go)；[internal/store/offline_learning_test.go](../../../../internal/store/offline_learning_test.go)；[docs/adr/0031-offline-learning-authorization.md](../../../../docs/adr/0031-offline-learning-authorization.md)。

已核对测试函数（存在不等于当前通过）：`TestOfflineDeviceAuthorization`、`TestOfflineOperationAtomicReplayAndConflict`、`TestOfflineOperationReceiptFailureRollsBack`、`TestOfflineOperationRejectsChangedSource`、`TestOfflinePackAggregateQuotaAndEmpty`。

| 子项与原始要求 | 独立结论 | 当前证据及限制 |
| --- | --- | --- |
| **O01.1** 定义设备/session namespace、下载 pack、对象 hash/epoch 和默认 24 小时授权期限，记录 ADR。 | 局部证据成立；最终门禁通过；功能提交410e774 | namespace/epoch/24h及ADR撤回边界。 |
| **O01.2** 建立明确授权/撤回与来源、登录变更序列；默认关闭，不将凭据写入清单或浏览器包。 | 局部证据成立；最终门禁通过；功能提交410e774 | 默认无grant；明确enable/revoke、session绑定、无自动下载。 |
| **O01.3** 验证空包、旧清单、时间异常、权限/源变化和恢复规则；明确离线不能实时获知服务器撤回。 | 局部证据成立；最终门禁通过；功能提交410e774 | 空/配额/旧epoch/到期/未来时间/坏manifest拒绝。 |

## O02 — 实现能力探测与离线专用外壳

证据入口：[internal/server/static/sw.js](../../../../internal/server/static/sw.js)；[internal/server/offline_learning.go](../../../../internal/server/offline_learning.go)；[internal/server/offline_learning_test.go](../../../../internal/server/offline_learning_test.go)。

已核对测试函数（存在不等于当前通过）：`TestOfflineHTTPAuthorizationManifestBytesAndRevocation`、`TestOfflineHTTPEmptyBoundsAndStaticIsolation`、`TestOfflineHTTPFrozenExcerptManifestAndIndependentSync`、`TestOfflineOperationBrowserGoHashInterop`。

| 子项与原始要求 | 独立结论 | 当前证据及限制 |
| --- | --- | --- |
| **O02.1** 建立根 scope 的 Service Worker 路由、版本化静态资源及独立 offline shell，先检测安全上下文与存储能力。 | 局部证据成立；最终门禁通过；功能提交410e774 | 根SW/专用shell白名单及版本化静态hash。 |
| **O02.2** 只缓存静态外壳和明确离线投影；登录/设置/CSRF/任务/私人录音及动态 SSR 不缓存。 | 局部证据成立；最终门禁通过；功能提交410e774 | 不缓存登录HTML；仅明确原音路由及静态资源，HEAD契约。 |
| **O02.3** 验证安装失败/不支持/禁用仍可使用原站，显示真实注册和升级状态，不依赖后台同步。 | 实际回退已证明；最终门禁通过；功能提交410e774 | 正式offline README/real-go-driver-excerpt：无SW API、注册SecurityError、IDB.open失败均显示online回退、私有UI隐藏、原3pending保留，真实SSR可用；另CDP bypass实际走Go404/SSR200。故障注入不冒称手机或浏览器API设置禁用。 |

## O03 — 提供已鉴权的原音和内容下载清单

证据入口：[internal/server/offline_learning.go](../../../../internal/server/offline_learning.go)；[internal/server/offline_acceptance_test.go](../../../../internal/server/offline_acceptance_test.go)。

已核对测试函数（存在不等于当前通过）：`TestOfflineAcceptanceChangedSourceClearDownloadsPreservesDraft`、`TestOfflineAcceptanceRestartAndNewLoginRejectOldAuthorization`、`TestOfflineAcceptanceDeletedSourceNeverAcknowledgesPendingMutation`、`TestOfflineAcceptanceEvidencePathCannotEscapeAndRemoteOnlyCannotDownload`、`TestOfflineAcceptanceFrozenNoteDownloadAndCatalog`。

| 子项与原始要求 | 独立结论 | 当前证据及限制 |
| --- | --- | --- |
| **O03.1** 创建鉴权的内容与本地 EvidenceAudio 下载清单，冻结大小、SHA、snapshot 和实际锚点。 | 局部证据成立；最终门禁通过；功能提交410e774 | auth manifest冻结snapshot/SHA/bytes，GET复核。 |
| **O03.2** 首版仅选定原音、转录、笔记和通过版；限 10 来源/500MiB，不加入远端 URL、DJ 解说或私人录音。 | 局部证据成立；最终门禁通过；功能提交410e774 | 仅episode/upload已本地EvidenceAudio，10source/500MiB，remoteonly拒绝。 |
| **O03.3** 下载前后复查 epoch、来源、权限及 hash；测试撤回竞争和字节身份，预览不触发模型/转码。 | 局部证据成立；最终门禁通过；功能提交410e774 | 当前permissions/audio身份/path复核，panic-provider支持零AI。 |

## O04 — 持久下载与配额错误恢复

证据入口：[internal/server/static/offline-packs.js](../../../../internal/server/static/offline-packs.js)；[scripts/offline-browser.test.js](../../../../scripts/offline-browser.test.js)。

| 子项与原始要求 | 独立结论 | 当前证据及限制 |
| --- | --- | --- |
| **O04.1** 实现 IndexedDB/Cache 下载状态，完整文件校验 size/hash 后才能标 ready，临时/失败/取消状态分开。 | 局部证据成立；最终门禁通过；功能提交410e774 | 整Blob hash/size成功才ready，failed/cancel/eviction测试。 |
| **O04.2** 执行总量和浏览器估计配额 50% 检查，提供明确的下载副本清理；保护未同步草稿和保留包。 | 局部证据成立；最终门禁通过；功能提交410e774 | quota50%/总容量/来源限额，清下载不清outbox/shared成员。 |
| **O04.3** 测试中断/取消/坏 hash/QuotaExceeded/驱逐/重载恢复，不能把仅有索引行当成文件仍可用。 | 当前修复实际证据成立；最终门禁通过；功能提交410e774 | Node完整24终态已归档，同文件/同包、staging/publish故障保旧；实际nativeIDB26用例含零fetch复用、publish tx abort旧ready、暂存清理及schema2，完整26条工具终态观察已归档session53638 exit0；原stdout未单存，不以观察JSON冒称原log。 |

## O05 — 支持离线原音Range与真实锚点

证据入口：[internal/server/static/sw.js](../../../../internal/server/static/sw.js)；[internal/server/static/offline-core.js](../../../../internal/server/static/offline-core.js)；[scripts/offline-browser.test.js](../../../../scripts/offline-browser.test.js)。

| 子项与原始要求 | 独立结论 | 当前证据及限制 |
| --- | --- | --- |
| **O05.1** 仅对完整验证原音文件提供 200、单 Range 206、无效/越界 416，核对 Content-Range 与字节边界。 | 局部证据成立；最终门禁通过；功能提交410e774 | 200/206/416、suffix/EOF/多段非法Range及原音byte测试。 |
| **O05.2** 接入根播放器的离线原音和补听区间，使用真实 audioSHA/snapshot 锚点，Blob URL 不参与来源身份。 | 局部证据成立；最终门禁通过；功能提交410e774 | startOffline连接根player，source/snapshot/hash及冻结excerpt独立进度。 |
| **O05.3** 离线浏览器验收 seek、末尾、刷新和笔记捕获；文件驱逐立即明确不可用，不下载远端或生成 DJ 兜底。 | 真实Go浏览器已证明；设备外部待验 | 真实Go driver记录实际WAV播放/seek、断网reload、note/draft/progress；专用shell两端seek夹紧、末尾暂停、暂停seek1.5刷新精确恢复，Source变化拒绝无AI兜底。手机系统驱逐外部待验。 |

## O06 — 保存可同步的本地操作队列

证据入口：[internal/server/static/offline-operations.js](../../../../internal/server/static/offline-operations.js)；[scripts/offline-browser.test.js](../../../../scripts/offline-browser.test.js)。

| 子项与原始要求 | 独立结论 | 当前证据及限制 |
| --- | --- | --- |
| **O06.1** 定义可同步 outbox：仅文字笔记/整理草稿和原音进度，保存 schema/namespace/UUID/hash/expectedRevision/锚点。 | 局部证据成立；最终门禁通过；功能提交410e774 | UUID/hash/CAS/namespace/anchor schema，文字和原音/补听进度白名单。 |
| **O06.2** 本地持久保存后显示待同步，ack 前不清理；同命令修改负载必须新身份，保存失败可复制文字。 | 局部证据成立；最终门禁通过；功能提交410e774 | durable put后提示、ack后删除、lostack同UUID唯一receipt及CAS顺序。 |
| **O06.3** 验证 reload/存储故障/401/logout 隔离及容量；禁止生成、转写、预算和任务控制入队。 | 局部证据成立；最终门禁通过；功能提交410e774 | 100/64KiB批次与容量、401/logout隔离；明确不允许paidjob。 |

## O07 — 服务端原子同步与去重

证据入口：[internal/store/offline_operations.go](../../../../internal/store/offline_operations.go)；[internal/store/offline_failures_test.go](../../../../internal/store/offline_failures_test.go)；[internal/server/offline_failures_test.go](../../../../internal/server/offline_failures_test.go)。

已核对测试函数（存在不等于当前通过）：`TestOfflineFailurePackBudgetAndCorruptHistory`、`TestOfflineFailureDeviceRefreshRollback`、`TestOfflineFailureCorruptPackSourceAndArticleMetadata`、`TestOfflineFailureClosedDatabaseNeverAuthorizes`、`TestOfflineFailurePackFrozenSourceChecks`、`TestOfflineFailureRequestBoundaries`、`TestOfflineFailureAudioFilesystemAndTemporaryCleanup`、`TestOfflineFailureStoredManifestCorruptionAndExpiry`、`TestOfflineStaticHEADAndMethodContract`。

| 子项与原始要求 | 独立结论 | 当前证据及限制 |
| --- | --- | --- |
| **O07.1** 提供明确前台同步接口，认证/CSRF/session/epoch 后按每条命令核对来源与修订。 | 局部证据成立；最终门禁通过；功能提交410e774 | 明确foreground、auth/CSRF/device/session/epoch。 |
| **O07.2** 把合法写入与 UUID/hash 回执放在同一事务，返回 ack/conflict/unavailable；复用笔记、整理、进度业务约束。 | 局部证据成立；最终门禁通过；功能提交410e774 | 单事务当前授权/source/CAS/write/receipt，重放同receipt。 |
| **O07.3** 测试响应丢失、重复/异 hash、旧修订、删除源、事务回滚和恢复；保持客户端文字，零模型调用。 | 局部证据成立；最终门禁通过；功能提交410e774 | receipt故障回滚、sourcechange/原音/补听/organizingdraftCAS；provider零调用。 |

## O08 — 离线重开、到期与登出清理

证据入口：[internal/server/static/offline-shell.js](../../../../internal/server/static/offline-shell.js)；[internal/server/static/private-state.js](../../../../internal/server/static/private-state.js)；[internal/server/offline_acceptance_test.go](../../../../internal/server/offline_acceptance_test.go)。

已核对测试函数（存在不等于当前通过）：`TestOfflineAcceptanceChangedSourceClearDownloadsPreservesDraft`、`TestOfflineAcceptanceRestartAndNewLoginRejectOldAuthorization`、`TestOfflineAcceptanceDeletedSourceNeverAcknowledgesPendingMutation`、`TestOfflineAcceptanceEvidencePathCannotEscapeAndRemoteOnlyCannotDownload`、`TestOfflineAcceptanceFrozenNoteDownloadAndCatalog`。

| 子项与原始要求 | 独立结论 | 当前证据及限制 |
| --- | --- | --- |
| **O08.1** 有效 namespace/期限内允许专用外壳断网重开，到期锁定；新登录不继承旧私有副本。 | 局部证据成立；最终门禁通过；功能提交410e774 | 有界本地grant重开，旧session/newlogin拒绝，无cachedHTML伪装。 |
| **O08.2** 回网先验证授权/撤回再显示内容或同步，拒旧 outbox；退出广播即时停止媒体并清理该设备私有存储。 | 局部证据成立；最终门禁通过；功能提交410e774 | online先verify撤回锁outbox清files/stop媒体，logout跨标签广播。 |
| **O08.3** 验证离线关页重开、换账号/同账号重新登录、过期/时钟变化、跨标签及服务器撤回；说明实时离线撤回限制。 | 桌面边界证据成立；设备外部待验 | 正式offline报告及Node24覆盖期限/登录/时钟，实际Go来源409锁文字/明确退出；synthetic HTTP+nativeIDB/SW26覆盖session/多标签，不混同Go业务或手机。 |

## O09 — 安全更新SW与清理下载副本

证据入口：[internal/server/static/offline-update.js](../../../../internal/server/static/offline-update.js)；[internal/server/static/offline-storage.js](../../../../internal/server/static/offline-storage.js)；[scripts/offline-browser.test.js](../../../../scripts/offline-browser.test.js)。

| 子项与原始要求 | 独立结论 | 当前证据及限制 |
| --- | --- | --- |
| **O09.1** 新静态缓存完整验证后才进入可切换状态，播放、录音和同步在途不强制激活/刷新。 | 局部证据成立；最终门禁通过；功能提交410e774 | stagehash全部后切换，播放/录音/同步busy与多客户端投票，不强制刷新。 |
| **O09.2** 定义存储 schema 不兼容的阻断与可复制草稿路径；旧缓存、临时文件和下载副本明确清理。 | schema复制与原子修复已证明；最终门禁通过；功能提交410e774 | readLockedTexts旧schema2原生IDB版本2只读复制，Owner namespace隔离；已查看正式schema2截图，Node24记录，旧outbox可核对不删除。publishPack原子发布，staging明确清理。 |
| **O09.3** 测试双客户端更新、坏新缓存、在途录音/播放、回退及清理；不能删除未同步 outbox。 | 桌面脚本实际证据成立；手机录音待验 | nativeIDB/SW26实际更新/双客户端/坏cache/busy保护、schema2复制；Node24同包保旧与原子abort。26终态目前执行者观察已报告，26条工具终态观察已归档，原stdout缺失如实保留。自然录音/手机不以桌面busy代验。 |

## O10 — 完成真实离线综合故障验收

证据入口：[scripts/offline-browser-acceptance.py](../../../../scripts/offline-browser-acceptance.py)；[scripts/offline-go-browser-acceptance.py](../../../../scripts/offline-go-browser-acceptance.py)；[internal/server/offline_go_browser_harness_test.go](../../../../internal/server/offline_go_browser_harness_test.go)。

已核对测试函数（存在不等于当前通过）：`TestOfflineGoBrowserHarness`。

| 子项与原始要求 | 独立结论 | 当前证据及限制 |
| --- | --- | --- |
| **O10.1** 构建实际离线综合矩阵：断网重开、Range、草稿、回网、ack 丢失、CAS、撤回、期限、配额和驱逐。 | 当前桌面矩阵已证明；最终门禁待验 | 正式offline README/node24完整log/native26当前结果与真实Go154.44s摘录+DB JSON/CDP JSON；断网/Range/ack丢失7重试仍各唯一、CAS/source/expire/quota/eviction按层分开，不混HTTP模拟与Go。 |
| **O10.2** 再覆盖 SW 更新与多标签登录/退出，逐项对照数据库、IndexedDB、Cache 与音频 hash。 | 当前桌面数据核对已证明；最终门禁通过；功能提交410e774 | 真实Go脱敏database-summary与音频SHA：原音rev1、两excerpt独立0.75/rev3及1.5/rev1；actualsource409锁outbox和logout清设备；SW多标签属synthetic26层，工具终态观察JSON已归档，原stdout未单存。 |
| **O10.3** 运行独立真实浏览器并查看截图，复验所有发现的问题；桌面结果与实体手机结果分别登记。 | 截图及修复复验已证明；工具观察已归档；原stdout缺失限制 | 正式目录6真实Go+1schema2截图已由执行者和协调者查看，SHA清单可核；samepack/staging/atomic/schema修复当前实际复验，实体手机pending不代填。native26完整观察JSON和Go154.44harness工具观察已归档，均不冒称完整原stdout。 |

## O11 — 登记实体手机使用与待验状态

证据入口：[docs/acceptance/2026-10-02-v4-offline-mobile.md](../../../../docs/acceptance/2026-10-02-v4-offline-mobile.md)。

| 子项与原始要求 | 独立结论 | 当前证据及限制 |
| --- | --- | --- |
| **O11.1** 形成实体手机验收表，逐项记录设备/系统/浏览器/HTTPS/源码/时间和可复核结果。 | 局部证据成立；最终门禁通过；功能提交410e774 | 实体设备/HTTPS/源码/SHA/时间/namespace及12场景记录模板齐全。 |
| **O11.2** 设备可用时执行后台原音 15 分钟、两段自然录音、离线重开/同步、来源删除及退出。 | 外部设备待验 | 实际iOS/Android不可用，15min后台/自然录音/离线/退出未验。 |
| **O11.3** 设备不可用时明确每项待验与操作步骤，不用 viewport/合成语音代填，也不擅自部署；登记工具与记录技术准备完成。 | 局部证据成立；最终门禁通过；功能提交410e774 | 模板明确viewport/合成录音不能代填、不擅自部署；技术准备与产品分开。 |

## V01 — 综合验证0069升级及v2恢复

证据入口：[internal/store/personal_learning_v4_upgrade_test.go](../../../../internal/store/personal_learning_v4_upgrade_test.go)；[internal/backup/personal_learning_v4_restore_test.go](../../../../internal/backup/personal_learning_v4_restore_test.go)；[docs/superpowers/validation/v4/root-integration.md](../../../../docs/superpowers/validation/v4/root-integration.md)。

已核对测试函数（存在不等于当前通过）：`TestPersonalLearningV4UpgradePreservesExecutionFacts`、`TestPersonalLearningV4RealRestoreRelationships`。

| 子项与原始要求 | 独立结论 | 当前证据及限制 |
| --- | --- | --- |
| **V01.1** 构造实际 0069 旧库，包含旧文章/会话/队列、已知 checkpoint、未知远端调用与预算控制，真实 Open 升级。 | 真实旧对象升级源码已核；最终门禁通过；功能提交410e774 | 实际0069包含旧article/revision/review、queue current/autoplay/revision/position/date；known/unknown jobs绑定真实articleID，真实Open升级及独立restore逐字段相等。根当前报告targetrace9.410s，测试源现行。 |
| **V01.2** 进行 v2 备份到独立目录再恢复，逐项对照新理解、缺口、用途、案例、对话、整理、控制、账本及私有录音。 | 当前目标恢复已证明；最终门禁通过；功能提交410e774 | 主集成报告实际go test ./internal/backup -run TestPersonalLearningV4RealRestoreRelationships -race -count=3 session56476 PASS65.736；现行真实二来源/0088关系/数字账本/控制/私人音频逐项对照。 |
| **V01.3** 证明已知响应零付费重发、旧 unknown 不伪造成功、临时 ZIP/浏览器音频不入备份；故障修复后重验。 | 当前目标恢复已证明；最终门禁通过；功能提交410e774 | 实际Worker Fatal供应商guard证明known零重发、unknown NULL不伪成功、ZIP排除、symlink音频拒绝；最新count3正式PASS，非全库/手机结果。 |

## V02 — 验收完整听学成文旅程

证据入口：[internal/server/personal_learning_v4_journey_test.go](../../../../internal/server/personal_learning_v4_journey_test.go)；[docs/superpowers/validation/v4/root-integration.md](../../../../docs/superpowers/validation/v4/root-integration.md)。

已核对测试函数（存在不等于当前通过）：`TestPersonalLearningV4CrossModuleJourney`、`TestPersonalLearningV4BrowserHarness`。

| 子项与原始要求 | 独立结论 | 当前证据及限制 |
| --- | --- | --- |
| **V02.1** 在同一问题的隔离固定副本串联播放→整理→问答→补听→理解→两用途文章→反馈→导出→离线同步。 | 最终同题实际工程旅程已证明；最终门禁通过；功能提交410e774 | 正式final fixture14:05:52，同q1反思明确关系→三来源问答→补听→理解两版本→同article综合r1/解释r3→真实反馈 reread→反馈后新preview ZIP11文件→实际离线UI同步；harness889.36s PASS、独立route13.638s。 |
| **V02.2** 至少 10 次连续导航核对同 audio 和真实锚点，注入网络/版本/停止/清理故障并检查文字与费用。 | 当前十导航及同题真实故障已证明；最终门禁通过；功能提交410e774 | 正式browser同Audio/URL/三draft/锚点/真断网成立；新独立同seed/q1完整链后真实Owner stop/APIpurge目标race包10.652s PASS：8usage逐列不变、模型2/6零增，理解Answer/已同步离线个人稿保留并标源不可用，源绑定reflection/turn/input/checkpoint/derived按privacy清理、旧ZIP/pack/file拒，pending unavailable不ack/不新增稿；不冒称原browser DB点击。 |
| **V02.3** 分别登记确定性桩、可用真实接口、人工和实体设备的证据；缺个人资料不杜撰，工程旅程不能被外部等待卡住。 | 最终工程记录完整；真实质量/手机待验 | final/harness.log终态模型question2/article6、8receipt各1、succeeded14/故障原尝试failed1；3session及18131显式关闭。协议Provider和20自建notes不替代真实个人资料/人评/手机，真实CLI单次模型合同失败另列。 |

## V03 — 执行最终技术门禁

证据入口：[scripts/cover-gate.sh](../../../../scripts/cover-gate.sh)；[docs/superpowers/validation/v4/root-integration.md](../../../../docs/superpowers/validation/v4/root-integration.md)。

| 子项与原始要求 | 独立结论 | 当前证据及限制 |
| --- | --- | --- |
| **V03.1** 冻结最终候选源码与夹具指纹，执行全量 race、覆盖率、lint、vet、构建、所有 JS 语法及 diff 检查。 | 最终技术门禁通过 | 最后冻结776文件SHA清单e65235091bf2adcacef751daa654b3def3d0a7e1343d2dee31c4d1d4b92aff32核零变；正式race.log session46720 EXIT0 Server154.586新执行/Storecached，同输入此前520.228PASS；cover64085/lint5011及vet/build/42JS/diff EXIT0。 |
| **V03.2** 修复失败根因并重跑受影响检查；保持 95% 规则、store 78.4/server 79.4 地板与原豁免，不修改门槛求通过。 | 最终技术门禁通过；门槛未改 | 95%规则及store78.4/server79.4/models原豁免不变，正式coverage profile当前queue95.0/server79.8/store79.2，CLI96.5/evalset97.5；最终race包含新增同题stop/purge，失败根因修复后同候选检查通过。 |
| **V03.3** 记录实际命令、结果、缓存适用性及未运行项；最后源码变化须重新核对证据，当前阶段检查不直接当最终结果。 | 最终同输入证据与缓存范围已证明 | 776文件清单SHA实际核对匹配，root-integration最后明确session/exit及Go cached范围：Store上一轮相同输入520.228s、本轮cached；Server新执行154.586s。原日志保留、不冒称所有包新跑；提交前相关source不许变更。 |

## V04 — 完成使用、配置、部署与验收文档

证据入口：[docs/personal-learning-v4.md](../../../../docs/personal-learning-v4.md)；[.env.example](../../../../.env.example)；[docs/production-deployment.md](../../../../docs/production-deployment.md)；[docs/evalset.md](../../../../docs/evalset.md)。

| 子项与原始要求 | 独立结论 | 当前证据及限制 |
| --- | --- | --- |
| **V04.1** 按实际路由、数据与默认值完成个人学习使用文档、README、配置示例、费用、导出/备份和恢复说明。 | 当前文档技术终校已证明；待最终交付 | documentation-check.md实际完整命令、4文档32本地links/anchors零错与退出0，各文件SHA可复核；使用/配置/default automatic/backup/export及404409就地保稿已对照源码。只核本地链接，不声称外站可达。 |
| **V04.2** 补齐语义开关、旧问答兼容、离线 HTTPS/期限/清理、草稿同步及异常处理，刷新领域术语和部署步骤。 | 当前文档技术終校已证明；待最终交付 | 正式4文档人工契约复核语义failclosed、QA模型冻结、HTTPS期限、schema文字出口、明确同步与cleanup、CLI预算receipt及unknown；实际模型/人工/手机未验明确单列，无部署声明。 |
| **V04.3** 核对链接/示例与实现，不泄露 .env/正文；未部署不写已部署，模型/人工/设备待验单列。 | 当前文档范围核对已证明；待最终交付 | 正式documentation-check记录32本地links/anchors及四SHA/diff退出0，未读取.env密钥；模型1428/983合同失败未写成真实质量通过，未声称实际Owner部署。外URL被跳过，不能称全部外部链接实访。 |

## V05 — 核对任务闭合并交付提交

证据入口：[docs/superpowers/validation/v4/root-integration.md](../../../../docs/superpowers/validation/v4/root-integration.md)。

| 子项与原始要求 | 独立结论 | 当前证据及限制 |
| --- | --- | --- |
| **V05.1** 逐卡核对实际实现、依赖、commit、当前测试和证据，逐子任务清点，不能以勾选推断完成。 | 技术交付核对完成 | 141条逐项实际源码/局部故障测试/正式browser及终态log已审，不凭checkbox；最后776输入全gate通过，功能commit410e774包含355文件/0080–0089，外部产品质量待验单列。 |
| **V05.2** 核对新迁移、文档、最终 HEAD/工作树与私有产物，形成一致提交及交付清单。 | 功能一致提交完成；文档终态由根收尾 | 实际git show确认410e774304e3907bc6cb81db321ba1e894a7cf5d包含355文件，0080–0089；核对时工作树仅本audit文档修改。根更新主卡/交付报告后独立docscommit并核clean，审计不提前声称未来工作树已clean。 |
| **V05.3** 所有工程行为完成才宣布工程完成；真实模型、资料、人评和设备待验保留状态与入口，不自动发布或部署。 | 工程技术交付成立；外部产品验收待验 | 所有实现/组合故障/桌面browser与当前全gate证据齐备并已功能提交410e774；真实个人资料、真人评分、手机、真实embedding收益及失败cold、真实文本模型合同失败/未发review仍明确限制，未发布部署。 |

## 当前树与后续更新规则

以下文件SHA256在审计落盘时取样；不是最终候选全树指纹。对应源或夹具改变后，相关行必须重新核对；最终gate/commit由根代理登记。

- [internal/server/article_feedback_cases.go](../../../../internal/server/article_feedback_cases.go)：`9961b2c18504d0d70ecdb5b626402fe5a8609a1f4bf9e947254c7fb1a78d5b04`
- [internal/store/evidence_gaps.go](../../../../internal/store/evidence_gaps.go)：`57da7f7563a064f430e2a2534f031a9395cbdeb21c88dd9c7866be691b052716`
- [internal/server/static/offline-packs.js](../../../../internal/server/static/offline-packs.js)：`8662a7adb87e73e0da284e1d346cbaf057b7b1a50a0f07557c92448d3616d680`
- [internal/server/static/offline-storage.js](../../../../internal/server/static/offline-storage.js)：`591c8b7eece1f9a2eadea11387b6e0395fcb978a339a139bd5fc3935dba7fa40`
- [internal/backup/personal_learning_v4_restore_test.go](../../../../internal/backup/personal_learning_v4_restore_test.go)：`351c8b1ddd25ae0dff2f5b60689e88462d5ca8be4d5a95585d695c0d0206cd2d`
- [internal/server/personal_learning_v4_journey_test.go](../../../../internal/server/personal_learning_v4_journey_test.go)：`d98880096783c1679abb1c8278d084a58352ca2305cdce986a942486ecf196cb`

## 后续局部证据更新

U03.3实际purge浏览器：[测试](../../../../internal/server/understanding_browser_purge_test.go)、[完整记录](../../plans/2026-10-02-understanding-snapshots-validation.md)、[清理前截图](understanding-purge/cwp-u03-before-purge.png)、[清理后截图](understanding-purge/cwp-u03-after-purge.png)。这是实际删除触发器与浏览器展示，不声称测试资料删除按钮。

V01 count3 PASS65.736s实际命令/session已落主集成报告；U SSR新增恢复模板/exact身份测试，fresh V02采用修复。均为局部当前证据，不代替最终门禁。

## 最新正式报告补证

- [V01当前命令与结果](root-integration.md)：count3 PASS65.736s。
- [F SSR与真实CLI预算/合同失败](../../plans/2026-10-02-quality-cases-browser-validation.md)：真实1428/983，review未调用，human pending。
- [G typed理解、缓存语义与真实DJ/加速睡眠](../../../../docs/acceptance/2026-10-02-gaps-browser.md)：加速睡眠不是手机长后台。
- [S0089后四个20样本基准](semantic-performance.md)：cold失败，保持closed。
- [E实际ZIP阅读及连续音频](export-browser/evidence.md)。
- [V02同题两用途、实际十导航、真断网UI同步](../v02/2026-10-02-personal-learning-journey.md)及[实际UI启播JSON](../v02/continuous-ten-nav-ui-play.json)。

## Theme导出与旧对象升级补证

[独立Theme真实下载/404/409报告](export-theme-browser/evidence.md)与[真实ZIP核对](export-theme-browser/archive-proof.json)已读取，131.05s browser PASS及11.876s目标race明确登记，未借用同题旅程服务生命周期。旧0069升级测试现行源码已核实旧文章、审校、队列状态及真实article job关系逐字段恢复；9.410s最新targetrace暂以根执行报告说明，非审计重复执行。

当前确实未闭合的工程证据：V02同题stop/purge新router组合已通过且原日志归档；V03完整race终态与新增测试后的受影响复验；V05冻结提交交付。O工具观察已归档，原stdout缺失保留限制；V04正式32链接终校已归档。外部仍为真实embedding质量与cold性能准入未过、真实文本冻结模型合同不一致/审阅未发、Owner真人五维与真实资料、iOS/Android实体HTTPS旅程。工程实现不应被这些外部等待冒充已验或反向卡住。

## 最终桌面矩阵与同题终态补证

已读取[O正式范围说明](offline/README.md)、[Node24完整日志](offline/node-24.log)、[真实Go driver摘录](offline/real-go-driver-excerpt.log)、[DB核对](offline/database-summary.json)、[CDP差异](offline/chrome-network-settings.json)及制品清单。nativeIDB26工具终态观察已追加，原stdout未单独保存；真实Go154.44s只作该报告限定范围，不把driver摘录叫完整harness日志。

已读取[V02最终完整报告](../v02/2026-10-02-personal-learning-journey.md)、[889.36s终态日志](../v02/final/harness.log)、[十导航JSON](../v02/final/ten-nav.json)和[最终11文件ZIP验证](../v02/final/export-zip.json)。当前V02主要工程集成成立；随后新独立同题完整链真实Owner控制/purge测试已补齐并通过，见下方现行原日志，不把新HTTP故障副本冒称原browser数据库操作。最终全库race执行中、提交未完成；没有工程完成声明。

## 最后日志归档核对

已读取[O26工具终态观察](offline/chromium-26-observation.json)（session53638、exit0、26断言）和[真实Go harness工具终态](offline/go-harness-observation.txt)（session24234、PASS154.44s/exit0）。这是明确标注的执行工具观察，原26 stdout未单独保存；不再当待执行，但也不冒称保存了不存在的完整原日志。

已读取[V04正式终校](documentation-check.md)：4文档32本地链接/anchors零错误、四SHA、diff退出0，与当前文件身份适用；外部URL不在该工具范围。当前剩余工程收口为最终同候选全库race终态、最终指纹/提交与交付状态；V02同题stop/purge已由新真实router副本测试补齐；late paid response竞态仍明确继承C09，不混证据范围。手机与真实质量待验不混入工程PASS。

## 同题停止/清理最后补证

已逐行核对[独立同题故障测试](../../../../internal/server/personal_learning_v4_faults_test.go)和[实际race完整日志](../v02/final/same-fixture-faults-race.log)：1个父测试PASS（方法8.91s、包10.652s）。新固定副本先完成同q1全链后真实Owner `/automation/action` stop、`/api/purge`，8条真实usage逐列不变、模型调用2/6不增，Owner理解Answer与已提交offline稿保留/锚点不可用；源派生文章与冻结input/checkpoint清，旧ZIP/设备pack/file拒绝，旧pending保持原字节但不ack/不生成新稿。源绑定reflection/turnOwnerInput依明确privacy purge清理，不能误称全部Owner正文都应留存。

该结果补齐V02.2工程故障要求：浏览器10次导航、独立同题HTTP故障副本、C09晚到付费竞态及O浏览器可复制文字各有独立范围，没有互相冒称。新增测试后最终全量race/cover/lint重新执行，V03/V05仍待终态与提交。

## 最终V03独立日志核对

已读取[root集成最后终态](root-integration.md)、[完整race原日志](gates/race.log)、[coverage门禁原日志](gates/cover-gate.log)、[lint原日志](gates/lint.log)，实际对[776文件SHA清单](gates/current-source-sha256.txt)运行SHA256核对，结果`e65235091bf2adcacef751daa654b3def3d0a7e1343d2dee31c4d1d4b92aff32`一致。Server154.586s为新执行，Store为cached、同输入上一轮520.228s；缓存适用性由最后冻结零变化与根记录明确说明。V03技术门禁已通过。

此前段落的“门禁执行中”是阶段事实，以此最终终态和V03逐项结论为现行。功能一致提交410e774已完成，V05技术登记成立；最后文档commit及clean由根代理实查；真实个人资料、人评、实体设备、真实embedding Recall以及失败cold准入、真实write模型合同/未发review仍单列，无部署。

## 最终交付登记

实际Git已核对功能提交 **`410e774304e3907bc6cb81db321ba1e894a7cf5d`**，标题`feat(learning): complete personal knowledge and offline learning workflows`，355文件。最后776源码/夹具冻结指纹和全部门禁当前通过，141条的局部证明由该最终门禁统一覆盖；没有以全库通过反推真实模型/人工/设备质量。

本审计核对工作树时只有此文档修改；根代理将单独完成主任务卡/计划/交付报告的文档提交并实际检查clean，本审计不预填未来结果。审计仅修改owned文档、不执行新增测试或生产动作，至此停止。外部待验：真实个人资料与五维评分、实体iOS/Android HTTPS/自然录音/长后台、真实embedding Recall及cold阈值未过；真实文本write实际模型不匹配失败、review未发，不自动改生产配置或部署。
