CREATE TABLE evidence_gaps (
 id TEXT PRIMARY KEY,parent_kind TEXT NOT NULL CHECK(parent_kind IN('question','candidate','update','article')),
 parent_id TEXT NOT NULL,parent_revision INTEGER NOT NULL,parent_hash TEXT NOT NULL,
 kind TEXT NOT NULL,origin TEXT NOT NULL CHECK(origin IN('program','model','owner')),
 explanation TEXT NOT NULL,coverage TEXT NOT NULL,state TEXT NOT NULL DEFAULT 'pending'
 CHECK(state IN('pending','helpful','insufficient','ignored','expired')),
 revision INTEGER NOT NULL DEFAULT 1,created_at TEXT NOT NULL DEFAULT(datetime('now')),
 updated_at TEXT NOT NULL DEFAULT(datetime('now'))
);
CREATE INDEX evidence_gaps_parent ON evidence_gaps(parent_kind,parent_id,created_at,id);
CREATE TABLE evidence_gap_operations (
 gap_id TEXT NOT NULL REFERENCES evidence_gaps(id) ON DELETE CASCADE,revision INTEGER NOT NULL,
 state TEXT NOT NULL,comment TEXT NOT NULL DEFAULT '',created_at TEXT NOT NULL DEFAULT(datetime('now')),
 PRIMARY KEY(gap_id,revision)
);
CREATE TRIGGER evidence_gap_question_delete AFTER DELETE ON learning_questions BEGIN
 DELETE FROM evidence_gaps WHERE parent_kind='question' AND parent_id=old.id;
END;
CREATE TRIGGER evidence_gap_candidate_delete AFTER DELETE ON knowledge_topic_candidates BEGIN
 DELETE FROM evidence_gaps WHERE parent_kind='candidate' AND parent_id=old.id;
END;
CREATE TRIGGER evidence_gap_update_delete AFTER DELETE ON knowledge_update_proposals BEGIN
 DELETE FROM evidence_gaps WHERE parent_kind='update' AND parent_id=old.id;
END;
CREATE TRIGGER evidence_gap_article_delete AFTER DELETE ON knowledge_articles BEGIN
 DELETE FROM evidence_gaps WHERE parent_kind='article' AND parent_id=old.id;
END;
-- Body copied from model projections must not survive dependent source purge.
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

-- Learning excerpt queue and independent progress identity.
CREATE TABLE learning_excerpts (
 id TEXT PRIMARY KEY,source_type TEXT NOT NULL CHECK(source_type IN('episode','upload')),source_id TEXT NOT NULL,
 snapshot_id TEXT NOT NULL REFERENCES source_snapshots(id),audio_sha256 TEXT NOT NULL CHECK(length(audio_sha256)>0),
 segment_ids_json TEXT NOT NULL CHECK(json_valid(segment_ids_json)),start_seconds REAL NOT NULL CHECK(start_seconds>=0),end_seconds REAL NOT NULL CHECK(end_seconds>start_seconds),
 created_at TEXT NOT NULL DEFAULT(datetime('now')),UNIQUE(snapshot_id,start_seconds,end_seconds)
);
DROP TRIGGER listening_queue_episode_purge;
DROP TRIGGER listening_queue_upload_purge;
DROP INDEX idx_listening_queue_order;
ALTER TABLE listening_queue_entries RENAME TO listening_queue_entries_old;
CREATE TABLE listening_queue_entries (
 id TEXT PRIMARY KEY,source_type TEXT NOT NULL CHECK(source_type IN('episode','upload')),source_id TEXT NOT NULL,
 mode TEXT NOT NULL CHECK(mode IN('original','dj','excerpt')),plan_id TEXT NOT NULL DEFAULT '',plan_version INTEGER NOT NULL DEFAULT 0,
 excerpt_id TEXT NOT NULL DEFAULT '',audio_sha256 TEXT NOT NULL DEFAULT '',title TEXT NOT NULL DEFAULT '',position INTEGER NOT NULL,
 invalid_reason TEXT NOT NULL DEFAULT '',created_at TEXT NOT NULL DEFAULT(datetime('now')),
 CHECK((mode='excerpt' AND excerpt_id!='' AND plan_id='' AND plan_version=0) OR (mode!='excerpt' AND excerpt_id='')),
 UNIQUE(source_type,source_id,mode,plan_id,plan_version,excerpt_id)
);
INSERT INTO listening_queue_entries(id,source_type,source_id,mode,plan_id,plan_version,audio_sha256,title,position,invalid_reason,created_at)
 SELECT id,source_type,source_id,mode,plan_id,plan_version,audio_sha256,title,position,invalid_reason,created_at FROM listening_queue_entries_old;
DROP TABLE listening_queue_entries_old;
CREATE INDEX idx_listening_queue_order ON listening_queue_entries(position,id);
CREATE TRIGGER listening_queue_episode_purge AFTER DELETE ON episodes BEGIN
 UPDATE listening_queue_state SET current_item_id=CASE WHEN current_item_id IN(SELECT id FROM listening_queue_entries WHERE source_type='episode' AND source_id=old.id) THEN '' ELSE current_item_id END,revision=revision+1 WHERE EXISTS(SELECT 1 FROM listening_queue_entries WHERE source_type='episode' AND source_id=old.id);
 UPDATE listening_queue_entries SET invalid_reason='来源已删除',title='已删除来源',audio_sha256='' WHERE source_type='episode' AND source_id=old.id;
END;
CREATE TRIGGER listening_queue_upload_purge AFTER DELETE ON uploads BEGIN
 UPDATE listening_queue_state SET current_item_id=CASE WHEN current_item_id IN(SELECT id FROM listening_queue_entries WHERE source_type='upload' AND source_id=old.id) THEN '' ELSE current_item_id END,revision=revision+1 WHERE EXISTS(SELECT 1 FROM listening_queue_entries WHERE source_type='upload' AND source_id=old.id);
 UPDATE listening_queue_entries SET invalid_reason='来源已删除',title='已删除来源',audio_sha256='' WHERE source_type='upload' AND source_id=old.id;
END;

CREATE TABLE learning_excerpt_progress (
 id TEXT PRIMARY KEY,excerpt_id TEXT NOT NULL UNIQUE REFERENCES learning_excerpts(id),
 item_offset_seconds REAL NOT NULL,speed REAL NOT NULL,seq INTEGER NOT NULL DEFAULT 1,
 revision INTEGER NOT NULL DEFAULT 1,updated_at TEXT NOT NULL DEFAULT(datetime('now'))
);
CREATE TRIGGER learning_excerpt_episode_purge AFTER DELETE ON episodes BEGIN
 DELETE FROM learning_excerpt_progress WHERE excerpt_id IN(SELECT id FROM learning_excerpts WHERE source_type='episode' AND source_id=old.id);
END;
CREATE TRIGGER learning_excerpt_upload_purge AFTER DELETE ON uploads BEGIN
 DELETE FROM learning_excerpt_progress WHERE excerpt_id IN(SELECT id FROM learning_excerpts WHERE source_type='upload' AND source_id=old.id);
END;
