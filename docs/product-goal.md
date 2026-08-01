# CloudWisePod 产品目标

状态：已确认  
日期：2026-08-01

## 一句话目标

CloudWisePod 是单 Owner、自托管、默认零 AI 成本的播客证据库：它将 Owner 主动选择的音频可靠地转化为持久、可检索、可回听且所有衍生内容都有真实引用的证据，并通过 Markdown 单向沉淀为个人知识库中的 KnowledgeNote。

## 要解决的问题

播客内容难以快速判断价值，也难以在未来检索、复用和回到原话核验。CloudWisePod 的价值不是“生成更多 AI 摘要”，而是缩短从听到内容到形成可信知识笔记的路径，同时保留完整证据链。

## 核心成功标准

Owner 应能在五分钟内判断一集已处理播客是否值得保留；值得保留的内容可以一键下载为 Obsidian 友好的 Markdown，并且摘要、关键要点、章节和金句都能跳回对应 EvidenceAudio 核验。

“KnowledgeCard 生成成功”只是中间状态。真正完成的闭环是：

```text
自动发现 Candidate
        ↓ Owner 选择
      Source
        ↓ 可靠处理
EvidenceAudio + Transcript + Citation + KnowledgeCard
        ↓ Owner 判断值得保留
KnowledgeNote → PersonalKnowledgeBase
```

## 产品边界

- 一个实例只有一个 Owner，首次认领后永久关闭注册。
- 自动刷新 RSS 并发现 Candidate，但不自动执行 AI 处理。
- Upload 与被选择的 Episode 进入同一套 Source 处理流程。
- CloudWisePod 是 EvidenceArchive；Obsidian 等外部系统是 PersonalKnowledgeBase。
- KnowledgeNote 通过确定性 Markdown 单向沉淀；CloudWisePod 不编辑或同步外部笔记。
- Groq 是默认零成本 Provider；付费 Provider 只允许 Owner 对单次任务显式授权。
- 本地 SQLite FTS 搜索是核心能力；单 Source Q&A 是次要探索能力。
- 默认按可能暴露在公网的 VPS 服务建立安全基线，HTTPS 由受信任反向代理终止。

## 质量与可靠性门槛

- 每个 Source 长期保存可独立播放的标准化 EvidenceAudio。
- ProcessingJob 持久化，程序重启后自动恢复，产物写入保持幂等。
- Transcript 与 KnowledgeCard 作为不可变 ArtifactVersion 保存，可比较和回退。
- KnowledgeCard 的摘要、关键要点、章节和金句全部绑定真实 Segment；金句逐字可核验。
- Q&A 无可靠 Citation 时明确拒答，不附加虚假依据。
- 模型、Prompt 或分块变更必须通过小型内容质量 EvalSet。
- 所有持久数据位于统一 `DATA_DIR`，备份必须能在全新实例恢复。
- Source 默认永久保留；只有 Owner 明确执行 Purge 才完整删除。
- 所有写请求有 CSRF 防护，登录有限流，外部抓取逐跳防 SSRF。

## 下一版本黄金旅程

在全新实例完成以下流程即视为核心目标达成：

1. 认领唯一 Owner。
2. 添加 RSS 并自动发现 Candidate。
3. 手动选择一集约 60 分钟的 Episode。
4. 处理期间重启程序，ProcessingJob 自动恢复。
5. 生成持久 EvidenceAudio、Transcript 和全部带真实 Citation 的 KnowledgeCard。
6. 全文搜索命中 Transcript 并跳转到对应音频位置。
7. 一键下载带 Citation 链接的 Obsidian Markdown。
8. 生成备份包，并在另一全新实例成功恢复。

整条旅程默认只使用 Groq，不产生付费调用；Q&A 不阻塞发布。

## 明确不做

- 多用户、租户隔离、团队协作、SaaS、支付或套餐。
- 完整复刻 Podwise。
- 自动处理所有新 Episode。
- Embedding、向量数据库、语义搜索或跨 Source RAG。
- 将 Q&A 回答自动写入 KnowledgeNote。
- Obsidian 插件、直接写入 Vault、Git/WebDAV 双向同步。
- 静默切换到付费 Provider。
- 内置云备份、定时云同步或自行终止 TLS。

## 文档优先级

如文档之间冲突，按以下顺序判断：

1. 本文与 [`CONTEXT.md`](../CONTEXT.md)。
2. [`docs/adr/`](adr/) 中未被取代的决策。
3. [`implementation-roadmap.md`](implementation-roadmap.md)。
4. [`README.md`](../README.md) 中对当前版本的说明。

Cloudflare/TypeScript 时代的设计、计划和部署文档仅保留为历史资料，不再定义产品方向。
