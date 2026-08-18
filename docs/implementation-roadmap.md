# CloudWisePod 实施路线

基线：Go、SQLite、单 Owner、自托管；现有学习能力和 ADR-0021 文章生产链路均已落地
目标：实现 [`product-goal.md`](product-goal.md) 与 [ADR-0022](adr/0022-learning-creation-workspaces.md) 定义的学习与创作闭环
状态：产品模型已确认，代码处于兼容迁移期

## 当前实施基线

现有代码已经具备：

- Podcast、Upload 与 Document Source；
- EvidenceAudio、EvidenceDocument、Transcript、Segment 与 Citation；
- Summary、KnowledgeCard、Highlight、KeyPoint、EvidenceQA、StudyChat、Paraphrase 与 KnowledgeNote；
- EditorialProfile、Theme、ArticleProposal、ArticleBrief、ArticleDraft、ArticleRevision、EvidenceMap；
- Scout、Curator、Writer、EvidenceReviewer、StyleEditor；
- 模型角色路由、预算、费用记录、幂等任务、备份与恢复；
- Markdown、公众号预览和 PublicationPackage。

这些能力是迁移资产，不再直接定义目标领域模型。当前实现与 ADR-0022 的差距见 [`learning-creation-migration-gap.md`](learning-creation-migration-gap.md)。旧黄金旅程保留在 [`v1-golden-journey-run.md`](v1-golden-journey-run.md) 作为历史实录。

## 迁移原则

1. **数据优先兼容**：现有 Source、KeyPoint、提案、Brief、Draft、Revision、审校、费用和备份数据不得丢失。
2. **先增加新语义，再删除旧语义**：通过迁移表、兼容读取和投影逐步替换旧对象，不进行一次性破坏性改名。
3. **来源忠实继续严格**：Transcript、Citation、SourceNote 与 SourceClaim 的准确性不能因产品重新定位而放宽。
4. **AI 不静默承担主张**：ProposedClaim 只有经 Owner 接受或编辑后才成为 OwnerClaim。
5. **普通 GET 不调用付费模型**：所有自动付费任务都有持久状态、预算、幂等键和可见失败。
6. **新自动发现不得继续沿用离池补货**：在 ProposalBatch 落地前，关闭或隔离现有自动补货，避免扩大错误语义。
7. **每阶段可构建、可启动、可回滚**：数据库迁移、备份恢复和旧记录浏览必须同步验证。

## Phase 0：冻结错误方向并建立迁移护栏

目标：阻止现有临时实现继续产生与新模型冲突的数据。

- 将 ADR-0022、产品目标和领域词汇作为设计权威。
- 关闭“提案离开池后立即补货”的自动行为；保留手动 Scout 作为迁移期入口。
- 将“必须恰好 5 条”改为兼容接受 1–10 条，生成侧目标 5 条、最多 10 条。
- 停止新增依赖 SourceScope 的产品功能；现有表和数据暂时只作兼容读取。
- 在 UI 显示“当前创作流程处于迁移期”，避免把旧 Theme/Scout 规则当作长期产品承诺。
- 建立迁移 EvalSet：标题变体、硬重复、相关延展、来源观点与作者主张混淆、模型偷补事实。

退出条件：新数据不再依赖固定五条和离池补货；旧数据仍可浏览、写作和导出。

## Phase 1：学习成果模型

目标：让 LearningWorkspace 稳定产出可用于发现的 KeyPoint，同时区分来源整理和个人思考。

- 引入 MaterialCandidate，记录来自 Summary、Highlight、SourceNote 或 PrimarySource 的候选来源。
- KeyPoint 增加 Ready、NeedsReview、OwnerConfirmed、Dismissed 质量状态。
- 建立 Source 级质量门槛：独立表达、来源位置、本集去重、非纯背景。
- 将 Owner 标注拆为 SourceNote 与 OwnerReflection：前者使用 Citation，后者使用 Reference。
- 引入 MaterialChange，区分实质发现变化和纯展示修改。
- PrimarySource 变化导致依赖失效时，将相关 KeyPoint 和下游对象标为 Stale。
- 为现有 KeyPoint 制定保守回填：有有效 Citation 的自动记录可标 Ready；无法判断的记录标 NeedsReview；Owner 手工记录按实际来源关系迁移。

退出条件：新 Source 能自动形成质量分层 KeyPoint；OwnerReflection 不会被误存为来源观点；重分析不会伪造新发现价值。

## Phase 2：画像相关性、Theme 降级与数据策略简化

目标：取消创作素材授权，让画像只负责发现相关性和创作偏好。

- 建立 DefaultEditorialProfile，首次创作无需先配置完整品牌。
- 引入 EditorialRelevance：Relevant、Adjacent、Irrelevant、OwnerIncluded、OwnerExcluded。
- 移除 UI 中的 SourceScope 授权流程；迁移期忽略旧授权缺失，不删除旧表，待确认无回滚需求后再清理。
- Theme 从 Scout 门禁改为组织、召回加权、定向探索和内容空白观察。
- ModelDataPolicy 改为默认开放、Source 级例外：ConfiguredProviders 或 LocalOnly。
- 混合素材没有共同 Provider 时，明确拆分或报阻断，不静默丢素材。
- RightsConstraint 作用于直接引语、长篇复制、原音、音乐、图片和其他媒体资产，不建立 Source 级创作许可。

退出条件：所有非 OwnerExcluded KeyPoint 都可按画像相关性参与本地发现；Theme 和 SourceScope 不再是生成前置条件；Provider 限制仍不可绕过。

## Phase 3：ProposalBatch 与 AutomaticDiscovery

目标：用素材快照和注意力背压替换库存补货式 Scout。

- 引入 DiscoveryWindow、ProposalBatch、SavedProposal 和批次素材快照。
- 自动触发默认规则：至少 6 项 MaterialChange、至少 2 个 Episode、30 分钟防抖、每画像每天最多一批。
- 每画像由 Owner 一次开启 AutomaticDiscovery，明确模型、预算、频率和暂停方式。
- 存在 Ready 或 Reviewing 批次时只记录“新素材已就绪”，不继续生成。
- 新素材作为种子，语义召回历史 KeyPoint；每个自动候选至少使用一项当前窗口的新价值。
- 批次目标 5 条、最多 10 条；允许更少并返回批次级不足原因和 ResearchNeed。
- CreationProposal 以 ProposedClaim 为身份，WorkingTitle 只是展示建议。
- 去重综合比较主张、受众、素材和作品承诺；同一主张的标题变体不占多个名额。
- 批次状态：Ready、Reviewing、Completed、Superseded、Failed；素材失效时为 Stale。
- 候选卡提供浏览层和展开层，并支持接受、编辑后接受、保存、带原因拒绝或转入 IdeationSession。

退出条件：AutomaticDiscovery 在无人逐条整理 Theme 的情况下产生可解释批次；不凑数、不堆积、不重复扣费，失败与成本可见。

## Phase 4：CreationHistory 与重复判断

目标：排除 Owner 已经实质表达过的内容，而不是只比较 CloudWisePod 内标题。

- 引入 CreationHistory、PublishedWork 与 UnpublishedWork。
- 支持正文粘贴、Markdown、URL 快照，以及标题+核心主张+摘要的降级导入。
- 从历史作品提取核心主张、目标受众、作品承诺和 CreationForm。
- 完整正文参与高置信度 HardDuplicate；摘要和标题只产生低置信度提醒。
- FollowUp 关联具体历史作品并说明新材料、反方视角、不同受众或更深分支的增量价值。
- EditorialFeedback 使用 HardDuplicate、WeakClaim、PoorFit、InsufficientMaterial、WrongAngle、NotNow 和 Other；不同原因影响后续发现，不静默修改画像。

退出条件：导入外部历史作品后，实质重复候选被排除，相关延展保留且解释差异。

## Phase 5：DirectedIdeation 与研究回路

目标：让 Owner 从自己的问题和判断出发，用已有学习成果形成 CreationProposal。

- 引入 IdeationSession、IdeationIntent、MaterialDiagnosis、ProposedClaim。
- 默认检索当前画像 Relevant 与 Adjacent 素材；Owner 可限定 Episode、Podcast、Theme、时间和 KeyPoint。
- MaterialDiagnosis 显示支持、反驳、补充和缺口；范围变化后重新诊断。
- Owner 可以编辑主张、增删素材、缩小范围、坚持立场、放弃，或从 ProposalBatch 转入会话继续细化。
- Owner 提升方向时，ProposedClaim 转为 OwnerClaim，并形成已接受 CreationProposal。
- 引入 BlockingResearchNeed、EnhancementNeed、Researching 和 ReadyForBrief。
- V1 通过 Owner 手动导入 Source 解决 ResearchNeed；ResearchPlan 只落数据与授权契约，不执行联网研究。

退出条件：Owner 能从模糊想法经过多轮素材诊断形成可承担主张；素材不足的好方向被保留，但不能越过阻断进入 Brief。

## Phase 6：CreationBrief 与通用作品边界

目标：把接受方向和授权创作保持为两个不同决策点。

- 将 ArticleProposal 兼容映射为 CreationProposal；V1 旧记录默认 CreationForm=Article。
- 引入 CreationBrief 与 CreationBriefDraft；现有 ArticleBrief 兼容映射为 Article 形态 Brief。
- 接受素材充足的 CreationProposal 后自动整理 CreationBriefDraft；画像可选择关闭自动整理。
- Brief 显示 OwnerClaim、预计 SourceClaim/SynthesisClaim/VerifiedFact、入选/淘汰素材、关系、ResearchNeed、结构、篇幅、风格和 RightsConstraint。
- BlockingResearchNeed 未解决时不得生成 Brief。
- 只有 Owner 确认 CreationBrief 才授权 WorkDraft 生成。
- 引入 WorkDraft 与 WorkRevision 领域接口；V1 底层可继续复用 article_drafts/article_revisions，直到兼容层稳定。

退出条件：Owner 接受方向后无需额外点击“让 Curator 生成”，但系统仍不能在 Brief 未确认时生成作品。

## Phase 7：ClaimMap 与 ClaimReview

目标：把质量门禁从“所有表达必须有 KeyPoint”升级为“主张身份、责任和核验真实”。

- 引入 ClaimMap：SourceClaim、OwnerClaim、SynthesisClaim、VerifiedFact。
- Writer 只能使用 CreationBrief 中已确认的主张，不得自行创造 OwnerClaim 或偷补模型事实。
- ClaimReview 检查来源归因、Owner 授权、综合边界、事实核验、ResearchNeed、RightsConstraint 和 Brief 外主张。
- StyleReview 保持独立，检查目标受众、结构、节奏、篇幅和画像风格。
- 将现有 EvidenceMap 迁移为 ClaimMap 兼容记录：Quoted/Paraphrased 映射为 SourceClaim，Synthesized 映射为 SynthesisClaim，Rhetorical 作为非主张表达保留。
- 旧 EvidenceReviewer 角色逐步重命名为 ClaimReviewer；配置迁移保留模型选择。
- Owner 修改 WorkRevision 后，只继承仍然逐字存在且身份未变化的 ClaimMap；其他主张重新审校。

退出条件：作品不能把来源观点写成客观事实，不能把 AI 综合写成来源原意，也不能把未确认判断归给 Owner。

## Phase 8：PublicationPackage、权利约束与创作历史闭环

目标：输出诚实归因、权利清晰且可回到内部 ClaimMap 的发布内容包。

- PublicationPackage 支持 Minimal、Standard、Detailed 三档来源密度。
- 直接引语、SourceClaim、VerifiedFact 和特定框架按作品形态强制适当归因。
- RightsConstraint 阻止长篇逐字复刻、未授权原音、音乐、图片和媒体资产进入发布包。
- Owner 发布后形成 PublishedWork，指向确切 WorkRevision 和 CreationProposal。
- 未发布但已实质写作的内容形成 UnpublishedWork 并参与去重。
- 备份覆盖 ClaimMap、ClaimReview、CreationHistory、RightsConstraint 和发布包。

退出条件：内部 ClaimMap 完整，对外来源密度可控；权利风险不会因不显示来源而被掩盖。

## Phase 9：AttentionQueue 与双工作空间导航

目标：首页回答“现在最值得我处理什么”，而不是罗列所有对象。

- LearningWorkspace 与 CreationWorkspace 各有完整入口，共享 Source、KeyPoint、OwnerReflection 与 ResearchNeed。
- 首页建立学习泳道：处理失败、NeedsReview、学习会话、ResearchNeed、新学习成果。
- 首页建立创作泳道：ProposalBatch、IdeationSession、CreationBriefDraft、ClaimReview、Stale、可发布作品。
- 显示学习如何满足 DiscoveryWindow、创作缺口如何返回学习。
- 普通事项可隐藏；失败、Stale 和预算阻断不得静默隐藏。
- 旧 Workbench 三栏逐步退场，保留兼容入口直到新旅程完成。

退出条件：Owner 不需要理解数据库对象，就能从首页进入最重要的学习或创作下一步。

## Phase 10：迁移收口与新黄金旅程

目标：证明新闭环可在真实实例持续使用并完整恢复。

- 执行 [`product-goal.md`](product-goal.md) 的 12 步 V1 验收旅程。
- 验证旧 ArticleProposal、ArticleBrief、ArticleDraft、ArticleRevision、EvidenceMap 与审校记录在新界面可读、可继续、可导出。
- 验证 SourceScope 退场后不扩大 ModelDataPolicy；旧授权数据保留审计但不再阻断创作。
- 验证 ProposalBatch 自动任务的幂等、预算、进程中断恢复和注意力背压。
- 验证 IdeationSession、OwnerClaim、ResearchNeed、CreationBrief、ClaimMap 和 CreationHistory 的备份恢复。
- 新建 `docs/v1-learning-creation-journey-run.md` 记录真实摩擦；旧黄金旅程不再作为验收权威。
- 所有迁移确认稳定后，再删除 SourceScope、固定五条补货和旧 EvidenceReviewer 等兼容代码。

退出条件：新模型数据可跨全新实例恢复；旧数据无损继续；真实学习、自动发现、定向构思、主张审校和手工发布完成闭环。

## 后续能力

- 执行 Owner 授权的联网 ResearchPlan；结果必须保存为 Source。
- Article 之外的 CreationForm：ShortCommentary、PostSeries、Script 等。
- Translation、Speaker 确认、生成视觉资产和编辑日历。
- PublicationPerformance 与画像调整建议；任何画像修改仍需 Owner 确认。
- 微信草稿箱 API、指标同步和跨渠道适配；仍不自动群发。

## 每阶段通用验证

```bash
go test ./...
go vet ./...
go build ./cmd/cloudwisepod
make cover-gate
make lint
git diff --check
```

涉及模型质量时运行对应 EvalSet；涉及付费任务时验证预算、幂等和费用记录；涉及迁移时使用真实 SQLite、备份恢复和旧数据兼容测试；涉及 ModelDataPolicy 时验证限制路径和故障切换不能绕过例外策略。
