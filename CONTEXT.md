# CloudWisePod 领域词汇表

CloudWisePod 将 Owner 主动选择或授权摄取的内容来源转化为可长期核验的证据，并以这些证据为原料支持个人知识沉淀与内容生产。本文件只定义领域语言；实现与取舍见 `docs/adr/`。

## 信息分层

**PrimarySource（原始来源）**：
CloudWisePod 中受保护的事实层，是所有衍生内容可核验性的最终锚点。音频 Source 的 PrimarySource 由 EvidenceAudio 与其 Transcript 共同构成；文档 Source 的 PrimarySource 是 EvidenceDocument。AI 不得改写或重新组织这一层的内容；Transcript 虽由 AI 产生，但其社会契约是对音频的忠实文字镜像，因此归入原始来源层。
_避免使用：Original、GroundTruth、RawData；它们要么偏实现，要么暗示未处理的原始文件_

**Derivative（衍生内容）**：
基于 PrimarySource 由 AI 生成或重新组织的全部内容。Derivative 内部按对原文的忠实程度分为 CitedDerivative 与 GeneratedDerivative 两类，二者必须可被 Owner 一眼区分。平台的责任是帮 Owner 区分两类，而不是替 Owner 判断。
_避免使用：AIContent、Generated、Output；它们没有表达“基于原始来源”这一关键关系_

**CitedDerivative（带证衍生）**：
Derivative 中声称忠实于原文的部分。必须通过 Citation 绑定一个或多个 Segment，且其表述受对应 Citation 模式约束（金句逐字可核验，摘要、要点和章节的位置由程序从 Segment 解析，AI 不得自行估算）。现有 Summary、KeyPoint、Chapter、Quote 与 Highlight 中声称出自原文的部分均属此类。契约与 ADR-0008 一致，不因升级而放宽。
_避免使用：Verified、Trustworthy；后者是 Owner 的判断，不是内容自身的属性_

**GeneratedDerivative（生成衍生）**：
Derivative 中由 AI 重新组织、讲解、类比、推论或举例的部分。它明确标注为 AI 生成、非原文，允许脱离原文表述。它可以引用 Segment 作为参考来源，但参考关系不等于忠实——Owner 不应将其当作可逐字核验的原话。具体参考关系由 Reference 定义。
_避免使用：Explanation、Commentary、Insight；它们是生成衍生的具体形态，不是类别名_

## 内容进入

**Owner（所有者）**：
唯一使用并拥有一个 CloudWisePod 实例内全部内容的人。一个实例只能被认领一次，不存在后续注册、成员关系或租户隔离。
_避免使用：User、Member、Tenant、Account_

**Candidate（候选内容）**：
通过 RSS 自动发现、但尚未被 Owner 选择处理的单集元数据。Candidate 不属于 EvidenceArchive，也不消耗 AI 处理资源。
_避免使用：Source；后者表示 Owner 已经选择的内容_

**IngestionPolicy（摄取策略）**：
Owner 为一个 Podcast 设定的新 Episode 入库规则。策略可以是 Manual（仅发现）、AllNew（全部自动处理）或 Filtered（按标题、简介、时长、关键词与预算筛选）；自动处理受 Podcast 级与全局预算约束，只生成内容生产必需的 Transcript、KnowledgeCard 与 KeyPoint，其他衍生产物仍按需生成。
_避免使用：Subscription、Cron、AutoProcess；它表达 Owner 对内容进入证据与生产流程的长期决策，而不是订阅关系或调度实现_

**Source（内容来源）**：
Owner 已明确选择、或通过 IngestionPolicy 授权进入证据处理与内容生产流程的内容来源。Source 可以是来自播客的 Episode、手动提供的音频 Upload，或来自网页、PDF、粘贴文本的 Document。
_避免使用：Candidate、Item、Content_

**Episode（播客单集）**：
来自播客订阅、通过 RSS 发现的一段音频内容。
_避免使用：FeedItem、RSSItem_

**Upload（手动上传）**：
由 Owner 主动提供的一段本地音频内容；文档文件属于 Document，不属于 Upload。
_避免使用：File、Attachment_

**Document（文档来源）**：
Owner 通过网页 URL、PDF 或粘贴文本提供的一等 Source。Document 与音频 Source 共享证据、KeyPoint 与内容选材语义，但不拥有 EvidenceAudio 或 Transcript。
_避免使用：Attachment、Reference、Upload；它是可独立处理和引用的来源，不是临时附件或弱关联_

## 证据

**EvidenceAudio（证据音频）**：
为每个音频 Source 长期保存、可独立播放的标准化音频。它保证 Citation 不依赖外部音频地址，但不承诺保留输入时的原始文件格式。
_避免使用：TemporaryAudio、OriginalAudio、Cache、Narration（后者是 AI 生成音轨，见 Narration）_

**EvidenceDocument（证据文档）**：
为每个 Document 长期保存、可独立核验的文档内容快照。网页后续变化或外部文件失效不能改变这份证据；对格式的标准化不得改写原文含义。
_避免使用：LivePage、Cache、Attachment；它是稳定证据，不是外部内容的临时副本_

**Transcript（转录稿）**：
EvidenceAudio 的带时间对齐文本表达，由一组有起止时间的 Segment 组成。
_避免使用：PlainText、Subtitle_

**Translation（对齐译文）**：
将 PrimarySource 原文按 Segment 对齐翻译成目标语言的 CitedDerivative。Translation 不得覆盖原语言 Transcript 或 EvidenceDocument，并作为可独立审阅和版本化的产物存在；译文中的直接引语必须标注为翻译，内部同时保留对应原文。
_避免使用：Transcript、PrimarySource、Localization；它不是原始证据或界面本地化_

**Segment（证据片段）**：
PrimarySource 中一段连续且具有稳定位置的文本，是 Citation 的最小核验单位。音频 Segment 属于 Transcript 并具有起止时间；文档 Segment 属于 EvidenceDocument 并具有稳定的文档位置。
_避免使用：Chunk；后者通常表示检索实现中的临时分组_

**Speaker（说话人）**：
音频 Source 中与一组 Segment 对齐的发言者身份。系统可以先产生 Speaker A、Speaker B 等稳定标识并建议实名映射，但只有经 Owner 确认的身份才能用于文章中的实名归因；未确认时不得由 AI 猜测姓名。
_避免使用：Guest、Host、Person；这些词预设了节目角色或已知身份，而 Speaker 可以尚未实名确认_

**Citation（证据引用）**：
一项衍生内容与一个或多个 Segment 之间的可验证关系。音频 Citation 的时间范围、文档 Citation 的位置都来自 Segment，不能由 AI 自行估算。Citation 只证明来源表达过相关内容，不证明该表达在客观世界中真实；未经独立核验的数字、预测与争议判断必须保留来源归因。
_避免使用：Timestamp、Source、Link；它们不能单独表达原文依据_

**Reference（参考关系）**：
一个 GeneratedDerivative 与一个或多个 Segment 之间的弱关联，表示该生成内容参考了这些片段。Reference 不声称忠实，不要求逐字，与 Citation 语义不同且不可互换。音频时间范围或文档位置仍由程序从 Segment 解析，AI 不得自行估算，以保证所引用的位置真实存在。Reference 与 Citation 互斥：一个 Derivative 的关联要么全是 Citation（当它是 CitedDerivative），要么全是 Reference（当它是 GeneratedDerivative），由其类别决定。
存储语义：二者在 schema 层用显式 relation_kind 字段标注（'citation' | 'reference'），不靠宿主实体类型隐式区分，以保证互斥铁律在数据层可查可约束。承载 Segment 关系的实体（keypoint_index.citations_json、annotations/pins/collections.segment_ids、Paraphrase、StudyChat 消息）统一增加 relation_kind 列；存量 Citation 数据回填为 'citation'。宿主实体类别与 relation_kind 必须配对正确，写入时校验。
_避免使用：SoftCitation、CitationLite、LooseLink；它们暗示 Reference 只是较弱的 Citation，掩盖了二者语义不可互换这一关键_

**KnowledgeCard（知识卡片）**：
AI 根据一个 Source 的 PrimarySource 生成、供 Owner 判断内容价值的结构化中间产物。摘要、关键要点和金句都必须带 Citation，金句必须逐字来自被引用的 Segment；Chapter 仅适用于具有时间轴的音频 Source。
_避免使用：KnowledgeNote、Summary_

**KeyPoint（关键观点）**：
表达一个具体观点、并通过 Citation 关联原文依据的最小可复用内容单元。KeyPoint 可以由 KnowledgeCard 自动生成，也可以由 Owner 选择 Segment 手工创建，或从自动版本派生人工编辑版；来源方式必须可见，重新分析不得覆盖人工成果。跨 Source 浏览与后续内容选材都以 KeyPoint 为核心；每个 KeyPoint 具有独立于 Pin 的生产状态：Inbox（尚未判断）、Shortlisted（值得用于未来选题）、Used（已用于内容生产）或 Dismissed（不用于内容生产），并可追溯参与过的提案、草稿与已发布文章。若 PrimarySource 版本变化后无法可靠重映射原 Citation，KeyPoint 必须标记为证据待修复而不是静默错挂。Summary 只提供整个 Source 的背景，Highlight 只适用于音频 Source，负责定位值得回听的原音区间。
_避免使用：Summary、Highlight、Insight、Takeaway；它们要么粒度或用途不同，要么无法表达可核验的观点单元_

**EvidenceArchive（证据库）**：
CloudWisePod 持久保存的 Source、EvidenceAudio、EvidenceDocument、Transcript、Citation、KnowledgeCard 及处理历史集合。它负责核验依据，不是 Owner 编辑最终知识的地方。
_避免使用：KnowledgeBase_

**EvidenceQA（证据问答）**：
Owner 针对单个 Source 提问、要求回答必须出自原文的问答模式，属 CitedDerivative。每个回答必须通过 Citation 引用实际参与回答的 Segment，音频时间或文档位置由程序解析，AI 不得自估；证据不足时明确拒答，不附加未被引用的检索片段来伪造依据。契约与 ADR-0008 一致，不因升级而放宽。它回答的是查证型问题：原文有没有说、在哪说的。
_避免使用：QA、Chat、Ask；它们没有表达“证据约束”这一关键属性_

**StudyChat（学习对话）**：
Owner 针对单个 Source 与 AI 进行的多轮学习型对话，属 GeneratedDerivative。它允许 AI 用别的话重讲、打比方、举例或拆解，目的是帮 Owner 消化内容，而非查证原文。回答通过 Reference 关联参考的 Segment，但明确标注为 AI 生成、非原文，不声称逐字忠实；不适用 EvidenceQA 的拒答策略，但必须始终标注自身为生成内容。它与 EvidenceQA 并存且不可混淆：查证用 EvidenceQA，学习用 StudyChat，二者在 UI 上明确区分。硬约束一（scope 缰绳）：StudyChat 的每一轮回答必须挂至少一条指向当前 Source Segment 的 Reference，若 AI 无法关联任何 Segment 则不允许生成，改提示 Owner 该问题已超出本集范围。这保证 StudyChat 永远是帮 Owner 消化本集内容，而非通用聊天助手；与 EvidenceQA 的无 Citation 拒答形成对偶：一个是无 Citation 拒答，一个是无 Reference 不生成。
硬约束二（防虚挂）：Reference 由生成回答的 AI 自选，但生成后须经一次独立的 Reference 校验，判断所挂 Reference 指向的 Segment 与回答之间是否存在可识别的语义相关（只判相关，不判逐字忠实，以尊重 GeneratedDerivative 允许重新组织的属性）。校验由独立的判定步骤完成，不参与内容生成。校验失败则该回答不呈现给 Owner，转而给出可见反馈说明该问题无法关联到本集内容，让 Owner 知道是问题越界而非系统故障。此约束防止 AI 为满足硬约束一而虚挂无关 Segment。
_避免使用：Tutor、Chatbot、Assistant、LearningChat；它们要么偏实现，要么没有表达“学习型、非查证”这一属性_

**StudySession（学习会话）**：
一次 StudyChat 多轮交互的会话容器与日志，按会话保存而非按 ArtifactVersion 版本化。它记录该会话内每一轮 Owner 提问与 AI 回答（含所挂 Reference），可供 Owner 回看；同一问题在不同 StudySession 里重新提问属于新会话，而非同一产物的不同版本，因此不适用 ArtifactVersion 的比较与回退语义。StudySession 可由 Owner 整体删除；这与 Purge（删除整个 Source 及其全部证据）不同，StudySession 的删除是会话级清理，不影响 Source 及其 EvidenceArchive。
_避免使用：Conversation、Thread、History、ChatLog；它们要么偏实现，要么没有表达“围绕单一 Source 的学习会话”这一边界_

**ReferenceCheck（参考校验）**：
StudyChat 硬约束二的独立校验步骤，对每一轮 StudyChat 回答执行。它取三元输入（Owner 问题 + AI 回答 + 所挂 Reference 指向的 Segment 文本），做二元判断：回答所讨论的主题，是否是 Reference 段所讨论内容的延伸、解释、例化或重组，而非离开这些内容去谈别的事物。判据是主题锚定，不是逐字忠实——放行概念解释、类比例化、结构重组；挡住主题漂移、预测建议评价、以及仅靠措辞蹭原文而无概念联系的虚挂。校验只做判断、不参与内容生成。校验角色可独立配置模型；若虚挂率上升，优先切换校验模型或引入第二判据，而不是让生成模型自证。校验失败则该回答不呈现，转而给 Owner 可见反馈（问题越界，非系统故障）。校验器的误杀与漏放纳入 EvalSet，作为同等公民接受质量评测。
_避免使用：Validator、Filter、Moderator；它们要么偏实现，要么没有表达“判主题锚定、非判忠实”这一专属语义_

## 知识沉淀

**KnowledgeNote（知识笔记）**：
Owner 判断值得保留后沉淀到 PersonalKnowledgeBase 的最终产物。它必须可复用，并能跳回对应的 PrimarySource。升级后 KnowledgeNote 可同时包含 CitedDerivative 与 GeneratedDerivative 两类内容，二者必须按类别结构化标注，随内容一起下沉：
- CitedDerivative 块标注为原文依据，带 Citation 跳转，跳回可逐字核验。
- GeneratedDerivative 块标注为 AI 讲解、非原文，带 Reference 跳转，跳回仅表示参考、不可核验。
两种块视觉可区分，且 Reference 跳转不得伪装成 Citation 跳转。具体形态：CitedDerivative 块用 Obsidian callout 标注为原文依据（如 > [!quote] 原文），带 Citation 跳转链接（锚点文字“引用”，URL 用 ?t=）；GeneratedDerivative 块用自定义 callout 类型标注（> [!ai-generated] AI 讲解·非原文），带 Reference 跳转链接（锚点文字“参考”，URL 用 ?ref=，与 Citation 的 ?t= 区分），且 callout 首行带文字前缀兜底，保证非 Obsidian 渲染器也能识别类别。Gist 正名后从 Chapter Citation 渲染中独立出来，作为 GeneratedDerivative callout 块置于对应 Chapter 区块之后，保持“区间真实在前、讲解自由在后”的视觉顺序。此标注是把 CloudWisePod 内部的 Cited/Generated 分级延伸到 PersonalKnowledgeBase 的最后一公里，避免几个月后 Owner 在外部知识库把 AI 讲解误当成原话。
_避免使用：Export、Markdown、KnowledgeCard；它们分别是动作、格式和中间产物_

**PersonalKnowledgeBase（个人知识库）**：
Owner 在 CloudWisePod 之外维护、编辑和组织 KnowledgeNote 的权威位置。CloudWisePod 只向它单向沉淀内容。
_避免使用：EvidenceArchive、CloudWisePod_

## 处理

**ProcessingJob（处理任务）**：
Owner 要求系统将一个 Source 转化为可核验证据的持久意图。ProcessingJob 不会因 CloudWisePod 暂停或重启而消失。
_避免使用：Goroutine、BackgroundTask、QueueMessage；它们是实现机制_

**ArtifactVersion（产物版本）**：
一次 ProcessingJob 尝试生成的不可变产物，覆盖 EvidenceDocument、Transcript、KnowledgeCard、Highlight、Narration；每种产物以 kind 区分。Source 可以选择当前采用的版本，但重新处理不能覆盖历史版本。Narration 的版本化粒度特殊：不是整集一组，而是每个 Highlight 的 Gist 各有一段独立版本化的 Narration，通过 highlight_id 关联到所属 Highlight（见 Narration）。Paraphrase 与 StudyChat 不纳入 ArtifactVersion：前者是高频、试错的阅读时按需触发，与 ProcessingJob 的低频有意重新分析语义不同，按锚点保留最近 N 次而非版本化（见 Paraphrase）；后者是多轮有状态会话，按 StudySession 单独保存。
_避免使用：CurrentResult、Upsert、Latest；它们会掩盖历史产物的身份_

**Provider（AI 供应商）**：
为转录、分析、问答或内容生产提供 AI 能力的外部服务。免费不再是 Provider 选择的产品硬约束；不同生产阶段可以使用不同的付费 Provider 与模型，但费用归属必须对 Owner 可见。
_避免使用：Model；模型是 Provider 内部的一项选择_

**Purge（彻底删除）**：
由 Owner 明确发起、不可撤销地移除一个 Source 及其全部证据和处理历史的动作。被 ArticleDraft 使用的 Source 在 Purge 后会使相关 ArticleRevision 标记为证据失效并禁止导出；被 PublishedArticle 使用的 Source 默认受保护，但 Owner 仍可为隐私或合规明确强制 Purge。强制 Purge 不得为了保住文章证据而秘密复制原文，只保留“证据已删除”的审计事实。Purge 同样会使 PersonalKnowledgeBase 中指向该 Source 的 Citation 与 Reference 失效。
_避免使用：Delete、Archive、Remove；它们没有表达完整且不可逆的删除边界_

**Archive（归档来源）**：
停止一个 Source 参与新选题和选材、但完整保留其 PrimarySource、证据链与历史关系的可逆动作。Archive 不影响已经存在的 ArticleRevision 或 PublishedArticle。
_避免使用：Purge、Delete；它是日常素材退场方式，不删除证据_

**Highlight（高光片段）**：
AI 根据整集 Transcript 判断出的、按价值密度选出的连续音频区间，属于 CitedDerivative。每个 Highlight 的 Citation 是一组 Segment 的并集（程序取 min(start)–max(end)），AI 只能选择 Segment ID，不能自行估算时间范围；区间本身可核验，点开即可回听原音。它与按主题划分的 Chapter、逐字的 Quote、文字要点的 KeyPoint 是并列关系，粒度比 Chapter 粗（按价值而非主题）、比 Quote 广（区间而非单句）。挂在 Highlight 上的文字讲解是 Gist，属 GeneratedDerivative，与 Highlight 本身的类别不同。
稳定身份：每个 Highlight 携带程序生成的稳定 ID（Highlight.ID），用于让挂在它上的 Gist 与 Narration 在 HighlightSet 刷新（重新生成高光）后仍能正确关联；刷新时程序尽量保留旧 ID 或按 Citation 集合重映射，避免 Gist/Narration 错挂到错误区间。
_避免使用：MustHear、Spotlight、Takeaway、BestPart；它们要么语义模糊，要么与 KeyPoint 冲突_

**Gist（要点说明）**：
对一段音频区间（Highlight 或 Chapter）内容的简短文字说明，由 AI 重新组织语言生成，非逐字原文。Gist 是 GeneratedDerivative：它通过 Reference 关联区间内的 Segment，表示参考了这些片段，但不声称逐字忠实，也不挂 Citation。所属区间本身（Highlight 或 Chapter）仍是 CitedDerivative，区间可核验，区间上的讲解自由。
_避免使用：Annotation、Summary；它们要么偏实现，要么与整集 Summary 冲突_
Gist 与 Paraphrase 的区别：Gist 是 AI 主动为每个区间生成的概括，覆盖全部区间；Paraphrase 是 Owner 按需触发的重讲，局部且不一定覆盖。

**Narration（解说音轨）**：
AI 将一个 GeneratedDerivative（首版仅 Gist）的文字用 TTS 合成为音频产物。Narration 是 GeneratedDerivative 的音频形态，明确标注为 AI 生成音、非原音；它不进入 EvidenceAudio，不作为 Citation 或 Reference 的核验依据，仅供收听。Narration 与 EvidenceAudio 是对照关系：EvidenceAudio 是原音事实层的音频，Narration 是衍生层的 AI 解说音频，二者不可混用。Narration 按音色与模型版本化（不可变、可比较回退、可重新生成），TTS 合成有成本，版本化避免重复付费。版本化粒度为每个 Highlight 的 Gist 各一段独立版本化的 Narration（通过 highlight_id 关联到所属 Highlight），换音色/模型时可只重生成某几段，满意的段落不受影响。
听觉分级：Narration 统一使用与原音明显不同的合成音色，且每段强制以固定开场白（如“AI 解说：”）开头，让 Owner 一耳朵可辨这是 AI 串场、非主播原话——这是信息分层视觉分级在音频通道的等价物。
音频格式与存储：Narration 以独立音频产物存储，物理隔离于 EvidenceAudio，二者的 serve 路径不可混用。Narration 的模型按角色路由与 ModelDataPolicy 选择；本地免费 TTS 可以继续作为默认配置，但不构成产品硬边界。Narration 可重新生成，不进入核心证据备份。
_避免使用：TTS、Voiceover、AudioSummary；TTS 是技术词偏实现，Voiceover 暗示覆盖原音，AudioSummary 与 KnowledgeCard.Summary 混淆_

**Paraphrase（复述讲解）**：
Owner 按需触发的、针对自己未理解部分的重新讲解，属 GeneratedDerivative。它用别的话重新讲同一件事，可包含类比、举例或拆解，目的是帮 Owner 消化 PrimarySource 中某处的内容。Paraphrase 通过 Reference 关联一个或多个 Segment，参考这些片段但不声称逐字忠实；可锚定在单个 Segment（如“这句没懂”）或一个区间（Highlight/Chapter，如“这段没懂”）。它与 Gist 互补：Gist 是主动、全覆盖的概括，Paraphrase 是按需、局部的重讲。不做整集 Paraphrase，以免与整集 Summary 重叠。
持久化语义：Paraphrase 不是 ArtifactVersion，而是独立轻量产物，按锚点保留最近 3 次。锚点为该次 Paraphrase 所挂 Reference 指向的 Segment 集合的稳定 key（排序后的 segment_ids 串）；同一锚点的多次 Paraphrase 互相淘汰最旧的，不同锚点独立保留。不版本化、不比较、不回退——只满足"再讲一次、对比上次"的实际诉求，避免高频试错触发导致版本爆炸。
_避免使用：Explanation、Commentary、Insight、Simplification；它们要么语义模糊，要么与 Gist 重叠_

**Annotation（标注）**：
Owner 在某个 Citation 上附加的个人文字注解。它不是 AI 生成的，不随重新分析消失；锚定在 Citation 指向的 Segment 上，保证证据不变则标注不丢。
_避免使用：Note、Comment、Remark_

**Pin（收藏）**：
Owner 标记某个 Citation"值得记住"的轻量动作。Pin 是 CloudWisePod 内的标记，不等于 KnowledgeNote——后者是沉淀到 PersonalKnowledgeBase 的最终 Markdown。
_避免使用：Bookmark、Favorite、Star_

**Collection（集合）**：
Owner 把跨 Source 的 Citation 按自定义主题组织成的组。它按主题组织而非按 Source，是 Owner 的个人组织层，不改变 EvidenceArchive 的结构。
_避免使用：Playlist、Folder、Tag_

**Theme（素材主题）**：
跨 Source 长期积累和组织 KeyPoint 的主题簇。AI 可以建议 Theme 及成员关系，Owner 可以确认、改名、合并、拆分或忽略；一个 KeyPoint 可以属于多个 Theme。Theme 展示来源分布、素材趋势、观点冲突、已发布文章与内容空白，但本身不是面向特定 EditorialProfile 的写作提案。
_避免使用：Collection、ArticleProposal、Tag；它不是纯人工引用集合、一次写作机会或无结构标签_

## 内容生产

**EditorialProfile（编辑画像）**：
一个内容品牌的长期编辑约束，定义目标读者、主题边界、语气、篇幅、标题风格、禁用表达、品牌词汇、固定结构、参考范文、对外来源署名密度与 SourceScope，并可指定默认模型组合和单篇预算。一个 Owner 可以维护多个 EditorialProfile；每个 ArticleProposal 必须且只能属于其中一个。
_避免使用：UserPreference、Prompt、公众号；它既不是全局个人偏好或实现提示词，也不限于单一发布渠道_

**SourceScope（素材范围）**：
一个 EditorialProfile 被明确授权用于内容生产的 Source 范围，可按 Podcast、Source 类型、Theme、语言和时间限制。Source 还可被标记为可公开使用、仅内部研究或禁止内容生产；新 Source 默认不会进入所有画像，素材权限失效会阻止相关 ArticleRevision 生成 PublicationPackage。
_避免使用：SearchFilter、Collection、Visibility；它是内容生产权限边界，不只是一次查询条件、素材分组或界面显示状态_

**ModelDataPolicy（模型数据策略）**：
一个 Source 对其内容可发送到哪些 AI Provider 的约束，分为 ExternalAllowed、ApprovedProvidersOnly 与 LocalOnly。它与 SourceScope 的发布用途权限相互独立；混合多个 Source 的任务继承其中最严格的策略，Provider 故障切换不得绕过该策略。
_避免使用：SourceScope、Privacy、ProviderConfig；它专门约束模型数据流向，而不是文章用途、笼统隐私标签或一般模型配置_

**EditorialFeedback（编辑反馈）**：
Owner 对 ArticleProposal、ArticleBrief 或 ArticleRevision 作出的显式编辑判断，包括接受、暂存、拒绝、合并及其原因，以及对标题、论点、素材和结构的修改。后续内容生产可以参考这些判断，但不得据此静默修改 EditorialProfile；系统提出的画像变更必须由 Owner 确认。
_避免使用：ImplicitPreference、TrainingData、Analytics；它是 Owner 可见且可解释的编辑决策，不是暗中推断的偏好_

**ArticleProposal（文章提案）**：
CloudWisePod 针对一个 EditorialProfile、从跨 Source 的 KeyPoint 中主动发现的候选写作方向，包含主题、核心论点、目标读者、写作价值与候选素材。提案分为 Fresh（近期来源）、Evergreen（历史素材重组）与 Follow-up（已发布文章的更新或延展）三种模式，并须与历史提案和 PublishedArticle 做重复检查。ArticleProposal 只有经 Owner 选择或调整后，才能进入完整文章生成；每个 EditorialProfile 可以配置各模式的生成节奏。
_避免使用：ArticleBrief、ArticleDraft、Topic；前两者已经进入选材或成稿阶段，后者没有表达提案所需的论点、读者与素材_

**ArticleBrief（文章简报）**：
Owner 批准 ArticleProposal 后形成的可审核写作契约，明确核心论点、目标读者、入选与淘汰的 KeyPoint、各节结构、预计长度和写作风格。Curator 必须在其中显式标出材料间的支持、补充与冲突关系；冲突不能被静默丢弃，须由 Owner 选择并列呈现、明确立场、缩小论点或淘汰材料。只有经 Owner 确认的 ArticleBrief 才能进入完整文章生成。
_避免使用：Outline、Prompt；前者没有覆盖选材决策，后者是实现手段而非领域对象_

**ArticleDraft（文章草稿）**：
Owner 确认 ArticleBrief 后创建的持续编辑对象，承载模型初稿、审校修订、Owner 手工修改与局部 AI 改写形成的 ArticleRevision 历史。模型初稿必须经过独立的证据审校与风格审校并完成修订，才能成为可交付版本。首版中每个实质性主张都必须可追溯到一个或多个 KeyPoint，并最终回到 Source、Citation 与 PrimarySource；AI 可以重组和综合观点，但不得以模型自身知识偷偷补充事实。ArticleDraft 不等于已经发布的文章，CloudWisePod 不得绕过 Owner 审核直接发布。未来可以引入播客以外的素材来源，但必须显式区分来源类型并保留各自的证据链。
_避免使用：KnowledgeNote、PublishedArticle；它们分别属于个人知识沉淀与已发布内容_

**ArticleRevision（文章修订版）**：
ArticleDraft 在某一时刻的不可变内容快照。AI 生成、审校修订、Owner 手工修改和局部 AI 改写都会产生新的 ArticleRevision；ArticleDraft 选择其中一个作为当前版本，但不得覆盖历史版本。证据与风格审校结果只属于接受检查的确切 ArticleRevision，不能由旧版本继承；修改后必须至少对受影响内容重新审校。每个 ArticleRevision 保留其素材来源与生成来历，发布后的内容也必须指向当时采用的确切版本。
_避免使用：Autosave、CurrentDraft、ArtifactVersion；它是编辑内容的领域版本，不是保存机制或通用处理产物_

**EvidenceMap（文章证据映射）**：
一个 ArticleRevision 中实质性表达与其素材依据之间的类型化映射。映射分为 Quoted（逐字引语，精确匹配原文）、Paraphrased（忠实转述 KeyPoint）、Synthesized（基于一个或多个 KeyPoint 的生成性综合、比较或推论）与 Rhetorical（不含事实主张的修辞表达）；Synthesized 只能声明参考关系，不能伪装成来源直接支持的结论。首版不允许没有 Source 依据的 ExternalFact。
_避免使用：Citation、Footnote、SourceList；它不仅记录来源，还明确文章表达与来源之间的语义关系_

**PublicationPackage（发布内容包）**：
从一个已通过当前硬性审校门槛的 ArticleRevision 生成的渠道适配内容集合，首版包含公众号兼容富文本、Markdown、纯文本、候选标题、摘要、推荐语、封面文案与视觉素材。内部逐段证据链始终完整；对外来源呈现按 EditorialProfile 配置为轻量、标准或严格，但直接引语必须明确署名，文末必须列出实际使用的 Source。AI 可以生成封面和正文示意图，但不得自动搬运网络图片；数据图必须有可追溯数据，所有生成图保留模型与提示词并由 Owner 选择。PublicationPackage 供 Owner 复制或导出，不代表已经发布。
_避免使用：ArticleDraft、PublishedArticle、Export；它是围绕确切文章版本组织的完整交付物，不只是正文、发布事实或导出动作_

**PublishedArticle（已发布文章）**：
Owner 确认某个 ArticleRevision 已在外部渠道发布后形成的发布记录，保存发布渠道、时间、外部地址与当时采用的确切版本。它参与后续选题的重复检查与 Follow-up 提案，但外部发布内容仍由对应渠道管理。
_避免使用：ArticleDraft、KnowledgeNote；它表达一次对外发布事实，而不是仍在编辑的草稿或个人知识笔记_

**PublicationPerformance（发布表现）**：
一个 PublishedArticle 在特定渠道获得的读者反馈指标，例如阅读、点赞、在看、分享、收藏与关注增长。首版由 Owner 手工记录；它可用于分析主题、结构、篇幅和标题的长期表现并调整提案排序，但不得据此静默修改 EditorialProfile。
_避免使用：EditorialFeedback、VanityMetrics、Analytics；它来自读者行为而非 Owner 编辑判断，并作为可解释的生产反馈而存在_
