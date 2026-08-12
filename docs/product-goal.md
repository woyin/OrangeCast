# CloudWisePod 产品目标

状态：已确认（2026-08-12 内容生产转型，见 ADR-0021）
日期：2026-08-01 / 修订：2026-08-12

## 一句话目标

CloudWisePod 是单 Owner、自托管、以可核验证据为底座的播客内容生产工作台：它把播客与后续扩展的文档等 Source 转化为可追溯的 KeyPoint，主动发现选题，协助 Owner 完成选材、写作、审校、修订与公众号发布内容包，并确保文章中的引用、转述与 AI 综合始终可区分、可回到 PrimarySource。

## 要解决的问题

播客转写和单集摘要本身不是终点。真正的浪费是：已经支付了下载、转写和分析成本，却让观点继续困在单集页面中，不能跨 Source 组合成稳定、可审核、可持续发布的内容。

CloudWisePod 的核心价值是缩短从“素材进入”到“可发布文章”的路径，同时保留三条信任边界：

- Citation 证明来源表达过什么，不把有出处误称为客观事实已验证。
- Writer 可以比较、重组和推论，但 GeneratedDerivative 不得伪装成来源原意。
- Owner 始终控制选题、Brief、最终版本与发布动作。

## 核心闭环

```text
Episode / Upload / Document
            ↓ 摄取与证据化
       PrimarySource
            ↓ 分析
          KeyPoint
            ↓ 语义组织
           Theme
            ↓ Scout
     ArticleProposal
            ↓ Owner 选择
       ArticleBrief
            ↓ Owner 确认
 Writer → EvidenceReviewer → StyleEditor → Writer 修订
            ↓
 ArticleDraft + immutable ArticleRevision + EvidenceMap
            ↓ 硬性证据门禁
     PublicationPackage
            ↓ Owner 在外部渠道发布
      PublishedArticle
            ↓
EditorialFeedback + PublicationPerformance
```

## 产品层级

1. **内容生产工作台是主产品**：素材 Inbox、Theme、提案、Brief、写作、审校、版本、公众号预览、看板和发布记录构成主导航与研发优先级。
2. **EvidenceArchive 是可信底座**：EvidenceAudio、EvidenceDocument、Transcript、Segment、Citation 和不可变处理产物继续提供核验与恢复能力。
3. **学习能力是次级研究区**：Highlight、DJ、Narration、EvidenceQA、StudyChat、Paraphrase、KnowledgeNote、Annotation、Pin 和 Collection 保留，但不再主导产品路线；知识图谱只有在帮助选题或选材时才提升为主入口。

## 内容生产原则

### KeyPoint 是选材原子

- 跨 Source 浏览、Theme 组织、语义检索、提案和 Brief 都以 KeyPoint 为核心。
- Summary 只提供整个 Source 的背景；Highlight 只负责音频回听。
- KeyPoint 具有 Inbox、Shortlisted、Used、Dismissed 生产状态，并显示关联过的提案、草稿和已发布文章。
- Owner 可以从 Segment 手工创建 KeyPoint，或从自动 KeyPoint 派生人工编辑版；重新分析不能覆盖人工成果。

### 系统主动提案，Owner 控制成稿

- Scout 按 EditorialProfile 的节奏生成 Fresh、Evergreen 和 Follow-up 提案，并检查历史提案与 PublishedArticle，避免重复。
- Owner 选择或调整 ArticleProposal 后，Curator 才生成 ArticleBrief。
- Brief 明确目标读者、核心论点、入选与淘汰素材、文章结构、篇幅、风格，以及材料之间的支持、补充和冲突关系。
- 只有 Owner 确认 Brief 后，系统才获得该篇文章的写作与审校授权。
- CloudWisePod 不绕过 Owner 自动生成发布库存，也不自动发布。

### 角色与模型分离

- Scout、Curator、Writer、EvidenceReviewer、StyleEditor、Translator 和 ImageCreator 是稳定职责，各自通过角色路由选择 Provider 与模型。
- 免费不再是产品硬约束；付费模型可以用于任何阶段。
- 提案任务可在全局与画像预算内自主付费；确认 Brief 授权该篇写作流水线，超过单篇上限才暂停并再次请求 Owner 决策。
- 每次生成记录实际 Provider、模型、Prompt 版本、费用、重试和故障切换。
- Provider 切换只按 Owner 配置的路由发生，不得绕过 ModelDataPolicy。

### 证据门禁优先于文风

- Writer 初稿必须经过独立 EvidenceReviewer 与 StyleEditor，再由 Writer 修订。
- EvidenceMap 将文章表达分为 Quoted、Paraphrased、Synthesized 和 Rhetorical；首版禁止无 Source 依据的 ExternalFact。
- 无依据事实、错误归因、错误译引、失真直接引语、歪曲冲突材料或失效 Citation 是硬错误，阻止生成 PublicationPackage。
- 标题、节奏、篇幅和轻微风格偏差是软建议，Owner 可以覆盖。
- 审校状态只属于接受检查的确切 ArticleRevision；任何 AI 或人工修改产生新 Revision，并至少重新检查受影响内容。
- Citation 只证明来源表达过，不证明其客观真实；未经独立核验的数字、预测和争议判断必须保留归因。

### 编辑成果不可覆盖

- ArticleDraft 是持续编辑对象，ArticleRevision 是不可变快照。
- AI 初稿、审校修订、手工修改与局部 AI 改写全部产生 Revision，可比较和回退。
- PublishedArticle 指向发布时采用的确切 Revision；后续修改不能覆盖发布历史。
- 所有人工判断、文章版本、EvidenceMap、审校结果和 Owner 选定的视觉素材都属于不可再生核心数据，必须备份。

## 素材与品牌边界

- 一个实例仍只有一个 Owner，但可以维护多个 EditorialProfile。
- 每个画像定义读者、主题边界、风格、篇幅、标题习惯、禁用表达、参考范文、来源署名密度、角色模型覆盖和预算。
- 每个画像通过 SourceScope 显式授权可用于生产的 Podcast、Source 类型、Theme、语言与时间范围；新 Source 默认不进入所有画像。
- “可用于对外内容”和“可发送给哪些模型”是两套独立约束。ModelDataPolicy 支持 ExternalAllowed、ApprovedProvidersOnly 和 LocalOnly，混合任务继承最严格策略。
- EditorialFeedback 是显式、可解释的学习信号。系统可以建议修改画像，但不能静默改变品牌定位。

## 多来源演进

- V1 以现有播客证据为唯一事实材料。
- V1.1 将网页 URL、PDF 和粘贴文本作为一等 Document Source，保存 EvidenceDocument 快照并形成可引用 Segment 与 KeyPoint。
- 其他音视频随后复用音频证据流程；自主联网研究最后引入。
- 所有新增素材必须显式成为 Source，保存来源类型与证据链；模型自身知识不得偷偷进入文章。
- 外语 PrimarySource 保留原文；Translation 按 Segment 对齐、独立版本化，译引明确标“译”。未确认 Speaker 身份时禁止 AI 实名归因。

## 发布边界

- 首版以 Markdown 为规范正文，提供公众号兼容实时预览和一键复制/导出。
- PublicationPackage 包含富文本、Markdown、纯文本、候选标题、摘要、推荐语、封面文案与视觉素材。
- 内部逐项证据链始终完整；外部来源呈现按画像选择轻量、标准或严格，但直接引语必须署名，文末必须列出实际使用的 Source。
- 可以生成封面和正文示意图，不自动搬运网络图片；数据图必须来自可追溯数据。
- 首版不接微信公众号草稿箱 API，不自动群发；Owner 在微信后台完成最终预览与发布。
- 个人看板与编辑日历管理生产节奏，但不引入团队协作、角色权限或多人审批。

## 自动摄取

- 每个 Podcast 可使用 Manual、AllNew 或 Filtered IngestionPolicy。
- 自动任务受 Podcast 级和全局预算约束，只生成内容生产必需的 Transcript、KnowledgeCard 与 KeyPoint。
- Highlight、Narration、StudyChat 等消费和学习产物仍按需生成。
- 自动处理失败进入可见待处理队列，不静默丢失。

## V1 黄金旅程：播客到公众号

在已有数据可无损升级的全新版本中完成以下流程，即视为 V1 核心目标达成：

1. Owner 创建一个 EditorialProfile，配置 SourceScope、角色模型、预算与提案节奏。
2. 为一个 Podcast 设置 Filtered 或 AllNew，新 Episode 自动形成 Transcript、KnowledgeCard 与 KeyPoint。
3. Owner 在统一素材 Inbox 中按 Episode、Podcast、Theme、状态和语义搜索浏览 KeyPoint，并手工补充一个遗漏观点。
4. Scout 基于多个 Episode 产生不重复的 ArticleProposal，明确出处和写作价值。
5. Owner 接受提案；Curator 生成 ArticleBrief，显示入选/淘汰材料与冲突关系。
6. Owner 确认 Brief；Writer、EvidenceReviewer、StyleEditor 和 Writer 修订流水线生成可交付 ArticleRevision。
7. Owner 在 Markdown 编辑器中局部修改；系统创建新 Revision 并增量重审。
8. 当前 Revision 通过硬性证据门禁，生成带来源列表的公众号 PublicationPackage。
9. Owner 一键复制富文本到公众号后台；CloudWisePod 不自动发布。
10. 备份后在全新实例恢复 Source、KeyPoint、画像、提案、Brief、全部 Revision、审校结果和选定视觉素材。

## 分期范围

- **V1：播客到公众号**——自动摄取、KeyPoint Inbox、Embedding/语义检索、Theme、EditorialProfile、Proposal → Brief → 多角色写作审校 → Revision、Markdown 编辑、公众号预览/导出、个人看板。
- **V1.1：多来源与视觉**——Document Source、EvidenceDocument、Translation、Speaker 确认、生成封面/正文配图、编辑日历。
- **V1.2：反馈闭环**——PublishedArticle、手工 PublicationPerformance、Follow-up 提案和画像调整建议。
- **后续**——其他音视频、自主联网研究、FactCheck、微信草稿箱 API、指标自动同步与跨渠道改编。

## 继续明确不做

- 多用户、租户隔离、团队协作、SaaS、支付或套餐。
- 绕过 Owner 确认 Brief 直接批量生成成稿，或自动发布/群发。
- 把 Citation 描述为客观事实验证，或把 Synthesized 推论伪装成来源原意。
- 使用模型自身知识静默补充事实，或让未入库的联网结果进入文章。
- 自动抓取网络图片并假定拥有版权。
- 因 Provider 故障切换而绕过预算、SourceScope 或 ModelDataPolicy。
- 覆盖 ArticleRevision、人工 KeyPoint、EditorialFeedback 或已发布版本。
- 在 V1 中接微信公众号 API、团队审批流或完整复刻 Podwise。

## 被本次转型废止的旧边界

- “CloudWisePod 的主定位是播客学习平台”。
- “默认零 AI 成本是产品硬约束”。
- “新 Episode 只能由 Owner 逐集手动处理”。
- “不做 Embedding、语义搜索或跨 Source RAG”。
- “CloudWisePod 不是内容生产工具”。

## 文档优先级

如文档之间冲突，按以下顺序判断：

1. 本文与 [`CONTEXT.md`](../CONTEXT.md)。
2. [`docs/adr/`](adr/) 中未被取代的决策；产品转型冲突以 ADR-0021 为准。
3. [`implementation-roadmap.md`](implementation-roadmap.md)。
4. [`README.md`](../README.md) 中对当前已实现版本的说明。

Cloudflare/TypeScript 时代的设计与计划只作为历史资料；README 描述当前实现，不得反向限制已确认的下一阶段产品方向。
