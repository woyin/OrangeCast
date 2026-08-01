-- Migration 0005: 不可变 ArtifactVersion（ADR-0011 / ADR-0008）
--
-- 变更：
--   1. artifact_versions：Transcript 与 KnowledgeCard 均作为不可变版本保存。
--      - 一次 ProcessingJob 尝试生成一个版本；重新处理不覆盖历史版本。
--      - 记录 provider / model / prompt_version / job_id / payload / created_at。
--      - payload 为 JSON：transcript 版本含带稳定 Segment ID 的 segments；
--        knowledge_card 版本含全部携带 Citation 的字段。
--   2. episodes / uploads 增加 current_transcript_version / current_card_version：
--      Source 显式指向当前采用版本，可回退到旧版本。
--   3. 旧的 transcripts / analyses 表保留为历史兼容视图（不再写入新数据）。

CREATE TABLE IF NOT EXISTS artifact_versions (
    id             TEXT PRIMARY KEY,
    source_type    TEXT NOT NULL,
    source_id      TEXT NOT NULL,
    kind           TEXT NOT NULL,             -- transcript | knowledge_card
    version        INTEGER NOT NULL,
    provider       TEXT NOT NULL,
    model          TEXT NOT NULL,
    prompt_version TEXT NOT NULL,
    job_id         TEXT NOT NULL REFERENCES processing_jobs(id),
    payload        TEXT NOT NULL,
    created_at     TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(source_type, source_id, kind, version)
);
CREATE INDEX IF NOT EXISTS idx_artifacts_source ON artifact_versions(source_type, source_id, kind, version);

ALTER TABLE episodes ADD COLUMN current_transcript_version INTEGER;
ALTER TABLE episodes ADD COLUMN current_card_version INTEGER;
ALTER TABLE uploads ADD COLUMN current_transcript_version INTEGER;
ALTER TABLE uploads ADD COLUMN current_card_version INTEGER;
