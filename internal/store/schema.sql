-- CloudWisePod schema (SQLite, 白纸重启)
-- 存储策略：元数据 + 内容全在 SQLite。transcript 分 plain_text（搜索/展示）+ segments_json（播放器联动）。

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

-- sources 多态抽象：source_type ∈ {episode, upload}，source_id 指向 episodes.id 或 uploads.id。
-- SQLite 无法对多态引用建 FK，应用层级联删除保证一致性（见 store 删除逻辑）。
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
    source_type    TEXT NOT NULL,  -- episode | upload
    source_id      TEXT NOT NULL,
    language       TEXT,
    plain_text     TEXT NOT NULL,  -- 纯文本，供 FTS 搜索与展示
    segments_json  TEXT NOT NULL,  -- [{start,end,text},...] 供播放器联动
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
    content_json  TEXT NOT NULL,  -- 完整 KnowledgeCard JSON
    created_at    TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(user_id, source_type, source_id)
);
CREATE INDEX IF NOT EXISTS idx_analyses_source ON analyses(user_id, source_type, source_id);

CREATE TABLE IF NOT EXISTS processing_jobs (
    id             TEXT PRIMARY KEY,
    user_id        TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    source_type    TEXT NOT NULL,
    source_id      TEXT NOT NULL,
    job_type       TEXT NOT NULL,  -- transcribe | analyze
    status         TEXT NOT NULL DEFAULT 'queued',  -- queued | running | succeeded | failed
    attempt_count  INTEGER NOT NULL DEFAULT 0,
    last_error     TEXT,
    created_at     TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at     TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_jobs_status ON processing_jobs(user_id, status);

CREATE TABLE IF NOT EXISTS usage_records (
    id              TEXT PRIMARY KEY,
    user_id         TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    operation       TEXT NOT NULL,  -- transcription | analysis | chat
    provider        TEXT NOT NULL,
    model           TEXT,
    input_units     INTEGER,
    output_units    INTEGER,
    estimated_cost  REAL,
    created_at      TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS settings (
    user_id              TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    active_provider      TEXT NOT NULL DEFAULT 'groq',  -- groq | openai，运行时实时切换
    transcription_model  TEXT,
    analysis_model       TEXT,
    qa_model             TEXT,
    updated_at           TEXT NOT NULL DEFAULT (datetime('now'))
);

-- 全文搜索：覆盖 transcript 正文 + analysis 摘要/标题
CREATE VIRTUAL TABLE IF NOT EXISTS search_index USING fts5(
    user_id UNINDEXED,
    source_type UNINDEXED,
    source_id UNINDEXED,
    title,
    body,
    tokenize = 'unicode61'
);
