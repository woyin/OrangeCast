-- C01（ADR-0024 §1）：跨来源创作素材选择快照。
-- 一次选择 = KeyPoint（来源主张素材）与 OwnerNote（个人材料，不得转成来源主张）
-- 的显式集合，附来源范围与资格解释；供构思/简报读取。选择本身不触发任何付费调用。

CREATE TABLE creation_selections (
    id                 TEXT PRIMARY KEY,
    editorial_profile_id TEXT NOT NULL DEFAULT '',
    title              TEXT NOT NULL DEFAULT '',
    material_ids_json  TEXT NOT NULL DEFAULT '[]',   -- KeyPoint ID 列表
    note_ids_json      TEXT NOT NULL DEFAULT '[]',   -- OwnerNote ID 列表
    scope_json         TEXT NOT NULL DEFAULT '{}',   -- 来源范围：[{source_type,source_id}]
    excluded_json      TEXT NOT NULL DEFAULT '[]',   -- 被排除材料与原因（显式解释，不静默过滤）
    status             TEXT NOT NULL DEFAULT 'draft', -- draft | confirmed
    created_at         TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at         TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_creation_selections_profile ON creation_selections(editorial_profile_id, status);
