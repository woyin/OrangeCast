---
status: accepted
date: 2026-08-18
supersedes: ADR-0021 product shape, discovery model, SourceScope, and evidence-review semantics
---

# ADR-0022：学习与创作构成同一闭环中的两个工作空间

CloudWisePod 定位为单 Owner、自托管的学习与创作工作空间。LearningWorkspace 将 Podcast、Document 等 Source 转化为来源忠实、可追溯和可复用的学习成果；CreationWorkspace 通过 AutomaticDiscovery 或 DirectedIdeation 帮助 Owner 形成自己的主张、补足研究缺口并创作作品。学习不从属于写作，创作也不能绕过学习成果和主张身份直接让模型补充事实。

## 决策

1. **来源忠实不等于来源权威**：Transcript、Citation、SourceNote 与 SourceClaim 必须准确回答“来源说了什么”，但 Citation 不证明来源说法在客观世界中成立。VerifiedFact、OwnerClaim 与 SynthesisClaim 分别承担事实核验、作者立场和综合推论的语义。
2. **学习成果统一进入 KeyPoint**：Summary、Highlight、SourceNote 等先形成 MaterialCandidate；通过 Source 级独立性、来源、去重和非背景检查后才成为 KeyPoint。OwnerReflection 属于个人思考，不伪装成来源观点。
3. **创作发现有自动与定向两条入口**：AutomaticDiscovery 根据新 MaterialChange 形成 ProposalBatch；DirectedIdeation 通过持久 IdeationSession 对 Owner 的 IdeationIntent 做 MaterialDiagnosis。两者都汇合为 CreationProposal，不自动生成作品或发布。
4. **自动发现采用批次而非库存补货**：跨集自动批次要求上一批之后至少 6 条新发现价值、来自至少 2 个 Episode，并在最后一次学习处理后防抖 30 分钟；每画像每天最多自动一批。批次目标 5 条实质不同方向、最多 10 条，素材不足时允许更少并说明缺口。存在未处理批次时暂停自动生成。
5. **新素材是种子，历史素材是召回补充**：每个自动候选至少使用一项当前 DiscoveryWindow 的 MaterialChange；纯历史重组属于 Owner 主动发起的 EvergreenExploration。Theme 只用于长期组织、召回加权、定向探索和内容空白观察，不再是 Scout 的审批门禁。
6. **重复按主张而非标题判断**：HardDuplicate 比较核心主张、受众、主要素材和作品承诺；FollowUp 必须关联具体 CreationHistory 并说明增量价值。外部 PublishedWork 与 UnpublishedWork 可以导入 CreationHistory 参与去重。
7. **AI 提议，Owner 承担**：AI 综合出的观点先是 ProposedClaim。Owner 接受或编辑 CreationProposal 时，ProposedClaim 才转为 OwnerClaim。素材不足的已接受方向进入 Researching；BlockingResearchNeed 未解决前不能形成 CreationBrief。
8. **接受方向后自动整理、确认方案后才创作**：素材充足时，接受 CreationProposal 自动生成 CreationBriefDraft；这不等于写作授权。只有 Owner 确认 CreationBrief，系统才可以生成具体作品。
9. **作品形态晚绑定**：发现阶段使用通用 CreationProposal，Owner 接受时选择 CreationForm。同一方向未来可以派生多种作品；V1 仍只实现 Article。
10. **主张审校取代证据覆盖率审校**：ClaimMap 区分 SourceClaim、OwnerClaim、SynthesisClaim 与 VerifiedFact。ClaimReview 检查来源归因、Owner 授权、综合边界、事实核验、未解决 ResearchNeed、RightsConstraint，以及 Writer 是否加入 Brief 外的新主张。
11. **取消创作素材授权**：Owner 纳入 CloudWisePod 的全部 Source 默认可以用于学习和创作，不再维护 EditorialProfile 级 SourceScope 或 Source 级 ContentUsePolicy。EditorialRelevance 负责画像相关性和 Owner 纳入/排除；RightsConstraint 只约束对外复制具体表达和媒体资产。
12. **模型数据策略默认开放、例外限制**：Source 默认可发送给 Owner 配置的 Provider；只有特殊 Source 才限制为 ConfiguredProviders 或 LocalOnly。混合素材没有共同 Provider 时必须拆分候选或明确提示。
13. **自动发现需要画像级一次授权**：Owner 开启 AutomaticDiscovery 时明确模型、门槛、防抖、频率、单批预算和暂停方式；默认画像不会自动花费。失败可见，同一素材快照的重试幂等。
14. **首页服务注意力而非对象管理**：AttentionQueue 以学习和创作两条泳道展示近期成果、阻断和下一步；失败、Stale、预算阻断和 ResearchNeed 不得静默隐藏。

## 保留自 ADR-0021 的决策

- 单 Owner、自托管和人工发布边界不变。
- AI 与 Owner 修改继续形成不可变作品修订，历史不得覆盖。
- 模型角色路由、预算、费用、Prompt 版本和故障切换继续可追踪。
- 模型自身知识不得静默成为作品事实；未来联网研究须先形成 Owner 授权的 ResearchPlan，结果保存为 Source 后再进入学习流程。
- PublicationPackage 仍由 Owner 复制或导出，系统不自动发布或群发。

## 后果

- 当前实现中的 SourceScope、Theme 强制 Scout、固定五条、离池补货、ArticleProposal/ArticleBrief/EvidenceMap/EvidenceReviewer 等属于迁移前模型，不能继续作为新功能设计依据。
- 迁移必须保留现有 Source、KeyPoint、提案、Brief、Draft、Revision、审校和费用记录；旧对象通过兼容投影逐步映射到新模型，不能一次性破坏性重写。
- `CONTEXT.md` 与 `docs/product-goal.md` 以本 ADR 为准；ADR-0021 保留为内容生产转型的历史背景。
