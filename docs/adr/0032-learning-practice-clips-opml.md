# ADR-0032：补齐 Podwise/Snipd 类学习闭环的四项缺口

2026-10-27，实施决策。来源：竞品行为记录见 [2026-09-12 调研](../research/2026-09-12-podwise-snipd.md)。

## 背景

调研记录确认 Podwise 与 Snipd 都把"同一份带时间戳的证据"复用到四个入口：可检索的知识产物、可迁移的订阅（OPML）、可自我检验的问题、可单独播放与分享的音频片段。CloudWisePod 已有转录、Citation、KeyPoint、DJ、精读文与回顾，但四项能力缺失或只有占位：没有 OPML 导入；没有练习问题（ReviewQuestion）；高光只有页面按钮，没有真实的片段音频；来源页与 MP3 命名的高光音频文件是历史残留。

## 决策

1. **练习问题（ReviewQuestion）是来源的现场产物。** 问题挂 Citation（必须指向当前转录版本中真实存在的 Segment，时间范围由程序解析），提示挂 Reference 并标注"AI 提示（非原文）"。生成是持久任务 `practice_review`，入队时冻结转录版本，同一版本重复入队命中同一意图；全部引用无效时不写库并显式失败。个人掌握度仍由既有回顾流程记录，不在此另立状态机。
2. **高光片段的音频由程序计算并缓存。** 区间取高光 Citation 的 min(start)–max(end)，用 ffmpeg 从 EvidenceAudio 切出 mp3/wav，按源音频 sha256 + 区间 + 格式做唯一缓存（`highlight_clips`），落在 `DATA_DIR/clips`，与 EvidenceAudio、Narration 物理隔离。命中缓存时不要求源文件在场；GET 只读缓存或本地转码，不调用远端模型。
3. **OPML 导入只完成发现。** 解析 OPML 2.0 大纲树，逐条复用既有 RSS 抓取与订阅写入；已存在的 feed 跳过，失败逐条报告。导入的订阅保持 `manual` 摄取策略与 `knowledge` 处理深度，`opml_present` 仅作展示来源，不扩大任何自动模型调用。
4. **不做翻译与思维导图。** 这两项被竞品列为标准能力，但当前没有实现，也不在本轮范围：Translation 需要按 Segment 对齐并作为 CitedDerivative 版本化，思维导图需要先定义与 Context 一致的产物身份；两者都不适合用"一处小改动"补齐。

## 后果

- 来源页、练习页与片段 URL 共享同一份 Citation/Reference 分级；问题、提示、片段都不冒充原文。
- 新增迁移 0093；片段与问题都在 Purge 时随来源清理，不进入备份的 Narration 语义也适用于片段（片段可按 sha256 重建）。
- 费用治理不变：练习问题走分析角色配置与既有预算/用量记账；片段与 OPML 不产生远端调用。
- 已知边界：片段下载需要本机可执行的 ffmpeg；中文/多语言问题质量取决于配置的分析模型与提示，仍以真实材料验收。

## 相关

- 领域词汇见 [CONTEXT.md](../../CONTEXT.md) 的 ReviewQuestion、HighlightClip。
- 竞品事实与本轮取舍记录见 [docs/research/2026-09-12-podwise-snipd.md](../research/2026-09-12-podwise-snipd.md)。

## 补充：翻译与结构导图（同日补齐）

首轮只补了练习问题、片段音频与 OPML。本轮继续补齐竞品明确列出的另两项：

5. **Translation（对齐译文）**：按 Segment 对齐翻译冻结的转录版本，身份 = 来源 + 转录版本 + 目标语言。译文是 CitedDerivative，只追加不覆盖原语言 Transcript；模型输出只接受 {"id":"译文"} 映射，未知 Segment 与空译文被丢弃，未译片段保留原文并显式标注。页面与只读 API 并列展示原文与译文，任何引用都必须标为“译文”。
6. **结构导图**：以已通过门禁的知识卡片的章节为骨架、要点为分支，节点时间全部由 Citation 解析，无法解析区间的节点不进入导图。导图是只读视图（/api/mindmap 与 /sources/{type}/{id}/mindmap），不调用模型、不新增原文以外的内容。

两者都进 Purge 清理；翻译任务走分析角色配置与既有预算/用量记账，导图不产生任何远端调用。
