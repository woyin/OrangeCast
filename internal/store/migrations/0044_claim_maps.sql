-- C09（ADR-0024 §6）：ClaimMap——作品修订中实质性表达的主张身份映射。
-- 每条 entry 对应正文片段，记录主张类型与材料身份；program check 在写入前完成。

CREATE TABLE claim_maps (
    id            TEXT PRIMARY KEY,
    draft_id      TEXT NOT NULL,
    revision_id   TEXT NOT NULL,
    excerpt       TEXT NOT NULL,
    claim_kind    TEXT NOT NULL, -- source_claim | owner_claim | synthesis_claim | verified_fact
    material_ids_json TEXT NOT NULL DEFAULT '[]',
    source_title  TEXT NOT NULL DEFAULT '',
    citation_refs_json TEXT NOT NULL DEFAULT '[]',
    created_at    TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_claim_maps_draft ON claim_maps(draft_id, revision_id);
