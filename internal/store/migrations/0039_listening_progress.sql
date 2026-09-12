-- D07（ADR-0024 §3）：听播进度持久化。
-- 每个 Source 一行（UNIQUE）；seq 单调递增：旧序号请求不覆盖新状态
-- （多标签/乱序请求防护）。完整播放（原音页）与 DJ 清单进度语义分开，
-- 通过 plan_id/plan_version 区分（空 = 非清单播放）。

CREATE TABLE listening_progress (
    id                   TEXT PRIMARY KEY,
    source_type          TEXT NOT NULL,
    source_id            TEXT NOT NULL,
    plan_id              TEXT NOT NULL DEFAULT '',
    plan_version         INTEGER NOT NULL DEFAULT 0,
    item_position        INTEGER NOT NULL DEFAULT 0,
    highlight_id         TEXT NOT NULL DEFAULT '',
    item_offset_seconds  REAL NOT NULL DEFAULT 0,
    speed                REAL NOT NULL DEFAULT 1,
    seq                  INTEGER NOT NULL DEFAULT 0,
    updated_at           TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(source_type, source_id)
);
