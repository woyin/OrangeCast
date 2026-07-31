# CloudWisePod 领域词汇表

本文件只记录领域概念的精确定义，不包含实现细节。实现决策见 `docs/adr/`。

## 词汇

### Source（内容来源）
多态抽象，指一段待处理的音频及其衍生内容。两种具体类型：
- **Episode**：来自订阅播客的单集，通过 RSS feed 发现，`audio_url` 指向外部 mp3。
- **Upload**：用户手动上传的音频文件，音频本体临时落盘于 VPS。

`source_type ∈ {episode, upload}` + `source_id` 联合标识一个 Source。SQLite 无法对此多态引用建外键，删除时由应用层级联清理（见 [ADR-0002](docs/adr/0002-source-cascade-delete.md)）。

### Transcript（转录）
音频转文字的结果，分两层存储（同表两列）：
- **plain_text**：纯文本，供全库搜索（FTS5）与摘要展示。
- **segments_json**：`[{start, end, text}]` 带时间戳的逐句分段，供播放器联动与 Q&A chunk 检索。

### KnowledgeCard（知识卡片）
AI 分析转录稿后生成的结构化产物：title、summary、keyPoints、chapters（带时间戳）、quotes（带时间戳）、tags、suggestedQuestions。对标 Podwise 的 summary/takeaways/outline。

### Chunk（检索片段）
Q&A 时把 transcript segments 按 N 句聚合成 chunk，作为 RAG 检索单元。每个 chunk 保留首句 start、末句 end，用作 LLM 回答的引用时间戳。

### Provider（AI 供应商）
抽象层，屏蔽 Groq 与 OpenAI 的 HTTP 契约差异。三类操作：Transcription（音频→文字）、Analysis（文字→KnowledgeCard）、QA（RAG 问答）。`active_provider` 全局开关，运行时实时切换。

### 处理流水线状态机
`unprocessed → queued → transcribing → transcribed → analyzing → processed`，任一步失败进 `failed`。`failed` 状态可重新入队。乐观锁保证同一 source 不重复处理。

## 与 Podwise 的映射
CloudWisePod 复刻 Podwise 的核心体验：RSS 订阅 → 转录 → AI 知识卡片 → 播放器时间戳联动 + Q&A 带引用。砍掉了多语言翻译、Notion/Readwise 集成、思维导图、CLI/MCP（自用非必需）。
