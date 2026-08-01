-- Migration 0001: baseline (锁定 v0.1.0 现有 schema 为迁移起点)
-- 全部使用 CREATE ... IF NOT EXISTS，因此：
--   - 全新数据库：建立全部 v0.1 表。
--   - 已存在的 v0.1.0 数据库：DDL 幂等，不修改已存在表，随后被记为 version=1。
-- 该迁移本身不改变业务行为，只是把"启动执行 schema.sql"替换为"有序迁移记录"。

CREATE TABLE IF NOT EXISTS users (
    id            TEXT PRIMARY KEY,
    email         TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    created_at    TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS sessions (
    token        TEXT PRIMARY KEY,
    user_id      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at   TEXT NOT NULL,
    created_at   TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_sessions_user ON sessions(user_id);

CREATE TABLE IF NOT EXISTS podcasts (
    id              TEXT PRIMARY KEY,
    user_id         TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    feed_url        TEXT NOT NULL,
    title           TEXT NOT NULL,
    description     TEXT,
    image_url       TEXT,
    last_fetched_at TEXT,
    created_at      TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(user_id, feed_url)
);
CREATE INDEX IF NOT EXISTS idx_podcasts_user ON podcasts(user_id);

CREATE TABLE IF NOT EXISTS episodes (
    id                TEXT PRIMARY KEY,
    user_id           TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    podcast_id        TEXT NOT NULL REFERENCES podcasts(id) ON DELETE CASCADE,
    guid              TEXT NOT NULL,
    title             TEXT NOT NULL,
    description       TEXT,
    audio_url         TEXT NOT NULL,
    duration_seconds  INTEGER,
    published_at      TEXT,
    processing_status TEXT NOT NULL DEFAULT 'unprocessed',
    created_at        TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(user_id, podcast_id, guid)
);
CREATE INDEX IF NOT EXISTS idx_episodes_status ON episodes(user_id, processing_status);

CREATE TABLE IF NOT EXISTS uploads (
    id                TEXT PRIMARY KEY,
    user_id           TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    original_filename TEXT NOT NULL,
    content_type      TEXT NOT NULL,
    size_bytes        INTEGER NOT NULL,
    duration_seconds  INTEGER,
    processing_status TEXT NOT NULL DEFAULT 'unprocessed',
    created_at        TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_uploads_status ON uploads(user_id, processing_status);

CREATE TABLE IF NOT EXISTS transcripts (
    id             TEXT PRIMARY KEY,
    user_id        TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    source_type    TEXT NOT NULL,
    source_id      TEXT NOT NULL,
    language       TEXT,
    plain_text     TEXT NOT NULL,
    segments_json  TEXT NOT NULL,
    created_at     TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(user_id, source_type, source_id)
);
CREATE INDEX IF NOT EXISTS idx_transcripts_source ON transcripts(user_id, source_type, source_id);

CREATE TABLE IF NOT EXISTS analyses (
    id            TEXT PRIMARY KEY,
    user_id       TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    source_type   TEXT NOT NULL,
    source_id     TEXT NOT NULL,
    title         TEXT NOT NULL,
    summary       TEXT NOT NULL,
    content_json  TEXT NOT NULL,
    created_at    TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(user_id, source_type, source_id)
);
CREATE INDEX IF NOT EXISTS idx_analyses_source ON analyses(user_id, source_type, source_id);

CREATE TABLE IF NOT EXISTS processing_jobs (
    id             TEXT PRIMARY KEY,
    user_id        TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    source_type    TEXT NOT NULL,
    source_id      TEXT NOT NULL,
    job_type       TEXT NOT NULL,
    status         TEXT NOT NULL DEFAULT 'queued',
    attempt_count  INTEGER NOT NULL DEFAULT 0,
    last_error     TEXT,
    created_at     TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at     TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_jobs_status ON processing_jobs(user_id, status);

CREATE TABLE IF NOT EXISTS usage_records (
    id              TEXT PRIMARY KEY,
    user_id         TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    operation       TEXT NOT NULL,
    provider        TEXT NOT NULL,
    model           TEXT,
    input_units     INTEGER,
    output_units    INTEGER,
    estimated_cost  REAL,
    created_at      TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS settings (
    user_id              TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    active_provider      TEXT NOT NULL DEFAULT 'groq',
   transcription_model  TEXT,
   analysis_model       TEXT,
   qa_model             TEXT,
    updated_at           TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE VIRTUAL TABLE IF NOT EXISTS search_index USING fts5(
    user_id UNINDEXED,
    source_type UNINDEXED,
    source_id UNINDEXED,
    title,
    body,
    tokenize = 'unicode61'
);
