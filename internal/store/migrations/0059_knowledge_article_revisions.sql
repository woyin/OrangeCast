ALTER TABLE knowledge_articles ADD COLUMN working_revision INTEGER NOT NULL DEFAULT 0;
ALTER TABLE knowledge_articles ADD COLUMN passed_revision INTEGER NOT NULL DEFAULT 0;
ALTER TABLE knowledge_articles ADD COLUMN review_model TEXT NOT NULL DEFAULT '';
CREATE TABLE knowledge_article_revisions (
 article_id TEXT NOT NULL REFERENCES knowledge_articles(id) ON DELETE CASCADE,
 revision INTEGER NOT NULL,parent_revision INTEGER NOT NULL DEFAULT 0,
 title TEXT NOT NULL,input_json TEXT NOT NULL,blocks_json TEXT NOT NULL,
 content_hash TEXT NOT NULL,origin TEXT NOT NULL,provider TEXT NOT NULL,model TEXT NOT NULL,prompt_version TEXT NOT NULL,
 evidence_status TEXT NOT NULL DEFAULT 'valid' CHECK(evidence_status IN ('valid','outdated','unavailable')),
 created_at TEXT NOT NULL DEFAULT (datetime('now')),PRIMARY KEY(article_id,revision)
);
CREATE TABLE knowledge_article_reviews (
 id TEXT PRIMARY KEY,article_id TEXT NOT NULL,revision INTEGER NOT NULL,job_id TEXT NOT NULL DEFAULT '',
 content_hash TEXT NOT NULL,passed INTEGER NOT NULL,issues_json TEXT NOT NULL,
 provider TEXT NOT NULL,model TEXT NOT NULL,prompt_version TEXT NOT NULL,
 created_at TEXT NOT NULL DEFAULT(datetime('now')),
 FOREIGN KEY(article_id,revision) REFERENCES knowledge_article_revisions(article_id,revision) ON DELETE CASCADE
);
CREATE UNIQUE INDEX idx_knowledge_review_job ON knowledge_article_reviews(job_id) WHERE job_id!='';
CREATE TABLE knowledge_article_material_refs (
 article_id TEXT NOT NULL,revision INTEGER NOT NULL,material_id TEXT NOT NULL,kind TEXT NOT NULL,
 source_type TEXT NOT NULL,source_id TEXT NOT NULL,snapshot_id TEXT NOT NULL,material_version INTEGER NOT NULL,
 PRIMARY KEY(article_id,revision,material_id),
 FOREIGN KEY(article_id,revision) REFERENCES knowledge_article_revisions(article_id,revision) ON DELETE CASCADE
);
CREATE INDEX idx_knowledge_material_note ON knowledge_article_material_refs(material_id,kind);
CREATE INDEX idx_knowledge_material_source ON knowledge_article_material_refs(source_type,source_id);
CREATE TABLE knowledge_article_feedback (
 id TEXT PRIMARY KEY,article_id TEXT NOT NULL,revision INTEGER NOT NULL,
 category TEXT NOT NULL CHECK(category IN ('useful','shallow','duplicate','misattribution')),
 comment TEXT NOT NULL DEFAULT '',created_at TEXT NOT NULL DEFAULT(datetime('now')),
 FOREIGN KEY(article_id,revision) REFERENCES knowledge_article_revisions(article_id,revision) ON DELETE CASCADE
);
CREATE TABLE knowledge_article_runs (
 job_id TEXT PRIMARY KEY REFERENCES processing_jobs(id) ON DELETE CASCADE,
 article_id TEXT NOT NULL REFERENCES knowledge_articles(id) ON DELETE CASCADE,
 parent_revision INTEGER NOT NULL,stage TEXT NOT NULL,result_json TEXT NOT NULL DEFAULT '',
 created_at TEXT NOT NULL DEFAULT(datetime('now'))
);
-- Current known legacy body is the first revision. Active drafts remain unpassed.
INSERT INTO knowledge_article_revisions(article_id,revision,title,input_json,blocks_json,content_hash,origin,provider,model,prompt_version)
 SELECT id,1,title,input_json,blocks_json,'legacy:'||id,'legacy',provider,model,'knowledge-article-v1'
 FROM knowledge_articles WHERE blocks_json!='[]';
UPDATE knowledge_articles SET working_revision=1,passed_revision=CASE WHEN status='ready' THEN 1 ELSE 0 END WHERE blocks_json!='[]';
INSERT INTO knowledge_article_reviews(id,article_id,revision,content_hash,passed,issues_json,provider,model,prompt_version)
 SELECT 'legacy:'||id,id,1,'legacy:'||id,1,issues_json,provider,model,'knowledge-article-v1' FROM knowledge_articles WHERE status='ready' AND working_revision=1;
CREATE TRIGGER knowledge_revision_refs AFTER INSERT ON knowledge_article_revisions BEGIN
 INSERT OR IGNORE INTO knowledge_article_material_refs
 SELECT new.article_id,new.revision,json_extract(m.value,'$.id'),json_extract(m.value,'$.kind'),json_extract(m.value,'$.source_type'),json_extract(m.value,'$.source_id'),COALESCE(json_extract(m.value,'$.snapshot_id'),''),json_extract(m.value,'$.version')
 FROM json_each(new.input_json,'$.materials') m
 WHERE EXISTS(SELECT 1 FROM json_each(new.blocks_json) b,json_each(b.value,'$.material_ids') used WHERE used.value=json_extract(m.value,'$.id'));
END;
INSERT OR IGNORE INTO knowledge_article_material_refs
 SELECT r.article_id,r.revision,json_extract(m.value,'$.id'),json_extract(m.value,'$.kind'),json_extract(m.value,'$.source_type'),json_extract(m.value,'$.source_id'),COALESCE(json_extract(m.value,'$.snapshot_id'),''),json_extract(m.value,'$.version')
 FROM knowledge_article_revisions r,json_each(r.input_json,'$.materials') m
 WHERE EXISTS(SELECT 1 FROM json_each(r.blocks_json) b,json_each(b.value,'$.material_ids') used WHERE used.value=json_extract(m.value,'$.id'));
CREATE TRIGGER knowledge_note_changed AFTER UPDATE ON owner_notes WHEN new.revision!=old.revision BEGIN
 UPDATE knowledge_article_revisions SET evidence_status='outdated' WHERE (article_id,revision) IN(SELECT article_id,revision FROM knowledge_article_material_refs WHERE material_id=new.id AND kind!='keypoint');
END;
CREATE TRIGGER knowledge_note_deleted AFTER DELETE ON owner_notes BEGIN
 UPDATE knowledge_article_revisions SET evidence_status='unavailable' WHERE (article_id,revision) IN(SELECT article_id,revision FROM knowledge_article_material_refs WHERE material_id=old.id AND kind!='keypoint');
END;
CREATE TRIGGER knowledge_keypoint_changed AFTER UPDATE ON keypoint_index WHEN new.content!=old.content OR new.description!=old.description OR new.card_version!=old.card_version OR new.evidence_status='stale' OR new.production_status='dismissed' BEGIN
 UPDATE knowledge_article_revisions SET evidence_status='outdated' WHERE (article_id,revision) IN(SELECT article_id,revision FROM knowledge_article_material_refs WHERE material_id=new.id AND kind='keypoint');
END;
CREATE TRIGGER knowledge_keypoint_deleted AFTER DELETE ON keypoint_index BEGIN
 UPDATE knowledge_article_revisions SET evidence_status='unavailable' WHERE (article_id,revision) IN(SELECT article_id,revision FROM knowledge_article_material_refs WHERE material_id=old.id AND kind='keypoint');
END;
