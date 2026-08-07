# ADR-0008：知识卡片采用证据优先契约

KnowledgeCard 的摘要、关键要点、章节和金句都必须引用一个或多个 Transcript segment，模型只选择 segment 标识，时间范围由程序从 segment 解析；金句必须能在被引用原文中逐字核验，不接受模型估算时间戳或无依据补充。该决策以减少内容数量和增加校验、重试成本换取可追溯性，无法验证的内容宁可省略，也不进入证据库。

同一契约适用于单 Source 证据问答（升级后命名为 EvidenceQA，属 CitedDerivative）：回答必须引用实际参与回答的 Transcript segment，证据不足时明确拒答，不能自动附加未被模型引用的检索片段来伪造依据。EvidenceQA 是查证型辅助探索能力，不跨 Source。注意：本次信息分层升级（ADR-0018）新增的 StudyChat 属 GeneratedDerivative，挂 Reference 而非 Citation、不拒答，是与本契约不同的学习型对话；EvidenceQA 与 StudyChat 并存且不可混淆。两者下沉 KnowledgeNote 的规则见 ADR-0018。
