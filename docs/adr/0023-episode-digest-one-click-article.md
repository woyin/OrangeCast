# ADR-0023：EpisodeDigest 单集精读文——一键成文、封闭集合、程序化门禁

状态：已确认
日期：2026-08-24
关联：修订 product-goal.md（"V1 只支持 Owner 手动导入 Source"与"明确不做"清单）；在 ADR-0018 信息分层与 ADR-0022 学习创作双空间之上新增第三条成文轨道；领域词汇见 CONTEXT.md「单集精读」。

## 背景

Owner 需求原话是"每一篇 transcribe 的 podcast 都可以一键洗稿成微信公众号、小红书文章"。"洗稿"（抹除来源、冒充原创）与产品宪法正面冲突（OwnerClaim 纪律、ClaimReview、"模型自身知识不能静默成为作品事实"）。经 grilling 会话逐层收敛，真实需求重新表述为：**从单集 Source 一键生成署名来源的精读文**——转述为主、标注式 AI 展开、织入 Owner 既有笔记、事实缺口经联网检索落源后引用；一键产出公众号长文版与小红书笔记版。

## 决策

### 1. 领域身份：学习侧衍生的成文产物，不是 CreationWorkspace 作品

EpisodeDigest（单集精读文）锚定单个 Source，与 KnowledgeCard、Narration 同层，但是 Derivative 中第一个可直接发布的形态。它不进入 CreationWorkspace 主张纪律：不需要 CreationProposal、OwnerClaim、Brief 确认与 ClaimReview。**正当性来源**：它不表达 Owner 自己的主张——正文是转述、标注展开、Owner 已写下的话与带出处的引用，四类内容在生成时由程序打标（DigestBlock），主张身份是构造时确定的，不是事后审校推断的。已发布的 EpisodeDigest 进入 CreationHistory，参与后续 HardDuplicate 去重——这是与创作空间的关键联动：Owner 日后在 CreationWorkspace 写同主题文章时，精读文会被去重系统看见。

被否决的替代：塞进 CreationWorkspace 当"快速通道作品"（Brief/ClaimReview 全得留空，领域语言被稀释）；独立第三 workspace（V1 只有一种产物，过度设计）。

### 2. 封闭集合：四类 DigestBlock，⑤ 类内容零例外

正文只允许四类构造单元，每类带程序赋予的类型身份：

| 块类型 | 身份 | 约束 |
|---|---|---|
| 转述 | CitedDerivative | 挂 Citation，可回溯 Segment |
| AI 展开 | GeneratedDerivative | 逐段标注"AI 展开"，**不含**具体数字/日期/事件/外部专名 |
| 笔记 | Owner 的话 | 来源限于已存在的 SourceNote / OwnerReflection / Annotation；AI 只许重排、不许改写 |
| 引用事实 | CitedDerivative | 挂指向已沉淀 Source（本集或检索落源）的 Citation |

AI 替 Owner 编造新观点（伪 OwnerClaim）永久禁止。模型内部知识永不作为事实来源，只能作为发现 FactGap 与生成检索意图的提示。

### 3. SourceSearch：修订宪法，检索结果必须落 Source

本 ADR 修订 product-goal.md 两处："未来联网研究必须先形成 ResearchPlan"与"V1 只支持 Owner 手动导入 Source"。修订范围严格限定于 EpisodeDigest 的 FactGap 消解通道：

- Writer 遇到事实缺口 → 生成检索意图 → 搜索（Groq `groq/compound`，复用现有 key 与计费审计）→ 选中结果经既有 `fetchWebDocument` SSRF 防护管道抓取 → **自动创建 Document Source** → 形成可引用依据 → 以引用事实块进入正文；
- 搜索摘要永不直接入文（宪法"临时搜索摘要不能直接进入作品"不放宽）；
- 检索落源在草稿侧栏逐条列出，Owner 发布前确认或剔除（剔除后对应段落降级或删除）。

CreationWorkspace 的 ResearchNeed/ResearchPlan 授权纪律不受影响。此修订的理由：Owner 明确选择 1+2+3+6 一起做（D3 混合体），而落源通道是唯一能让联网事实合法进入正文的路径——它把"AI 补事实"改写为"AI 发现事实缺口并自动取证"。

### 4. 程序化门禁替代 ClaimReview（G2）

门禁由代码强制，非 LLM 审校：

- 引用事实块必须挂有效 Citation；无 Citation 的硬事实块（数字/日期/专名启发式扫描）生成时即拒绝，转 FactGap；
- AI 展开块必须带标注，漏标 = 生成失败；
- 直接引语长度对照 RightsConstraint 上限，超限自动截断为转述；
- Owner 笔记块 AI 只许重排不许改写；
- 全部通过 → 草稿页（含 ⑥b 侧栏）→ Owner 读一遍点发布。

正当性：ClaimReview 防的是"Writer 偷偷加新主张"，封闭集合构造在结构上排除了它——写作器根本拿不到无标签的内容槽位。剩余的不变量（标注完整性、Citation 有效性、引语长度）是机械可验证的，代码比 LLM 审校可靠。

被否决的替代：G1 完整宪法门禁（一键变三键，前述决策白定）；G3 无门禁（信任 prompt 等于放弃不变量）。

### 5. 触发：手动单集 + 列表批量（T1）

Source 详情页"生成精读文"按钮；单集列表页批量勾选（复用现有批量入队交互）。不做转录完成自动触发——自动生成会为"只学不写"的集付费，且违背"作品是 Owner 选择的结果"的产品立场。批量逐集隔离失败（D1）：一集失败不影响其余，失败集可见、可单集重试，与 AttentionQueue"失败不能静默隐藏"一致。白名单播客自动生成留待真实需求出现（届时只是把按钮换成 cron，门禁与管道不变）。

### 6. 双渠道：长文版唯一事实源 + 受约束改写（F3）

公众号长文版是唯一事实源；小红书笔记版由 DigestRewrite 生成——改写输入只有已过门禁的长文 DigestBlock 集合，不得引入新事实，产物过同一套 G2 门禁加渠道格式约束（字数、话题 tag 数）。等价于"翻译"而非"再创作"。

被否决的替代：F1 机械渲染（小红书版像摘要卡片，无人转发的长文腔）；F2 两次独立生成（成本×2、两份事实表述、幂等复杂化）。

### 7. 成本与降级

- Writer / 搜索 / 改写全部计入现有月度预算池（editorial_costs），超支即停，不设精读文子预算；
- 搜索 Provider 用 Groq `groq/compound`：不新增 API key、不新增计费体系；
- 搜索不可用时**降级生成**（C1）：文章仍产出，硬事实段降级为标注展开或省略，侧栏标"联网补充不可用"，可稍后重试补齐——宁可少两段事实，不交白卷；
- 每次生成为不可变修订，沿用 ArtifactVersion 版本语义（仓库既有惯例：KnowledgeCard/Highlight/Narration 均版本化），重新生成不覆盖历史。

## 不变

- 四类 DigestBlock 之外的内容永不进入正文；AI 内部知识永不作为事实来源。
- 搜索摘要永不直接入文；检索落源必须可见、可剔除。
- CreationWorkspace 的 OwnerClaim → Brief 确认 → ClaimReview 纪律一条不动，本 ADR 不为其开快速通道。
- 默认零成本承诺不破：精读文全部计费走现有预算治理，手动触发即单次授权。
