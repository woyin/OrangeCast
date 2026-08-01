-- Migration 0002: 从内容模型移除租户所有权 (ADR-0007)
-- 依据：单 Owner 产品边界——内容天然属于实例，不再携带 user_id。
--
-- 调用约定：执行本迁移前，应用层必须先检查 users 行数：
--   0 个 → 保持未认领；1 个 → 升级为 Owner；>1 个 → 停止并要求显式选择。
-- 该守卫在 Go 代码 (RequireSafeForSingleOwner) 中执行，不在 SQL 里猜测。
--
-- 实现要点：
--   * SQLite 在 foreign_keys=ON 时执行 DROP TABLE 会对子表触发隐式 DELETE 级联，
--     因此必须先在 staging 表里保存 episode 数据，再重建 podcasts/episodes，
--     避免 DROP TABLE podcasts 把已复制的 episode 行清空。
--   * 删除用户级列（DROP COLUMN）前必须先删除依赖该列的索引。

-- podcasts: UNIQUE(user_id, feed_url) → UNIQUE(feed_url)
CREATE TABLE podcasts_new (
    id              TEXT PRIMARY KEY,
    feed_url        TEXT NOT NULL,
    title           TEXT NOT NULL,
    description     TEXT,
    image_url       TEXT,
    last_fetched_at TEXT,
    created_at      TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(feed_url)
);
INSERT INTO podcasts_new (id, feed_url, title, description, image_url, last_fetched_at, created_at)
SELECT id, feed_url, title, description, image_url, last_fetched_at, created_at FROM podcasts;

-- episodes 数据先落入 staging（无外键），防止 DROP TABLE podcasts 级联删除。
CREATE TABLE episodes_stage (
    id                TEXT PRIMARY KEY,
    podcast_id        TEXT NOT NULL,
    guid              TEXT NOT NULL,
    title             TEXT NOT NULL,
    description       TEXT,
    audio_url         TEXT NOT NULL,
    duration_seconds  INTEGER,
    published_at      TEXT,
    processing_status TEXT NOT NULL DEFAULT 'unprocessed',
    created_at        TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(podcast_id, guid)
);
INSERT INTO episodes_stage (id, podcast_id, guid, title, description, audio_url, duration_seconds, published_at, processing_status, created_at)
SELECT id, podcast_id, guid, title, description, audio_url, duration_seconds, published_at, processing_status, created_at FROM episodes;

-- 现在可以安全 DROP podcasts（隐式级联只会清空旧 episodes 表，数据已在 staging）。
DROP TABLE podcasts;
ALTER TABLE podcasts_new RENAME TO podcasts;
CREATE INDEX idx_podcasts_feed ON podcasts(feed_url);

-- 重建 episodes：podcast_id 仍引用 podcasts(id) ON DELETE CASCADE；guid 在 podcast 内唯一。
CREATE TABLE episodes_new (
    id                TEXT PRIMARY KEY,
    podcast_id        TEXT NOT NULL REFERENCES podcasts(id) ON DELETE CASCADE,
    guid              TEXT NOT NULL,
    title             TEXT NOT NULL,
    description       TEXT,
    audio_url         TEXT NOT NULL,
    duration_seconds  INTEGER,
    published_at      TEXT,
    processing_status TEXT NOT NULL DEFAULT 'unprocessed',
    created_at        TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(podcast_id, guid)
);
INSERT INTO episodes_new (id, podcast_id, guid, title, description, audio_url, duration_seconds, published_at, processing_status, created_at)
SELECT id, podcast_id, guid, title, description, audio_url, duration_seconds, published_at, processing_status, created_at FROM episodes_stage;
DROP TABLE episodes_stage;
DROP TABLE episodes;
ALTER TABLE episodes_new RENAME TO episodes;
CREATE INDEX idx_episodes_status ON episodes(processing_status);

-- uploads: 先删除依赖 user_id 的索引，再 DROP COLUMN（SQLite 3.35+）。
-- 外键 users(id) 随列一并删除；随后重建不含 user_id 的状态索引。
DROP INDEX IF EXISTS idx_uploads_status;
ALTER TABLE uploads DROP COLUMN user_id;
CREATE INDEX idx_uploads_status ON uploads(processing_status);

-- transcripts: UNIQUE(user_id, source_type, source_id) → UNIQUE(source_type, source_id)
CREATE TABLE transcripts_new (
    id             TEXT PRIMARY KEY,
    source_type    TEXT NOT NULL,
    source_id      TEXT NOT NULL,
    language       TEXT,
    plain_text     TEXT NOT NULL,
    segments_json  TEXT NOT NULL,
    created_at     TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(source_type, source_id)
);
INSERT INTO transcripts_new (id, source_type, source_id, language, plain_text, segments_json, created_at)
SELECT id, source_type, source_id, language, plain_text, segments_json, created_at FROM transcripts;
DROP TABLE transcripts;
ALTER TABLE transcripts_new RENAME TO transcripts;
CREATE INDEX idx_transcripts_source ON transcripts(source_type, source_id);

-- analyses: 同上
CREATE TABLE analyses_new (
    id            TEXT PRIMARY KEY,
    source_type   TEXT NOT NULL,
    source_id     TEXT NOT NULL,
    title         TEXT NOT NULL,
    summary       TEXT NOT NULL,
    content_json  TEXT NOT NULL,
    created_at    TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(source_type, source_id)
);
INSERT INTO analyses_new (id, source_type, source_id, title, summary, content_json, created_at)
SELECT id, source_type, source_id, title, summary, content_json, created_at FROM analyses;
DROP TABLE analyses;
ALTER TABLE analyses_new RENAME TO analyses;
CREATE INDEX idx_analyses_source ON analyses(source_type, source_id);

-- processing_jobs: 先删除依赖 user_id 的索引，再 DROP COLUMN（SQLite 3.35+）。
DROP INDEX IF EXISTS idx_jobs_status;
ALTER TABLE processing_jobs DROP COLUMN user_id;
CREATE INDEX IF NOT EXISTS idx_jobs_status ON processing_jobs(status);

-- usage_records: user_id 为普通列 + FK；直接 DROP COLUMN（无 user_id 索引）
ALTER TABLE usage_records DROP COLUMN user_id;

-- search_index: FTS 虚拟表不能 DROP COLUMN；重建。
CREATE VIRTUAL TABLE search_index_new USING fts5(
    source_type UNINDEXED,
    source_id UNINDEXED,
    title,
    body,
    tokenize = 'unicode61'
);
INSERT INTO search_index_new (source_type, source_id, title, body)
SELECT source_type, source_id, title, body FROM search_index;
DROP TABLE search_index;
ALTER TABLE search_index_new RENAME TO search_index;
