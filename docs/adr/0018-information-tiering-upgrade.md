# ADR-0018：信息分层升级——从绝对证据库到分层学习平台

日期：2026-08-07  
状态：已确认  
关联：取代 ADR-0008 与 ADR-0016 中"所有 Q&A / 衍生内容统一挂 Citation"的隐含假设；ADR-0016 关于 Gist 的关联描述已被其修订记录正名，本文给出完整背景。

## 背景

升级前 CloudWisePod 定位为"绝对精准的学习资料来源"：所有衍生内容必须逐字可核验（ADR-0008），单 Source Q&A 必须挂 Citation、证据不足拒答。这一契约建立了产品差异化，但也封死了 Owner 的另一类核心诉求——"这段我没懂，帮我用别的话重讲一遍"。这类诉求本质要求 AI 脱离原文，与"逐字可核验"不可调和。强行用证据型能力承载它，必然导致要么拒答（Owner 觉得没用）、要么硬凑 Citation 伪装有据（信任崩塌）。

Owner 希望将产品升级为"助我学习的平台"，并明确：信息分两种——原始来源（episode 原内容）与 AI 总结/生成。

## 决策

引入两层信息模型，并为生成层设独立的弱关联与硬约束。

### 1. 信息分层（CONTEXT.md「信息分层」）

- **PrimarySource（原始来源）** = EvidenceAudio + Transcript。受保护的事实层，AI 不得改写。Transcript 虽由 AI 产生，但其社会契约是对音频的忠实文字镜像，因此归入原始来源。
- **Derivative（衍生内容）** 内部按对原文的忠实程度分两类，必须可被 Owner 一眼区分：
  - **CitedDerivative（带证衍生）**：挂 Citation，契约与 ADR-0008 一致，不放宽。覆盖 Summary/KeyPoint/Chapter/Quote/Highlight。
  - **GeneratedDerivative（生成衍生）**：明确标注"AI 生成、非原文"，允许脱离原文表述。

### 2. 独立关联类型 Reference

为 GeneratedDerivative 新增 **Reference（参考关系）**，与 Citation 物理与语义分离。Reference 不声称忠实、不要求逐字，但时间范围仍由程序从 Segment 解析（AI 不得自估，保证时间点真实存在）。**Reference 与 Citation 互斥**：一个 Derivative 的关联要么全是 Citation，要么全是 Reference，由其类别决定。

被否决的替代：复用 Citation 加 `kind` 字段。否决理由——`Citation` 已被 ADR-0008 锁死为"可逐字核验"，让它承担两种含义会把歧义渗进 schema、查询、UI、文档与 Owner 心智，而产品差异化正建立在"Citation 是真的"上。

### 3. Gist 正名

ADR-0016 引入的 Gist 定义为"非逐字、重新组织语言"，却挂 Citation，与"可核验"语义冲突。本次将其明确为 GeneratedDerivative，关联类型改为 Reference；Highlight 区间本身仍是 CitedDerivative，不变。详见 ADR-0016 修订记录。

### 4. 新增产物

- **Paraphrase（复述讲解）**：Owner 按需触发的局部重讲，GeneratedDerivative，挂 Reference。与 Gist 互补：Gist 主动全覆盖概括，Paraphrase 按需局部重讲。不做整集 Paraphrase，以免与 Summary 重叠。不纳入 ArtifactVersion——详见附录 R2（触发模式与 ProcessingJob 不同，按锚点保留最近 3 次而非版本化）。此条修正了本 ADR 初稿中 Paraphrase 纳入 ArtifactVersion 的判断：同构的是 AI 对内容的结构化产物，不同构的是触发模式，而触发模式决定持久化策略。
- **StudyChat（学习对话）**：围绕单一 Source 的多轮学习对话，GeneratedDerivative，挂 Reference。与 EvidenceQA 并存且不可混淆——查证用 EvidenceQA（拒答），学习用 StudyChat。
- **StudySession（学习会话）**：StudyChat 的会话容器与日志，按会话保存，不纳入 ArtifactVersion（多轮有状态、无版本比较价值），可由 Owner 整体删除。
- 现有单 Source Q&A 正名为 **EvidenceQA（证据问答）**，CitedDerivative，契约不变。

被否决的学习形态：跨 Source 整合型（c），因其直接撞 product-goal「明确不做：跨 Source RAG」红线，需单独立项，不混入本次升级。结构型（思维导图）、测试型（Quiz）列入未来，不进当前升级。

### 5. StudyChat 两条硬约束

防止 GeneratedDerivative 退化为通用幻觉聊天助手：

- **硬约束一（scope 缰绳）**：每轮回答必须挂至少一条指向当前 Source Segment 的 Reference，无 Reference 不生成，改提示 Owner 该问题已超出本集范围。与 EvidenceQA「无 Citation 拒答」形成对偶。
- **硬约束二（防虚挂）**：Reference 由生成 AI 自选，但生成后须经一次独立的相关性校验（只判相关、不判逐字忠实），校验失败则该回答不呈现，转而给 Owner 可见反馈（问题越界，非系统故障）。被否决的替代：信任 AI 自选（等于放弃约束）/ Owner 手动选段（牺牲交互、退化为多轮 Paraphrase）。

### 6. KnowledgeNote 分级标注

KnowledgeNote 的 Markdown 必须按类别结构化标注，随内容下沉：CitedDerivative 块标"原文依据"带 Citation 跳转（可逐字核验）；GeneratedDerivative 块标"AI 讲解·非原文"带 Reference 跳转（仅参考、不可核验）。两种块视觉可区分，Reference 跳转不得伪装成 Citation 跳转。此标注是把内部分级延伸到 PersonalKnowledgeBase 的最后一公里，避免 Owner 日后把 AI 讲解误当原话。

关于 StudyChat 下沉：禁止自动写入 KnowledgeNote；允许 Owner 手动选择精彩回答后下沉（标为 GeneratedDerivative 块）。

## 取舍

- **代价**：schema 增加 Reference 关系与 Paraphrase/StudySession 表；UI 需在多处区分两类内容；StudyChat 增加独立校验步骤的延迟与成本。
- **收益**：在不放宽 ADR-0008 硬契约的前提下，为 Owner 开出"有标记的生成内容"通道；保留证据库差异化，同时承载学习诉求；消除 Gist 既有语言债务；分级贯穿到外部知识库，不污染长期信任根基。

## 不变

- PrimarySource 仍不可改写；CitedDerivative 的 Citation 契约不放宽；单 Owner、自托管、默认零成本、不做跨 Source RAG 等边界全部不变。

---

## 附录：实现层约束 R1–R4（2026-08-07 第二轮 grilling 结论）

以上决策定义领域语言；以下四条约束规定领域语言如何落到 schema 与生成层，作为实施的硬约束。

### R1. Citation 与 Reference 在 schema 层用 relation_kind 显式区分

Citation 在现状中不是独立表，而是嵌在 keypoint_index.citations_json、annotations/pins/collections.segment_ids、KnowledgeCard payload 中的关系字段。升级后每个承载 Segment 关系的位置增加一列 relation_kind IN ('citation','reference')，显式标注语义，不靠宿主实体类型隐式区分。存量 Citation 数据回填 relation_kind='citation'，citations_json 字段本身不动（避免格式迁移）。写入时校验宿主实体类别与 relation_kind 配对正确（CitedDerivative 必须 citation，GeneratedDerivative 必须 reference）。

被否决的替代：复用 segment_ids 靠宿主类型隐式区分（互斥铁律将只活在文档里）；重建为一等关系表（大重构，推迟本次升级）。

### R2. Paraphrase 不纳入 ArtifactVersion，按锚点保留最近 3 次

Paraphrase 是高频、试错性的阅读时按需触发，与 ProcessingJob 低频有意的重新分析语义不同。若每次触发都生成不可变 ArtifactVersion，会出现版本爆炸、version 号膨胀、比较回退价值稀释。改为独立轻量表，按锚点保留最近 3 次：锚点 = 该次 Paraphrase 所挂 Reference 指向的 Segment 集合的稳定 key（排序后的 segment_ids 串），同锚点多次触发互相淘汰最旧，不同锚点独立保留。不版本化、不比较、不回退。

被否决的替代：每次触发一个 ArtifactVersion（爆炸）；引入采纳机制（认知负担甩给 Owner）；不持久化（丧失回看对比能力）。

### R3. StudyChat 用 ReferenceCheck 做主题锚定校验

硬约束二的独立校验步骤命名为 ReferenceCheck。输入三元组（Owner 问题 + AI 回答 + Reference 段文本），做二元判断——回答主题是否扎根于 Reference 段所讨论内容（延伸/解释/例化/重组），而非离开这些内容去谈别的。判据是主题锚定，不是逐字忠实：放行概念解释、类比例化、结构重组；挡住主题漂移、预测建议评价、措辞蹭原文式虚挂。校验器只判断、不生成。

模型选择：与生成 AI 同模型、独立 prompt，这是默认零成本约束下的妥协。标注为可替换点：若虚挂率上升，第一道干预是切换校验模型或引入第二判据，而非调 prompt。校验失败：回答不呈现 + 可见反馈（问题越界，非系统故障）。校验器的误杀与漏放纳入 EvalSet 作为同等公民接受评测。

被否决的替代：只看回答+Reference 缺问题上下文（误杀合法类比）；让生成 AI 自证参考理由（结构性不可信）；不同模型（违背默认零成本）。

### R4. KnowledgeNote Markdown 用双层 callout 标注分级

CitedDerivative 块：Obsidian callout 标注为原文依据（> [!quote] 原文），带 Citation 跳转（锚点文字“引用”，URL 用 ?t=）。GeneratedDerivative 块：自定义 callout 类型（> [!ai-generated] AI 讲解·非原文），带 Reference 跳转（锚点文字“参考”，URL 用 ?ref=，与 Citation 的 ?t= 区分）。callout 首行带文字前缀兜底，保证非 Obsidian 渲染器也能识别类别。Gist 正名后从 Chapter Citation 渲染中独立，作为 GeneratedDerivative callout 块置于对应 Chapter 区块之后，保持“区间真实在前、讲解自由在后”的视觉顺序。render.go 现有 Gist 蹭 Chapter Citation 链接的渲染（render.go 第 87-90 行）须相应修改。

被否决的替代：纯 callout 无前缀（非 Obsidian 渲染器退化丢标记）；纯加粗前缀无 callout（视觉区分不足，违背 CONTEXT.md 视觉可区分要求）。
