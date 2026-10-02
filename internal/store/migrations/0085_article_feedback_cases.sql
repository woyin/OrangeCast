CREATE TABLE article_quality_feedback (
 id TEXT PRIMARY KEY, article_id TEXT NOT NULL, revision INTEGER NOT NULL, content_hash TEXT NOT NULL,
 paragraph_index INTEGER NOT NULL, paragraph_hash TEXT NOT NULL, category TEXT NOT NULL,
 comment TEXT NOT NULL, created_at TEXT NOT NULL DEFAULT(datetime('now'))
);
CREATE TABLE article_quality_cases (
 id TEXT PRIMARY KEY, feedback_id TEXT NOT NULL REFERENCES article_quality_feedback(id) ON DELETE CASCADE,
 version INTEGER NOT NULL, expected TEXT NOT NULL, classification TEXT NOT NULL DEFAULT 'unclassified',
 classification_evidence TEXT NOT NULL DEFAULT '{}', state TEXT NOT NULL DEFAULT 'accepted',
 input_json TEXT NOT NULL, blocks_json TEXT NOT NULL, fingerprint TEXT NOT NULL,
 created_at TEXT NOT NULL DEFAULT(datetime('now')), updated_at TEXT NOT NULL DEFAULT(datetime('now')),
 UNIQUE(feedback_id,version)
);
CREATE TABLE article_quality_commands (
 request_key TEXT PRIMARY KEY, command_hash TEXT NOT NULL, case_id TEXT NOT NULL REFERENCES article_quality_cases(id) ON DELETE CASCADE
);
CREATE TRIGGER quality_revision_unavailable AFTER UPDATE OF evidence_status ON knowledge_article_revisions WHEN new.evidence_status='unavailable' BEGIN
 UPDATE article_quality_cases SET input_json='',blocks_json='',state='unavailable' WHERE feedback_id IN(SELECT id FROM article_quality_feedback WHERE article_id=new.article_id AND revision=new.revision);
END;

CREATE TRIGGER quality_revision_deleted BEFORE DELETE ON knowledge_article_revisions BEGIN
 UPDATE article_quality_cases SET input_json='',blocks_json='',state='unavailable' WHERE feedback_id IN(SELECT id FROM article_quality_feedback WHERE article_id=old.article_id AND revision=old.revision);
END;

CREATE TRIGGER quality_episodes_purged BEFORE DELETE ON episodes BEGIN
 UPDATE article_quality_cases SET input_json='',blocks_json='',state='unavailable'
 WHERE EXISTS(SELECT 1 FROM json_each(CASE WHEN json_valid(input_json) THEN input_json ELSE '{}' END,'$.materials') m
 WHERE (json_extract(m.value,'$.source_type')='episode' AND json_extract(m.value,'$.source_id')=old.id)
 OR EXISTS(SELECT 1 FROM json_each(m.value,'$.understanding_references') r WHERE json_extract(r.value,'$.source_type')='episode' AND json_extract(r.value,'$.source_id')=old.id));
END;

CREATE TRIGGER quality_uploads_purged BEFORE DELETE ON uploads BEGIN
 UPDATE article_quality_cases SET input_json='',blocks_json='',state='unavailable'
 WHERE EXISTS(SELECT 1 FROM json_each(CASE WHEN json_valid(input_json) THEN input_json ELSE '{}' END,'$.materials') m
 WHERE (json_extract(m.value,'$.source_type')='upload' AND json_extract(m.value,'$.source_id')=old.id)
 OR EXISTS(SELECT 1 FROM json_each(m.value,'$.understanding_references') r WHERE json_extract(r.value,'$.source_type')='upload' AND json_extract(r.value,'$.source_id')=old.id));
END;

CREATE TRIGGER quality_documents_purged BEFORE DELETE ON documents BEGIN
 UPDATE article_quality_cases SET input_json='',blocks_json='',state='unavailable'
 WHERE EXISTS(SELECT 1 FROM json_each(CASE WHEN json_valid(input_json) THEN input_json ELSE '{}' END,'$.materials') m
 WHERE (json_extract(m.value,'$.source_type')='document' AND json_extract(m.value,'$.source_id')=old.id)
 OR EXISTS(SELECT 1 FROM json_each(m.value,'$.understanding_references') r WHERE json_extract(r.value,'$.source_type')='document' AND json_extract(r.value,'$.source_id')=old.id));
END;
