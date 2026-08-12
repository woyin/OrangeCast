-- Migration 0016: 内容生产工作台的持久化底座（ADR-0021 / roadmap Phase 8）
--
-- 文章编辑历史独立于 artifact_versions：前者包含 Owner 手工编辑和审校语义，
-- 后者只记录 ProcessingJob 产生的证据和分析产物。

CREATE TABLE IF NOT EXISTS editorial_profiles (
    id                   TEXT PRIMARY KEY,
    name                 TEXT NOT NULL UNIQUE,
    target_audience      TEXT NOT NULL DEFAULT '',
    voice                TEXT NOT NULL DEFAULT '',
    style_guide          TEXT NOT NULL DEFAULT '',
    source_attribution   TEXT NOT NULL DEFAULT 'standard',
    monthly_budget_cents INTEGER,
    created_at           TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at           TEXT NOT NULL DEFAULT (datetime('now'))
);

-- SourceScope 首版精确授权到具体 Source；后续 Theme/语言/时间规则可在不破坏该语义的前提下扩展。
CREATE TABLE IF NOT EXISTS editorial_source_scopes (
    editorial_profile_id TEXT NOT NULL REFERENCES editorial_profiles(id) ON DELETE CASCADE,
    source_type          TEXT NOT NULL,
    source_id            TEXT NOT NULL,
    created_at           TEXT NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (editorial_profile_id, source_type, source_id)
);
CREATE INDEX IF NOT EXISTS idx_editorial_scope_source ON editorial_source_scopes(source_type, source_id);

-- 用途权限和模型数据策略是独立约束。默认保守：新既有来源可被发送到外部模型，
-- 但不会自动进入任何 EditorialProfile 的 SourceScope。
ALTER TABLE episodes ADD COLUMN production_use TEXT NOT NULL DEFAULT 'public';
ALTER TABLE episodes ADD COLUMN model_data_policy TEXT NOT NULL DEFAULT 'external_allowed';
ALTER TABLE episodes ADD COLUMN archived_at TEXT;
ALTER TABLE uploads ADD COLUMN production_use TEXT NOT NULL DEFAULT 'public';
ALTER TABLE uploads ADD COLUMN model_data_policy TEXT NOT NULL DEFAULT 'external_allowed';
ALTER TABLE uploads ADD COLUMN archived_at TEXT;

CREATE TABLE IF NOT EXISTS article_proposals (
    id                   TEXT PRIMARY KEY,
    editorial_profile_id TEXT NOT NULL REFERENCES editorial_profiles(id) ON DELETE CASCADE,
    kind                 TEXT NOT NULL,
    status               TEXT NOT NULL DEFAULT 'proposed',
    title                TEXT NOT NULL,
    thesis               TEXT NOT NULL DEFAULT '',
    audience             TEXT NOT NULL DEFAULT '',
    rationale            TEXT NOT NULL DEFAULT '',
    candidate_keypoints_json TEXT NOT NULL DEFAULT '[]',
    created_at           TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at           TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_article_proposals_profile_status ON article_proposals(editorial_profile_id, status, created_at DESC);

CREATE TABLE IF NOT EXISTS article_briefs (
    id                   TEXT PRIMARY KEY,
    proposal_id          TEXT NOT NULL REFERENCES article_proposals(id) ON DELETE CASCADE,
    status               TEXT NOT NULL DEFAULT 'draft',
    thesis               TEXT NOT NULL DEFAULT '',
    audience             TEXT NOT NULL DEFAULT '',
    outline_markdown     TEXT NOT NULL DEFAULT '',
    material_plan_json   TEXT NOT NULL DEFAULT '[]',
    conflict_plan_json   TEXT NOT NULL DEFAULT '[]',
    style                TEXT NOT NULL DEFAULT '',
    target_length        INTEGER,
    confirmed_at         TEXT,
    created_at           TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at           TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_article_briefs_proposal ON article_briefs(proposal_id, created_at DESC);

CREATE TABLE IF NOT EXISTS article_drafts (
    id                   TEXT PRIMARY KEY,
    editorial_profile_id TEXT NOT NULL REFERENCES editorial_profiles(id) ON DELETE RESTRICT,
    brief_id             TEXT NOT NULL REFERENCES article_briefs(id) ON DELETE RESTRICT,
    title                TEXT NOT NULL DEFAULT '',
    current_revision_id  TEXT,
    status               TEXT NOT NULL DEFAULT 'drafting',
    created_at           TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at           TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_article_drafts_profile_status ON article_drafts(editorial_profile_id, status, updated_at DESC);

CREATE TABLE IF NOT EXISTS article_revisions (
    id                   TEXT PRIMARY KEY,
    draft_id             TEXT NOT NULL REFERENCES article_drafts(id) ON DELETE CASCADE,
    version              INTEGER NOT NULL,
    title                TEXT NOT NULL DEFAULT '',
    markdown             TEXT NOT NULL,
    origin               TEXT NOT NULL,
    provider             TEXT,
    model                TEXT,
    prompt_version       TEXT,
    cost_cents           INTEGER,
    created_at           TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(draft_id, version)
);
CREATE INDEX IF NOT EXISTS idx_article_revisions_draft ON article_revisions(draft_id, version DESC);

CREATE TABLE IF NOT EXISTS evidence_maps (
    id                   TEXT PRIMARY KEY,
    revision_id          TEXT NOT NULL REFERENCES article_revisions(id) ON DELETE CASCADE,
    kind                 TEXT NOT NULL,
    excerpt              TEXT NOT NULL DEFAULT '',
    keypoint_ids_json    TEXT NOT NULL DEFAULT '[]',
    created_at           TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_evidence_maps_revision ON evidence_maps(revision_id);

CREATE TABLE IF NOT EXISTS article_reviews (
    id                   TEXT PRIMARY KEY,
    revision_id          TEXT NOT NULL REFERENCES article_revisions(id) ON DELETE CASCADE,
    kind                 TEXT NOT NULL,
    status               TEXT NOT NULL,
    issues_json          TEXT NOT NULL DEFAULT '[]',
    provider             TEXT,
    model                TEXT,
    created_at           TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_article_reviews_revision_kind ON article_reviews(revision_id, kind, created_at DESC);

CREATE TABLE IF NOT EXISTS editorial_feedback (
    id                   TEXT PRIMARY KEY,
    editorial_profile_id TEXT NOT NULL REFERENCES editorial_profiles(id) ON DELETE CASCADE,
    entity_type          TEXT NOT NULL,
    entity_id            TEXT NOT NULL,
    action               TEXT NOT NULL,
    reason               TEXT NOT NULL DEFAULT '',
    details_json         TEXT NOT NULL DEFAULT '{}',
    created_at           TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_editorial_feedback_profile_entity ON editorial_feedback(editorial_profile_id, entity_type, entity_id, created_at DESC);
