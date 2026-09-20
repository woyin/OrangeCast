-- R19: durable ClaimWriter output identity and replay-safe revision persistence.
-- origin_job_id 把 ArticleRevision 绑定到产生它的持久任务；同 (draft, job) 只有一个修订。
ALTER TABLE article_revisions ADD COLUMN origin_job_id TEXT NOT NULL DEFAULT '';
CREATE UNIQUE INDEX IF NOT EXISTS idx_article_revisions_origin_job ON article_revisions(draft_id, origin_job_id) WHERE origin_job_id != '';

-- claim_writing_intents：写作意图到任务的持久映射。processing_jobs 的部分唯一
-- 索引只约束 queued/running 任务；任务完成后重复点击/重放必须仍复用同一个任务。
-- intent_id 是幂等身份（claim_writing:<briefID>:v<version>），并发入队由主键唯一性
-- 兜底，不使用进程锁。不加外键：processing_jobs 不做级联删除，任务保留完整历史
-- （与 usage_records 同语义）；映射随来源 Brief/任务生命周期保留，不参与 Purge。
CREATE TABLE IF NOT EXISTS claim_writing_intents (
    intent_id  TEXT PRIMARY KEY,
    job_id     TEXT NOT NULL,
    draft_id   TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_claim_writing_intents_draft ON claim_writing_intents(draft_id);

-- claim_writing_drafts：Writer 专属草稿映射（每兼容 ArticleBrief 一个 Writer 草稿）。
-- 不对 article_drafts(brief_id) 加全局唯一索引：旧 schema/API 允许同 Brief 多草稿，
-- 全局唯一会让含历史重复的真实旧库升级失败；新路径的单草稿语义由本映射主键保证，
-- 历史重复草稿保持原样不受影响。无外键：Purge 不删除草稿，历史保留。
CREATE TABLE IF NOT EXISTS claim_writing_drafts (
    article_brief_id TEXT PRIMARY KEY,
    draft_id         TEXT NOT NULL,
    created_at       TEXT NOT NULL DEFAULT (datetime('now'))
);
