# ADR-0022 实现差距清单

状态：迁移规划基线（2026-08-18）
目标模型：[`product-goal.md`](product-goal.md) / [ADR-0022](adr/0022-learning-creation-workspaces.md)
当前实现基线：commit `68901b7`

本文只描述“当前代码是什么”与“目标模型要求什么”的差距，不重新定义领域语言。迁移顺序以 [`implementation-roadmap.md`](implementation-roadmap.md) 为准。

## 必须先停止扩大的旧语义

| 当前实现 | 目标模型 | 风险 | 首个迁移动作 |
|---|---|---|---|
| 提案离开 `proposed` 后后台补货至 5 条 | ProposalBatch 由新 MaterialChange、Owner 刷新或反馈重构触发；未处理批次产生注意力背压 | 在素材不变时付费洗牌，生成标题变体 | 关闭自动补货 goroutine，保留手动 Scout 兼容入口 |
| Scout 输出必须恰好 5 条 | 目标 5 条、最多 10 条、允许更少并说明原因 | 鼓励模型凑数和伪多样性 | Provider 校验改为 1–10 条，增加批次级缺口字段 |
| Theme 必须 confirmed 且跨至少两个 Source 才能 Scout | Theme 只是组织和召回加权；自动发现由 DiscoveryWindow 驱动 | AutomaticDiscovery 仍依赖 Owner 手工组装 | 新发现请求直接使用画像相关 KeyPoint；Theme 作为可选过滤或权重 |
| EditorialProfile 逐 Source SourceScope 授权 | Owner 纳入的 Source 默认可创作；画像只做 EditorialRelevance | 重复授权阻断自动发现 | 移除 UI 门禁和服务层 CanUseSourceForPublication 检查；保留表作迁移审计 |
| Source 有 public/internal/disabled 生产用途 | 无 ContentUsePolicy；RightsConstraint 限制具体对外表达和资产 | 把思想复用、隐私、版权混为一谈 | 停止新增用途策略，设计 RightsConstraint |

## 学习成果差距

| 目标对象/能力 | 当前实现 | 缺口 |
|---|---|---|
| MaterialCandidate | KnowledgeCard 直接写入 KeyPoint | 无临时候选、Source 级质量门槛和淘汰原因 |
| KeyPoint 质量状态 | inbox/shortlisted/used/dismissed 是生产状态 | 缺 Ready/NeedsReview/OwnerConfirmed/Dismissed；生产使用历史与素材质量混在一起 |
| SourceNote / OwnerReflection | Annotation 只有一类 | 无 Citation 忠实整理与 Reference 个人反思的语义区分 |
| MaterialChange | 依赖记录创建和更新时间 | 无“实质发现价值变化”事件，重分析可能被误认为新素材 |
| EditorialRelevance | Theme 和 SourceScope 代替相关性 | 无 Relevant/Adjacent/Irrelevant 和 OwnerIncluded/OwnerExcluded |

## 自动发现差距

| 目标对象/能力 | 当前实现 | 缺口 |
|---|---|---|
| ProposalBatch | ArticleProposal 独立存储 | 无批次身份、素材快照、生成原因、批次状态和不足说明 |
| DiscoveryWindow | 无 | 无上一批快照、6 条/2 Episode 门槛、30 分钟防抖和每日频率 |
| 注意力背压 | 无 | 未处理候选存在时仍可继续生成 |
| 新素材种子 + 历史召回 | Scout 读取 Theme 全部成员 | 无种子/历史区分和“每候选至少一项新价值”校验 |
| CreationProposal | ArticleProposal 以标题为主要展示与去重信号 | 无 ProposedClaim 稳定身份、CreationForm 建议和素材成熟度 |
| HardDuplicate / FollowUp | 标题正规化和 bigram 近似比较 | 无主张、受众、素材、作品承诺和 CreationHistory 比较 |
| SavedProposal | parked 状态 | 缺“已由 Owner 判断、退出批次压力、继续参与去重”的明确语义 |

## 定向构思与研究差距

| 目标对象/能力 | 当前实现 | 缺口 |
|---|---|---|
| IdeationSession | 手动 Proposal 表单 | 无持久多轮会话、范围约束、每轮诊断和继续细化 |
| IdeationIntent | title/thesis/rationale 表单字段 | 输入被过早当成确定提案，不是待检验意图 |
| MaterialDiagnosis | 无 | 不区分支持、反驳、补充和缺口 |
| ProposedClaim / OwnerClaim | proposal.thesis | AI 综合和 Owner 承担混在同一字符串中 |
| ResearchNeed / Researching | 无 | 素材不足只能失败或继续生成，无法保留方向并返回学习 |
| ResearchPlan | 无 | 未来联网研究没有问题、范围、预算和授权契约 |

## Brief 与作品差距

| 目标对象/能力 | 当前实现 | 缺口 |
|---|---|---|
| CreationBriefDraft | Owner 接受后还需手动点击 Curator | 多一个无决策价值的启动动作；无自动整理状态 |
| CreationBrief | ArticleBrief 只描述 thesis、outline、material/conflict、style、length | 无 OwnerClaim、预计主张类型、ResearchNeed 和 RightsConstraint |
| CreationForm | 固定 Article | 领域层无法表达同方向派生不同作品；V1 可继续只实现 Article |
| WorkDraft / WorkRevision | ArticleDraft / ArticleRevision | 名称和聚合边界锁定长文章；可先通过兼容接口过渡 |

## 主张审校差距

| 目标对象/能力 | 当前实现 | 缺口 |
|---|---|---|
| ClaimMap | EvidenceMap: quoted/paraphrased/synthesized/rhetorical | 无 SourceClaim/OwnerClaim/SynthesisClaim/VerifiedFact 责任语义 |
| ClaimReview | EvidenceReviewer 要求表达由 KeyPoint 支撑 | OwnerClaim 被迫伪装成来源观点；无法检查 Brief 外新主张和 ResearchNeed |
| VerifiedFact | ExternalFact 一律禁止 | 无“来源说过”与“客观核验完成”的独立状态 |
| RightsConstraint | 直接引语与来源列表规则 | 无长篇复刻、音频、音乐、图片等资产级权利检查 |
| 来源密度 | light/standard/strict | 可兼容映射为 Minimal/Standard/Detailed，但须按主张类型强制最低归因 |

## 创作历史与首页差距

| 目标对象/能力 | 当前实现 | 缺口 |
|---|---|---|
| CreationHistory | 内部 Proposal/Draft；PublishedArticle 尚未完整落地 | 无外部 PublishedWork/UnpublishedWork 导入和内容级去重 |
| EditorialFeedback 原因 | proposal 只有状态 | 无 HardDuplicate、WeakClaim、PoorFit、InsufficientMaterial、WrongAngle、NotNow 等行为差异 |
| AttentionQueue | Workbench 三栏 + 任务进度页 | 无学习/创作双泳道和闭环提示；ResearchNeed、Stale、预算阻断没有统一入口 |
| DefaultEditorialProfile | 必须手工创建画像 | 首次创作仍有配置门槛 |

## 数据兼容映射建议

| 旧对象 | 迁移期映射 |
|---|---|
| ArticleProposal | CreationProposal，默认 `CreationForm=Article`；现有 thesis 暂映射为未分型的 ProposedClaim/OwnerClaim，需 Owner 后续确认 |
| parked Proposal | SavedProposal |
| ArticleBrief | Article 形态 CreationBrief；material/conflict 原样保留 |
| ArticleDraft | WorkDraft，CreationForm=Article |
| ArticleRevision | WorkRevision |
| EvidenceMap quoted/paraphrased | SourceClaim |
| EvidenceMap synthesized | SynthesisClaim |
| EvidenceMap rhetorical | 非主张表达，保留但不进入 ClaimMap 主张计数 |
| EvidenceReviewer 结果 | 旧式 ClaimReview 兼容记录，标记 review_contract=evidence-v1 |
| source_attribution light/standard/strict | Minimal/Standard/Detailed |
| SourceScope | 迁移审计数据；不再作为创作门禁，稳定后删除 |
| Theme 状态 | 保留组织状态；不再决定能否发现 |
| proposal provider/model/cost | 迁移到 ProposalBatch 或 CreationProposal 生成来历 |

## 不允许的迁移捷径

- 不直接重命名数据库表后让旧版本无法回滚；
- 不把现有 proposal.thesis 自动认定为 OwnerClaim；
- 不因取消 SourceScope 而绕过 LocalOnly 或 ConfiguredProviders；
- 不把所有旧 KeyPoint 自动标为 OwnerConfirmed；
- 不将旧 EvidenceMap 的 synthesized 映射为 SourceClaim；
- 不删除旧 Proposal、Brief、Draft、Revision 或审校历史；
- 不在 ProposalBatch 落地前继续扩展自动补货策略；
- 不以“文档已改”为由声称当前产品已经实现 ADR-0022。
