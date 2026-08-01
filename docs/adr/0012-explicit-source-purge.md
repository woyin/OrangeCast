# ADR-0012：Source 默认永久保留并支持显式彻底删除

Candidate 可以直接移除；Source、EvidenceAudio 与全部 ArtifactVersion 默认永久保留，不设自动清理期限。Owner 仍可在明确确认 Citation 将失效后执行 Purge，在一个一致性边界内删除 Source、证据音频、转录与知识卡片版本、处理任务和搜索索引；备份副本按独立轮换策略到期。该决策以默认磁盘增长换取证据长期可用，同时保留 Owner 主动清除私密内容的能力。
