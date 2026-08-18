# V1 学习—创作工作空间验收记录

> 状态：**代码级验收完成；真实 Owner 旅程待执行。**
>
> 本记录刻意区分自动化验证和需要 Owner 在真实 Provider / 发布目标中执行的验收，不以测试替代真实发布或外部费用确认。

## 已自动验证的闭环

| 阶段 | 可验证行为 | 自动化证据 |
| --- | --- | --- |
| 学习质量 | 自动 KeyPoint 初始为 `needs_review`；Owner 标记 `ready` 后只记录一条幂等 `MaterialChange` | `internal/store/creation_workspace_test.go` |
| 自动发现门槛 | 仅已审、未陈旧、相关的 Episode 学习成果计入；至少 6 条、2 个 Episode、防抖、每日上限和未处理批次背压均有效 | `internal/store/discovery_schedule_test.go` |
| 付费调用前领取 | 相同素材快照先持久化领取；并发观察者不会再次调用 Provider | `internal/store/proposal_batches_test.go` |
| 自动发现结果 | Fake Scout 输出写入可见 `ProposalBatch` 与 `CreationProposal`；Provider 失败保留 `failed` 批次 | `internal/server/automatic_discovery_test.go` |
| Owner 主张 | `ProposedClaim` 只有经 Owner 接受/编辑后才成为 `OwnerClaim`；随后创建可审阅 `CreationBrief` | `internal/server/creation_workspace_test.go` |
| 研究阻断 | 新增的 blocking `ResearchNeed` 会在确认 Brief 时再次阻止授权 | `internal/store/creation_brief_test.go` |
| 来源与个人表达 | `SourceNote` 必须有本 Source 的 Citation；`OwnerReflection` 明确单独存储；RightsConstraint 独立保存 | `internal/store/owner_notes_test.go` |
| 兼容迁移 | 旧 EvidenceMap 作为只读 ClaimMap 兼容投影；EvidenceReviewer 会同时写 ClaimReview | `internal/store/claim_review.go`、`internal/server/editorial_review_workflow.go` |
| 恢复 | SQLite 一致性备份恢复 MaterialCandidate、MaterialChange、EditorialRelevance 与 ProposalBatch | `internal/backup/backup_test.go` |

## Owner 真实旅程（待执行）

在有真实、已获授权的 Provider 配置与发布目标时，Owner 应完成以下步骤并补充日期、画像和结果：

1. 导入至少两个新 Episode，确认生成的 KeyPoint；逐条完成质量决策。
2. 在工作台显式启用 AutomaticDiscovery，配置 Provider、模型、每日上限、防抖和单批预算。
3. 等待防抖后确认只生成一个 ProposalBatch，检查来源、费用、短缺原因和历史重复提醒。
4. 编辑并接受一个 ProposedClaim，检查其转化为 OwnerClaim 和 CreationBriefDraft。
5. 添加及解决一个 blocking ResearchNeed，确认其在解决前阻止 Brief 确认。
6. 在兼容 Article 工作流中完成 Writer、ClaimReview / EvidenceReview、Owner 修订和 PublicationPackage。
7. 执行 `cloudwisepod backup <archive>`，恢复到全新 DATA_DIR；验证学习、批次、主张、Brief、作品修订和导出仍可浏览。
8. 对实际发布目标执行一次富文本粘贴/导出验证；系统不得自动发布。

## 已知边界

- 外部 Provider 调用无法与 SQLite 事务构成严格的 exactly-once 分布式事务。系统以**调用前持久化素材快照领取**防止并发重复调用，并将故障批次保留为可见记录；Owner 在不确定远端是否已经完成时必须人工决定是否重试。
- 单批预算会在 Provider 返回用量后据实记录并标记超限；由于既有 Provider 契约未提供跨 Provider 的可计费上限，它不是调用前的硬金额预留。不得将它表述为绝不会超支的硬 cap。
- V1 的 DirectedIdeation 先持久化 Owner 的探索合同、诊断和研究缺口；联网研究仍必须经 Owner 确认的 ResearchPlan，结果先进入 Source / LearningWorkspace。
- 旧 Article / Evidence 对象保留可读、可继续和可导出；其迁移以兼容投影渐进进行，不破坏历史数据。
