-- Current and passed material identities are indexed independently of full article bodies.
CREATE TABLE knowledge_article_material_index (
 article_id TEXT NOT NULL REFERENCES knowledge_articles(id) ON DELETE CASCADE,
 role TEXT NOT NULL, material_id TEXT NOT NULL, source_type TEXT NOT NULL, source_id TEXT NOT NULL,
 snapshot_id TEXT NOT NULL DEFAULT '', material_version INTEGER NOT NULL,
 PRIMARY KEY(article_id,role,material_id)
);
CREATE INDEX knowledge_article_material_identity ON knowledge_article_material_index(material_id,article_id);
CREATE INDEX knowledge_article_material_source ON knowledge_article_material_index(source_type,source_id,article_id);
INSERT OR IGNORE INTO knowledge_article_material_index
 SELECT a.id,'working',json_extract(CASE WHEN m.type='object' THEN m.value ELSE '{}' END,'$.id'),COALESCE(json_extract(CASE WHEN m.type='object' THEN m.value ELSE '{}' END,'$.source_type'),''),COALESCE(json_extract(CASE WHEN m.type='object' THEN m.value ELSE '{}' END,'$.source_id'),''),COALESCE(json_extract(CASE WHEN m.type='object' THEN m.value ELSE '{}' END,'$.snapshot_id'),''),COALESCE(json_extract(CASE WHEN m.type='object' THEN m.value ELSE '{}' END,'$.version'),0)
 FROM knowledge_articles a LEFT JOIN knowledge_article_revisions r ON r.article_id=a.id AND r.revision=a.working_revision,json_each(CASE WHEN json_valid(COALESCE(r.input_json,a.input_json)) THEN COALESCE(r.input_json,a.input_json) ELSE '{}' END,'$.materials') m WHERE json_extract(CASE WHEN m.type='object' THEN m.value ELSE '{}' END,'$.id') IS NOT NULL;
INSERT OR IGNORE INTO knowledge_article_material_index
 SELECT a.id,'passed',json_extract(CASE WHEN m.type='object' THEN m.value ELSE '{}' END,'$.id'),COALESCE(json_extract(CASE WHEN m.type='object' THEN m.value ELSE '{}' END,'$.source_type'),''),COALESCE(json_extract(CASE WHEN m.type='object' THEN m.value ELSE '{}' END,'$.source_id'),''),COALESCE(json_extract(CASE WHEN m.type='object' THEN m.value ELSE '{}' END,'$.snapshot_id'),''),COALESCE(json_extract(CASE WHEN m.type='object' THEN m.value ELSE '{}' END,'$.version'),0)
 FROM knowledge_articles a JOIN knowledge_article_revisions r ON r.article_id=a.id AND r.revision=a.passed_revision,json_each(CASE WHEN json_valid(r.input_json) THEN r.input_json ELSE '{}' END,'$.materials') m WHERE json_extract(CASE WHEN m.type='object' THEN m.value ELSE '{}' END,'$.id') IS NOT NULL;
CREATE TRIGGER knowledge_article_material_insert AFTER INSERT ON knowledge_articles BEGIN
 INSERT OR IGNORE INTO knowledge_article_material_index SELECT new.id,'working',json_extract(CASE WHEN m.type='object' THEN m.value ELSE '{}' END,'$.id'),COALESCE(json_extract(CASE WHEN m.type='object' THEN m.value ELSE '{}' END,'$.source_type'),''),COALESCE(json_extract(CASE WHEN m.type='object' THEN m.value ELSE '{}' END,'$.source_id'),''),COALESCE(json_extract(CASE WHEN m.type='object' THEN m.value ELSE '{}' END,'$.snapshot_id'),''),COALESCE(json_extract(CASE WHEN m.type='object' THEN m.value ELSE '{}' END,'$.version'),0) FROM json_each(CASE WHEN json_valid(COALESCE((SELECT input_json FROM knowledge_article_revisions WHERE article_id=new.id AND revision=new.working_revision),new.input_json)) THEN COALESCE((SELECT input_json FROM knowledge_article_revisions WHERE article_id=new.id AND revision=new.working_revision),new.input_json) ELSE '{}' END,'$.materials') m WHERE json_extract(CASE WHEN m.type='object' THEN m.value ELSE '{}' END,'$.id') IS NOT NULL;
END;
CREATE TRIGGER knowledge_article_material_update AFTER UPDATE OF input_json,working_revision,passed_revision ON knowledge_articles BEGIN
 DELETE FROM knowledge_article_material_index WHERE article_id=new.id;
 INSERT OR IGNORE INTO knowledge_article_material_index SELECT new.id,'working',json_extract(CASE WHEN m.type='object' THEN m.value ELSE '{}' END,'$.id'),COALESCE(json_extract(CASE WHEN m.type='object' THEN m.value ELSE '{}' END,'$.source_type'),''),COALESCE(json_extract(CASE WHEN m.type='object' THEN m.value ELSE '{}' END,'$.source_id'),''),COALESCE(json_extract(CASE WHEN m.type='object' THEN m.value ELSE '{}' END,'$.snapshot_id'),''),COALESCE(json_extract(CASE WHEN m.type='object' THEN m.value ELSE '{}' END,'$.version'),0) FROM json_each(CASE WHEN json_valid(COALESCE((SELECT input_json FROM knowledge_article_revisions WHERE article_id=new.id AND revision=new.working_revision),new.input_json)) THEN COALESCE((SELECT input_json FROM knowledge_article_revisions WHERE article_id=new.id AND revision=new.working_revision),new.input_json) ELSE '{}' END,'$.materials') m WHERE json_extract(CASE WHEN m.type='object' THEN m.value ELSE '{}' END,'$.id') IS NOT NULL;
 INSERT OR IGNORE INTO knowledge_article_material_index SELECT new.id,'passed',json_extract(CASE WHEN m.type='object' THEN m.value ELSE '{}' END,'$.id'),COALESCE(json_extract(CASE WHEN m.type='object' THEN m.value ELSE '{}' END,'$.source_type'),''),COALESCE(json_extract(CASE WHEN m.type='object' THEN m.value ELSE '{}' END,'$.source_id'),''),COALESCE(json_extract(CASE WHEN m.type='object' THEN m.value ELSE '{}' END,'$.snapshot_id'),''),COALESCE(json_extract(CASE WHEN m.type='object' THEN m.value ELSE '{}' END,'$.version'),0) FROM knowledge_article_revisions r,json_each(CASE WHEN json_valid(r.input_json) THEN r.input_json ELSE '{}' END,'$.materials') m WHERE r.article_id=new.id AND r.revision=new.passed_revision AND json_extract(CASE WHEN m.type='object' THEN m.value ELSE '{}' END,'$.id') IS NOT NULL;
END;
CREATE TABLE knowledge_update_settings (
 id INTEGER PRIMARY KEY CHECK(id=1), enabled INTEGER NOT NULL DEFAULT 0,
 daily_limit INTEGER NOT NULL DEFAULT 2 CHECK(daily_limit BETWEEN 1 AND 10)
);
INSERT INTO knowledge_update_settings(id)VALUES(1);
CREATE TABLE knowledge_update_cursor(id INTEGER PRIMARY KEY CHECK(id=1),last_seq INTEGER NOT NULL DEFAULT 0);
INSERT INTO knowledge_update_cursor(id)VALUES(1);
CREATE TABLE knowledge_update_pending_articles(
 article_id TEXT PRIMARY KEY REFERENCES knowledge_articles(id) ON DELETE CASCADE,
 last_seq INTEGER NOT NULL, created_at TEXT NOT NULL DEFAULT(datetime('now'))
);
CREATE TABLE knowledge_update_proposals(
 id TEXT PRIMARY KEY,article_id TEXT NOT NULL REFERENCES knowledge_articles(id) ON DELETE CASCADE,
 parent_revision INTEGER NOT NULL,parent_hash TEXT NOT NULL,passed_revision INTEGER NOT NULL,
 question_id TEXT NOT NULL DEFAULT '',question_revision INTEGER NOT NULL DEFAULT 0,
 input_hash TEXT NOT NULL UNIQUE,material_fingerprint TEXT NOT NULL,input_json TEXT NOT NULL,provider TEXT NOT NULL,model TEXT NOT NULL,prompt_version TEXT NOT NULL,
 state TEXT NOT NULL DEFAULT 'pending',reason TEXT NOT NULL DEFAULT '',analysis_json TEXT NOT NULL DEFAULT '{}',
 changes_json TEXT NOT NULL DEFAULT '[]',generated_revision INTEGER NOT NULL DEFAULT 0,
 analysis_admitted_at TEXT NOT NULL DEFAULT '',job_id TEXT NOT NULL DEFAULT '',automated INTEGER NOT NULL DEFAULT 0,last_seq INTEGER NOT NULL DEFAULT 0,
 deferred_state TEXT NOT NULL DEFAULT '',owner_action TEXT NOT NULL DEFAULT '',owner_reason TEXT NOT NULL DEFAULT '',created_at TEXT NOT NULL DEFAULT(datetime('now')),updated_at TEXT NOT NULL DEFAULT(datetime('now'))
);
CREATE INDEX knowledge_update_proposals_article ON knowledge_update_proposals(article_id,created_at DESC,id);
CREATE INDEX knowledge_update_proposals_state ON knowledge_update_proposals(state,created_at,id);
CREATE INDEX knowledge_update_proposals_material ON knowledge_update_proposals(article_id,parent_hash,material_fingerprint,state);
CREATE TRIGGER knowledge_change_question AFTER UPDATE OF revision ON learning_questions WHEN new.revision!=old.revision BEGIN
 INSERT INTO knowledge_learning_changes(kind,material_id,source_type,source_id,material_version)VALUES('learning_question',new.id,'','',new.revision);
END;
CREATE TRIGGER knowledge_change_note_delete AFTER DELETE ON owner_notes BEGIN
 INSERT INTO knowledge_learning_changes(kind,material_id,source_type,source_id,material_version)VALUES('material_deleted',old.id,old.source_type,old.source_id,old.revision);
END;
CREATE TRIGGER knowledge_change_key_eligibility AFTER UPDATE OF production_status,stale_at,evidence_status ON keypoint_index WHEN new.production_status!=old.production_status OR new.stale_at IS NOT old.stale_at OR new.evidence_status!=old.evidence_status BEGIN
 INSERT INTO knowledge_learning_changes(kind,material_id,source_type,source_id,material_version)VALUES('material_eligibility',new.id,new.source_type,new.source_id,new.card_version);
END;
CREATE TRIGGER knowledge_change_key_delete AFTER DELETE ON keypoint_index BEGIN
 INSERT INTO knowledge_learning_changes(kind,material_id,source_type,source_id,material_version)VALUES('material_deleted',old.id,old.source_type,old.source_id,old.card_version);
END;
CREATE TRIGGER knowledge_change_episode_source AFTER UPDATE OF current_transcript_version,archived_at,model_data_policy,approved_providers_json ON episodes WHEN new.current_transcript_version IS NOT old.current_transcript_version OR new.archived_at IS NOT old.archived_at OR new.model_data_policy!=old.model_data_policy OR new.approved_providers_json!=old.approved_providers_json BEGIN
 INSERT INTO knowledge_learning_changes(kind,material_id,source_type,source_id,material_version)VALUES('source_changed',new.id,'episode',new.id,COALESCE(new.current_transcript_version,0));
END;
CREATE TRIGGER knowledge_change_upload_source AFTER UPDATE OF current_transcript_version,archived_at,model_data_policy,approved_providers_json ON uploads WHEN new.current_transcript_version IS NOT old.current_transcript_version OR new.archived_at IS NOT old.archived_at OR new.model_data_policy!=old.model_data_policy OR new.approved_providers_json!=old.approved_providers_json BEGIN
 INSERT INTO knowledge_learning_changes(kind,material_id,source_type,source_id,material_version)VALUES('source_changed',new.id,'upload',new.id,COALESCE(new.current_transcript_version,0));
END;
CREATE TRIGGER knowledge_change_document_source AFTER UPDATE OF version,archived_at,model_data_policy,approved_providers_json ON documents WHEN new.version!=old.version OR new.archived_at IS NOT old.archived_at OR new.model_data_policy!=old.model_data_policy OR new.approved_providers_json!=old.approved_providers_json BEGIN
 INSERT INTO knowledge_learning_changes(kind,material_id,source_type,source_id,material_version)VALUES('source_changed',new.id,'document',new.id,new.version);
END;
CREATE TRIGGER knowledge_update_episode_purge BEFORE DELETE ON episodes BEGIN
 INSERT INTO knowledge_learning_changes(kind,material_id,source_type,source_id,material_version)VALUES('source_deleted',old.id,'episode',old.id,0);
 UPDATE knowledge_update_proposals SET state='insufficient',reason='来源已清理，旧正文不能继续外发',updated_at=datetime('now') WHERE article_id IN(SELECT article_id FROM knowledge_article_material_index WHERE source_type='episode' AND source_id=old.id) AND state NOT IN ('completed','ignored','parent_changed');
END;
CREATE TRIGGER knowledge_update_upload_purge BEFORE DELETE ON uploads BEGIN
 INSERT INTO knowledge_learning_changes(kind,material_id,source_type,source_id,material_version)VALUES('source_deleted',old.id,'upload',old.id,0);
 UPDATE knowledge_update_proposals SET state='insufficient',reason='来源已清理，旧正文不能继续外发',updated_at=datetime('now') WHERE article_id IN(SELECT article_id FROM knowledge_article_material_index WHERE source_type='upload' AND source_id=old.id) AND state NOT IN ('completed','ignored','parent_changed');
END;
CREATE TRIGGER knowledge_update_document_purge BEFORE DELETE ON documents BEGIN
 INSERT INTO knowledge_learning_changes(kind,material_id,source_type,source_id,material_version)VALUES('source_deleted',old.id,'document',old.id,0);
 UPDATE knowledge_update_proposals SET state='insufficient',reason='来源已清理，旧正文不能继续外发',updated_at=datetime('now') WHERE article_id IN(SELECT article_id FROM knowledge_article_material_index WHERE source_type='document' AND source_id=old.id) AND state NOT IN ('completed','ignored','parent_changed');
END;
