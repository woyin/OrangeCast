-- R20：独立主张/风格审校的恢复与幂等身份。
-- origin_job_id 把 claim_reviews / article_reviews 绑定到产生它的持久任务；
-- 同 (revision, job) 只落一条审校（重放不重复审校）。旧数据 origin_job_id=''，
-- 不受部分唯一索引约束，继续按既有兼容规则读取。
ALTER TABLE claim_reviews ADD COLUMN origin_job_id TEXT NOT NULL DEFAULT '';
ALTER TABLE article_reviews ADD COLUMN origin_job_id TEXT NOT NULL DEFAULT '';
CREATE UNIQUE INDEX IF NOT EXISTS idx_claim_reviews_origin_job ON claim_reviews(work_revision_id, origin_job_id) WHERE origin_job_id != '';
CREATE UNIQUE INDEX IF NOT EXISTS idx_article_reviews_origin_job ON article_reviews(revision_id, origin_job_id) WHERE origin_job_id != '';

-- revision_review_intents：精确 revision+kind → 持久任务的映射。processing_jobs 的
-- 部分唯一索引只约束 queued/running 任务；任务完成后（succeeded/failed）重复点击/
-- 重放必须仍定位同一个任务（succeeded 复用，failed 真实重试同 job）。intent_id 是
-- 幂等身份（claim_review:<revisionID> / style_review:<revisionID>），并发入队由主键
-- 唯一性兜底，不使用进程锁。不加外键：任务保留完整历史（与 usage_records 同语义）；
-- 映射随修订生命周期保留，不参与 Purge。
CREATE TABLE IF NOT EXISTS revision_review_intents (
    intent_id   TEXT PRIMARY KEY,
    job_id      TEXT NOT NULL,
    revision_id TEXT NOT NULL,
    kind        TEXT NOT NULL,
    created_at  TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_revision_review_intents_revision ON revision_review_intents(revision_id);
