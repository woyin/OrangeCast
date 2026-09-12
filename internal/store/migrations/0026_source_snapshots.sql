-- B01（ADR-0024 §4）：按版本读取的来源快照。
-- 快照保存血缘身份（转录/文档版本 + EvidenceAudio 哈希），不复制正文；
-- 读取按冻结的确切版本解析：新版本成为 current 后，旧快照链接仍指向旧内容。
-- 同 ID 不同版本的 Segment 不视为同一证据；Purge 后快照标 purged（明确失效，
-- 保留行作为"依据已删除"的审计事实，不私存原文）。

CREATE TABLE source_snapshots (
    id                 TEXT PRIMARY KEY,
    source_type        TEXT NOT NULL,
    source_id          TEXT NOT NULL,
    kind               TEXT NOT NULL,              -- 'audio' | 'document'
    title              TEXT NOT NULL,
    content_version    INTEGER NOT NULL,            -- audio: transcript artifact 版本；document: documents.version
    content_version_id TEXT NOT NULL DEFAULT '',    -- audio: artifact_versions.id；document: documents.id（精确行身份）
    audio_sha256       TEXT NOT NULL DEFAULT '',    -- 冻结时 EvidenceAudio 哈希；document 为空
    legacy             INTEGER NOT NULL DEFAULT 0,  -- 1 = 无法可靠推定版本血缘（如音频身份缺失）
    status             TEXT NOT NULL DEFAULT 'active', -- active | missing_audio | purged
    created_at         TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(source_type, source_id, kind, content_version)
);
CREATE INDEX IF NOT EXISTS idx_source_snapshots_source ON source_snapshots(source_type, source_id);
