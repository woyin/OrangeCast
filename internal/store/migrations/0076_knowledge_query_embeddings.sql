CREATE TABLE knowledge_query_embeddings (
 config_id TEXT NOT NULL REFERENCES knowledge_embedding_configs(id) ON DELETE CASCADE,
 job_id TEXT NOT NULL REFERENCES processing_jobs(id) ON DELETE CASCADE,
 query_hash TEXT NOT NULL,delete_epoch INTEGER NOT NULL,scope_revision INTEGER NOT NULL,
 dimensions INTEGER NOT NULL,vector BLOB NOT NULL,
 created_at TEXT NOT NULL DEFAULT (datetime('now')),
 expires_at TEXT NOT NULL DEFAULT (datetime('now','+30 days')),
 PRIMARY KEY(config_id,query_hash,delete_epoch)
);
CREATE TRIGGER embedding_query_epoch AFTER UPDATE OF delete_epoch ON knowledge_embedding_state WHEN new.delete_epoch!=old.delete_epoch BEGIN
 DELETE FROM knowledge_query_embeddings;
END;
CREATE TABLE knowledge_query_requests (
 request_key TEXT PRIMARY KEY,config_id TEXT NOT NULL,query_hash TEXT NOT NULL,
 delete_epoch INTEGER NOT NULL,
 job_id TEXT REFERENCES processing_jobs(id) ON DELETE CASCADE,
 created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
