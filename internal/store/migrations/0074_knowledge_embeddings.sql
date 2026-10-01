-- Cache-only migration: never enqueues a paid processing job or enables indexing.
CREATE TABLE knowledge_embedding_configs (
 id TEXT PRIMARY KEY,connection_id TEXT NOT NULL,provider TEXT NOT NULL,model TEXT NOT NULL,
 dimensions INTEGER NOT NULL CHECK(dimensions BETWEEN 1 AND 2048),unit TEXT NOT NULL CHECK(unit='input_tokens'),
 enabled INTEGER NOT NULL DEFAULT 0 CHECK(enabled IN (0,1)),revision INTEGER NOT NULL DEFAULT 1,
 created_at TEXT NOT NULL DEFAULT (datetime('now')),updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE knowledge_embedding_sources (
 config_id TEXT NOT NULL REFERENCES knowledge_embedding_configs(id) ON DELETE CASCADE,
 source_type TEXT NOT NULL CHECK(source_type IN ('episode','upload','document')),source_id TEXT NOT NULL,
 PRIMARY KEY(config_id,source_type,source_id)
);
CREATE TABLE knowledge_embedding_state (id INTEGER PRIMARY KEY CHECK(id=1),delete_epoch INTEGER NOT NULL DEFAULT 0,index_epoch INTEGER NOT NULL DEFAULT 0);
INSERT INTO knowledge_embedding_state(id)VALUES(1);
CREATE TABLE knowledge_embedding_vectors (
 config_id TEXT NOT NULL REFERENCES knowledge_embedding_configs(id) ON DELETE CASCADE,
 doc_key TEXT NOT NULL REFERENCES knowledge_search_docs(key) ON DELETE CASCADE,
 window_no INTEGER NOT NULL,revision INTEGER NOT NULL,content_hash TEXT NOT NULL,
 dimensions INTEGER NOT NULL CHECK(dimensions BETWEEN 1 AND 2048),vector BLOB NOT NULL,
 created_at TEXT NOT NULL DEFAULT (datetime('now')),PRIMARY KEY(config_id,doc_key,window_no)
);
CREATE TABLE knowledge_embedding_events (
 config_id TEXT NOT NULL REFERENCES knowledge_embedding_configs(id) ON DELETE CASCADE,
 doc_key TEXT NOT NULL,action TEXT NOT NULL CHECK(action IN ('upsert','delete')),reason TEXT NOT NULL DEFAULT '',
 created_at TEXT NOT NULL DEFAULT (datetime('now')),PRIMARY KEY(config_id,doc_key)
);
CREATE INDEX idx_embedding_events_order ON knowledge_embedding_events(config_id,created_at,doc_key);
CREATE TRIGGER embedding_doc_insert AFTER INSERT ON knowledge_search_docs BEGIN
 INSERT INTO knowledge_embedding_events(config_id,doc_key,action) SELECT id,new.key,'upsert' FROM knowledge_embedding_configs WHERE 1
 ON CONFLICT(config_id,doc_key) DO UPDATE SET action='upsert',reason='',created_at=datetime('now');
END;
CREATE TRIGGER embedding_doc_update AFTER UPDATE ON knowledge_search_docs BEGIN
 DELETE FROM knowledge_embedding_vectors WHERE doc_key=old.key;
 UPDATE knowledge_embedding_state SET delete_epoch=delete_epoch+1,index_epoch=index_epoch+1 WHERE id=1;
 INSERT INTO knowledge_embedding_events(config_id,doc_key,action) SELECT id,new.key,'upsert' FROM knowledge_embedding_configs WHERE 1
 ON CONFLICT(config_id,doc_key) DO UPDATE SET action='upsert',reason='',created_at=datetime('now');
END;
CREATE TRIGGER embedding_doc_delete AFTER DELETE ON knowledge_search_docs BEGIN
 DELETE FROM knowledge_embedding_vectors WHERE doc_key=old.key;
 UPDATE knowledge_embedding_state SET delete_epoch=delete_epoch+1,index_epoch=index_epoch+1 WHERE id=1;
 INSERT INTO knowledge_embedding_events(config_id,doc_key,action) SELECT id,old.key,'delete' FROM knowledge_embedding_configs WHERE 1
 ON CONFLICT(config_id,doc_key) DO UPDATE SET action='delete',reason='',created_at=datetime('now');
END;
CREATE TRIGGER embedding_episode_update AFTER UPDATE OF model_data_policy,approved_providers_json,archived_at ON episodes BEGIN
 UPDATE knowledge_embedding_state SET delete_epoch=delete_epoch+1,index_epoch=index_epoch+1 WHERE id=1;
 DELETE FROM knowledge_embedding_vectors WHERE doc_key IN (SELECT d.key FROM knowledge_search_docs d WHERE (d.source_type='episode' AND d.source_id=new.id) OR (d.kind='article' AND EXISTS(SELECT 1 FROM knowledge_article_material_refs m WHERE m.article_id=d.object_id AND m.revision=d.revision AND m.source_type='episode' AND m.source_id=new.id)));
 INSERT INTO knowledge_embedding_events(config_id,doc_key,action,reason) SELECT c.id,d.key,'upsert','source_policy_changed' FROM knowledge_embedding_configs c,knowledge_search_docs d WHERE (d.source_type='episode' AND d.source_id=new.id) OR (d.kind='article' AND EXISTS(SELECT 1 FROM knowledge_article_material_refs m WHERE m.article_id=d.object_id AND m.revision=d.revision AND m.source_type='episode' AND m.source_id=new.id))
 ON CONFLICT(config_id,doc_key) DO UPDATE SET action='upsert',reason='source_policy_changed',created_at=datetime('now');
END;
CREATE TRIGGER embedding_episode_delete AFTER DELETE ON episodes BEGIN
 UPDATE knowledge_embedding_state SET delete_epoch=delete_epoch+1,index_epoch=index_epoch+1 WHERE id=1;
 DELETE FROM knowledge_embedding_vectors WHERE doc_key IN (SELECT d.key FROM knowledge_search_docs d WHERE (d.source_type='episode' AND d.source_id=old.id) OR (d.kind='article' AND EXISTS(SELECT 1 FROM knowledge_article_material_refs m WHERE m.article_id=d.object_id AND m.revision=d.revision AND m.source_type='episode' AND m.source_id=old.id)));
 INSERT INTO knowledge_embedding_events(config_id,doc_key,action,reason) SELECT c.id,d.key,'upsert','source_policy_changed' FROM knowledge_embedding_configs c,knowledge_search_docs d WHERE (d.source_type='episode' AND d.source_id=old.id) OR (d.kind='article' AND EXISTS(SELECT 1 FROM knowledge_article_material_refs m WHERE m.article_id=d.object_id AND m.revision=d.revision AND m.source_type='episode' AND m.source_id=old.id))
 ON CONFLICT(config_id,doc_key) DO UPDATE SET action='upsert',reason='source_policy_changed',created_at=datetime('now');
END;
CREATE TRIGGER embedding_upload_update AFTER UPDATE OF model_data_policy,approved_providers_json,archived_at ON uploads BEGIN
 UPDATE knowledge_embedding_state SET delete_epoch=delete_epoch+1,index_epoch=index_epoch+1 WHERE id=1;
 DELETE FROM knowledge_embedding_vectors WHERE doc_key IN (SELECT d.key FROM knowledge_search_docs d WHERE (d.source_type='upload' AND d.source_id=new.id) OR (d.kind='article' AND EXISTS(SELECT 1 FROM knowledge_article_material_refs m WHERE m.article_id=d.object_id AND m.revision=d.revision AND m.source_type='upload' AND m.source_id=new.id)));
 INSERT INTO knowledge_embedding_events(config_id,doc_key,action,reason) SELECT c.id,d.key,'upsert','source_policy_changed' FROM knowledge_embedding_configs c,knowledge_search_docs d WHERE (d.source_type='upload' AND d.source_id=new.id) OR (d.kind='article' AND EXISTS(SELECT 1 FROM knowledge_article_material_refs m WHERE m.article_id=d.object_id AND m.revision=d.revision AND m.source_type='upload' AND m.source_id=new.id))
 ON CONFLICT(config_id,doc_key) DO UPDATE SET action='upsert',reason='source_policy_changed',created_at=datetime('now');
END;
CREATE TRIGGER embedding_upload_delete AFTER DELETE ON uploads BEGIN
 UPDATE knowledge_embedding_state SET delete_epoch=delete_epoch+1,index_epoch=index_epoch+1 WHERE id=1;
 DELETE FROM knowledge_embedding_vectors WHERE doc_key IN (SELECT d.key FROM knowledge_search_docs d WHERE (d.source_type='upload' AND d.source_id=old.id) OR (d.kind='article' AND EXISTS(SELECT 1 FROM knowledge_article_material_refs m WHERE m.article_id=d.object_id AND m.revision=d.revision AND m.source_type='upload' AND m.source_id=old.id)));
 INSERT INTO knowledge_embedding_events(config_id,doc_key,action,reason) SELECT c.id,d.key,'upsert','source_policy_changed' FROM knowledge_embedding_configs c,knowledge_search_docs d WHERE (d.source_type='upload' AND d.source_id=old.id) OR (d.kind='article' AND EXISTS(SELECT 1 FROM knowledge_article_material_refs m WHERE m.article_id=d.object_id AND m.revision=d.revision AND m.source_type='upload' AND m.source_id=old.id))
 ON CONFLICT(config_id,doc_key) DO UPDATE SET action='upsert',reason='source_policy_changed',created_at=datetime('now');
END;
CREATE TRIGGER embedding_document_update AFTER UPDATE OF model_data_policy,approved_providers_json,archived_at ON documents BEGIN
 UPDATE knowledge_embedding_state SET delete_epoch=delete_epoch+1,index_epoch=index_epoch+1 WHERE id=1;
 DELETE FROM knowledge_embedding_vectors WHERE doc_key IN (SELECT d.key FROM knowledge_search_docs d WHERE (d.source_type='document' AND d.source_id=new.id) OR (d.kind='article' AND EXISTS(SELECT 1 FROM knowledge_article_material_refs m WHERE m.article_id=d.object_id AND m.revision=d.revision AND m.source_type='document' AND m.source_id=new.id)));
 INSERT INTO knowledge_embedding_events(config_id,doc_key,action,reason) SELECT c.id,d.key,'upsert','source_policy_changed' FROM knowledge_embedding_configs c,knowledge_search_docs d WHERE (d.source_type='document' AND d.source_id=new.id) OR (d.kind='article' AND EXISTS(SELECT 1 FROM knowledge_article_material_refs m WHERE m.article_id=d.object_id AND m.revision=d.revision AND m.source_type='document' AND m.source_id=new.id))
 ON CONFLICT(config_id,doc_key) DO UPDATE SET action='upsert',reason='source_policy_changed',created_at=datetime('now');
END;
CREATE TRIGGER embedding_document_delete AFTER DELETE ON documents BEGIN
 UPDATE knowledge_embedding_state SET delete_epoch=delete_epoch+1,index_epoch=index_epoch+1 WHERE id=1;
 DELETE FROM knowledge_embedding_vectors WHERE doc_key IN (SELECT d.key FROM knowledge_search_docs d WHERE (d.source_type='document' AND d.source_id=old.id) OR (d.kind='article' AND EXISTS(SELECT 1 FROM knowledge_article_material_refs m WHERE m.article_id=d.object_id AND m.revision=d.revision AND m.source_type='document' AND m.source_id=old.id)));
 INSERT INTO knowledge_embedding_events(config_id,doc_key,action,reason) SELECT c.id,d.key,'upsert','source_policy_changed' FROM knowledge_embedding_configs c,knowledge_search_docs d WHERE (d.source_type='document' AND d.source_id=old.id) OR (d.kind='article' AND EXISTS(SELECT 1 FROM knowledge_article_material_refs m WHERE m.article_id=d.object_id AND m.revision=d.revision AND m.source_type='document' AND m.source_id=old.id))
 ON CONFLICT(config_id,doc_key) DO UPDATE SET action='upsert',reason='source_policy_changed',created_at=datetime('now');
END;
CREATE TRIGGER embedding_snapshot_purge AFTER UPDATE OF status ON source_snapshots WHEN new.status='purged' BEGIN
 UPDATE knowledge_embedding_state SET delete_epoch=delete_epoch+1,index_epoch=index_epoch+1 WHERE id=1;
 DELETE FROM knowledge_embedding_vectors WHERE doc_key IN (SELECT d.key FROM knowledge_search_docs d WHERE (d.source_type=new.source_type AND d.source_id=new.source_id) OR (d.kind='article' AND EXISTS(SELECT 1 FROM knowledge_article_material_refs m WHERE m.article_id=d.object_id AND m.revision=d.revision AND m.source_type=new.source_type AND m.source_id=new.source_id)));
END;
CREATE TRIGGER embedding_article_refs_insert AFTER INSERT ON knowledge_article_material_refs BEGIN
 UPDATE knowledge_embedding_state SET delete_epoch=delete_epoch+1,index_epoch=index_epoch+1 WHERE id=1;
 DELETE FROM knowledge_embedding_vectors WHERE doc_key IN (SELECT key FROM knowledge_search_docs WHERE kind='article' AND object_id=new.article_id);
 INSERT INTO knowledge_embedding_events(config_id,doc_key,action) SELECT c.id,d.key,'upsert' FROM knowledge_embedding_configs c,knowledge_search_docs d WHERE d.kind='article' AND d.object_id=new.article_id
 ON CONFLICT(config_id,doc_key) DO UPDATE SET action='upsert',reason='',created_at=datetime('now');
END;
CREATE TRIGGER embedding_article_refs_update AFTER UPDATE ON knowledge_article_material_refs BEGIN
 UPDATE knowledge_embedding_state SET delete_epoch=delete_epoch+1,index_epoch=index_epoch+1 WHERE id=1;
 DELETE FROM knowledge_embedding_vectors WHERE doc_key IN (SELECT key FROM knowledge_search_docs WHERE kind='article' AND object_id=new.article_id);
 INSERT INTO knowledge_embedding_events(config_id,doc_key,action) SELECT c.id,d.key,'upsert' FROM knowledge_embedding_configs c,knowledge_search_docs d WHERE d.kind='article' AND d.object_id=new.article_id
 ON CONFLICT(config_id,doc_key) DO UPDATE SET action='upsert',reason='',created_at=datetime('now');
END;
CREATE TRIGGER embedding_article_refs_delete AFTER DELETE ON knowledge_article_material_refs BEGIN
 UPDATE knowledge_embedding_state SET delete_epoch=delete_epoch+1,index_epoch=index_epoch+1 WHERE id=1;
 DELETE FROM knowledge_embedding_vectors WHERE doc_key IN (SELECT key FROM knowledge_search_docs WHERE kind='article' AND object_id=old.article_id);
 INSERT INTO knowledge_embedding_events(config_id,doc_key,action) SELECT c.id,d.key,'upsert' FROM knowledge_embedding_configs c,knowledge_search_docs d WHERE d.kind='article' AND d.object_id=old.article_id
 ON CONFLICT(config_id,doc_key) DO UPDATE SET action='upsert',reason='',created_at=datetime('now');
END;
