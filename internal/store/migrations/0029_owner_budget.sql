-- B04（ADR-0024 §5）：单 Owner 全局月度预算与自动处理日限额，以及在途预占。
-- monthly_budget_cents / auto_daily_job_limit 为 NULL 表示未配置（不启用该约束）。
-- budget_reservations 是任务级在途预估：held 计入月度占用；结算以实际费用替换；
-- 释放区分"未发生远端调用"与"远端结果未知"（后者保留已落账 receipt 计数）。

ALTER TABLE settings ADD COLUMN monthly_budget_cents INTEGER;
ALTER TABLE settings ADD COLUMN auto_daily_job_limit INTEGER;

CREATE TABLE budget_reservations (
    id                 TEXT PRIMARY KEY,
    job_id             TEXT NOT NULL,
    operation          TEXT NOT NULL DEFAULT '',
    estimated_cost_cents INTEGER NOT NULL DEFAULT 0,
    status             TEXT NOT NULL DEFAULT 'held', -- held | settled | released_no_call | released_unknown
    reason             TEXT NOT NULL DEFAULT '',
    actual_cost_cents  INTEGER,
    created_at         TEXT NOT NULL DEFAULT (datetime('now')),
    settled_at         TEXT,
    UNIQUE(job_id)
);
CREATE INDEX IF NOT EXISTS idx_budget_reservations_status ON budget_reservations(status);

-- 修复：digest_* 任务允许无画像记账（0023 的 FK 使空 profile 行无法插入，
-- 违背 ADR-0023"精读文计入月度预算池"）。重建表去掉 profile 外键；
-- Owner 确认的画像行为不受影响（非 digest 任务仍要求画像，由应用层校验）。
CREATE TABLE editorial_usage_records_new (
    id                 TEXT PRIMARY KEY,
    editorial_profile_id TEXT NOT NULL DEFAULT '',
    article_draft_id   TEXT REFERENCES article_drafts(id) ON DELETE SET NULL,
    task_kind          TEXT NOT NULL,
    entity_type        TEXT NOT NULL,
    entity_id          TEXT NOT NULL,
    provider           TEXT NOT NULL,
    model              TEXT NOT NULL,
    prompt_version     TEXT NOT NULL,
    input_units        INTEGER NOT NULL,
    output_units       INTEGER NOT NULL,
    cost_cents         INTEGER NOT NULL,
    retry_count        INTEGER NOT NULL DEFAULT 0,
    fallback_from      TEXT,
    created_at         TEXT NOT NULL DEFAULT (datetime('now'))
);
INSERT INTO editorial_usage_records_new SELECT * FROM editorial_usage_records;
DROP TABLE editorial_usage_records;
ALTER TABLE editorial_usage_records_new RENAME TO editorial_usage_records;
CREATE INDEX IF NOT EXISTS idx_editorial_usage_profile_month ON editorial_usage_records(editorial_profile_id, created_at);
CREATE INDEX IF NOT EXISTS idx_editorial_usage_draft ON editorial_usage_records(article_draft_id, created_at);
