# ADR-0010：统一持久数据目录并验证备份恢复

SQLite 数据库、EvidenceAudio 及其他 EvidenceArchive 持久文件统一位于一个明确的 `DATA_DIR`；CloudWisePod 提供命令生成包含 SQLite 一致性快照与文件清单的可移植备份包，并以全新实例恢复测试作为发布门槛。首版不内置云端备份或定时同步，以单目录可搬迁性和可验证恢复换取低运维复杂度。
