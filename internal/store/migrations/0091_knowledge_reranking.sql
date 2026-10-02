CREATE TABLE knowledge_rerank_cache (
 fingerprint TEXT PRIMARY KEY,
 job_id TEXT NOT NULL REFERENCES processing_jobs(id),
 scores_json TEXT NOT NULL,
 created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE knowledge_search_feedback (
 id TEXT PRIMARY KEY,
 query TEXT NOT NULL,
 doc_key TEXT NOT NULL,
 revision INTEGER NOT NULL,
 label TEXT NOT NULL CHECK(label IN ('relevant','irrelevant')),
 method TEXT NOT NULL,
 created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TRIGGER knowledge_search_feedback_purge AFTER DELETE ON knowledge_search_docs BEGIN
 DELETE FROM knowledge_search_feedback WHERE doc_key=old.key;
END;
