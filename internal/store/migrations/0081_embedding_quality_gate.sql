-- Index authorization is separate from quality-gated search activation.
ALTER TABLE knowledge_embedding_configs ADD COLUMN semantic_enabled INTEGER NOT NULL DEFAULT 0 CHECK(semantic_enabled IN (0,1));
ALTER TABLE knowledge_embedding_configs ADD COLUMN window_capacity INTEGER NOT NULL DEFAULT 50000 CHECK(window_capacity BETWEEN 1 AND 50000);
CREATE TABLE knowledge_embedding_quality_reports (
 id TEXT PRIMARY KEY,config_id TEXT NOT NULL REFERENCES knowledge_embedding_configs(id) ON DELETE CASCADE,
 identity TEXT NOT NULL,report_json TEXT NOT NULL,passed INTEGER NOT NULL CHECK(passed IN (0,1)),created_at TEXT NOT NULL DEFAULT(datetime('now'))
);
CREATE INDEX idx_embedding_quality_current ON knowledge_embedding_quality_reports(config_id,created_at,id);
CREATE TRIGGER embedding_quality_new_document AFTER INSERT ON knowledge_search_docs BEGIN
 UPDATE knowledge_embedding_state SET index_epoch=index_epoch+1 WHERE id=1;
END;
-- Skips and event repairs change measured coverage even before vector adoption.
CREATE TRIGGER embedding_quality_event_insert AFTER INSERT ON knowledge_embedding_events BEGIN UPDATE knowledge_embedding_state SET index_epoch=index_epoch+1 WHERE id=1; END;
CREATE TRIGGER embedding_quality_event_update AFTER UPDATE ON knowledge_embedding_events BEGIN UPDATE knowledge_embedding_state SET index_epoch=index_epoch+1 WHERE id=1; END;
CREATE TRIGGER embedding_quality_event_delete AFTER DELETE ON knowledge_embedding_events BEGIN UPDATE knowledge_embedding_state SET index_epoch=index_epoch+1 WHERE id=1; END;
