# CloudWisePod 产品目标

状态：已确认（2026-08-18 学习与创作闭环，见 ADR-0022）
日期：2026-08-01 / 修订：2026-08-18

## 一句话目标

CloudWisePod 是单 Owner、自托管的学习与创作工作空间：它把 Podcast、Document 等 Source 转化为来源忠实、可追溯和可复用的学习成果，帮助 Owner 理解内容、形成自己的判断，并通过自动发现或定向构思发展出值得表达的创作方向。

## 核心承诺

来源是学习与思考的起点，不是绝对权威。CloudWisePod 严格区分：

- **SourceClaim**：来源表达过什么；
- **OwnerClaim**：Owner 愿意承担什么判断；
- **SynthesisClaim**：作品基于多项素材形成了什么综合推论；
- **VerifiedFact**：哪些事实、数字或事件已经单独核验。

AI 可以总结、比较、质疑、提出 ProposedClaim 和 ResearchNeed，但只有 Owner 能承担主张、确认创作方案并决定发布。模型自身知识不能静默成为作品事实。

## 产品闭环

```text
Source
  ↓
LearningWorkspace
  ├─ Transcript / Summary / Highlight
  ├─ SourceNote / OwnerReflection
  └─ MaterialCandidate → KeyPoint
                         ↓
                 WritableMaterial
                         ↓
       ┌─────────────────┴─────────────────┐
       │                                   │
AutomaticDiscovery                 DirectedIdeation
       │                                   │
ProposalBatch                    IdeationSession
       └─────────────────┬─────────────────┘
                         ↓
                 CreationProposal
                         ↓ Owner 接受主张
          ┌──────────────┴──────────────┐
          │                             │
   素材足够                       BlockingResearchNeed
          │                             │
CreationBriefDraft                  Researching
          │                             ↓
          │                  ResearchPlan / 继续学习
          └──────────────┬──────────────┘
                         ↓
              Owner 确认 CreationBrief
                         ↓
              作品生成 + ClaimReview
                         ↓
                PublicationPackage
                         ↓
                    Owner 发布
```

## 两个工作空间

### LearningWorkspace

学习空间帮助 Owner 理解 Source，而不是只为写作做预处理：

- 保存音频或文档来源、Transcript、Segment 和稳定来源位置；
- 生成 Summary、Highlight、KnowledgeCard 等学习产物；
- 支持 EvidenceQA、StudyChat、Paraphrase、回听与笔记；
- 区分忠实来源整理的 SourceNote 与个人理解的 OwnerReflection；
- 从学习产物形成 MaterialCandidate，经 Source 级质量门槛成为 KeyPoint；
- 接收 CreationWorkspace 返回的 ResearchNeed，推动补充学习。

### CreationWorkspace

创作空间帮助 Owner从 WritableMaterial 发展自己的表达：

- AutomaticDiscovery 在 Owner 授权的节奏和预算内生成 ProposalBatch；
- DirectedIdeation 通过 IdeationSession 诊断 Owner 想法与现有素材的关系；
- Owner 接受或编辑 ProposedClaim 后承担为 OwnerClaim；
- 素材不足的方向进入 Researching，而不是让模型偷偷补事实；
- 素材充足时自动整理 CreationBriefDraft；
- Owner 确认 CreationBrief 后才授权作品生成；
- ClaimReview 确认主张身份、归因、核验和权利边界；
- PublicationPackage 由 Owner 复制或导出，系统不自动发布。

## 学习成果与可写素材

### MaterialCandidate 与 KeyPoint

Summary、Highlight、SourceNote、Transcript 等可以产生多个 MaterialCandidate，但只有满足以下条件的候选才成为 KeyPoint：

- 能独立表达一个具体观点；
- 有明确来源位置；
- 与本 Source 的其他 KeyPoint 不构成近似重复；
- 不是纯背景、章节标题或情绪化摘句。

KeyPoint 不随 EditorialProfile 复制。其质量状态为：

- `Ready`：自动检查通过，可参与发现；
- `NeedsReview`：来源、去重、归因或推论存在疑点；
- `OwnerConfirmed`：Owner 确认或实质编辑，发现排序更高；
- `Dismissed`：保留历史但不再参与发现。

产品界面将可用于创作的 KeyPoint 称为 WritableMaterial。

### EditorialRelevance

KeyPoint 与 EditorialProfile 的关系由 EditorialRelevance 表达：

- AI 可评为 Relevant、Adjacent 或 Irrelevant；
- Owner 可强制 Included 或 Excluded；
- AI 重新评分不得覆盖 Owner 决策；
- Excluded 素材在该画像下不参加自动发现和定向构思；
- Adjacent 可以在 IdeationSession 中作为扩展素材召回。

Theme 保留为长期组织、召回加权、定向探索和内容空白观察工具，不再是 AutomaticDiscovery 的审批门禁。

## AutomaticDiscovery

AutomaticDiscovery 只自动生成候选，不自动生成作品或发布内容。

### 启用与预算

- 每个 EditorialProfile 由 Owner 一次开启；
- 开启时明确 Scout 模型、触发门槛、防抖、每日频率、单批预算和暂停方式；
- 默认画像不会自动花费；
- 失败必须可见，同一素材快照重试必须幂等。

### DiscoveryWindow

自动跨集发现的默认触发条件：

- 自上一批素材快照以来至少 6 项 MaterialChange；
- 来自至少 2 个 Episode；
- 最后一次学习处理完成后防抖 30 分钟；
- 每个画像每天最多自动一批；
- 存在 Ready 或 Reviewing 的未处理批次时暂停自动生成。

MaterialChange 指发现价值的实质变化，例如新 KeyPoint 达到 Ready、NeedsReview 提升为 Ready、Owner 实质编辑后确认或纳入画像。错别字、展示元数据和语义相同的重分析不算新变化。

### 批次质量

- 每个 ProposalBatch 以 5 条实质不同方向为目标，最多 10 条；
- 素材不足时允许少于 5 条，但必须解释原因和素材缺口；
- 禁止用同义标题或轻微改写填充数量；
- 新 KeyPoint 是发现种子，系统可语义召回历史 KeyPoint；
- 每个自动候选必须至少使用一项当前窗口的新价值；
- 纯历史重组属于 Owner 主动发起的 EvergreenExploration。

### 候选筛选

ProposalBatch 浏览层显示：

- WorkingTitle；
- 一句 ProposedClaim；
- 目标受众与表达价值；
- 推荐 CreationForm；
- 新旧素材数量；
- 与 CreationHistory 的关系；
- 素材成熟度和 ResearchNeed。

展开后显示素材关系、综合过程、相似内容差异、模型、费用和批次原因。Owner 可以接受、编辑后接受、保存以后考虑、带原因拒绝，或转入 IdeationSession 继续细化。

## DirectedIdeation

DirectedIdeation 使用持久 IdeationSession，而不是一次性表单：

1. Owner 提交 IdeationIntent；
2. 系统检索当前画像的 Relevant 与 Adjacent 素材；
3. MaterialDiagnosis 区分支持、反驳、补充和缺口；
4. AI 提出多个 ProposedClaim；
5. Owner 可以编辑主张、增删材料、缩小范围、坚持立场或放弃；
6. Owner 明确提升一个方向时才形成 CreationProposal。

Owner 可以在会话内限定 Episode、Podcast、Theme、时间范围和具体 KeyPoint。每次范围变化都重新诊断，但不修改画像长期规则。AutomaticDiscovery 中接近但不成熟的候选可以转入 IdeationSession。

## 主张、研究与 Brief

### AI 提议，Owner 承担

ProposedClaim 是 AI 基于 WritableMaterial 提出的候选观点，不属于 Source，也尚不属于 Owner。Owner 接受或编辑 CreationProposal 时，它才转为 OwnerClaim。

OwnerClaim 可以不被 Source 直接支持，但不得伪装成来源观点；其中可外部验证的事实和数字仍需单独核验。Writer 不得自行创造 Brief 中不存在的新 OwnerClaim。

### ResearchNeed

素材不足不等于方向无价值：

- Owner 可以接受核心主张，让 CreationProposal 进入 Researching；
- BlockingResearchNeed 未解决前不能形成 CreationBrief；
- EnhancementNeed 只表示补充后更好，不必阻断；
- Owner 可以补充 Source、补选已有材料、缩小主张、改为明确归因或放弃。

未来联网研究必须先形成 ResearchPlan，并经 Owner 授权。采用的研究结果必须保存为 Source，再经过 LearningWorkspace 形成 KeyPoint；临时搜索摘要不能直接进入作品。V1 只支持 Owner 手动导入 Source。

### CreationBrief

Owner 接受素材充足的 CreationProposal 后，系统自动整理 CreationBriefDraft。草案必须显示：

- OwnerClaim；
- 预计 SourceClaim、SynthesisClaim 和 VerifiedFact；
- 入选与淘汰素材；
- 支持、反驳、补充和冲突；
- ResearchNeed；
- 结构、篇幅与风格；
- RightsConstraint 风险。

生成草案不等于写作授权。只有 Owner 确认 CreationBrief 后，系统才可以生成作品。

## 作品与 ClaimReview

### CreationForm

CreationProposal 不预先绑定渠道或长文章。Owner 接受时选择 CreationForm，例如 Article、ShortCommentary、PostSeries 或 Script。同一方向可以派生多个作品形态，并保留共同的 CreationProposal。

V1 只实现 Article，其他形态只保留领域扩展点。

### ClaimMap

每个作品修订中的实质性表达必须标明身份：

- `SourceClaim`：来源表达过的观点，必须关联 KeyPoint；
- `OwnerClaim`：Owner 明确承担的判断；
- `SynthesisClaim`：基于多项素材形成的比较或推论；
- `VerifiedFact`：已通过适当 Source 完成核验的事实、数字或事件。

### ClaimReview

ClaimReview 独立检查：

- 主张类型是否正确；
- SourceClaim 是否准确归因；
- OwnerClaim 是否经过 Owner 确认；
- SynthesisClaim 是否伪装成来源原意；
- VerifiedFact 是否完成核验；
- BlockingResearchNeed 是否仍未解决；
- Writer 是否加入 Brief 外的新主张；
- 直接引语和媒体资产是否符合 RightsConstraint。

StyleReview 继续独立检查受众、结构、节奏、篇幅和画像风格。

## 去重与创作历史

CreationProposal 的稳定身份是核心主张，不是标题。WorkingTitle 和 AlternativeTitles 只是展示建议；同一主张的标题变体不能占据多个批次名额。

HardDuplicate 综合比较：

- 核心主张；
- 目标受众；
- 主要 WritableMaterial；
- 作品承诺；
- 历史作品内容。

CreationHistory 收录 CloudWisePod 内外部的 PublishedWork 与 UnpublishedWork。完整正文可以参与高置信度硬排除；只有摘要或标题时只做低置信度提醒。FollowUp 必须关联具体历史作品并说明新素材、反方视角、不同受众或更深分支带来的增量价值。

拒绝 CreationProposal 时使用轻量结构化 EditorialFeedback：HardDuplicate、WeakClaim、PoorFit、InsufficientMaterial、WrongAngle、NotNow 或 Other。不同原因影响后续去重和再发现，但不得静默修改 EditorialProfile。

## 素材、权利与模型数据

Owner 纳入 CloudWisePod 的所有 Source 默认可以参与学习与创作。不再维护 EditorialProfile 级 SourceScope 或 Source 级 ContentUsePolicy。

保留两个不同边界：

- **RightsConstraint**：约束 PublicationPackage 中长篇逐字复制、原音、音乐、图片和其他具体表达或媒体资产；不限制思想、观点、总结、评论和综合；
- **ModelDataPolicy**：默认可发送给 Owner 配置的 Provider，只有特殊 Source 才限制为 ConfiguredProviders 或 LocalOnly。混合素材没有共同 Provider 时必须拆分候选或明确提示。

## AttentionQueue

首页不罗列所有对象，而按学习与创作两条泳道显示需要 Owner 注意的下一步。

### 学习泳道

- Source 处理失败；
- NeedsReview KeyPoint；
- 未完成学习会话；
- ResearchNeed 和 Researching 方向；
- 最近形成的新学习成果。

### 创作泳道

- 待筛选 ProposalBatch；
- 正在细化的 IdeationSession；
- 待确认 CreationBriefDraft；
- 等待 ClaimReview 或 Owner 修改的作品；
- Stale 对象；
- 可生成 PublicationPackage 的作品。

失败、Stale 和预算阻断不能静默隐藏。

## 不可变成果与恢复

以下成果属于不可再生核心数据，必须备份并可恢复：

- SourceNote、OwnerReflection、Owner 编辑的 KeyPoint；
- EditorialRelevance 的 Owner 覆盖；
- ProposalBatch 及素材快照；
- IdeationSession、MaterialDiagnosis、ProposedClaim 与 OwnerClaim；
- CreationProposal、EditorialFeedback、ResearchNeed 与 ResearchPlan；
- CreationBrief 及 Owner 确认；
- 全部作品修订、ClaimMap、ClaimReview 与 StyleReview；
- CreationHistory、PublishedWork 和 PublicationPackage；
- 模型、Prompt、费用、重试和故障切换来历。

## V1 验收旅程

1. Owner 使用默认 EditorialProfile，配置模型、预算并开启 AutomaticDiscovery。
2. 新 Episode 自动形成 Transcript、Summary、Highlight、MaterialCandidate 与 KeyPoint；NeedsReview 可见。
3. SourceNote 与 OwnerReflection 在 LearningWorkspace 中清晰区分。
4. 至少 6 项新 MaterialChange、来自至少 2 个 Episode，经过防抖后形成 ProposalBatch；Theme 不是生成前置条件。
5. 批次提供 3–10 条实质不同候选，显示 ProposedClaim、素材、历史差异与缺口；同义变体不占名额。
6. Owner 将一个自动候选转入 IdeationSession 细化；另从自己的 IdeationIntent 开始一个 DirectedIdeation。
7. Owner 接受一个 CreationProposal，ProposedClaim 转为 OwnerClaim；若有阻断缺口则进入 Researching，补充 Source 后解除。
8. 系统自动生成 CreationBriefDraft；Owner 审阅 Claim、材料、冲突、ResearchNeed、结构和权利风险后确认。
9. Writer 生成 Article 修订，ClaimReview 与 StyleReview 完成；Owner 修改后产生不可变新修订并重新审校。
10. 通过门禁的修订生成 Minimal、Standard 或 Detailed 来源密度的 PublicationPackage，由 Owner 手工发布。
11. 导入一篇 CloudWisePod 外部历史作品，验证 HardDuplicate 与 FollowUp。
12. 备份并在全新实例恢复学习成果、发现批次、构思会话、主张、Brief、修订、审校与创作历史。

## 明确不做

- 自动生成作品库存、自动发布或自动群发；
- 把来源忠实描述为客观事实已验证；
- 让模型自身知识静默成为作品事实；
- 用标题变体填充 ProposalBatch；
- 在未处理批次存在时继续堆积自动候选；
- 要求 Owner 为每个 Source 或 EditorialProfile 逐项授权创作素材；
- 把 Theme 恢复成 AutomaticDiscovery 的审批门禁；
- 静默修改 EditorialProfile、OwnerClaim、作品修订或已发布历史；
- V1 中实现 Article 之外的 CreationForm、联网 ResearchPlan 执行、团队协作或自动发布。

## 文档优先级

如文档冲突，按以下顺序判断：

1. 本文与 [`CONTEXT.md`](../CONTEXT.md)；
2. [ADR-0022](adr/0022-learning-creation-workspaces.md)；
3. 未被 ADR-0022 修订的历史 ADR；
4. [`implementation-roadmap.md`](implementation-roadmap.md)；
5. README 中对当前实现的说明。

ADR-0021 保留为从学习工具转向内容生产的历史决策；产品形态、发现模型、素材权限和主张审校以 ADR-0022 为准。
