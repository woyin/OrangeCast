-- Migration 0008: KeyPoint 全局索引 + Owner 标注体系（ADR-0017）

-- KeyPoint 物化索引（只读投影，真理来源是 artifact_versions.payload）
CREATE TABLE IF NOT EXISTS keypoint_index (
    id             TEXT PRIMARY KEY,
    source_type    TEXT NOT NULL,
    source_id      TEXT NOT NULL,
    source_title   TEXT NOT NULL,
    content        TEXT NOT NULL,
    description    TEXT NOT NULL DEFAULT '',
    citations_json TEXT NOT NULL,
    time_start     REAL NOT NULL,
    time_end       REAL NOT NULL,
    card_version   INTEGER NOT NULL,
    created_at     TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_keypoints_source ON keypoint_index(source_type, source_id);
CREATE INDEX IF NOT EXISTS idx_keypoints_created ON keypoint_index(created_at DESC);

-- KeyPoint 全文搜索（FTS5）
CREATE VIRTUAL TABLE IF NOT EXISTS keypoint_search USING fts5(
    keypoint_id UNINDEXED,
    content,
    description,
    source_title,
    tokenize = 'unicode61'
);

-- Annotation（Owner 在一组 Segment 上的个人标注，ADR-0017）
CREATE TABLE IF NOT EXISTS annotations (
    id             TEXT PRIMARY KEY,
    source_type    TEXT NOT NULL,
    source_id      TEXT NOT NULL,
    segment_ids    TEXT NOT NULL,
    time_start     REAL NOT NULL,
    time_end       REAL NOT NULL,
    body           TEXT NOT NULL,
    created_at     TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at     TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(source_type, source_id, segment_ids)
);
CREATE INDEX IF NOT EXISTS idx_annotations_source ON annotations(source_type, source_id);

-- Pin（收藏标记）
CREATE TABLE IF NOT EXISTS pins (
    source_type    TEXT NOT NULL,
    source_id      TEXT NOT NULL,
    segment_ids    TEXT NOT NULL,
    time_start     REAL NOT NULL,
    time_end       REAL NOT NULL,
    source_title   TEXT NOT NULL DEFAULT '',
    note           TEXT,
    created_at     TEXT NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (source_type, source_id, segment_ids)
);
CREATE INDEX IF NOT EXISTS idx_pins_created ON pins(created_at DESC);

-- Collection（跨 Source 主题集合）
CREATE TABLE IF NOT EXISTS collections (
    id             TEXT PRIMARY KEY,
    title          TEXT NOT NULL,
    description    TEXT,
    created_at     TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at     TEXT NOT NULL DEFAULT (datetime('now'))
);

-- Collection 成员（每个成员是一组 Segment 引用）
CREATE TABLE IF NOT EXISTS collection_items (
    collection_id  TEXT NOT NULL REFERENCES collections(id) ON DELETE CASCADE,
    source_type    TEXT NOT NULL,
    source_id      TEXT NOT NULL,
    segment_ids    TEXT NOT NULL,
    time_start     REAL NOT NULL,
    time_end       REAL NOT NULL,
    source_title   TEXT NOT NULL DEFAULT '',
    note           TEXT,
    added_at       TEXT NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (collection_id, source_type, source_id, segment_ids)
);
CREATE INDEX IF NOT EXISTS idx_collection_items ON collection_items(collection_id);
