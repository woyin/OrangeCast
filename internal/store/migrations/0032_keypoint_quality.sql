-- K02（ADR-0024 §5）：自动重点的可解释质量判定结果。
-- 结果按 (keypoint_id, content_fingerprint) 幂等持久化：同一内容重复判定不产生新行。
-- input_snapshot_json 保存判定依据（引用 Segment 原文等），可追溯、可复核。
-- 本迁移只持久化结果；K03 再消费结果更新 keypoint_index.quality_status。

CREATE TABLE keypoint_quality_results (
    id                   TEXT PRIMARY KEY,
    keypoint_id          TEXT NOT NULL,
    source_type          TEXT NOT NULL,
    source_id            TEXT NOT NULL,
    card_version         INTEGER NOT NULL,
    content_fingerprint  TEXT NOT NULL,
    decision             TEXT NOT NULL,              -- ready | needs_review | invalid
    reasons_json         TEXT NOT NULL DEFAULT '[]',
    input_snapshot_json  TEXT NOT NULL DEFAULT '',
    provider             TEXT NOT NULL DEFAULT '',
    model                TEXT NOT NULL DEFAULT '',
    job_id               TEXT NOT NULL DEFAULT '',
    created_at           TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(keypoint_id, content_fingerprint)
);
CREATE INDEX IF NOT EXISTS idx_kp_quality_source ON keypoint_quality_results(source_type, source_id, card_version);
