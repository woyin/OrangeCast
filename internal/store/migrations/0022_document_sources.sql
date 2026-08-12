-- Migration 0022: first-class pasted Document Source / EvidenceDocument snapshot.
CREATE TABLE documents (
    id TEXT PRIMARY KEY,
    title TEXT NOT NULL,
    origin_kind TEXT NOT NULL DEFAULT 'pasted',
    origin_url TEXT NOT NULL DEFAULT '',
    content TEXT NOT NULL,
    content_sha256 TEXT NOT NULL,
    production_use TEXT NOT NULL DEFAULT 'public',
    model_data_policy TEXT NOT NULL DEFAULT 'external_allowed',
    archived_at TEXT,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX idx_documents_created ON documents(created_at DESC);
