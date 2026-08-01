# ADR-0011：处理产物不可变并保留版本

每次 ProcessingJob 尝试生成新的不可变 Transcript 与 KnowledgeCard ArtifactVersion，记录 Provider、模型、Prompt 版本和创建时间；Source 显式指向当前采用的版本，允许 Owner 比较或回退，重新处理不得覆盖历史产物。该决策以少量文本与 JSON 存储和更复杂的版本选择换取可审计、可评测和可恢复性；已沉淀到个人知识库的 KnowledgeNote 不受版本切换影响。
