---
status: superseded by ADR-0022
date: 2026-08-12
superseded_date: 2026-08-18
---

# ADR-0021：转型为以证据为底座的播客内容生产工作台

> 历史决策：本 ADR 完成了从纯学习工具到内容生产的转型；产品形态、发现模型、SourceScope 与审校语义已由 [ADR-0022](0022-learning-creation-workspaces.md) 修订。实现仍处于迁移期，因此旧对象和路由可能暂时存在。

CloudWisePod 的主定位从“播客学习平台”转为“播客内容生产工作台”。EvidenceArchive 不再是产品终点，而是选题与写作的可信底座；产品主闭环改为从跨 Source KeyPoint 主动发现 ArticleProposal，经 Owner 确认 ArticleBrief 后，由多角色模型完成写作、证据审校、风格审校与修订，最终生成由 Owner 手工发布的 PublicationPackage。作出这一选择，是因为转写与单集摘要已经产生高成本材料，却缺少跨集复用和持续发布的出口；保留学习平台为主会继续让素材价值停在单集消费阶段。

## 决策

1. **KeyPoint 成为生产原子**：统一素材 Inbox、Theme、语义检索、提案和 Brief 都围绕 KeyPoint；Summary 只作 Source 背景，Highlight 只作音频回听。Owner 可手工创建 KeyPoint，人工成果不被重新分析覆盖。
2. **采用两次 Owner 决策点**：系统可主动发现 ArticleProposal；Owner 接受后生成 ArticleBrief；只有 Owner 确认 Brief，系统才生成全文。系统不自动发布。
3. **文章采用不可变修订历史**：ArticleDraft 持续存在，AI 与人工修改都创建 ArticleRevision；审校状态只属于确切 Revision，PublicationPackage 只能从通过硬性证据门槛的当前 Revision 生成。
4. **文章证据关系类型化**：EvidenceMap 区分 Quoted、Paraphrased、Synthesized 与 Rhetorical。跨来源综合属于 GeneratedDerivative，不能伪装成来源直接支持；首版禁止 ExternalFact。Citation 只证明来源表达过，不代表客观事实已经验证。
5. **多角色、多模型和付费成为常规能力**：Scout、Curator、Writer、EvidenceReviewer、StyleEditor 等角色独立路由 Provider/模型。付费模型可用于任何阶段；提案可在预算内自主调用，Owner 确认 Brief 即授权单篇流水线。费用、模型和 Prompt 来历必须可见。
6. **解除跨 Source 语义限制**：Embedding、语义检索和跨 Source RAG 成为选题、去重和选材的核心基础设施，但只负责候选召回；最终依据仍必须落到 KeyPoint、Citation 与 PrimarySource。单 Source EvidenceQA 的严格范围不变。
7. **允许受控自动摄取**：Podcast 可配置 Manual、AllNew 或 Filtered IngestionPolicy；自动流程只生成写作所需证据与 KeyPoint，并受预算约束。
8. **支持多品牌且隔离素材和模型数据**：每个 ArticleProposal 属于一个 EditorialProfile。SourceScope 决定素材能否用于某品牌；ModelDataPolicy 独立决定素材能否发送给云端 Provider，混合任务继承最严格策略。
9. **分期扩展 Source**：V1 只使用播客事实材料；V1.1 将 URL、PDF 和粘贴文本升级为一等 Document Source，保存 EvidenceDocument。其他音视频随后接入，自主联网研究最后引入；任何外部材料都必须先成为 Source。
10. **保留单 Owner 和人工发布**：现有学习能力降为次级研究区但不删除；首版使用 Markdown 编辑、公众号预览与复制/导出，不接微信 API、不做团队审批、不自动群发。
11. **提案供给与单集深读必须显式受控**：每次 Scout 头脑风暴生成 5 条候选；提案池低于 5 条时，仅在 Owner 让提案离开池或主动触发补货后后台补货，普通 GET 不调用 Provider。跨 Episode 模式仍要求每条候选覆盖至少两个 Episode；DeepRead 是 Owner 明确选择一个 Episode 后的独立提案类型，只允许使用该 Episode 的 KeyPoint，不改变默认跨集规则。

## 取代与修订

- **取代 ADR-0009**：默认零成本和每次付费尝试单独授权不再成立，改为角色路由、全局/画像/单篇预算与 Brief 授权点；不得静默切换到未配置模型。
- **修订 ADR-0012**：已被 PublishedArticle 使用的 Source 默认阻止 Purge，但 Owner 可为隐私或合规强制删除；系统不得私留原文副本，只保留证据已删除的审计事实。
- **取代 ADR-0016 中“CloudWisePod 不是内容生产工具”的理由**：Highlight 的证据语义和音频体验仍有效，但不再定义产品主边界。
- **取代 ADR-0018 中“默认零成本、不做跨 Source RAG”的不变项**：CitedDerivative / GeneratedDerivative、Citation / Reference 和 EvidenceQA 的信任契约继续有效。
- **扩展 ADR-0017**：KeyPoint 全局索引从只读投影升级为生产素材层，增加生产状态、人工 KeyPoint、Theme 和文章使用关系；具体 schema 仍由后续设计决定。
- **保留 ADR-0003 的单 Owner、自托管边界**；不建设多用户、团队协作或 SaaS。

## 后果

- 主导航、产品目标、数据模型和备份边界都以内容生产为优先；学习功能只维持和复用。
- 新增的人工编辑成果、ArticleRevision、EvidenceMap、审校结果、画像、反馈和选定视觉素材属于不可再生核心数据，必须备份并可在 Provider 不可用时阅读与导出。
- EvidenceReviewer 对无依据事实、归因、译引、直接引语、冲突处理和失效证据执行硬门禁；StyleEditor 的建议可由 Owner 覆盖。
- 文档来源、对齐翻译、Speaker 身份、视觉生成、发布表现和联网 FactCheck 按路线图分期实现，不能借“未来能力”放宽 V1 的证据要求。
