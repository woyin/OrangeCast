-- R17（C07）：CreationBrief 不可变修订与精确确认版本。
ALTER TABLE creation_briefs ADD COLUMN current_version INTEGER NOT NULL DEFAULT 1;
ALTER TABLE creation_briefs ADD COLUMN confirmed_version INTEGER NOT NULL DEFAULT 0;

CREATE TABLE creation_brief_revisions (
    id TEXT PRIMARY KEY,
    brief_id TEXT NOT NULL REFERENCES creation_briefs(id) ON DELETE CASCADE,
    version INTEGER NOT NULL,
    origin_job_id TEXT NOT NULL DEFAULT '',
    owner_claim TEXT NOT NULL,
    claim_plan_json TEXT NOT NULL DEFAULT '[]',
    material_plan_json TEXT NOT NULL DEFAULT '[]',
    research_need_ids_json TEXT NOT NULL DEFAULT '[]',
    outline TEXT NOT NULL DEFAULT '',
    style TEXT NOT NULL DEFAULT '',
    target_length INTEGER,
    claim_type TEXT NOT NULL DEFAULT '',
    unresolved_questions_json TEXT NOT NULL DEFAULT '[]',
    notes TEXT NOT NULL DEFAULT '',
    curator_prompt_version TEXT NOT NULL DEFAULT '',
    curator_input_snapshot_json TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(brief_id, version)
);
CREATE UNIQUE INDEX idx_creation_brief_revisions_job ON creation_brief_revisions(brief_id, origin_job_id) WHERE origin_job_id != '';

-- 旧 brief 内容列保留兼容读取，但 revisions/current_version 成为 R17 真源；
-- 已确认旧 brief 的 confirmed_version 必须回填 1，draft 保持 0，不能丢授权。
INSERT INTO creation_brief_revisions
(id, brief_id, version, origin_job_id, owner_claim, claim_plan_json, material_plan_json,
 research_need_ids_json, outline, style, target_length)
SELECT lower(hex(randomblob(16))), id, 1, '', owner_claim, claim_plan_json, material_plan_json,
       research_need_ids_json, outline, style, target_length
FROM creation_briefs;

UPDATE creation_briefs SET current_version=1;
UPDATE creation_briefs SET confirmed_version=CASE WHEN status='confirmed' THEN 1 ELSE 0 END;
