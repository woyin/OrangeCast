-- Preserve existing relationships while admitting immutable personal understanding identities.
CREATE TABLE learning_question_links_new (
 question_id TEXT NOT NULL REFERENCES learning_questions(id) ON DELETE CASCADE,
 kind TEXT NOT NULL CHECK(kind IN ('source','keypoint','note','evidence','article','understanding')),
 object_id TEXT NOT NULL, source_type TEXT NOT NULL DEFAULT '', source_id TEXT NOT NULL DEFAULT '',
 version INTEGER NOT NULL DEFAULT 0, state TEXT NOT NULL CHECK(state IN ('confirmed','suggested')),
 origin TEXT NOT NULL DEFAULT 'owner', created_at TEXT NOT NULL DEFAULT (datetime('now')),
 PRIMARY KEY(question_id,kind,object_id)
);
INSERT INTO learning_question_links_new SELECT * FROM learning_question_links;
DROP TRIGGER learning_question_episode_purge;
DROP TRIGGER learning_question_upload_purge;
DROP TRIGGER learning_question_document_purge;
DROP TRIGGER learning_question_note_delete;
DROP TRIGGER learning_question_keypoint_delete;
DROP TRIGGER learning_question_article_delete;
DROP TRIGGER evidence_gap_episode_delete;
DROP TRIGGER evidence_gap_upload_delete;
DROP TRIGGER evidence_gap_document_delete;
DROP TRIGGER evidence_gap_snapshot_purge;
DROP TRIGGER export_state_learning_question_links_insert;
DROP TRIGGER export_state_learning_question_links_update;
DROP TRIGGER export_state_learning_question_links_delete;
DROP TABLE learning_question_links;
ALTER TABLE learning_question_links_new RENAME TO learning_question_links;
CREATE INDEX learning_question_links_source ON learning_question_links(source_type,source_id);
CREATE INDEX learning_question_links_object ON learning_question_links(kind,object_id);
CREATE TRIGGER learning_question_episode_purge BEFORE DELETE ON episodes BEGIN
 INSERT INTO learning_question_operations(question_id,revision,action,detail) SELECT id,revision+1,'source_purged','episode:'||old.id FROM learning_questions WHERE id IN(SELECT question_id FROM learning_question_links WHERE source_type='episode' AND source_id=old.id);
 UPDATE learning_questions SET revision=revision+1,updated_at=datetime('now') WHERE id IN(SELECT question_id FROM learning_question_links WHERE source_type='episode' AND source_id=old.id);
 DELETE FROM learning_question_links WHERE source_type='episode' AND source_id=old.id;
END;
CREATE TRIGGER learning_question_upload_purge BEFORE DELETE ON uploads BEGIN
 INSERT INTO learning_question_operations(question_id,revision,action,detail) SELECT id,revision+1,'source_purged','upload:'||old.id FROM learning_questions WHERE id IN(SELECT question_id FROM learning_question_links WHERE source_type='upload' AND source_id=old.id);
 UPDATE learning_questions SET revision=revision+1,updated_at=datetime('now') WHERE id IN(SELECT question_id FROM learning_question_links WHERE source_type='upload' AND source_id=old.id);
 DELETE FROM learning_question_links WHERE source_type='upload' AND source_id=old.id;
END;
CREATE TRIGGER learning_question_document_purge BEFORE DELETE ON documents BEGIN
 INSERT INTO learning_question_operations(question_id,revision,action,detail) SELECT id,revision+1,'source_purged','document:'||old.id FROM learning_questions WHERE id IN(SELECT question_id FROM learning_question_links WHERE source_type='document' AND source_id=old.id);
 UPDATE learning_questions SET revision=revision+1,updated_at=datetime('now') WHERE id IN(SELECT question_id FROM learning_question_links WHERE source_type='document' AND source_id=old.id);
 DELETE FROM learning_question_links WHERE source_type='document' AND source_id=old.id;
END;
CREATE TRIGGER learning_question_note_delete BEFORE DELETE ON owner_notes BEGIN
 UPDATE learning_questions SET revision=revision+1,updated_at=datetime('now') WHERE id IN(SELECT question_id FROM learning_question_links WHERE kind='note' AND object_id=old.id);
 DELETE FROM learning_question_links WHERE kind='note' AND object_id=old.id;
END;
CREATE TRIGGER learning_question_keypoint_delete BEFORE DELETE ON keypoint_index BEGIN
 UPDATE learning_questions SET revision=revision+1,updated_at=datetime('now') WHERE id IN(SELECT question_id FROM learning_question_links WHERE kind='keypoint' AND object_id=old.id);
 DELETE FROM learning_question_links WHERE kind='keypoint' AND object_id=old.id;
END;
CREATE TRIGGER learning_question_article_delete BEFORE DELETE ON knowledge_articles BEGIN
 DELETE FROM learning_question_links WHERE kind='article' AND object_id=old.id;
END;
CREATE TRIGGER evidence_gap_episode_delete BEFORE DELETE ON episodes BEGIN
 DELETE FROM evidence_gaps WHERE parent_kind='question' AND parent_id IN(SELECT question_id FROM learning_question_links WHERE source_type='episode' AND source_id=old.id);
 DELETE FROM evidence_gaps WHERE parent_kind='article' AND parent_id IN(SELECT article_id FROM knowledge_article_material_refs WHERE source_type='episode' AND source_id=old.id);
 DELETE FROM evidence_gaps WHERE parent_kind='candidate' AND parent_id IN(SELECT c.id FROM knowledge_topic_candidates c JOIN knowledge_discovery_batches b ON b.id=c.batch_id WHERE EXISTS(SELECT 1 FROM json_tree(CASE WHEN json_valid(b.input_json) THEN b.input_json ELSE '{}' END) WHERE key='source_id' AND value=old.id));
 DELETE FROM evidence_gaps WHERE parent_kind='update' AND parent_id IN(SELECT p.id FROM knowledge_update_proposals p WHERE EXISTS(SELECT 1 FROM json_tree(CASE WHEN json_valid(p.input_json) THEN p.input_json ELSE '{}' END) WHERE key='source_id' AND value=old.id));
END;
CREATE TRIGGER evidence_gap_upload_delete BEFORE DELETE ON uploads BEGIN
 DELETE FROM evidence_gaps WHERE parent_kind='question' AND parent_id IN(SELECT question_id FROM learning_question_links WHERE source_type='upload' AND source_id=old.id);
 DELETE FROM evidence_gaps WHERE parent_kind='article' AND parent_id IN(SELECT article_id FROM knowledge_article_material_refs WHERE source_type='upload' AND source_id=old.id);
 DELETE FROM evidence_gaps WHERE parent_kind='candidate' AND parent_id IN(SELECT c.id FROM knowledge_topic_candidates c JOIN knowledge_discovery_batches b ON b.id=c.batch_id WHERE EXISTS(SELECT 1 FROM json_tree(CASE WHEN json_valid(b.input_json) THEN b.input_json ELSE '{}' END) WHERE key='source_id' AND value=old.id));
 DELETE FROM evidence_gaps WHERE parent_kind='update' AND parent_id IN(SELECT p.id FROM knowledge_update_proposals p WHERE EXISTS(SELECT 1 FROM json_tree(CASE WHEN json_valid(p.input_json) THEN p.input_json ELSE '{}' END) WHERE key='source_id' AND value=old.id));
END;
CREATE TRIGGER evidence_gap_document_delete BEFORE DELETE ON documents BEGIN
 DELETE FROM evidence_gaps WHERE parent_kind='question' AND parent_id IN(SELECT question_id FROM learning_question_links WHERE source_type='document' AND source_id=old.id);
 DELETE FROM evidence_gaps WHERE parent_kind='article' AND parent_id IN(SELECT article_id FROM knowledge_article_material_refs WHERE source_type='document' AND source_id=old.id);
 DELETE FROM evidence_gaps WHERE parent_kind='candidate' AND parent_id IN(SELECT c.id FROM knowledge_topic_candidates c JOIN knowledge_discovery_batches b ON b.id=c.batch_id WHERE EXISTS(SELECT 1 FROM json_tree(CASE WHEN json_valid(b.input_json) THEN b.input_json ELSE '{}' END) WHERE key='source_id' AND value=old.id));
 DELETE FROM evidence_gaps WHERE parent_kind='update' AND parent_id IN(SELECT p.id FROM knowledge_update_proposals p WHERE EXISTS(SELECT 1 FROM json_tree(CASE WHEN json_valid(p.input_json) THEN p.input_json ELSE '{}' END) WHERE key='source_id' AND value=old.id));
END;
CREATE TRIGGER evidence_gap_snapshot_purge AFTER UPDATE OF status ON source_snapshots WHEN new.status='purged' AND old.status!='purged' BEGIN
 DELETE FROM evidence_gaps WHERE parent_kind='question' AND parent_id IN(SELECT question_id FROM learning_question_links WHERE source_type=new.source_type AND source_id=new.source_id);
 DELETE FROM evidence_gaps WHERE parent_kind='article' AND parent_id IN(SELECT article_id FROM knowledge_article_material_refs WHERE source_type=new.source_type AND source_id=new.source_id);
 DELETE FROM evidence_gaps WHERE parent_kind='candidate' AND parent_id IN(SELECT c.id FROM knowledge_topic_candidates c JOIN knowledge_discovery_batches b ON b.id=c.batch_id WHERE EXISTS(SELECT 1 FROM json_tree(CASE WHEN json_valid(b.input_json) THEN b.input_json ELSE '{}' END) WHERE key='source_id' AND value=new.source_id));
 DELETE FROM evidence_gaps WHERE parent_kind='update' AND parent_id IN(SELECT p.id FROM knowledge_update_proposals p WHERE EXISTS(SELECT 1 FROM json_tree(CASE WHEN json_valid(p.input_json) THEN p.input_json ELSE '{}' END) WHERE key='source_id' AND value=new.source_id));
END;
CREATE TRIGGER export_state_learning_question_links_insert AFTER INSERT ON learning_question_links BEGIN UPDATE learning_export_state SET seq=seq+1 WHERE singleton=1; END;
CREATE TRIGGER export_state_learning_question_links_update AFTER UPDATE ON learning_question_links BEGIN UPDATE learning_export_state SET seq=seq+1 WHERE singleton=1; END;
CREATE TRIGGER export_state_learning_question_links_delete AFTER DELETE ON learning_question_links BEGIN UPDATE learning_export_state SET seq=seq+1 WHERE singleton=1; END;
CREATE TRIGGER learning_question_understanding_delete BEFORE DELETE ON understanding_snapshots BEGIN
 UPDATE learning_questions SET revision=revision+1,updated_at=datetime('now') WHERE id IN(SELECT question_id FROM learning_question_links WHERE kind='understanding' AND object_id=old.id);
 DELETE FROM learning_question_links WHERE kind='understanding' AND object_id=old.id;
END;
