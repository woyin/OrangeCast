-- D02：DJ 播放清单持久化。清单按 (source, version) 递增；旧清单不被替换。
CREATE TABLE dj_plans (
    id                  TEXT PRIMARY KEY,
    source_type         TEXT NOT NULL,
    source_id           TEXT NOT NULL,
    version             INTEGER NOT NULL,
    target_seconds      REAL NOT NULL DEFAULT 0,
    total_seconds       REAL NOT NULL DEFAULT 0,
    highlight_version   INTEGER NOT NULL,
    input_snapshot_json TEXT NOT NULL DEFAULT '',
    created_at          TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(source_type, source_id, highlight_version, version)
);
CREATE INDEX IF NOT EXISTS idx_dj_plans_source ON dj_plans(source_type, source_id);

CREATE TABLE dj_plan_items (
    id           TEXT PRIMARY KEY,
    plan_id      TEXT NOT NULL REFERENCES dj_plans(id) ON DELETE CASCADE,
    position     INTEGER NOT NULL,
    kind         TEXT NOT NULL,
    highlight_id TEXT NOT NULL DEFAULT '',
    narration_id TEXT NOT NULL DEFAULT '',
    segment_ids_json TEXT NOT NULL DEFAULT '[]',
    start_seconds REAL NOT NULL DEFAULT 0,
    end_seconds   REAL NOT NULL DEFAULT 0,
    est_seconds   REAL NOT NULL DEFAULT 0,
    reason        TEXT NOT NULL DEFAULT '',
    UNIQUE(plan_id, position)
);
