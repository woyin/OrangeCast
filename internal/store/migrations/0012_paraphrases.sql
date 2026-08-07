-- Migration 0012: Paraphrase 复述讲解（GeneratedDerivative，ADR-0018 R2）
--
-- Paraphrase 是 Owner 按需触发的局部重讲，属 GeneratedDerivative，挂 Reference（relation_kind='reference'）。
-- 它不纳入 ArtifactVersion（触发模式高频、试错，与 ProcessingJob 低频有意重新分析不同），
-- 而是独立轻量表，按锚点（anchor = 排序后的 segment_ids 串）保留最近 3 次：
-- 同一锚点多次触发互相淘汰最旧的，不同锚点独立保留。
--
-- 与 annotations/pins/collection_items 不同，此表的 relation_kind 恒为 'reference'（写入层强制）。

CREATE TABLE IF NOT EXISTS paraphrases (
    id             TEXT PRIMARY KEY,
    source_type    TEXT NOT NULL,
    source_id      TEXT NOT NULL,
    anchor         TEXT NOT NULL,             -- 排序后的 segment_ids 串，作为"同锚点淘汰"的键
    segment_ids    TEXT NOT NULL,             -- JSON 数组，Reference 指向的 Segment ID
    relation_kind  TEXT NOT NULL DEFAULT 'reference',
    time_start     REAL NOT NULL,
    time_end       REAL NOT NULL,
    question       TEXT NOT NULL DEFAULT '',  -- Owner 的疑问（可为空）
    body           TEXT NOT NULL,             -- AI 生成的复述讲解（非逐字原文）
    provider       TEXT NOT NULL DEFAULT '',
    model          TEXT NOT NULL DEFAULT '',
    created_at     TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_paraphrases_source ON paraphrases(source_type, source_id);
CREATE INDEX IF NOT EXISTS idx_paraphrases_anchor ON paraphrases(source_type, source_id, anchor, created_at);
