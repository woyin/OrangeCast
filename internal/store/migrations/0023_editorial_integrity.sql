-- Migration 0023: make publication eligibility and paid editorial execution auditable.
ALTER TABLE article_revisions ADD COLUMN evidence_invalidated_at TEXT;
ALTER TABLE article_revisions ADD COLUMN evidence_invalidation_reason TEXT;

ALTER TABLE article_reviews ADD COLUMN prompt_version TEXT;
ALTER TABLE article_reviews ADD COLUMN cost_cents INTEGER;

ALTER TABLE article_proposals ADD COLUMN provider TEXT;
ALTER TABLE article_proposals ADD COLUMN model TEXT;
ALTER TABLE article_proposals ADD COLUMN prompt_version TEXT;
ALTER TABLE article_proposals ADD COLUMN cost_cents INTEGER;

ALTER TABLE editorial_profiles ADD COLUMN per_article_budget_cents INTEGER;
ALTER TABLE settings ADD COLUMN curator_provider TEXT;
ALTER TABLE settings ADD COLUMN curator_model TEXT;

-- Prices are Owner-managed because Provider pricing changes independently of
-- an application release. Rates are integer cents per million input/output units.
CREATE TABLE model_prices (
    provider                       TEXT NOT NULL,
    model                          TEXT NOT NULL,
    input_cents_per_million        INTEGER NOT NULL CHECK(input_cents_per_million >= 0),
    output_cents_per_million       INTEGER NOT NULL CHECK(output_cents_per_million >= 0),
    updated_at                     TEXT NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY(provider, model)
);

CREATE TABLE editorial_usage_records (
    id                 TEXT PRIMARY KEY,
    editorial_profile_id TEXT NOT NULL REFERENCES editorial_profiles(id) ON DELETE RESTRICT,
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
CREATE INDEX idx_editorial_usage_profile_month ON editorial_usage_records(editorial_profile_id, created_at);
CREATE INDEX idx_editorial_usage_draft ON editorial_usage_records(article_draft_id, created_at);

CREATE TABLE editorial_role_fallbacks (
    role TEXT PRIMARY KEY,
    provider TEXT NOT NULL,
    model TEXT NOT NULL,
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

-- One durable claim per confirmed Brief prevents double-clicks and retries from
-- starting several paid Writer calls for the same authorization.
CREATE TABLE editorial_task_claims (
    id              TEXT PRIMARY KEY,
    task_kind       TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    status          TEXT NOT NULL,
    attempt_count   INTEGER NOT NULL DEFAULT 1,
    last_error      TEXT,
    result_json     TEXT,
    lease_until     TEXT,
    created_at      TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at      TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(task_kind, idempotency_key)
);
CREATE INDEX idx_editorial_task_claims_status ON editorial_task_claims(status, lease_until);

-- Provider-specific approval is stored beside the source policy it qualifies.
ALTER TABLE episodes ADD COLUMN approved_providers_json TEXT NOT NULL DEFAULT '[]';
ALTER TABLE uploads ADD COLUMN approved_providers_json TEXT NOT NULL DEFAULT '[]';
ALTER TABLE documents ADD COLUMN approved_providers_json TEXT NOT NULL DEFAULT '[]';

ALTER TABLE podcasts ADD COLUMN ingestion_include_keywords TEXT NOT NULL DEFAULT '';
ALTER TABLE podcasts ADD COLUMN ingestion_exclude_keywords TEXT NOT NULL DEFAULT '';

ALTER TABLE documents ADD COLUMN series_id TEXT NOT NULL DEFAULT '';
ALTER TABLE documents ADD COLUMN version INTEGER NOT NULL DEFAULT 1;
UPDATE documents SET series_id=id WHERE series_id='';
CREATE UNIQUE INDEX idx_documents_series_version ON documents(series_id,version);

CREATE VIRTUAL TABLE document_search USING fts5(document_id UNINDEXED,title,content,tokenize='unicode61');
INSERT INTO document_search(document_id,title,content) SELECT id,title,content FROM documents;

CREATE TABLE document_knowledge_cards (
    document_id TEXT PRIMARY KEY REFERENCES documents(id) ON DELETE CASCADE,
    payload TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

-- Versioned, rebuildable retrieval projection; never a source of evidence.
CREATE TABLE keypoint_embeddings (
    keypoint_id TEXT PRIMARY KEY REFERENCES keypoint_index(id) ON DELETE CASCADE,
    provider TEXT NOT NULL,
    model TEXT NOT NULL,
    dimensions INTEGER NOT NULL,
    vector_json TEXT NOT NULL,
    indexed_at TEXT NOT NULL DEFAULT (datetime('now'))
);
