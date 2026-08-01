# CloudWisePod 领域词汇表

CloudWisePod 将 Owner 主动选择的播客音频转化为可长期核验的证据，并帮助 Owner 将值得保留的内容沉淀到外部个人知识库。本文件只定义领域语言；实现与取舍见 `docs/adr/`。

## 内容进入

**Owner（所有者）**：
唯一使用并拥有一个 CloudWisePod 实例内全部内容的人。一个实例只能被认领一次，不存在后续注册、成员关系或租户隔离。
_避免使用：User、Member、Tenant、Account_

**Candidate（候选内容）**：
通过 RSS 自动发现、但尚未被 Owner 选择处理的单集元数据。Candidate 不属于 EvidenceArchive，也不消耗 AI 处理资源。
_避免使用：Source；后者表示 Owner 已经选择的内容_

**Source（内容来源）**：
Owner 已明确选择进入处理流程的一段音频。Source 可以是 Episode 或 Upload。
_避免使用：Candidate、Item、Content_

**Episode（播客单集）**：
来自播客订阅、通过 RSS 发现的一段音频内容。
_避免使用：FeedItem、RSSItem_

**Upload（手动上传）**：
由 Owner 主动提供的一段本地音频内容。
_避免使用：File、Attachment_

## 证据

**EvidenceAudio（证据音频）**：
为每个 Source 长期保存、可独立播放的标准化音频。它保证 Citation 不依赖外部音频地址，但不承诺保留输入时的原始文件格式。
_避免使用：TemporaryAudio、OriginalAudio、Cache_

**Transcript（转录稿）**：
EvidenceAudio 的带时间对齐文本表达，由一组有起止时间的 Segment 组成。
_避免使用：PlainText、Subtitle_

**Segment（转录片段）**：
Transcript 中具有明确起止时间的一段连续文本，是 Citation 的最小核验单位。
_避免使用：Chunk；后者通常表示检索实现中的临时分组_

**Citation（证据引用）**：
一项衍生内容与一个或多个 Segment 之间的可验证关系。Citation 的时间范围来自 Segment，不能由 AI 自行估算。
_避免使用：Timestamp、Source、Link；它们不能单独表达原文依据_

**KnowledgeCard（知识卡片）**：
AI 根据 Transcript 生成、供 Owner 判断内容价值的结构化中间产物。摘要、关键要点、章节和金句都必须带 Citation，金句必须逐字来自被引用的 Segment。
_避免使用：KnowledgeNote、Summary_

**EvidenceArchive（证据库）**：
CloudWisePod 持久保存的 Source、EvidenceAudio、Transcript、Citation、KnowledgeCard 及处理历史集合。它负责核验依据，不是 Owner 编辑最终知识的地方。
_避免使用：KnowledgeBase_

## 知识沉淀

**KnowledgeNote（知识笔记）**：
Owner 判断值得保留后沉淀到 PersonalKnowledgeBase 的最终产物。它必须可复用，并能通过 Citation 回到 EvidenceAudio 核验。
_避免使用：Export、Markdown、KnowledgeCard；它们分别是动作、格式和中间产物_

**PersonalKnowledgeBase（个人知识库）**：
Owner 在 CloudWisePod 之外维护、编辑和组织 KnowledgeNote 的权威位置。CloudWisePod 只向它单向沉淀内容。
_避免使用：EvidenceArchive、CloudWisePod_

## 处理

**ProcessingJob（处理任务）**：
Owner 要求系统将一个 Source 转化为可核验证据的持久意图。ProcessingJob 不会因 CloudWisePod 暂停或重启而消失。
_避免使用：Goroutine、BackgroundTask、QueueMessage；它们是实现机制_

**ArtifactVersion（产物版本）**：
一次 ProcessingJob 尝试生成的不可变 Transcript 或 KnowledgeCard。Source 可以选择当前采用的版本，但重新处理不能覆盖历史版本。
_避免使用：CurrentResult、Upsert、Latest；它们会掩盖历史产物的身份_

**Provider（AI 供应商）**：
为转录、分析或问答提供 AI 能力的外部服务。Groq 是默认零成本 Provider；任何付费 Provider 都只授权给一次明确的 ProcessingJob 尝试。
_避免使用：Model；模型是 Provider 内部的一项选择_

**Purge（彻底删除）**：
由 Owner 明确发起、不可撤销地移除一个 Source 及其全部证据和处理历史的动作。Purge 会使 PersonalKnowledgeBase 中指向该 Source 的 Citation 失效。
_避免使用：Delete、Archive、Remove；它们没有表达完整且不可逆的删除边界_
