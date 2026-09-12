-- ADR-0023: EpisodeDigest 单集精读文——一键成文、封闭集合、程序化门禁。
-- 版本空间与 narrations 同构：二维 (source_type, source_id) × version，
-- current 取 MAX(version)，不加 is_current 指针。

CREATE TABLE episode_digests (
    id             TEXT PRIMARY KEY,
    source_type    TEXT NOT NULL,
    source_id      TEXT NOT NULL,
    version        INTEGER NOT NULL,
    title          TEXT NOT NULL,
    degraded       INTEGER NOT NULL DEFAULT 0,
    provider       TEXT NOT NULL,
    model          TEXT NOT NULL,
    prompt_version TEXT NOT NULL,
    created_at     TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(source_type, source_id, version)
);
CREATE INDEX idx_episode_digests_source ON episode_digests(source_type, source_id, version);

-- 正文构造单元：类型身份在生成时由程序赋予（DigestBlock，ADR-0023 §2）。
-- citations_json 指向 target_source_id 的 Segment ID 列表；target 为空表示本集 Source。
CREATE TABLE digest_blocks (
    id               TEXT PRIMARY KEY,
    digest_id        TEXT NOT NULL REFERENCES episode_digests(id) ON DELETE CASCADE,
    position         INTEGER NOT NULL,
    block_type       TEXT NOT NULL,
    text             TEXT NOT NULL,
    citations_json   TEXT NOT NULL DEFAULT '[]',
    target_source_id TEXT NOT NULL DEFAULT '',
    note_id          TEXT NOT NULL DEFAULT '',
    created_at       TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(digest_id, position)
);
CREATE INDEX idx_digest_blocks_digest ON digest_blocks(digest_id);

-- ⑥b 侧栏：本次生成经 SourceSearch 沉淀的 Document Source，发布前逐条确认或剔除。
CREATE TABLE digest_search_sources (
    id          TEXT PRIMARY KEY,
    digest_id   TEXT NOT NULL REFERENCES episode_digests(id) ON DELETE CASCADE,
    query       TEXT NOT NULL,
    url         TEXT NOT NULL,
    title       TEXT NOT NULL,
    document_id TEXT NOT NULL,
    status      TEXT NOT NULL DEFAULT 'pending',
    created_at  TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(digest_id, url)
);
CREATE INDEX idx_digest_search_sources ON digest_search_sources(digest_id, status);

-- 未消解的 FactGap：搜索不可用（C1 降级）或未命中时保留，供草稿页展示与日后补齐。
CREATE TABLE digest_fact_gaps (
    id          TEXT PRIMARY KEY,
    digest_id   TEXT NOT NULL REFERENCES episode_digests(id) ON DELETE CASCADE,
    text        TEXT NOT NULL,
    document_id TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(digest_id, text)
);
CREATE INDEX idx_digest_fact_gaps ON digest_fact_gaps(digest_id);

-- DigestRewrite 渠道语气版本（F3）：长文版是唯一事实源，渠道版本是其语气投影。
CREATE TABLE digest_rewrites (
    id         TEXT PRIMARY KEY,
    digest_id  TEXT NOT NULL REFERENCES episode_digests(id) ON DELETE CASCADE,
    channel    TEXT NOT NULL,
    text       TEXT NOT NULL,
    provider   TEXT NOT NULL,
    model      TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(digest_id, channel)
);
