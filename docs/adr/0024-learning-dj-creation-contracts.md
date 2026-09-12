# ADR-0024：学习与创作并重的交付契约——处理深度、串场解说、来源快照与兼容连接

状态：已确认
日期：2026-09-12
关联：落实[个人播客方向建议](../research/2026-09-12-personal-podcast-direction.md)与[原子提交实施计划](../superpowers/plans/2026-09-12-learning-dj-creation-atomic-plan.md)；修订 ADR-0019 的解说词范围与"自动摄取止于重点"的既有默认；在 ADR-0022 双工作空间与 ADR-0023 单集精读之上统一交付与迁移契约。

## 背景

2026-08 的实现把"学习"收敛为内容生产的前处理：自动摄取在分析完成后即停止，不生成 Highlight 与 Narration（`TestPipeline_AutomatedIngestionStopsAfterKeyPoints`）；Narration 只合成 Highlight.Gist；DJ 只属于手动处理路径。与此同时，单集精读文（EpisodeDigest）与跨集个人文章各有入口，但学习成果到成文的衔接、生成任务的恢复与可回溯性未经真实旅程验证。

用户已确认产品定位为**学习与创作并重**：听完沉淀知识，也能继续生成文章。这要求四项契约修订：

1. 学习（读重点、DJ 精听、个人笔记）、单集精读、个人文章三条轨道有明确入口与交付定义；
2. 自动摄取"永远不生成 DJ"的旧默认改为按订阅处理深度 opt-in，且不静默扩大存量订阅的处理量；
3. "解说只朗读 Highlight Gist"的旧边界放宽为允许明确标注的开场、串场与收尾解说；
4. 来源快照、精读修订、新旧创作契约连接与迁移路径成文，避免各任务自行发明语义。

## 决策

### 1. 三条交付轨道的入口与交付物

| 轨道 | 入口 | 交付物 |
| --- | --- | --- |
| 学习 | 单集/来源详情页：读重点、听 DJ、写笔记 | 整集重点（含质量状态）、DJ 播放清单、SourceNote/OwnerReflection/Annotation 等个人笔记及其 Markdown 导出 |
| 单集精读 | 单集页"生成精读文"或列表批量 | EpisodeDigest 不可变修订（四类 DigestBlock），可预览、修订、导出 Markdown（ADR-0023） |
| 个人文章 | 素材选择、自动候选或定向构思 → 主张与 Brief 确认 | 经审校的作品修订与发布包，进入创作历史（ADR-0022） |

学习成果可以独立保存与导出，不要求进入创作流程；创作可以从个人想法出发检索已有素材。自动化（订阅处理、自动发现）只生成 Owner 选择开启的产物；文章生成由用户发起或确认写作方案触发，发布始终由 Owner 决定。

### 2. 处理深度 opt-in：修订"自动摄取止于重点"

- **IngestionPolicy 与 ProcessingDepth 分离**：策略回答"哪些新集处理"（Manual/AllNew/Filtered，语义不变）；深度回答"处理到哪里"——`knowledge`（转录、卡片、重点，现默认）与 `knowledge_dj`（另加 Highlight、Narration 与 DJ 清单准备）。
- **自动化只生成选择开启的产物**：新订阅默认 `knowledge`；开启 DJ 深度是显式设置，只影响之后入队的新意图，已经排队的任务使用入队时的快照。
- **存量订阅不静默扩大处理深度**：回填为 `knowledge`，行为与旧默认完全一致；不因升级而批量重跑旧集。
- 旧测试 `TestPipeline_AutomatedIngestionStopsAfterKeyPoints` 所固化的规则由按深度断言替代（B08）。

被否决的替代：保持"自动永不 DJ"（学习并重定位下，新集永远无法自动准备好精听）；默认全部订阅自动 DJ（静默扩大模型调用量，违背费用可见与 opt-in 原则）。

### 3. 修订 ADR-0019：允许明确标注的 DJ 串场解说

ADR-0019 将 Narration 限定为"首版仅合成 Highlight.Gist，整集 Summary、KeyPoint、Quote 等可核验/忠实内容一律不读"。本 ADR 在不推翻其分级原则的前提下放宽**解说词来源**：

- DJ 播放清单允许三类受约束串场：**开场**（说明本集与精听范围）、**串场**（基于相邻高光 Gist 的衔接过渡）、**收尾**（提示回听与记笔记）。
- 串场文本首版优先使用受约束文本模板与现有 Gist 拼装，避免额外无必要模型链；后续可用生成模型，但输出仍是 GeneratedDerivative。
- 不变量保持：所有语音标注为 AI 解说（合成音色 + 固定开场白）；不把个人判断伪装成主播原话；不逐字朗读 Summary/KeyPoint/Quote 等可核验内容；串场引用输入来源（Gist/Segment 身份）。
- 部分解说合成失败不阻塞清单：跳过该段解说直接播下一原音（延续 ADR-0019 R3 容错）。

### 4. 来源快照契约

可回溯性是三条轨道的共同前提，约定如下：

- 提供按 **Source + 明确版本**读取不可变来源快照的小接口：正文 Segment、原音身份（EvidenceAudio 哈希）、标题、卡片与引用位置；覆盖 Episode、Upload、Document。
- 产物保存时记录血缘（Transcript/Document 版本、EvidenceAudio 哈希）；引用绑定快照身份而非"当前版本"指针。
- **原音文件更新不得覆盖历史引用依赖的内容**：保留相应证据版本，或明确标为不可回听；不允许旧链接静默指向新转录。
- 无法可靠推定版本的旧产物标 legacy/待核实，不冒充当前版本输入；同 ID 不同版本的 Segment 不视为同一证据。
- 快照身份纳入备份恢复与 Purge 失效传播。

### 5. 单集精读修订与失败恢复契约

- EpisodeDigest 每次调整为**新修订**（记录父修订与原因），不原地覆盖旧正文；来源剔除派生移除相应事实块的新修订，旧版本仍可审计（细化 ADR-0023 的版本语义）。
- 渠道改写（DigestRewrite）为独立任务：输入指定修订与渠道，不整篇重生成；主文与渠道产物分开计费与失败。
- 生成任务按阶段 checkpoint：compose、检索落源、补织可独立恢复与重试；未完成修订不作为当前可用结果。
- "入队"与"成文成功"是两个状态；列表与进度页不得把已存在入口当成验收完成。

### 6. 新旧创作契约的兼容连接

- 新创作对象（CreationProposal/CreationBrief，ADR-0022）与旧文章对象（ArticleProposal/ArticleBrief）建立**持久一对一映射**，记录新契约版本；旧 Article 记录继续可读、可继续、可导出。
- Owner 确认新 Brief 后幂等建立所需兼容 Article 记录；不要求 Owner 重复确认一套旧对象，也不把未确认方向迁成已确认主张。
- 主张身份审校（ClaimMap v2）未落地前，新类型文章不得借旧 Writer/Review 路径获得错误的"就绪"状态。

### 7. 迁移路径与历史定位

- 新 schema 使用增量迁移（0026 起），不修改已运行的 0025 及更早迁移；每张涉及数据的任务同时处理回填、读取、备份恢复与 Purge 传播。
- 旧数据缺少版本血缘时标 legacy；可确定的输入才回填。
- [实施路线图](../implementation-roadmap.md)中 Phase 0–3 已落地部分（MaterialCandidate/质量状态字段、ProposalBatch、EditorialRelevance、工作空间 HTTP 接线、放宽五条契约）为既有基线；旧路线图与[迁移差距表](../learning-creation-migration-gap.md)转为历史线索，执行顺序与任务粒度以[原子提交实施计划](../superpowers/plans/2026-09-12-learning-dj-creation-atomic-plan.md)为准。旧 EpisodeDigest 闭环计划按实际代码归档状态，不复制其缺失的插件执行指令。

## 不变

- Evidence-first 契约（ADR-0008）、信息分层（ADR-0018）、Reference/Citation 互斥不变。
- 主张纪律（ADR-0022）：AI 提议、Owner 承担；ClaimReview/StyleReview 职责不变。
- EpisodeDigest 封闭集合与程序化门禁（ADR-0023）不变，本 ADR 只细化修订与恢复语义。
- 默认零成本与按单次授权付费（ADR-0009）；处理深度与自动发现均为 opt-in。
- Narration 不进 EvidenceAudio、不作核验依据（ADR-0019）；单 Owner、SQLite、现有技术栈不迁移。
