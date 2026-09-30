CREATE TABLE knowledge_learning_changes (
 seq INTEGER PRIMARY KEY AUTOINCREMENT,kind TEXT NOT NULL,material_id TEXT NOT NULL,
 source_type TEXT NOT NULL,source_id TEXT NOT NULL,material_version INTEGER NOT NULL,
 created_at TEXT NOT NULL DEFAULT(datetime('now'))
);
CREATE INDEX idx_knowledge_changes_material ON knowledge_learning_changes(material_id,seq);
INSERT INTO knowledge_learning_changes(kind,material_id,source_type,source_id,material_version,created_at)
 SELECT kind,id,source_type,source_id,revision,updated_at FROM owner_notes
 UNION ALL SELECT 'keypoint',id,source_type,source_id,card_version,created_at FROM keypoint_index ORDER BY 6,2;
CREATE TRIGGER knowledge_change_note_new AFTER INSERT ON owner_notes BEGIN
 INSERT INTO knowledge_learning_changes(kind,material_id,source_type,source_id,material_version)VALUES(new.kind,new.id,new.source_type,new.source_id,new.revision);END;
CREATE TRIGGER knowledge_change_note_update AFTER UPDATE ON owner_notes WHEN new.revision!=old.revision BEGIN
 INSERT INTO knowledge_learning_changes(kind,material_id,source_type,source_id,material_version)VALUES(new.kind,new.id,new.source_type,new.source_id,new.revision);END;
CREATE TRIGGER knowledge_change_key_new AFTER INSERT ON keypoint_index BEGIN
 INSERT INTO knowledge_learning_changes(kind,material_id,source_type,source_id,material_version)VALUES('keypoint',new.id,new.source_type,new.source_id,new.card_version);END;
CREATE TRIGGER knowledge_change_key_update AFTER UPDATE ON keypoint_index WHEN new.content!=old.content OR new.description!=old.description OR new.card_version!=old.card_version OR new.quality_status!=old.quality_status BEGIN
 INSERT INTO knowledge_learning_changes(kind,material_id,source_type,source_id,material_version)VALUES('keypoint',new.id,new.source_type,new.source_id,new.card_version);END;
CREATE TABLE knowledge_discovery_cursors(profile_id TEXT PRIMARY KEY REFERENCES editorial_profiles(id),last_seq INTEGER NOT NULL DEFAULT 0);
CREATE TABLE knowledge_discovery_batches (
 id TEXT PRIMARY KEY,profile_id TEXT NOT NULL REFERENCES editorial_profiles(id),input_hash TEXT NOT NULL,
 input_json TEXT NOT NULL,scope_json TEXT NOT NULL,provider TEXT NOT NULL,model TEXT NOT NULL,prompt_version TEXT NOT NULL,
 last_seq INTEGER NOT NULL DEFAULT 0,automated INTEGER NOT NULL DEFAULT 0,
 article_id TEXT NOT NULL DEFAULT '',status TEXT NOT NULL DEFAULT 'pending',reason TEXT NOT NULL DEFAULT '',
 created_at TEXT NOT NULL DEFAULT(datetime('now')),UNIQUE(profile_id,input_hash)
);
CREATE TABLE knowledge_topic_candidates (
 id TEXT PRIMARY KEY,batch_id TEXT NOT NULL REFERENCES knowledge_discovery_batches(id) ON DELETE CASCADE,
 direction_hash TEXT NOT NULL,topic_json TEXT NOT NULL,status TEXT NOT NULL DEFAULT 'waiting',
 article_id TEXT NOT NULL DEFAULT '',reason TEXT NOT NULL DEFAULT '',selection_json TEXT NOT NULL DEFAULT '[]',
 created_at TEXT NOT NULL DEFAULT(datetime('now')),UNIQUE(batch_id,direction_hash)
);
CREATE UNIQUE INDEX idx_knowledge_candidate_article ON knowledge_topic_candidates(article_id) WHERE article_id!='';
ALTER TABLE knowledge_articles ADD COLUMN discovery_batch_id TEXT NOT NULL DEFAULT '';
ALTER TABLE knowledge_articles ADD COLUMN candidate_id TEXT NOT NULL DEFAULT '';
CREATE UNIQUE INDEX idx_knowledge_article_candidate ON knowledge_articles(candidate_id) WHERE candidate_id!='';
INSERT INTO knowledge_discovery_batches(id,profile_id,input_hash,input_json,scope_json,provider,model,prompt_version,automated,article_id,status)
 SELECT 'legacy:'||id,profile_id,input_hash,input_json,'{}',provider,model,'knowledge-article-v1',automated,id,status FROM knowledge_articles;
UPDATE knowledge_articles SET discovery_batch_id='legacy:'||id;
INSERT INTO knowledge_topic_candidates(id,batch_id,direction_hash,topic_json,status,article_id)
 SELECT 'legacy:'||id,'legacy:'||id,input_hash,topic_json,CASE WHEN status='insufficient' THEN 'insufficient' ELSE 'written' END,id FROM knowledge_articles WHERE topic_json!='{}';
UPDATE knowledge_articles SET candidate_id='legacy:'||id WHERE topic_json!='{}';
DROP INDEX idx_knowledge_articles_active;
CREATE UNIQUE INDEX idx_knowledge_articles_active ON knowledge_articles(profile_id)
 WHERE status IN ('discover','select','write','review','revise','review_final');
