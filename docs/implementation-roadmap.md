# CloudWisePod 实施路线

基线：现有 Go 单 Owner、自托管证据与学习平台（旧 Phase 0–7 已完成）
目标：完成 [`product-goal.md`](product-goal.md) 定义的内容生产工作台，架构决策见 [ADR-0021](adr/0021-evidence-grounded-content-workbench.md)

## V1 验证冲刺：真实黄金旅程（当前唯一优先级）

状态：已确认（2026-08-14 方向拷问会话；取代本文件后续阶段的排序权威）

依据：截至落盘，生产闭环代码已齐备（迁移至 0023，Phase 8–13 及 Phase 15 的 Document Source 已实现），但真实实例中 320 个 Episode 候选、19 个旧管线 Transcript、0 KeyPoint、0 EditorialProfile、0 ArticleProposal——ADR-0021 转型的核心闭环从未被真实使用过。继续横向加功能等于在未验证的地基上加盖楼层。

### 冻结范围

- Phase 16（Translation/Speaker）、Phase 17（视觉资产/编辑日历）、Phase 18（发布反馈闭环）及一切新功能开发冻结。
- 不编写存量素材回填工具；已知缺口登记进旅程报告。
- 唯一允许的代码变更是交战规则定义的"硬阻塞最小修复"；另经 Owner 批准（方案 B，2026-08-14）：冲刺开始前的既红门禁修复——35 处导出符号注释、旅程关键路径测试（rss Filtered 摄取、provider Curator/Writer）、store/server 覆盖率缺口改为登记地板（见 KNOWN_GAP_FLOORS）。

### 唯一目标与验收口径（DoD）

- Owner 在本地真实实例上完整走一遍 `product-goal.md` 的 V1 黄金旅程全部 10 步。
- 发布验证到"富文本粘贴进微信公众号后台编辑器"为止；群发不是验收条件。
- 第 10 步备份→全新实例恢复全部编辑成果必须真实执行。
- 旅程报告与摩擦清单落盘 [`v1-golden-journey-run.md`](v1-golden-journey-run.md)。

### 交战规则

- 一口气走完；摩擦只记录不修复。记录四要素：所在步骤、期望、实际、Owner 本能反应。
- 仅硬阻塞允许最小修复后原地继续，不从头重来。硬阻塞指：产品完全无法表达必要操作（如无入口），或继续前进有数据损坏风险。
- "懒得用 UI、SQL 更快"算摩擦，不算硬阻塞；被迫直接改库推进即是硬阻塞信号。
- 允许用 UI 内任何手动方式绕过；绕过方式本身就是摩擦证据。

### 旅程参数（已确认）

- 素材：Owner 内容上熟悉且仍在更新的一个播客；新处理 3–5 集（批量入队 + Filtered 或 AllNew IngestionPolicy）。
- 19 个旧管线 Source 不回填、不重新处理；"存量已处理 Source 无 KeyPoint 回填路径"作为已知缺口登记。
- 预算：EditorialProfile 内显式设置单篇与月度上限，量级"肉疼但不打断旅程"；确保预算治理被真实行使，包括超限"暂停并请求 Owner 决策"路径被触发或至少被检验。

### 摩擦清单排序（下一版本唯一需求来源）

1. 第一层按步骤权重：越靠近发布出口（Scout / Brief / 写作审校 / 发布包）的阻断性摩擦，优先级越高。
2. 第二层按杠杆：修复所解锁的步骤数 × 复现频率。
3. Phase 16/17/18 维持冻结；仅"不解决则旅程无法重复进行"的单点例外可由 Owner 显式批准（是例外解冻，不是整体解冻）。
4. 排序结果与本文冲突时，以旅程报告为准，并回写更新本文。

## 执行原则

- 每个阶段结束时应用必须可构建、可启动、可从上一版本数据安全升级。
- 先建立数据关系、版本和恢复能力，再开放自动付费任务或批量生成。
- Embedding/RAG 只负责候选召回，证据必须落到 KeyPoint、Citation 与 PrimarySource。
- AI 与人工修改一律产生不可变 ArticleRevision；硬性审校结果只属于确切 Revision。
- SourceScope、ModelDataPolicy 和预算在任务入队前校验，不能依赖 Prompt 约束。
- 新功能维持 ADR-0020 的测试与 lint 门禁；涉及迁移、任务恢复、备份和文件生命周期时使用真实 SQLite 与临时目录集成测试。
- 现有学习功能不删除，但除非直接服务素材研究，不阻塞内容生产里程碑。

## 已完成基线

旧路线 Phase 0–7 已完成：Go 单实现、可升级 SQLite、单 Owner 与公网安全、持久 EvidenceAudio、SQLite durable jobs、不可变 Transcript/KnowledgeCard、稳定 Segment/Citation、分段 FTS、Markdown KnowledgeNote、备份恢复和严格单 Source EvidenceQA。真实一小时播客黄金旅程已于 2026-08-02 验证。

这组能力成为新工作台的证据底座，不再是最终产品闭环。

## V1：播客到公众号

### Phase 8：内容生产迁移与权限底座

目标：在不破坏现有 Source 和 ArtifactVersion 的前提下，引入内容生产的稳定身份、权限和版本边界。

- 为 EditorialProfile、SourceScope、ModelDataPolicy、ArticleProposal、ArticleBrief、ArticleDraft、ArticleRevision、EvidenceMap、审校结果与 EditorialFeedback 建立迁移。
- ArticleRevision 不复用通用 ArtifactVersion：前者是持续编辑历史，后者是 ProcessingJob 产物历史。
- Source 增加生产用途与 Archive 状态；实现使用关系检查和证据失效标记。
- 更新 Purge：Draft 使用时显示影响，删除后相关 Revision 标记证据失效；PublishedArticle 的保护在 V1.2 随发布记录落地。
- 扩展备份 manifest，覆盖新表、Revision、EvidenceMap、审校结果与后续选定资产。

退出条件：现有数据库无损升级；新对象可创建、版本化、备份和恢复；权限或证据失效不能被静默忽略。

### Phase 9：自动摄取与 KeyPoint 素材 Inbox

目标：让内容工作台持续获得可整理、可追溯的播客素材。

- Podcast 支持 Manual、AllNew、Filtered IngestionPolicy，以及 Podcast 级和全局预算。
- 自动流程只入队 EvidenceAudio、Transcript、KnowledgeCard 和 KeyPoint；失败进入可见队列。
- 将 keypoint_index 从只读展示投影演进为具有稳定生产身份的素材层，同时保留 ArtifactVersion 真理来源与派生关系。
- 增加 Inbox、Shortlisted、Used、Dismissed 状态、批量操作和按 Episode/Podcast/时间/状态筛选。
- 支持从 Transcript Segment 手工创建 KeyPoint、从自动 KeyPoint 派生人工编辑版，以及 PrimarySource 变化后的证据重映射/待修复状态。
- 显示 KeyPoint 参与过的 Proposal、Brief 和 Draft；PublishedArticle 关系在 V1.2 补齐。

退出条件：新 Episode 可按策略自动形成 KeyPoint；Owner 能在统一 Inbox 完成筛选和人工补充，重新分析不会覆盖人工成果。

### Phase 10：Embedding、Theme 与 Scout

目标：从跨 Episode 素材中发现可写且不重复的选题。

- 为 KeyPoint 建立可重建的 Embedding 索引；Provider、模型、维度和索引版本可追踪。
- 语义检索与关键词检索混合召回，只返回具有有效 Citation 且符合 SourceScope/ModelDataPolicy 的候选。
- 实现 Theme 建议、Owner 确认/改名/合并/拆分/忽略及多对多 KeyPoint 关系。
- Theme 页面展示来源分布、新增趋势、观点支持/补充/冲突、已用素材和内容空白。
- Scout 按 EditorialProfile 生成 Fresh、Evergreen、Follow-up 和显式 DeepRead ArticleProposal，记录候选素材、目标读者、核心论点、写作价值，并对历史提案做重复检查；跨 Episode 模式要求每条候选覆盖至少两个 Episode，DeepRead 必须由 Owner 选择单个 Episode；每次头脑风暴严格返回 5 条。PublishedArticle 去重与 Follow-up 的更深关联在 V1.2 继续完善。
- 提案池低于 5 条时，在提案接受/搁置/拒绝后后台自动补货；普通工作台 GET 不触发付费调用，且保留手动补货入口。
- 建立角色路由基础：首选/备用 Provider、超时、重试、预算、实际费用与 Prompt 版本；提供经济/均衡/质量优先预设。

退出条件：Scout 能从多个 Episode 产生带真实来源、通过权限过滤且不与历史提案明显重复的候选选题；Owner 也能在明确选择一个 Episode 后生成单集 DeepRead 候选，提案池能在离开池后恢复到 5 条目标。

### Phase 11：Curator 与 ArticleBrief

目标：在生成全文前，用低成本、可审核的方式锁定选材与论证结构。

- Owner 可接受、暂存、拒绝或合并 Proposal，并记录结构化 EditorialFeedback。
- Curator 生成 ArticleBrief：论点、读者、入选/淘汰 KeyPoint、各节素材、篇幅和风格。
- 识别 KeyPoint 间的支持、补充和冲突；冲突必须由 Owner 选择并列、站队、缩小论点或淘汰，不能静默消失。
- Brief 编辑产生历史，只有 Owner 显式确认后才授权单篇写作流水线与预算。
- 执行前计算所有素材的 SourceScope 与 ModelDataPolicy，混合任务采用最严格策略。

退出条件：Owner 能在不生成全文的情况下看清文章将写什么、用什么、舍弃什么，以及如何处理冲突。

### Phase 12：多角色写作、EvidenceMap 与质量门禁

目标：产出可追溯、可审校、不可覆盖的文章版本。

- Writer 按已确认 Brief 生成初稿及逐项 EvidenceMap，区分 Quoted、Paraphrased、Synthesized、Rhetorical；拒绝 ExternalFact。
- EvidenceReviewer 独立检查素材支撑、直接引语、来源归因、Citation 有效性、冲突处理与“有出处不等于已事实验证”。
- StyleEditor 独立检查 EditorialProfile、标题、结构、节奏、重复、篇幅和禁用表达。
- Writer 根据两类审校意见修订；证据硬错误未解决时停留在待处理，不能无限自动重试掩盖问题。
- AI 初稿、审校修订、Owner 手改和局部 AI 操作都创建 ArticleRevision；支持比较、选择当前版本与回退。
- 修改后按 diff 增量重审；任何当前 Revision 的硬性审校失效都会阻止生成 PublicationPackage。
- 建立写作 EvalSet：无依据事实、错误归因、虚假译引、歪曲反方、综合冒充原意、软风格偏差。

退出条件：无法把含硬证据错误的 Revision 标记为可交付；历史 Revision 可比较回退，审校与模型/Prompt/费用来历完整。

### Phase 13：编辑器、发布内容包与个人看板

目标：把已审校文章可靠交付到微信公众号后台。

- Markdown 作为正文规范格式，提供公众号兼容实时预览。
- 支持局部精简、展开、换语气、改标题和检查依据；每次操作创建 Revision。
- 证据与审校警告显示在旁栏，不污染正文。
- PublicationPackage 输出公众号富文本、Markdown、纯文本、候选标题、摘要、推荐语和封面文案。
- 对外来源按画像提供轻量/标准/严格密度；直接引语强制署名，文末列出实际 Source。
- 提供按生产阶段组织的个人工作台：1 选题池、2 Brief 审核、3 写作与审校；画像、SourceScope、模型价格和手动兜底进入设置区，空材料 Brief 不得授权写作。
- 完成一键复制与文件导出；不接微信 API。

退出条件：Owner 能完成 [`product-goal.md`](product-goal.md) 的 V1 黄金旅程，并把通过证据门禁的文章粘贴到微信公众号编辑器。

### Phase 14：V1 恢复与发布验证

目标：证明内容生产成果不会因升级、重启、Provider 故障或迁移而丢失。

- 更新 backup/restore E2E，验证画像、KeyPoint 人工编辑、Theme 人工关系、Proposal、Brief、全部 Revision、EvidenceMap、审校和费用记录。
- 验证恢复后不连接原 AI Provider 也能浏览、编辑、回退和导出历史文章。
- 对自动摄取、提案扫描、写作与审校任务执行进程中断恢复和幂等测试。
- 执行真实跨 Episode 播客到公众号黄金旅程，并人工检查证据映射和粘贴排版。

退出条件：V1 数据可跨全新实例恢复；任务重试不重复扣费或创建歧义版本；真实文章完成发布前人工验收。

## V1.1：多来源与视觉

### Phase 15：Document 成为一等 Source

- 接入网页 URL、PDF 与粘贴文本，保存不可变 EvidenceDocument 快照。
- 为文档建立稳定位置的 Segment、Citation、KnowledgeCard、KeyPoint、全文搜索与版本历史。
- 网页抓取复用 SSRF、重定向、体积、超时与内容类型安全策略。
- 抽象音频/文档共享的 PrimarySource 与 Segment 行为，不强迫 Document 伪装成 Transcript。

### Phase 16：Translation 与 Speaker 归因

- 原语言 PrimarySource 永不被译文覆盖；Translation 按 Segment 对齐并独立版本化。
- KeyPoint 使用画像目标语言但引用原文 Segment；译引显示原文/译文并标“译”。
- 音频生成稳定 Speaker A/B 标识，允许建议实名映射和 Owner 确认。
- 未确认 Speaker 禁止实名归因；EvidenceReviewer 增加翻译忠实度与实名归因检查。

### Phase 17：视觉资产与编辑日历

- ImageCreator 生成多个封面方案、正文示意图与配图位置建议，保留模型和 Prompt。
- 不抓取网络图片；数据图只接受可追溯结构化数据。
- Owner 选定资产进入不可再生备份，未采用候选可清理或重建。
- 增加目标发布日期、优先级、画像节奏、个人编辑日历与内容缺口提示。

## V1.2：发布反馈闭环

### Phase 18：发布记录、表现与 Follow-up

- PublishedArticle 绑定确切 ArticleRevision，记录渠道、时间和外部链接。
- PublishedArticle 落地后启用 Source 默认 Purge 保护、KeyPoint 使用关系与提案重复检查；Owner 强制删除时只保留证据已删除的审计事实。
- 首版手工录入阅读、点赞、在看、分享、收藏和关注增长等 PublicationPerformance。
- 以长期模式而非单篇爆款分析主题、结构、篇幅和标题表现。
- Scout 生成 Follow-up 提案，寻找新证据、反方观点和可延展分支。
- 系统可根据 EditorialFeedback 与 PublicationPerformance 提议画像变更，但必须由 Owner 确认。

## 后续候选

- 其他音视频 Source，复用 EvidenceAudio/Transcript 流程。
- 自主联网研究与独立 FactCheck；检索结果必须先保存为 Source 才能进入文章。
- 微信公众号草稿箱 API 与发布指标自动同步，仍不自动群发。
- 从已审校 ArticleRevision 派生短帖、视频脚本、邮件简报等跨渠道 PublicationPackage。
- 数据规模证明 SQLite 向量方案不足后，再评估独立向量数据库；不提前引入运维负担。

## 每阶段通用验证

```bash
go test ./...
go vet ./...
go build ./cmd/cloudwisepod
make cover-gate
make lint
git diff --check
```

涉及模型质量的阶段必须同时运行对应 EvalSet；涉及付费任务时必须验证预算、幂等和实际费用记录；涉及 SourceScope 或 ModelDataPolicy 时必须包含拒绝路径与故障切换测试。
